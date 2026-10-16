//go:build freebsd

package netgraph

/*
#cgo LDFLAGS: -lnetgraph
#include <netgraph.h>
#include <sys/types.h>
#include <sys/socket.h>
#include <sys/sysctl.h>
#include <stdlib.h>
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// defaultRecvSpace is used if net.graph.recvspace cannot be read, matching
// the kernel's own default (see net.graph.recvspace in sysctl(8)).
const defaultRecvSpace = 20480

// ngMsgHeaderSize is sizeof(struct ng_mesg.header): 1+1+2+4*5+32 = 56 bytes,
// verified against /usr/include/netgraph/ng_message.h.
const ngMsgHeaderSize = 56

// ngVersion is NG_VERSION, used when building a struct ng_mesg header by
// hand (see cSendMsg). Verified on this host against
// /usr/include/netgraph/ng_message.h.
//
// ngsaOverhead (offsetof(struct sockaddr_ng, sg_data), also needed to build
// the address) is defined once, in validate.go, since that file has no
// build tag and so is already compiled into this platform's build too.
const ngVersion = 8

// lastToken is this package's own token allocator; see nextToken.
var lastToken uint32

// nextToken hands out the token used for the next outgoing request. It is a
// package-level atomic counter rather than a call to NgSendMsg (which would
// share libnetgraph's process-global gMsgId, see msg.c) — owning message
// delivery (see cSendMsg's comment) means owning token allocation too.
//
// It starts at 1, not 0: the mask below matches libnetgraph's own masking
// of gMsgId with INT_MAX, but a node-originated asynchronous message is
// built by the kernel with NG_MKMESSAGE, which zeroes the whole header —
// such a message arrives with token 0. If our first request also used
// token 0, Recv could mistake that unrelated async message for the reply
// to our very first call, which is exactly the mismatch this package
// exists to prevent.
func nextToken() uint32 {
	return atomic.AddUint32(&lastToken, 1) & 0x7fffffff
}

// asErrno recovers the syscall.Errno cgo attaches to a C call's second
// return value. cgo guarantees this is a real Go nil when the call did not
// fail (verified against this host: a successful NgMkSockNode returns a nil
// error, not a zero-value syscall.Errno), so this is only ever called along
// a path that already knows the call failed.
func asErrno(err error) syscall.Errno {
	if errno, ok := err.(syscall.Errno); ok {
		return errno
	}
	return syscall.EIO
}

// cMkSockNode wraps NgMkSockNode, creating a netgraph socket node — this
// also creates the kernel node itself — and returning its control and data
// socket descriptors. An empty name creates an anonymous node.
func cMkSockNode(name string) (cs, ds int, errno syscall.Errno) {
	var cname *C.char
	if name != "" {
		cname = C.CString(name)
		defer C.free(unsafe.Pointer(cname))
	}
	var ccs, cds C.int
	rc, cerr := C.NgMkSockNode(cname, &ccs, &cds)
	if rc < 0 {
		return -1, -1, asErrno(cerr)
	}
	return int(ccs), int(cds), 0
}

// cSendMsg delivers a control message on control socket cs to path and
// returns the token it assigned the request.
//
// This deliberately does NOT call NgSendMsg. NgSendMsg's C implementation
// (NgDeliverMsg, in /usr/src/lib/libnetgraph/msg.c) does, after its
// sendto(2) succeeds:
//
//	if (msg->header.cmd & NGM_HASREPLY && !(msg->header.flags & NGF_RESP))
//		n = poll(&rfds, 1, INFTIM);
//
// Every generic command this package sends bakes NGM_HASREPLY into its cmd
// value, so that poll runs on essentially every call — with an infinite
// timeout. It only waits for the socket to become readable, which an
// unrelated already-queued message already satisfies, so it does not
// actually confirm the reply arrived; it just blocks the caller, with no
// way for SO_RCVTIMEO or any deadline in this package to intervene, if the
// target node accepts the message and never replies. Do not "simplify"
// this back to NgSendMsg — that reintroduces an unbounded hang.
//
// Building the 56-byte ng_mesg header and the struct sockaddr_ng address
// ourselves and calling sendto(2) directly removes the hazard at its root:
// this function only ever does one sendto and returns.
func cSendMsg(cs int, path string, cookie, cmd int32, arg []byte) (tok uint32, errno syscall.Errno) {
	tok = nextToken()

	// struct ng_mesg header, 56 bytes (ngMsgHeaderSize), little-endian on
	// this platform: version@0, spare@1, spare2@2, arglen@4, cmd@8,
	// flags@12, token@16, typecookie@20, cmdstr[32]@24.
	msg := make([]byte, ngMsgHeaderSize+len(arg))
	msg[0] = ngVersion
	// msg[1] (spare) and msg[2:4] (spare2) stay zero.
	binary.LittleEndian.PutUint32(msg[4:8], uint32(len(arg)))
	binary.LittleEndian.PutUint32(msg[8:12], uint32(cmd))
	// msg[12:16] (flags) stays zero: NGF_ORIG — this is an original
	// request, not a reply.
	binary.LittleEndian.PutUint32(msg[16:20], tok)
	binary.LittleEndian.PutUint32(msg[20:24], uint32(cookie))
	// cmdstr is informational only (used by NGM_BINARY2ASCII and debug
	// tracing, not by message dispatch — confirmed against
	// /usr/src/sys/netgraph/ng_socket.c and ng_base.c); filled in purely
	// to match what NgSendMsg itself would have put there.
	copy(msg[24:56], fmt.Sprintf("cmd%d", cmd))
	copy(msg[56:], arg)

	// struct sockaddr_ng: sg_len (1 byte) + sg_family (1 byte) + sg_data
	// (path). Deliberately NOT NUL-terminated in sg_data: ngc_send
	// (/usr/src/sys/netgraph/ng_socket.c:250-253) computes
	// len = sg_len - offsetof(sg_data), then does
	// path = malloc(len+1); bcopy(sg_data, path, len); path[len] = '\0' —
	// the kernel allocates its own len+1 buffer and appends the NUL
	// itself, unconditionally, and never reads one from sg_data. Spending
	// one of the (NG_NODESIZ+ngsaOverhead)-byte sg_len budget's bytes on a
	// NUL nobody reads is a libnetgraph convention (NgDeliverMsg,
	// msg.c: sg_len = strlen(path)+1+NGSA_OVERHEAD), not a kernel
	// requirement, and it costs this package one real byte of path length
	// for nothing. ValidatePath has already rejected any path that would
	// still overflow sg_len at this narrower encoding.
	addrLen := ngsaOverhead + len(path)
	addr := make([]byte, addrLen)
	addr[0] = byte(addrLen)
	addr[1] = C.AF_NETGRAPH
	copy(addr[2:], path)

	rc, cerr := C.sendto(C.int(cs), unsafe.Pointer(&msg[0]), C.size_t(len(msg)), 0,
		(*C.struct_sockaddr)(unsafe.Pointer(&addr[0])), C.socklen_t(addrLen))
	if rc < 0 {
		return 0, asErrno(cerr)
	}
	return tok, 0
}

// cRecvMsg wraps NgRecvMsg, reading one control message from cs into buf and
// returning the number of bytes actually delivered. It does not itself
// enforce a deadline; the caller must have armed one with SO_RCVTIMEO (see
// setRecvTimeout in client.go) so this cannot block past it.
func cRecvMsg(cs int, buf []byte) (n int, errno syscall.Errno) {
	rc, cerr := C.NgRecvMsg(C.int(cs), (*C.struct_ng_mesg)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)), nil)
	if rc < 0 {
		return -1, asErrno(cerr)
	}
	return int(rc), 0
}

// cRecvSpace reads net.graph.recvspace, the control socket's receive space:
// a legitimate response can fill it, so response buffers must be sized from
// it rather than from an arbitrary constant. Falls back to the kernel's own
// documented default if the sysctl cannot be read.
func cRecvSpace() int {
	name := C.CString("net.graph.recvspace")
	defer C.free(unsafe.Pointer(name))

	var val C.ulong
	size := C.size_t(unsafe.Sizeof(val))
	rc, _ := C.sysctlbyname(name, unsafe.Pointer(&val), &size, nil, 0)
	if rc != 0 || val <= 0 {
		return defaultRecvSpace
	}
	return int(val)
}

// cSendData wraps NgSendData, writing one frame to hook on data socket ds.
//
// NgSendData is safe to call here, unlike NgSendMsg: its implementation
// (/usr/src/lib/libnetgraph/sock.c) fills in a struct sockaddr_ng with the
// hook name and makes exactly one sendto(2) call. There is no poll(), no
// receive, and no wait of any kind in it, so it cannot block past the
// syscall. Every Ng* function this package calls was read for that property
// before being used.
func cSendData(ds int, hook string, b []byte) syscall.Errno {
	chook := C.CString(hook)
	defer C.free(unsafe.Pointer(chook))

	rc, cerr := C.NgSendData(C.int(ds), chook, (*C.u_char)(unsafe.Pointer(&b[0])), C.size_t(len(b)))
	if rc < 0 {
		return asErrno(cerr)
	}
	return 0
}

// cRecvData wraps NgRecvData, reading one frame from data socket ds into
// buf and returning the number of bytes delivered along with the name of
// the hook it arrived on.
//
// NgRecvData is a single recvfrom(2) plus a strlcpy of the source address's
// hook name (sock.c). recvfrom respects SO_RCVTIMEO, so the caller's
// deadline (armed by ReadData via setRecvTimeout) actually bounds it —
// which is why this is safe where anything built on poll(INFTIM) would not
// be.
//
// The hook buffer is NG_HOOKSIZ bytes because that is the size NgRecvData
// strlcpy()s into unconditionally; a shorter buffer would be overrun.
func cRecvData(ds int, buf []byte) (n int, hook string, errno syscall.Errno) {
	var chook [C.NG_HOOKSIZ]C.char

	rc, cerr := C.NgRecvData(C.int(ds), (*C.u_char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)), &chook[0])
	if rc < 0 {
		return -1, "", asErrno(cerr)
	}
	return int(rc), C.GoString(&chook[0]), 0
}
