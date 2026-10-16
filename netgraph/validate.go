package netgraph

import (
	"fmt"
	"strings"
)

// Kernel-side limits from /usr/include/netgraph/ng_message.h, verified on
// FreeBSD 14.3-RELEASE-p3. Each *SIZ constant already counts the trailing
// NUL the kernel stores the string with, so the longest string that fits is
// one byte shorter.
const (
	ngTypeSiz   = 32  // NG_TYPESIZ
	ngHookSiz   = 32  // NG_HOOKSIZ
	ngNodeSiz   = 32  // NG_NODESIZ
	ngCmdStrSiz = 32  // NG_CMDSTRSIZ
	ngPathSiz   = 512 // NG_PATHSIZ (informational only — see ngMaxPathLen)

	// ngsaOverhead is offsetof(struct sockaddr_ng, sg_data), from
	// /usr/include/netgraph/ng_socket.h: sg_len (1 byte, unsigned char) +
	// sg_family (1 byte, sa_family_t) precede sg_data. Duplicated from
	// cgo_freebsd.go's identical constant: this file carries no build tag
	// (ValidatePath must work on every platform), so it cannot import a
	// freebsd-only definition.
	ngsaOverhead = 2

	// ngMaxSgLen is the largest value the kernel accepts for sg_len when a
	// message is sent to a destination address on a control socket. From
	// ngc_send, /usr/src/sys/netgraph/ng_socket.c:241:
	//
	//	if (sap->sg_len > NG_NODESIZ + offsetof(struct sockaddr_ng, sg_data)) {
	//		error = EINVAL;
	//		goto release;
	//	}
	//
	// Anything above this is rejected outright, before the path is ever
	// parsed. That is the binding bound — not the 255 an unsigned char's
	// range would otherwise permit.
	ngMaxSgLen = ngNodeSiz + ngsaOverhead // 32 + 2 = 34

	// ngMaxPathLen is the real maximum netgraph path length this package
	// can deliver to a destination address, in bytes.
	//
	// NG_PATHSIZ (512, ngPathSiz above) is NOT the limit — that is the
	// obvious wrong answer to reach for here. The path travels to the
	// kernel inside a struct sockaddr_ng (ng_socket.h), and ngc_send
	// (quoted above, ng_socket.c:241) rejects the send with EINVAL,
	// unconditionally, before the path is parsed, whenever sg_len exceeds
	// ngMaxSgLen. cSendMsg (cgo_freebsd.go) sets
	//
	//	sg_len = ngsaOverhead + len(path)
	//
	// with no NUL terminator counted in sg_data — ngc_send itself
	// allocates its own buffer and NUL-terminates the path after copying
	// exactly sg_len-ngsaOverhead bytes out of it (ng_socket.c:250-253:
	// `len = sap->sg_len - offsetof(...); path = malloc(len+1); ...;
	// path[len] = '\0';`), so no byte of the budget needs to be spent on a
	// terminator the kernel will never read. Solving for len(path) —
	// subtracting ngsaOverhead back out of ngMaxSgLen — gives the bound
	// below.
	//
	// Confirmed empirically against a live kernel: addressing a node by a
	// hand-built 32-byte path (sg_len 34, no embedded NUL) succeeds and
	// gets a reply from the right node (see
	// TestNodeInfoOverA32ByteAddress); restoring a trailing NUL byte in
	// sg_data (sg_len 35) is rejected with EINVAL, because that already
	// exceeds ngMaxSgLen.
	ngMaxPathLen = ngMaxSgLen - ngsaOverhead // 34 - 2 = 32
)

// ValidateNodeName reports whether s is usable as a netgraph node name: not
// empty, short enough to fit NG_NODESIZ including the terminating NUL the
// kernel appends, and free of '.' and ':', which the kernel's path parser
// uses to separate a node name from a hook and to terminate the node part
// respectively.
func ValidateNodeName(s string) error {
	if s == "" {
		return fmt.Errorf("netgraph: node name must not be empty")
	}
	if len(s) > ngNodeSiz-1 {
		return fmt.Errorf("netgraph: node name %q is %d bytes, must fit NG_NODESIZ=%d including the terminator", s, len(s), ngNodeSiz)
	}
	if strings.ContainsAny(s, ".:") {
		return fmt.Errorf("netgraph: node name %q must not contain '.' or ':'", s)
	}
	return nil
}

// ValidateHookName reports whether s is usable as a netgraph hook name: not
// empty and short enough to fit NG_HOOKSIZ including the terminating NUL.
func ValidateHookName(s string) error {
	if s == "" {
		return fmt.Errorf("netgraph: hook name must not be empty")
	}
	if len(s) > ngHookSiz-1 {
		return fmt.Errorf("netgraph: hook name %q is %d bytes, must fit NG_HOOKSIZ=%d including the terminator", s, len(s), ngHookSiz)
	}
	return nil
}

// ValidatePath reports whether s is usable as a netgraph path: not empty
// and short enough that ngc_send (ng_socket.c) will accept the resulting
// sockaddr_ng instead of rejecting it outright with EINVAL. See
// ngMaxPathLen for the derivation.
func ValidatePath(s string) error {
	if s == "" {
		return fmt.Errorf("netgraph: path must not be empty")
	}
	if len(s) > ngMaxPathLen {
		return fmt.Errorf("netgraph: path %q is %d bytes, longer than the %d-byte limit — ngc_send (ng_socket.c) rejects a destination address whose sg_len exceeds NG_NODESIZ+offsetof(sg_data) with EINVAL, before the path is even parsed", s, len(s), ngMaxPathLen)
	}
	return nil
}

// ValidateTypeName reports whether s is usable as a netgraph node type name:
// not empty and short enough to fit NG_TYPESIZ including the terminating
// NUL.
func ValidateTypeName(s string) error {
	if s == "" {
		return fmt.Errorf("netgraph: type name must not be empty")
	}
	if len(s) > ngTypeSiz-1 {
		return fmt.Errorf("netgraph: type name %q is %d bytes, must fit NG_TYPESIZ=%d including the terminator", s, len(s), ngTypeSiz)
	}
	return nil
}
