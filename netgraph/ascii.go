//go:build freebsd

package netgraph

// ASCII conversion: NGM_ASCII2BINARY and NGM_BINARY2ASCII.
//
// # What this file guarantees, and why it exists at all
//
// A caller could send NGM_ASCII2BINARY with Call and build the payload by
// hand. This file exists because two details of that payload are easy to
// get wrong in ways that produce misleading errors, and one of them has
// already cost this project an afternoon.
//
// Guarantee 1: the text you pass is handed to the kernel's parser as you
// wrote it. This package splits it into a command word and an argument
// string — nothing else. In particular it does NOT reinterpret positional
// arguments, and that is deliberate:
//
//	mkpeer { type="hole" ourhook="a" peerhook="b" }   // works
//	mkpeer hole a b                                   // does not
//
// The kernel converts arguments with ng_parse (/usr/src/sys/netgraph/
// ng_parse.c). Every generic command whose argument is a structure — mkpeer,
// connect, name, rmhook — is parsed by ng_struct_parse, which is
// ng_parse_composite with CT_STRUCT, and its very first act after
// allocating is:
//
//	if (ng_parse_get_token(s, off, &len) != T_LBRACE) { error = EINVAL; }
//
// So a missing brace is EINVAL, with no hint as to why. The second form
// above is nonetheless what ngctl(8) accepts, which is what makes this a
// trap: ngctl never sends that text anywhere. Its mkpeer command
// (/usr/src/usr.sbin/ngctl/mkpeer.c) snprintf()s av[1], av[2] and av[3]
// straight into a struct ngm_mkpeer in userland and sends the binary
// message with NgSendMsg. The positional form is ngctl's own command-line
// syntax, not netgraph's ASCII syntax, and nothing about either one says
// so.
//
// Guarantee 2: the inner message's arglen is the length of the ASCII
// string, never the size of a binary structure, and this package never
// clamps it to one. The payload of an NGM_ASCII2BINARY message is a whole
// struct ng_mesg whose cmdstr names the command and whose data is the
// argument text; ng_base.c's handler validates
//
//	msg->header.arglen >= sizeof(struct ng_mesg) + ascii->header.arglen
//
// and then writes ascii->data[ascii->header.arglen - 1] = '\0'. Treating
// that inner arglen as a binary structure size and clamping it accordingly
// is what previously broke hand-typed ngctl commands with E2BIG: the text
// is routinely longer than the structure it will parse into, so the clamp
// truncated the string and the kernel rejected the result. What is sent
// here is sizeof(ng_mesg) + len(text) + 1 bytes, with the inner arglen set
// to len(text) + 1, and nothing compares either number against a struct
// size.
//
// Guarantee 3: both round trips are token-matched and deadline-bounded.
// libnetgraph has NgSendAsciiMsg for exactly this job and it is unusable
// here on both counts (/usr/src/lib/libnetgraph/msg.c:89-153): it calls
// NgSendMsg, which reaches NgDeliverMsg's poll(&rfds, 1, INFTIM), and it
// then collects the conversion result with NgAllocRecvMsg — a bare
// recvfrom that returns whatever message is next on the socket without
// looking at its token. Both failures are exactly what this package exists
// to prevent, so the conversion is done here with Call instead.

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// splitAsciiCmd splits ASCII command text into the command word and the
// argument text that follows it, the same split NgSendAsciiMsg performs:
// leading space is skipped, the first run of non-space characters is the
// command, and everything after the space that follows it — unmodified,
// braces and all — is the argument string.
func splitAsciiCmd(text string) (cmd, args string, err error) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if trimmed == "" {
		return "", "", fmt.Errorf("netgraph: ASCII command must not be empty")
	}
	if i := strings.IndexAny(trimmed, " \t\r\n"); i >= 0 {
		cmd = trimmed[:i]
		args = strings.TrimLeft(trimmed[i:], " \t\r\n")
	} else {
		cmd = trimmed
	}
	if len(cmd) > ngCmdStrSiz-1 {
		return "", "", fmt.Errorf("netgraph: ASCII command name %q is %d bytes, must fit NG_CMDSTRSIZ=%d including the terminator", cmd, len(cmd), ngCmdStrSiz)
	}
	return cmd, args, nil
}

// encodeMsgHeader writes a struct ng_mesg header describing arg into a
// fresh buffer and appends arg to it. This is the inner-message form both
// conversion commands carry as their payload.
func encodeMsgHeader(version uint8, flags, token uint32, cookie, cmd int32, cmdstr string, arg []byte) []byte {
	buf := make([]byte, ngMsgHeaderSize+len(arg))
	buf[0] = version
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(arg)))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(cmd))
	binary.LittleEndian.PutUint32(buf[12:16], flags)
	binary.LittleEndian.PutUint32(buf[16:20], token)
	binary.LittleEndian.PutUint32(buf[20:24], uint32(cookie))
	copy(buf[24:56], cmdstr)
	copy(buf[ngMsgHeaderSize:], arg)
	return buf
}

// fromAscii performs the NGM_ASCII2BINARY round trip against the node at
// path and returns the binary message the kernel produced. Nothing is sent
// to that command's own destination — see CallAscii for that.
func (s *Socket) fromAscii(path, text string, timeout time.Duration) (*Msg, error) {
	cmd, args, err := splitAsciiCmd(text)
	if err != nil {
		return nil, err
	}

	// The inner ng_mesg: only cmdstr, arglen and data are read by the
	// kernel's handler, but version is filled in too because the handler
	// bcopy()s this whole header into its answer, so leaving it zero
	// would hand the caller back a binary message claiming version 0.
	// arglen counts the terminating NUL, which the handler relies on
	// being inside the buffer (it writes data[arglen-1] = '\0').
	inner := encodeMsgHeader(ngVersion, 0, 0, 0, 0, cmd, append([]byte(args), 0))

	m, err := s.Call(path, GenericCookie, NGM_ASCII2BINARY, inner, timeout)
	if err != nil {
		return nil, err
	}

	// The reply's argument is itself a struct ng_mesg: header plus the
	// parsed binary arguments. decodeMsg applies the same header and
	// truncation checks it applies to a message off the wire.
	bin, err := decodeMsg(m.Arg, len(m.Arg))
	if err != nil {
		return nil, &Error{Op: "ascii2binary", Path: path, Err: err}
	}
	return bin, nil
}

// FromAscii converts ASCII command text into a binary message by asking the
// node at path to parse it, and returns the result without sending it
// anywhere. Use CallAscii to convert and then execute in one step.
//
// path selects the node that does the parsing, which matters: the kernel
// looks the command name up in that node's own type cmdlist first and only
// then in the generic list, so a node-type-specific command must be
// converted at a node of that type. It waits up to defaultTimeout.
//
// The text must use the kernel's brace syntax for any command whose
// arguments are a structure; see this file's header for why the positional
// form ngctl(8) accepts does not work here.
func (s *Socket) FromAscii(path, text string) (*Msg, error) {
	return s.fromAscii(path, text, defaultTimeout)
}

// CallAscii converts ASCII command text to binary at the node at path and
// then sends that command to the same node, the same two-step sequence
// NgSendAsciiMsg performs — but with the conversion reply matched by token
// and both steps bounded by timeout, which NgSendAsciiMsg gives neither of.
//
// The returned message depends on whether the command has a reply, which
// this decides from NGM_HASREPLY in the converted command value rather than
// guessing:
//
//   - For a command that carries NGM_HASREPLY (nodeinfo, listhooks, the
//     rest of the read-only queries), the returned Msg is the node's reply
//     and its Flags carry NGF_RESP.
//   - For a command that does not (mkpeer, connect, name, rmhook,
//     shutdown), there is no reply to wait for and waiting would simply
//     time out. The returned Msg is then the binary form of the command
//     that was sent, with NGF_RESP clear. Test Flags&NGF_RESP to tell the
//     two apart; the error is what reports whether the command succeeded,
//     since a command with no reply still reports failure synchronously
//     through the send.
func (s *Socket) CallAscii(path, cmd string, timeout time.Duration) (*Msg, error) {
	start := time.Now()

	bin, err := s.fromAscii(path, cmd, timeout)
	if err != nil {
		return nil, err
	}

	if bin.Cmd&ngmHasReply == 0 {
		if _, err := s.Send(path, bin.TypeCookie, bin.Cmd, bin.Arg); err != nil {
			return nil, err
		}
		return bin, nil
	}

	remaining := timeout - time.Since(start)
	if remaining <= 0 {
		return nil, ErrTimeout
	}
	return s.Call(path, bin.TypeCookie, bin.Cmd, bin.Arg, remaining)
}

// ToAscii renders m as ASCII by asking this socket's own node to unparse
// it. That node is a socket node, so it can only render commands its own
// type or the generic list defines; use ToAsciiAt to render a
// node-type-specific message at a node of that type.
func (s *Socket) ToAscii(m *Msg) (string, error) {
	return s.ToAsciiAt(".:", m)
}

// ToAsciiAt renders m as ASCII by asking the node at path to unparse it,
// via NGM_BINARY2ASCII, and waits up to defaultTimeout for the answer.
//
// The kernel matches m's type cookie and command against the cmdlist of
// the node at path and then the generic list, and picks the message or
// response argument type according to NGF_RESP in m.Flags — so a reply
// must be passed with its flags intact or it will be rendered with the
// wrong argument type, or rejected with ENOSYS.
//
// This method is not in the original API sketch, which had only
// ToAscii(m). It is here because ToAscii can only ever address this
// package's own socket node, which makes it unable to render exactly the
// node-type-specific payloads the package otherwise carries as opaque
// bytes; ToAscii is now a one-line wrapper over this.
func (s *Socket) ToAsciiAt(path string, m *Msg) (string, error) {
	if m == nil {
		return "", fmt.Errorf("netgraph: ToAscii: message must not be nil")
	}

	inner := encodeMsgHeader(ngVersion, m.Flags, uint32(m.Token), m.TypeCookie, m.Cmd, m.CmdStr, m.Arg)

	resp, err := s.Call(path, GenericCookie, NGM_BINARY2ASCII, inner, defaultTimeout)
	if err != nil {
		return "", err
	}

	// The reply's argument is a struct ng_mesg whose own data is the
	// NUL-terminated ASCII string and whose arglen counts that NUL.
	ascii, err := decodeMsg(resp.Arg, len(resp.Arg))
	if err != nil {
		return "", &Error{Op: "binary2ascii", Path: path, Err: err}
	}
	return cstring(ascii.Arg), nil
}
