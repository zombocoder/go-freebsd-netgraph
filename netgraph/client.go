//go:build freebsd

package netgraph

import (
	"encoding/binary"
	"fmt"
	"sync"
	"syscall"
	"time"
)

// Socket is a netgraph socket node: one control endpoint (for messages) and
// one data endpoint (for hook traffic), created together by NgMkSockNode and
// closed together.
type Socket struct {
	cs, ds    int
	recvspace int
	name      string

	closeOnce sync.Once
	closeErr  error
}

// Open creates a netgraph socket node and returns a Socket wrapping it. An
// empty name creates an anonymous node; a non-empty one gives the node that
// global name (see ValidateNodeName for what makes a name valid).
func Open(name string) (*Socket, error) {
	if name != "" {
		if err := ValidateNodeName(name); err != nil {
			return nil, err
		}
	}

	cs, ds, errno := cMkSockNode(name)
	if errno != 0 {
		return nil, errnoToError("open", name, errno)
	}

	s := &Socket{cs: cs, ds: ds, recvspace: cRecvSpace()}
	s.resolveName(name)
	return s, nil
}

// resolveName records the node's name for NodeName(). If the caller named
// the node explicitly, that name is authoritative. Otherwise the node is
// anonymous and only addressable by its kernel-assigned numeric ID; this
// queries it and formats it the same way NgMkSockNode does internally
// ("[id]") so the result is directly usable as a path prefix. A failure
// here is not fatal to Open — the socket is still fully usable — so
// NodeName simply reports empty in that case.
func (s *Socket) resolveName(requested string) {
	if requested != "" {
		s.name = requested
		return
	}
	m, err := s.Call(".:", GenericCookie, NGM_NODEINFO, nil, 2*time.Second)
	if err != nil || len(m.Arg) < 68 {
		s.name = ""
		return
	}
	// struct nodeinfo { char name[32]; char type[32]; ng_ID_t id; ... }
	id := binary.LittleEndian.Uint32(m.Arg[64:68])
	s.name = fmt.Sprintf("[%x]", id)
}

// NodeName returns the node's name: what was passed to Open, or the
// kernel-assigned "[id]" form for an anonymous node.
func (s *Socket) NodeName() string {
	return s.name
}

// Close closes both the control and data sockets. It is idempotent: a
// second Close returns the same result as the first without touching the
// descriptors again, so `defer s.Close()` alongside an explicit Close() is
// safe.
func (s *Socket) Close() error {
	s.closeOnce.Do(func() {
		errCs := syscall.Close(s.cs)
		errDs := syscall.Close(s.ds)
		switch {
		case errCs != nil:
			s.closeErr = &Error{Op: "close", Path: s.name, Err: errCs}
		case errDs != nil:
			s.closeErr = &Error{Op: "close", Path: s.name, Err: errDs}
		}
	})
	return s.closeErr
}

// Send submits a control message to path and returns the token the kernel
// will echo back in its reply. It does not wait for that reply — not even
// for a command that carries NGM_HASREPLY, where the underlying libnetgraph
// call otherwise would (see cSendMsg's comment) — use Recv or Call for
// that.
func (s *Socket) Send(path string, cookie, cmd int32, arg []byte) (Token, error) {
	if err := ValidatePath(path); err != nil {
		return 0, err
	}
	tok, errno := cSendMsg(s.cs, path, cookie, cmd, arg)
	if errno != 0 {
		return 0, errnoToError("send", path, errno)
	}
	return Token(tok), nil
}

// Recv reads control messages until one whose token matches tok arrives, or
// the deadline passes. NgRecvMsg hands back whatever message is next on the
// socket, not necessarily the answer to any particular call; Recv discards
// anything that does not match tok and keeps reading until the deadline —
// see doc.go for why replies must be matched by token rather than trusted
// in arrival order.
//
// Recv never blocks past timeout: every read is bounded by a socket receive
// deadline (SO_RCVTIMEO) derived from the time remaining, so a reply that
// never comes yields ErrTimeout rather than an indefinite hang.
func (s *Socket) Recv(tok Token, timeout time.Duration) (*Msg, error) {
	deadline := time.Now().Add(timeout)
	buf := make([]byte, s.recvspace)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, ErrTimeout
		}
		if err := setRecvTimeout(s.cs, remaining); err != nil {
			return nil, &Error{Op: "recv", Path: "", Err: err}
		}

		n, errno := cRecvMsg(s.cs, buf)
		if errno != 0 {
			if errno == syscall.EAGAIN || errno == syscall.EWOULDBLOCK {
				// The armed SO_RCVTIMEO fired with nothing matching
				// having arrived. Loop back to the top: it recomputes
				// remaining, which is usually <= 0 by now and returns
				// ErrTimeout — but timeval rounding can leave a sliver of
				// time on the clock, in which case it correctly re-arms
				// the deadline and reads again instead.
				continue
			}
			if errno == syscall.EINTR {
				// A spurious signal interrupted the read. Go installs its
				// handlers with SA_RESTART so this should be rare, but
				// treat it the same as EAGAIN: loop back and retry,
				// bounded by the same deadline.
				continue
			}
			return nil, errnoToError("recv", "", errno)
		}

		msg, err := decodeMsg(buf, n)
		if err != nil {
			return nil, err
		}
		if msg.Token == tok {
			return msg, nil
		}
		// A reply to a different, earlier (or later) call. Discard it and
		// keep waiting for ours until the deadline expires — this is the
		// token-matching guarantee this package exists to provide.
	}
}

// Call sends a control message and waits for its reply, matched by token.
// See Send and Recv for what each half guarantees.
func (s *Socket) Call(path string, cookie, cmd int32, arg []byte, timeout time.Duration) (*Msg, error) {
	tok, err := s.Send(path, cookie, cmd, arg)
	if err != nil {
		return nil, err
	}
	return s.Recv(tok, timeout)
}

// setRecvTimeout arms SO_RCVTIMEO on fd for d. d is never allowed to reach
// the kernel as a zero timeval, which for SO_RCVTIMEO means "no timeout" —
// i.e. block forever — the exact failure mode this package guarantees
// cannot happen.
func setRecvTimeout(fd int, d time.Duration) error {
	if d <= 0 {
		d = time.Microsecond
	}
	tv := syscall.NsecToTimeval(d.Nanoseconds())
	if tv.Sec == 0 && tv.Usec == 0 {
		tv.Usec = 1
	}
	return syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
}

// decodeMsg parses the ng_mesg header (56 bytes, verified against
// /usr/include/netgraph/ng_message.h) out of buf[:n] and returns the
// decoded Msg.
//
// If the header's arglen claims more argument data than n actually
// delivered, the response was truncated by the kernel because the receive
// buffer did not fit it — the header survives intact but the data past the
// buffer's capacity was silently discarded (this is normal datagram-socket
// truncation, not a network error). Handing back a short buffer here would
// let a caller that trusts arglen read uninitialized memory, so this
// returns ErrTruncated instead. Sizing Recv's buffer from
// net.graph.recvspace (see cRecvSpace) keeps this from happening for any
// legitimately-sized response.
func decodeMsg(buf []byte, n int) (*Msg, error) {
	if n < ngMsgHeaderSize {
		return nil, fmt.Errorf("netgraph: received %d bytes, shorter than the %d-byte message header", n, ngMsgHeaderSize)
	}

	version := buf[0]
	arglen := binary.LittleEndian.Uint32(buf[4:8])
	cmd := binary.LittleEndian.Uint32(buf[8:12])
	flags := binary.LittleEndian.Uint32(buf[12:16])
	token := binary.LittleEndian.Uint32(buf[16:20])
	typecookie := binary.LittleEndian.Uint32(buf[20:24])
	cmdstr := cstring(buf[24:56])

	delivered := n - ngMsgHeaderSize
	if int(arglen) > delivered {
		return nil, ErrTruncated
	}

	arg := make([]byte, arglen)
	copy(arg, buf[ngMsgHeaderSize:ngMsgHeaderSize+int(arglen)])

	return &Msg{
		Version:    version,
		Flags:      flags,
		Token:      Token(token),
		TypeCookie: int32(typecookie),
		Cmd:        int32(cmd),
		CmdStr:     cmdstr,
		Arg:        arg,
	}, nil
}
