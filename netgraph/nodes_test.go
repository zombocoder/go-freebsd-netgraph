//go:build freebsd

package netgraph

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// mkNodeInfo builds a wire-format struct nodeinfo.
func mkNodeInfo(name, typ string, id, hooks uint32) []byte {
	b := make([]byte, nodeInfoSize)
	copy(b[niNameOff:], name)
	copy(b[niTypeOff:], typ)
	binary.LittleEndian.PutUint32(b[niIDOff:], id)
	binary.LittleEndian.PutUint32(b[niHooksOff:], hooks)
	return b
}

// The offsets are verified against the C header by an offsetof probe (see
// docs/task3-report.md), but the probe does not run in CI. This pins the
// values so an edit to nodes.go that shifts one shows up as a test failure
// rather than as a mysteriously wrong node name.
func TestStructLayoutConstants(t *testing.T) {
	cases := []struct {
		name      string
		got, want int
	}{
		{"nodeInfoSize", nodeInfoSize, 72},
		{"niName", niNameOff, 0},
		{"niType", niTypeOff, 32},
		{"niID", niIDOff, 64},
		{"niHooks", niHooksOff, 68},
		{"linkInfoSize", linkInfoSize, 136},
		{"liOurHook", liOurHookOff, 0},
		{"liPeerHook", liPeerHookOff, 32},
		{"liNodeInfo", liNodeInfoOff, 64},
		{"hlNodeInfo", hlNodeInfoOff, 0},
		{"hlLink", hlLinkOff, 72},
		{"nlCount", nlCountOff, 0},
		{"nlArray", nlArrayOff, 4},
		{"typeInfoSize", typeInfoSize, 36},
		{"tiName", tiNameOff, 0},
		{"tiNumNodes", tiNumNodesOff, 32},
		{"tlCount", tlCountOff, 0},
		{"tlArray", tlArrayOff, 4},
		{"mkPeerSize", mkPeerSize, 96},
		{"connectSize", connectSize, 576},
		{"connectOurHook", connectOurHookOff, 512},
		{"connectPeerHook", connectPeerHookOff, 544},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d (verified with offsetof against ng_message.h)", c.name, c.got, c.want)
		}
	}
}

// A fixed-width char array is NUL-padded on the wire. Left untrimmed, a
// name comes back as "hole\x00\x00..." and every comparison against it
// fails in a way that looks like a kernel bug.
func TestDecodeNodeInfoTrimsFixedWidthFields(t *testing.T) {
	ni, err := decodeNodeInfo(mkNodeInfo("ngtesthole", "hole", 0x1234, 3))
	if err != nil {
		t.Fatal(err)
	}
	if ni.Name != "ngtesthole" {
		t.Errorf("Name = %q (len %d), want %q — trailing NULs were not trimmed", ni.Name, len(ni.Name), "ngtesthole")
	}
	if ni.Type != "hole" {
		t.Errorf("Type = %q (len %d), want %q", ni.Type, len(ni.Type), "hole")
	}
	if ni.ID != 0x1234 || ni.NumHooks != 3 {
		t.Errorf("ID/NumHooks = %#x/%d, want 0x1234/3", ni.ID, ni.NumHooks)
	}
}

// cstring must stop at the first NUL, not merely strip trailing zeros:
// ngctl(8)'s own mkpeer.c snprintf()s into an uninitialised struct and
// ships whatever stack bytes followed each terminator, so a fixed-width
// field can legitimately carry garbage past its NUL.
func TestDecodeNodeInfoStopsAtFirstNUL(t *testing.T) {
	b := mkNodeInfo("hole", "socket", 1, 0)
	copy(b[niNameOff+10:], "GARBAGE")
	ni, err := decodeNodeInfo(b)
	if err != nil {
		t.Fatal(err)
	}
	if ni.Name != "hole" {
		t.Errorf("Name = %q, want %q — decoding must stop at the first NUL, not trim trailing zeros", ni.Name, "hole")
	}
}

func TestDecodeNodeInfoShortBuffer(t *testing.T) {
	if _, err := decodeNodeInfo(make([]byte, nodeInfoSize-1)); !errors.Is(err, ErrTruncated) {
		t.Errorf("want ErrTruncated for a buffer shorter than struct nodeinfo, got %v", err)
	}
}

// A declared count larger than the delivered data is the truncation hazard
// ErrTruncated exists for; indexing on it reads out of bounds.
func TestDecodeNodeListRejectsOverlongCount(t *testing.T) {
	arg := make([]byte, nlArrayOff+nodeInfoSize) // room for exactly one
	binary.LittleEndian.PutUint32(arg[nlCountOff:], 2)
	copy(arg[nlArrayOff:], mkNodeInfo("a", "socket", 1, 0))

	if _, err := decodeNodeList(arg); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated for numnames=2 with room for 1, got %v", err)
	}
}

// The bound must be computed in wide arithmetic: count*nodeInfoSize in
// 32-bit would wrap to a small value and pass.
func TestDecodeNodeListCountCannotOverflowTheBound(t *testing.T) {
	arg := make([]byte, nlArrayOff+nodeInfoSize)
	binary.LittleEndian.PutUint32(arg[nlCountOff:], 0xffffffff)
	if _, err := decodeNodeList(arg); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated for numnames=0xffffffff, got %v", err)
	}
}

func TestDecodeNodeListHappyPath(t *testing.T) {
	arg := make([]byte, nlArrayOff+2*nodeInfoSize)
	binary.LittleEndian.PutUint32(arg[nlCountOff:], 2)
	copy(arg[nlArrayOff:], mkNodeInfo("one", "socket", 1, 0))
	copy(arg[nlArrayOff+nodeInfoSize:], mkNodeInfo("two", "hole", 2, 1))

	got, err := decodeNodeList(arg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "one" || got[1].Name != "two" || got[1].Type != "hole" {
		t.Fatalf("decodeNodeList = %+v", got)
	}
}

func TestDecodeHookListRejectsOverlongCount(t *testing.T) {
	arg := make([]byte, hlLinkOff+linkInfoSize)
	copy(arg[hlNodeInfoOff:], mkNodeInfo("n", "hole", 1, 2)) // claims 2 hooks
	if _, _, err := decodeHookList(arg); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated for hooks=2 with room for 1, got %v", err)
	}
}

func TestDecodeHookListHappyPath(t *testing.T) {
	arg := make([]byte, hlLinkOff+linkInfoSize)
	copy(arg[hlNodeInfoOff:], mkNodeInfo("ngtesthole", "hole", 7, 1))
	link := arg[hlLinkOff:]
	copy(link[liOurHookOff:], "left")
	copy(link[liPeerHookOff:], "h0")
	copy(link[liNodeInfoOff:], mkNodeInfo("peernode", "socket", 9, 1))

	ni, hooks, err := decodeHookList(arg)
	if err != nil {
		t.Fatal(err)
	}
	if ni.Name != "ngtesthole" || ni.Type != "hole" {
		t.Fatalf("nodeinfo = %+v", ni)
	}
	if len(hooks) != 1 {
		t.Fatalf("got %d hooks, want 1", len(hooks))
	}
	h := hooks[0]
	if h.Name != "left" || h.PeerHook != "h0" || h.PeerName != "peernode" || h.PeerType != "socket" || h.PeerID != 9 {
		t.Fatalf("hook = %+v", h)
	}
}

func TestDecodeTypeListRejectsOverlongCount(t *testing.T) {
	arg := make([]byte, tlArrayOff+typeInfoSize)
	binary.LittleEndian.PutUint32(arg[tlCountOff:], 5)
	if _, err := decodeTypeList(arg); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated for numtypes=5 with room for 1, got %v", err)
	}
}

func TestDecodeTypeListHappyPath(t *testing.T) {
	arg := make([]byte, tlArrayOff+typeInfoSize)
	binary.LittleEndian.PutUint32(arg[tlCountOff:], 1)
	copy(arg[tlArrayOff+tiNameOff:], "socket")
	binary.LittleEndian.PutUint32(arg[tlArrayOff+tiNumNodesOff:], 4)

	got, err := decodeTypeList(arg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "socket" || got[0].NumNodes != 4 {
		t.Fatalf("decodeTypeList = %+v", got)
	}
}

// NGM_CONNECT's peer path lives in a char[NG_PATHSIZ] field inside the
// message payload, not in the sockaddr_ng that addresses the message, so
// the sg_len limit ValidatePath applies does not bind it.
func TestValidateEmbeddedPathUsesPathSiz(t *testing.T) {
	if err := validateEmbeddedPath(strings.Repeat("p", ngPathSiz-1)); err != nil {
		t.Errorf("%d bytes fits NG_PATHSIZ=%d: %v", ngPathSiz-1, ngPathSiz, err)
	}
	if err := validateEmbeddedPath(strings.Repeat("p", ngPathSiz)); err == nil {
		t.Errorf("%d bytes leaves no room for the terminator and must be rejected", ngPathSiz)
	}
	if err := validateEmbeddedPath(""); err == nil {
		t.Error("empty peer path must be rejected")
	}
}

func TestSplitAsciiCmd(t *testing.T) {
	cmd, args, err := splitAsciiCmd(`  mkpeer { type="hole" ourhook="a" peerhook="b" }`)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "mkpeer" {
		t.Errorf("cmd = %q, want mkpeer", cmd)
	}
	// The argument text must reach the kernel exactly as written; this
	// package must not reinterpret it (see ascii.go's header).
	if args != `{ type="hole" ourhook="a" peerhook="b" }` {
		t.Errorf("args = %q, want the brace text unmodified", args)
	}

	if _, _, err := splitAsciiCmd("   "); err == nil {
		t.Error("empty ASCII command must be rejected")
	}
	if _, _, err := splitAsciiCmd(strings.Repeat("c", ngCmdStrSiz) + " x"); err == nil {
		t.Errorf("a command name of %d bytes does not fit NG_CMDSTRSIZ=%d and must be rejected", ngCmdStrSiz, ngCmdStrSiz)
	}
}

// The inner ng_mesg an ASCII2BINARY payload carries must declare the
// length of the ASCII STRING, including its NUL — not the size of any
// binary structure. ng_base.c validates
// msg->arglen >= sizeof(ng_mesg) + ascii->arglen and then writes
// ascii->data[ascii->arglen-1] = '\0'.
func TestAsciiInnerArglenIsTheStringLength(t *testing.T) {
	const args = `{ type="hole" ourhook="a" peerhook="b" }`
	inner := encodeMsgHeader(ngVersion, 0, 0, 0, 0, "mkpeer", append([]byte(args), 0))

	if len(inner) != ngMsgHeaderSize+len(args)+1 {
		t.Fatalf("payload is %d bytes, want %d (header + text + NUL)", len(inner), ngMsgHeaderSize+len(args)+1)
	}
	arglen := binary.LittleEndian.Uint32(inner[4:8])
	if int(arglen) != len(args)+1 {
		t.Fatalf("inner arglen = %d, want %d (the ASCII string plus its NUL, never a binary struct size)", arglen, len(args)+1)
	}
	// The kernel's own bound, restated here so a change to the encoder
	// that broke it would fail before ever reaching the kernel.
	if uint64(len(inner)) < uint64(ngMsgHeaderSize)+uint64(arglen) {
		t.Fatalf("payload of %d bytes is shorter than sizeof(ng_mesg)+arglen=%d; the kernel rejects this with EINVAL", len(inner), ngMsgHeaderSize+int(arglen))
	}
	if got := cstring(inner[24:56]); got != "mkpeer" {
		t.Fatalf("cmdstr = %q, want mkpeer", got)
	}
	if inner[len(inner)-1] != 0 {
		t.Fatal("the ASCII string must be NUL-terminated inside the payload")
	}
}
