//go:build freebsd

package netgraph

import (
	"fmt"
	"syscall"
	"time"
)

// WriteData writes one frame to hook, a hook on this socket's own node. The
// frame is delivered to whatever node is connected on the other side of
// that hook.
//
// This is a single sendto(2) on the data socket and does not wait for
// anything. (NgSendData, which it uses, is exactly that one sendto plus the
// sockaddr_ng setup — read at /usr/src/lib/libnetgraph/sock.c; unlike
// NgSendMsg there is no poll() in it, so it carries none of the unbounded
// wait that made this package hand-roll its control-message send.)
//
// An empty frame is rejected rather than sent: netgraph would accept a
// zero-length datagram, but on the receiving side a zero-length read is
// indistinguishable from the socket's end-of-data condition, so a caller
// that sent one would be handing its peer an ambiguous signal.
func (s *Socket) WriteData(hook string, b []byte) error {
	if err := ValidateHookName(hook); err != nil {
		return err
	}
	if len(b) == 0 {
		return fmt.Errorf("netgraph: writedata %q: frame must not be empty", hook)
	}
	if errno := cSendData(s.ds, hook, b); errno != 0 {
		return errnoToError("writedata", hook, errno)
	}
	return nil
}

// ReadData reads one frame from the data socket into b and reports which
// hook of this socket's own node it arrived on. It waits at most timeout;
// with no traffic it returns ErrTimeout rather than blocking, the same
// guarantee Recv gives for control messages and by the same mechanism (a
// SO_RCVTIMEO armed on the data socket before every read).
//
// The frame is read whole and then copied into b. Reading straight into a
// short b would work — recvfrom(2) on a datagram socket silently discards
// the excess — but silently is the problem: the caller would get a full
// buffer and no indication that the rest of the frame had been thrown away.
// Instead an over-long frame yields ErrTruncated, and n and hook still
// describe what arrived so the caller can size a bigger buffer and retry.
// The read goes directly into b whenever b is already at least as large as
// the socket's receive space, since no frame can exceed that.
func (s *Socket) ReadData(b []byte, timeout time.Duration) (n int, hook string, err error) {
	deadline := time.Now().Add(timeout)

	// A frame can be as large as the socket's receive space and no larger:
	// ngs_rcvdata (ng_socket.c) appends it to the receive buffer, which
	// fails outright rather than truncating if it does not fit. So a
	// buffer of that size can never come up short.
	buf := b
	direct := len(b) >= s.recvspace
	if !direct {
		buf = make([]byte, s.recvspace)
	}

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, "", ErrTimeout
		}
		if err := setRecvTimeout(s.ds, remaining); err != nil {
			return 0, "", &Error{Op: "readdata", Path: "", Err: err}
		}

		got, hookName, errno := cRecvData(s.ds, buf)
		if errno != 0 {
			switch errno {
			// EAGAIN and EWOULDBLOCK are the same value on FreeBSD, so
			// only one may appear in a switch.
			case syscall.EAGAIN:
				// The armed SO_RCVTIMEO fired. Loop: the top
				// recomputes the remaining time and returns
				// ErrTimeout once it is gone, or re-arms and reads
				// again if timeval rounding left a sliver.
				continue
			case syscall.EINTR:
				// A signal interrupted the read; retry, still
				// bounded by the same deadline.
				continue
			}
			return 0, "", errnoToError("readdata", hookName, errno)
		}

		if direct {
			return got, hookName, nil
		}
		copy(b, buf[:got])
		if got > len(b) {
			return len(b), hookName, ErrTruncated
		}
		return got, hookName, nil
	}
}
