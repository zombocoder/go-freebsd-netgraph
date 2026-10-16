//go:build freebsd && integration

package netgraph

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zombocoder/go-freebsd-netgraph/internal/testutil"
)

// needRoot skips the calling test unless running as root; every test below
// creates a real netgraph socket node, which requires it. Delegates to
// internal/testutil so this check has one implementation shared across this
// package's integration test files rather than being redeclared in each
// (the plan's Task 2 and Task 3 test listings each define their own
// needRoot verbatim, which would collide as duplicate declarations in the
// same package — see docs/task2-report.md).
func needRoot(t *testing.T) {
	t.Helper()
	testutil.NeedRoot(t)
}

func TestOpenAndClose(t *testing.T) {
	needRoot(t)
	s, err := Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close must be safe to call twice — defer Close() alongside an explicit
	// Close() is a normal Go shape and must not panic or error.
	if err := s.Close(); err != nil {
		t.Fatalf("second Close should be a no-op, got %v", err)
	}
}

func TestCallReturnsTheMatchingReply(t *testing.T) {
	needRoot(t)
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	m, err := s.Call(".:", GenericCookie, NGM_NODEINFO, nil, 2*time.Second)
	if err != nil {
		t.Fatalf("Call NODEINFO: %v", err)
	}
	if m.Flags&NGF_RESP == 0 {
		t.Fatalf("expected a response message, flags=%#x", m.Flags)
	}
	if len(m.Arg) == 0 {
		t.Fatal("NODEINFO returned an empty payload")
	}
}

// The property the library exists to guarantee. Two calls are issued
// back to back; if replies were handed out in arrival order without token
// matching, the second Call would receive the first call's answer.
func TestCallMatchesTokensUnderInterleaving(t *testing.T) {
	needRoot(t)
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Send one request without collecting it, leaving a reply queued.
	tok1, err := s.Send(".:", GenericCookie, NGM_NODEINFO, nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let the reply land

	// Now a Call must return ITS OWN reply, not the queued one.
	m, err := s.Call(".:", GenericCookie, NGM_LISTTYPES, nil, 2*time.Second)
	if err != nil {
		t.Fatalf("Call LISTTYPES: %v", err)
	}
	if m.Token == Token(tok1) {
		t.Fatal("Call returned the earlier request's reply — token matching is broken")
	}
	if m.Cmd != NGM_LISTTYPES {
		t.Fatalf("Call returned a reply to cmd %#x, want LISTTYPES %#x", m.Cmd, NGM_LISTTYPES)
	}
}

func TestRecvTimesOutRatherThanBlocking(t *testing.T) {
	needRoot(t)
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	start := time.Now()
	_, err = s.Recv(Token(0xdeadbeef), 200*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout for a reply that will never come, got %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Recv took %v; the deadline was 200ms", d)
	}
}

// Defect 1 evidence: Send must not block waiting for a reply, even for a
// command that carries NGM_HASREPLY. If Send still routed through
// NgSendMsg (NgDeliverMsg in msg.c), it would poll(POLLIN, INFTIM) after
// its sendto succeeds — an unbounded wait no deadline in this package
// could interrupt. NGM_NODEINFO carries NGM_HASREPLY, so it exercises
// exactly that path.
//
// A timing assertion cannot actually catch a regression here: netgraph
// delivers a generic control message synchronously inside the sendto(2)
// call itself, so by the time NgDeliverMsg's poll(&rfds, 1, INFTIM) runs,
// the socket is already readable and the poll returns immediately — no
// generic command sent to a socket node can ever make that poll actually
// wait. A previous version of this test asserted only "Send returns within
// 50ms", which passed even with cSendMsg reverted to call NgSendMsg
// (verified: PASS in 0.12s with the revert in place). The 50ms check below
// is kept as a sanity floor — it documents the expectation and would catch
// a gross unrelated slowdown — but it is not what makes this test fail if
// cSendMsg is "simplified" back to NgSendMsg.
//
// The behavioural difference between the two implementations that IS
// observable: with the fix, the token Send returns comes from this
// package's own nextToken() counter (lastToken in cgo_freebsd.go); routed
// through NgSendMsg, it instead comes from libnetgraph's process-global
// gMsgId, and our own counter never advances. So this test reads lastToken
// before calling Send and requires the returned token to be exactly what
// our allocator would hand out next — written so it does not assume this
// is the first Send in the process, since other tests in the suite also
// call Send/Call and advance the same counter. This assertion, not the
// timing one, is what stands between this package and a silent
// reintroduction of the poll(INFTIM) hang.
//
// Two properties both matter for the second half: Send must return
// promptly (it must not itself be doing a receive), AND the reply it
// triggered must still be sitting on the socket, retrievable by a later
// Recv with the same token — a "fix" that simply dropped the reply instead
// of waiting for it would also make Send return fast, which is why both
// are checked.
func TestSendAllocatesTokenFromOwnCounter(t *testing.T) {
	needRoot(t)
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	prev := atomic.LoadUint32(&lastToken)
	wantTok := Token((prev + 1) & 0x7fffffff)

	start := time.Now()
	tok, err := s.Send(".:", GenericCookie, NGM_NODEINFO, nil)
	sendElapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// A single sendto(2) on a local socket; generous but nowhere near what
	// an INFTIM poll waiting on a reply would look like. See the comment
	// above: this alone cannot detect the regression this test guards
	// against, which is why the token check below also runs.
	if sendElapsed > 50*time.Millisecond {
		t.Fatalf("Send took %v; it must return immediately after sendto(2), not wait for a reply", sendElapsed)
	}
	if tok != wantTok {
		t.Fatalf("Send returned token %d, want %d (this package's own nextToken() counter advanced by exactly one); a token that does not match means cSendMsg is no longer allocating from lastToken — most likely because it has been routed back through NgSendMsg and libnetgraph's gMsgId", tok, wantTok)
	}

	// Give the kernel time to deliver the reply into our receive queue
	// without us having read it yet, proving Send did not consume it.
	time.Sleep(100 * time.Millisecond)

	m, err := s.Recv(tok, 2*time.Second)
	if err != nil {
		t.Fatalf("Recv after Send: %v", err)
	}
	if m.Token != tok {
		t.Fatalf("Recv returned token %d, want %d", m.Token, tok)
	}
	if m.Flags&NGF_RESP == 0 {
		t.Fatalf("expected a response message, flags=%#x", m.Flags)
	}
}

func TestCallOnMissingNodeIsNotFound(t *testing.T) {
	needRoot(t)
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Call("nosuchnode99:", GenericCookie, NGM_NODEINFO, nil, time.Second)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
