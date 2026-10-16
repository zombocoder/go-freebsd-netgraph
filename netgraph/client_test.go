//go:build freebsd

package netgraph

import (
	"errors"
	"testing"
)

// These are pure decode-logic tests: no kernel, no root. They exercise
// decodeMsg and the generic command constants directly, independent of the
// integration tests in client_integration_test.go, which do need root.

func TestGenericConstantsBakeInFlags(t *testing.T) {
	if GenericCookie != 1137070366 {
		t.Errorf("GenericCookie = %d, want 1137070366", GenericCookie)
	}
	if NGM_NODEINFO != (6 | 0x10000000 | 0x20000000) {
		t.Errorf("NGM_NODEINFO = %#x, want 6|NGM_READONLY|NGM_HASREPLY", NGM_NODEINFO)
	}
	if NGM_LISTTYPES != (10 | 0x10000000 | 0x20000000) {
		t.Errorf("NGM_LISTTYPES = %#x, want 10|NGM_READONLY|NGM_HASREPLY", NGM_LISTTYPES)
	}
	if NGF_RESP != 0x00000001 {
		t.Errorf("NGF_RESP = %#x, want 0x00000001", NGF_RESP)
	}
}

// buildRawMsg constructs a synthetic ng_mesg (56-byte header + arg) exactly
// as the kernel would lay it out on the wire, so decodeMsg can be tested
// without a live socket.
func buildRawMsg(token, cmd, flags, typecookie uint32, arg []byte) []byte {
	buf := make([]byte, ngMsgHeaderSize+len(arg))
	buf[0] = 8 // version
	// buf[1] spare, buf[2:4] spare2 left zero
	putU32 := func(off int, v uint32) {
		buf[off] = byte(v)
		buf[off+1] = byte(v >> 8)
		buf[off+2] = byte(v >> 16)
		buf[off+3] = byte(v >> 24)
	}
	putU32(4, uint32(len(arg)))
	putU32(8, cmd)
	putU32(12, flags)
	putU32(16, token)
	putU32(20, typecookie)
	copy(buf[24:56], "cmd")
	copy(buf[56:], arg)
	return buf
}

func TestDecodeMsgHappyPath(t *testing.T) {
	arg := []byte("hello netgraph")
	raw := buildRawMsg(42, uint32(NGM_NODEINFO), NGF_RESP, uint32(GenericCookie), arg)

	m, err := decodeMsg(raw, len(raw))
	if err != nil {
		t.Fatalf("decodeMsg: %v", err)
	}
	if m.Token != Token(42) {
		t.Errorf("Token = %d, want 42", m.Token)
	}
	if m.Cmd != NGM_NODEINFO {
		t.Errorf("Cmd = %#x, want %#x", m.Cmd, NGM_NODEINFO)
	}
	if m.Flags&NGF_RESP == 0 {
		t.Errorf("Flags = %#x, want NGF_RESP set", m.Flags)
	}
	if m.TypeCookie != GenericCookie {
		t.Errorf("TypeCookie = %d, want %d", m.TypeCookie, GenericCookie)
	}
	if string(m.Arg) != string(arg) {
		t.Errorf("Arg = %q, want %q", m.Arg, arg)
	}
	if m.CmdStr != "cmd" {
		t.Errorf("CmdStr = %q, want %q", m.CmdStr, "cmd")
	}
}

// This is the real bug the task description names: a receive buffer smaller
// than the arglen a legitimate response header describes must be reported
// as truncated, not handed back as a short slice a caller would over-read.
func TestDecodeMsgDetectsTruncation(t *testing.T) {
	arg := make([]byte, 1000)
	raw := buildRawMsg(1, uint32(NGM_LISTHOOKS), NGF_RESP, uint32(GenericCookie), arg)

	// Simulate a receive that only delivered the header plus 10 bytes of
	// argument data, even though the header's arglen still says 1000 —
	// exactly what a kernel-truncated datagram looks like on the wire.
	truncated := raw[:ngMsgHeaderSize+10]

	_, err := decodeMsg(truncated, len(truncated))
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("decodeMsg on a truncated buffer = %v, want ErrTruncated", err)
	}
}

func TestDecodeMsgRejectsShortHeader(t *testing.T) {
	_, err := decodeMsg(make([]byte, 10), 10)
	if err == nil {
		t.Fatal("decodeMsg on a 10-byte buffer (shorter than the 56-byte header) should fail")
	}
}

// CmdStr must be decoded with cstring, the same first-NUL-stops-the-string
// helper nodes.go uses for every other fixed-width C char array, not with
// bytes.TrimRight(..., "\x00"). The two agree whenever the field's bytes
// past the terminator are zero, which is all that "cmd" alone (buildRawMsg's
// fixture) exercises — so this test deliberately plants non-NUL garbage
// after the terminator, the pathological case TrimRight mishandles: it only
// strips a trailing run of NULs, so it would leave the garbage attached
// instead of stopping at the first NUL.
func TestDecodeMsgCmdStrStopsAtFirstNUL(t *testing.T) {
	raw := buildRawMsg(1, uint32(NGM_NODEINFO), NGF_RESP, uint32(GenericCookie), nil)
	copy(raw[24+10:56], "GARBAGE")

	m, err := decodeMsg(raw, len(raw))
	if err != nil {
		t.Fatalf("decodeMsg: %v", err)
	}
	if m.CmdStr != "cmd" {
		t.Errorf("CmdStr = %q, want %q — decoding must stop at the first NUL, not trim trailing zeros", m.CmdStr, "cmd")
	}
}
