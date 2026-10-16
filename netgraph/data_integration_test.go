//go:build freebsd && integration

package netgraph

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// ng_echo returns whatever it is sent, so a frame written to the hook must
// come back on the same hook.
func TestHookDataRoundTrip(t *testing.T) {
	needRoot(t)
	s := openForTest(t)

	if err := s.MkPeer(".:", "echo", "e0", "in"); err != nil {
		t.Fatalf("MkPeer echo: %v", err)
	}
	// Removing the hook is enough to remove the node too: ng_echo's
	// disconnect method calls ng_rmnode_self once its last hook goes
	// (/usr/src/sys/netgraph/ng_echo.c).
	t.Cleanup(func() { _ = s.RmHook(".:", "e0") })

	want := []byte("netgraph round trip")
	if err := s.WriteData("e0", want); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	buf := make([]byte, 256)
	n, hook, err := s.ReadData(buf, 2*time.Second)
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if hook != "e0" {
		t.Fatalf("frame came back on hook %q, want e0", hook)
	}
	if !bytes.Equal(buf[:n], want) {
		t.Fatalf("got %q, want %q", buf[:n], want)
	}
}

func TestReadDataTimesOut(t *testing.T) {
	needRoot(t)
	s := openForTest(t)
	buf := make([]byte, 64)
	start := time.Now()
	if _, _, err := s.ReadData(buf, 200*time.Millisecond); !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout with no traffic, got %v", err)
	}
	// The deadline must actually bound the call, not merely be reported
	// after an unbounded wait ended for some other reason.
	if d := time.Since(start); d > time.Second {
		t.Fatalf("ReadData took %v; the deadline was 200ms", d)
	}
}

// A frame larger than the caller's buffer must be reported, not silently
// clipped: recvfrom(2) on a datagram socket throws the excess away and
// tells nobody, so a caller with a short buffer would otherwise get a full
// buffer and no indication that the rest of the frame is gone.
func TestReadDataReportsAFrameTooLargeForTheBuffer(t *testing.T) {
	needRoot(t)
	s := openForTest(t)

	if err := s.MkPeer(".:", "echo", "e1", "in"); err != nil {
		t.Fatalf("MkPeer echo: %v", err)
	}
	t.Cleanup(func() { _ = s.RmHook(".:", "e1") })

	want := bytes.Repeat([]byte("A"), 500)
	if err := s.WriteData("e1", want); err != nil {
		t.Fatalf("WriteData: %v", err)
	}

	small := make([]byte, 100)
	n, hook, err := s.ReadData(small, 2*time.Second)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated for a 500-byte frame into a 100-byte buffer, got n=%d hook=%q err=%v", n, hook, err)
	}
	if hook != "e1" {
		t.Errorf("hook = %q, want e1 — the hook must still be reported alongside the truncation", hook)
	}
	if n != len(small) {
		t.Errorf("n = %d, want %d (the bytes that did fit)", n, len(small))
	}
	if !bytes.Equal(small, want[:len(small)]) {
		t.Errorf("the bytes that fit are not the head of the frame")
	}
}

func TestWriteDataRejectsUnusableArguments(t *testing.T) {
	needRoot(t)
	s := openForTest(t)

	if err := s.WriteData("", []byte("x")); err == nil {
		t.Error("an empty hook name must be rejected before it reaches the kernel")
	}
	if err := s.WriteData("nosuchhook", nil); err == nil {
		t.Error("an empty frame must be rejected")
	}
}
