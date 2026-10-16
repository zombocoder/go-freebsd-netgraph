//go:build freebsd && integration

package netgraph

import (
	"strings"
	"testing"
	"time"
)

// The trap: the kernel's ASCII parser needs braces. Positional arguments
// work in ngctl(8) only because ngctl parses them itself before sending.
func TestAsciiRequiresBraceSyntax(t *testing.T) {
	needRoot(t)
	s := openForTest(t)

	// The documented, working form.
	err := func() error {
		_, err := s.CallAscii(".:",
			`mkpeer { type="hole" ourhook="ha" peerhook="left" }`, 2*time.Second)
		return err
	}()
	if err != nil {
		t.Fatalf("brace syntax should work: %v", err)
	}
	t.Cleanup(func() { _ = s.RmHook(".:", "ha") })

	// It must have actually built the graph, not merely returned nil: a
	// CallAscii that converted the text and then dropped the command on
	// the floor would also produce no error.
	if _, hooks, err := s.ListHooks(".:"); err != nil {
		t.Fatalf("ListHooks after CallAscii: %v", err)
	} else {
		var found bool
		for _, h := range hooks {
			if h.Name == "ha" && h.PeerType == "hole" && h.PeerHook == "left" {
				found = true
			}
		}
		if !found {
			t.Fatalf("CallAscii returned nil but built no hook ha to a hole node: %+v", hooks)
		}
	}

	// The form that looks right and is not. If this ever starts succeeding,
	// the kernel changed and the documentation needs revisiting.
	if _, err := s.CallAscii(".:", `mkpeer hole hb left`, 2*time.Second); err == nil {
		t.Fatal("positional ASCII arguments should be rejected by the kernel parser")
	}

	// And it must have been rejected without side effects: if the parser
	// had somehow accepted it, a hook hb would exist.
	if _, hooks, err := s.ListHooks(".:"); err != nil {
		t.Fatalf("ListHooks: %v", err)
	} else {
		for _, h := range hooks {
			if h.Name == "hb" {
				t.Fatalf("the positional form was rejected but still created hook hb: %+v", hooks)
			}
		}
	}
}

// FromAscii converts without executing. The converted command must carry
// the generic cookie and the real NGM_MKPEER value, and its argument must
// be a struct ngm_mkpeer laid out exactly as MkPeer would have built it —
// which is the whole point of asking the kernel to parse rather than
// parsing in Go.
func TestFromAsciiProducesTheSameBytesAsMkPeer(t *testing.T) {
	needRoot(t)
	s := openForTest(t)

	m, err := s.FromAscii(".:", `mkpeer { type="hole" ourhook="fa" peerhook="left" }`)
	if err != nil {
		t.Fatalf("FromAscii: %v", err)
	}
	if m.TypeCookie != GenericCookie {
		t.Errorf("TypeCookie = %d, want %d", m.TypeCookie, GenericCookie)
	}
	if m.Cmd != NGM_MKPEER {
		t.Errorf("Cmd = %#x, want NGM_MKPEER %#x", m.Cmd, NGM_MKPEER)
	}
	if len(m.Arg) != mkPeerSize {
		t.Fatalf("Arg is %d bytes, want sizeof(struct ngm_mkpeer) = %d", len(m.Arg), mkPeerSize)
	}
	if got := cstring(m.Arg[mkPeerTypeOff : mkPeerTypeOff+ngTypeSiz]); got != "hole" {
		t.Errorf("type field = %q, want hole", got)
	}
	if got := cstring(m.Arg[mkPeerOurHookOff : mkPeerOurHookOff+ngHookSiz]); got != "fa" {
		t.Errorf("ourhook field = %q, want fa", got)
	}
	if got := cstring(m.Arg[mkPeerPeerHookOff : mkPeerPeerHookOff+ngHookSiz]); got != "left" {
		t.Errorf("peerhook field = %q, want left", got)
	}

	// Conversion alone must not have built anything.
	if _, hooks, err := s.ListHooks(".:"); err != nil {
		t.Fatalf("ListHooks: %v", err)
	} else {
		for _, h := range hooks {
			if h.Name == "fa" {
				t.Fatal("FromAscii executed the command; it must only convert it")
			}
		}
	}
}

func TestToAsciiRoundTrip(t *testing.T) {
	needRoot(t)
	s := openForTest(t)
	m, err := s.Call(".:", GenericCookie, NGM_NODEINFO, nil, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	txt, err := s.ToAscii(m)
	if err != nil {
		t.Fatalf("ToAscii: %v", err)
	}
	if txt == "" {
		t.Fatal("ToAscii returned an empty string for a valid NODEINFO reply")
	}

	// "Not empty" is too weak to fail for the right reason: a conversion
	// that rendered the wrong argument type, or rendered a different
	// message entirely, would also be non-empty. The reply describes this
	// very socket node, so its rendered form must name the node's own type
	// and ID.
	ni, err := decodeNodeInfo(m.Arg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(txt, `type="socket"`) {
		t.Errorf("ToAscii = %q, want it to contain type=\"socket\"", txt)
	}
	if !strings.Contains(txt, "id=") {
		t.Errorf("ToAscii = %q, want it to contain the node id field", txt)
	}
	// Only fields that differ from their type default are rendered:
	// ng_unparse_composite skips a field whose value equals what
	// getDefault produces, which is why an idle socket node renders
	// neither name="" nor hooks=0. Asserting on those would be
	// asserting on something the kernel never emits.
	if ni.Type != "socket" {
		t.Errorf("the binary reply says type %q; the test's premise is wrong", ni.Type)
	}
}

// A message rendered to ASCII and parsed back must produce the same bytes.
// This is the strongest available check that both directions agree on the
// inner-ng_mesg framing, including that ASCII2BINARY's inner arglen is the
// string length and not a structure size.
func TestAsciiRoundTripsThroughBinary(t *testing.T) {
	needRoot(t)
	s := openForTest(t)

	arg := make([]byte, mkPeerSize)
	copy(arg[mkPeerTypeOff:], "hole")
	copy(arg[mkPeerOurHookOff:], "rt0")
	copy(arg[mkPeerPeerHookOff:], "left")

	txt, err := s.ToAscii(&Msg{
		Version:    8,
		TypeCookie: GenericCookie,
		Cmd:        NGM_MKPEER,
		CmdStr:     "mkpeer",
		Arg:        arg,
	})
	if err != nil {
		t.Fatalf("ToAscii: %v", err)
	}
	if !strings.Contains(txt, `"rt0"`) {
		t.Fatalf("ToAscii = %q, want it to mention ourhook rt0", txt)
	}

	back, err := s.FromAscii(".:", "mkpeer "+txt)
	if err != nil {
		t.Fatalf("FromAscii(%q): %v", txt, err)
	}
	if back.Cmd != NGM_MKPEER || back.TypeCookie != GenericCookie {
		t.Fatalf("round trip changed the command: cmd=%#x cookie=%d", back.Cmd, back.TypeCookie)
	}
	if len(back.Arg) != len(arg) {
		t.Fatalf("round trip changed the argument length: %d, want %d", len(back.Arg), len(arg))
	}
	for i := range arg {
		if arg[i] != back.Arg[i] {
			t.Fatalf("round trip changed byte %d: %#x, want %#x", i, back.Arg[i], arg[i])
		}
	}
}
