//go:build freebsd && integration

package netgraph

import (
	"errors"
	"strings"
	"testing"
)

// needRoot is declared once for this package, in client_integration_test.go.
// The plan's Task 3 listing repeats it verbatim; declaring it again here
// would be a duplicate declaration and would not compile.

// openForTest opens an anonymous socket node and arranges for it to be
// closed when the test finishes.
//
// The socket is closed from a t.Cleanup rather than a defer, and this is
// load-bearing rather than stylistic. Deferred calls in a test function
// body run when that function returns, which is BEFORE any registered
// cleanup runs. The plan's test listings pair `defer s.Close()` with
// `t.Cleanup(func() { _ = s.Shutdown(...) })`, which means the socket is
// already closed by the time the shutdown is attempted, the shutdown fails
// with EBADF, and the error is discarded — so the node survives the test.
// That is not hypothetical: written that way, TestConnectAndPeerInfo left
// ngtestca and ngtestcb behind and the next run failed with EADDRINUSE on
// NameNode, which is exactly the "leaked node breaks the NEXT run" failure
// this cleanup exists to prevent. Registering the close first makes it run
// last, because cleanups run in LIFO order.
func openForTest(t *testing.T) *Socket {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// Build a small graph, inspect it, tear it down. ng_hole discards whatever
// it receives, which makes it the safest peer for a test.
func TestGraphLifecycle(t *testing.T) {
	needRoot(t)
	s := openForTest(t)

	if err := s.MkPeer(".:", "hole", "h0", "left"); err != nil {
		t.Fatalf("MkPeer: %v", err)
	}
	if err := s.NameNode(".:h0", "ngtesthole"); err != nil {
		t.Fatalf("NameNode: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown("ngtesthole:") })

	ni, err := s.NodeInfo("ngtesthole:")
	if err != nil {
		t.Fatalf("NodeInfo: %v", err)
	}
	if ni.Name != "ngtesthole" || ni.Type != "hole" {
		t.Fatalf("NodeInfo = %+v", ni)
	}

	_, hooks, err := s.ListHooks("ngtesthole:")
	if err != nil {
		t.Fatalf("ListHooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].Name != "left" {
		t.Fatalf("ListHooks = %+v", hooks)
	}

	nodes, err := s.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	var found bool
	for _, n := range nodes {
		if n.Name == "ngtesthole" {
			found = true
		}
	}
	if !found {
		t.Fatal("the named node did not appear in ListNodes")
	}

	if err := s.Shutdown("ngtesthole:"); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, err := s.NodeInfo("ngtesthole:"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("node should be gone, got %v", err)
	}
}

// Connect and the peer half of a HookInfo are not covered by the plan's
// tests, and the peer fields are the ones whose offsets are easiest to get
// wrong: peerhook sits at 32 inside struct linkinfo and the peer's
// nodeinfo at 64, so a decoder off by one field still produces
// plausible-looking strings. This builds a graph where every peer field
// has a distinct, checkable value.
func TestConnectAndPeerInfo(t *testing.T) {
	needRoot(t)
	s := openForTest(t)

	if err := s.MkPeer(".:", "hole", "ca", "left"); err != nil {
		t.Fatalf("MkPeer a: %v", err)
	}
	if err := s.NameNode(".:ca", "ngtestca"); err != nil {
		t.Fatalf("NameNode a: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown("ngtestca:") })

	if err := s.MkPeer(".:", "hole", "cb", "left"); err != nil {
		t.Fatalf("MkPeer b: %v", err)
	}
	if err := s.NameNode(".:cb", "ngtestcb"); err != nil {
		t.Fatalf("NameNode b: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown("ngtestcb:") })

	bInfo, err := s.NodeInfo("ngtestcb:")
	if err != nil {
		t.Fatalf("NodeInfo b: %v", err)
	}

	if err := s.Connect("ngtestca:", "toB", "ngtestcb:", "toA"); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ni, hooks, err := s.ListHooks("ngtestca:")
	if err != nil {
		t.Fatalf("ListHooks: %v", err)
	}
	if ni.Name != "ngtestca" || ni.NumHooks != 2 {
		t.Fatalf("nodeinfo = %+v, want name ngtestca with 2 hooks", ni)
	}
	if len(hooks) != 2 {
		t.Fatalf("got %d hooks, want 2: %+v", len(hooks), hooks)
	}

	var link *HookInfo
	for i := range hooks {
		if hooks[i].Name == "toB" {
			link = &hooks[i]
		}
	}
	if link == nil {
		t.Fatalf("no hook named toB in %+v", hooks)
	}
	if link.PeerHook != "toA" {
		t.Errorf("PeerHook = %q, want toA", link.PeerHook)
	}
	if link.PeerName != "ngtestcb" {
		t.Errorf("PeerName = %q, want ngtestcb", link.PeerName)
	}
	if link.PeerType != "hole" {
		t.Errorf("PeerType = %q, want hole", link.PeerType)
	}
	if link.PeerID != bInfo.ID {
		t.Errorf("PeerID = %#x, want %#x (ngtestcb's own ID)", link.PeerID, bInfo.ID)
	}
}

// A 31-character node name is the longest ValidateNodeName allows
// (NG_NODESIZ-1), and addressing it by name means sending the path
// "<name>:" — 32 bytes, exactly ngMaxPathLen. This is the end-to-end check
// that the encoding cSendMsg now uses (sg_len = ngsaOverhead + len(path),
// no NUL counted in sg_data) really is accepted by a live kernel and
// resolves to the right node, not just that ValidatePath's arithmetic
// allows it through.
func TestNodeInfoOverA32ByteAddress(t *testing.T) {
	needRoot(t)
	s := openForTest(t)

	name := strings.Repeat("z", ngNodeSiz-1) // 31 bytes
	path := name + ":"                       // 32 bytes
	if len(path) != ngMaxPathLen {
		t.Fatalf("test setup: path %q is %d bytes, want exactly ngMaxPathLen=%d", path, len(path), ngMaxPathLen)
	}

	if err := s.MkPeer(".:", "hole", "mx", "left"); err != nil {
		t.Fatalf("MkPeer: %v", err)
	}
	if err := s.NameNode(".:mx", name); err != nil {
		t.Fatalf("NameNode: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(path) })

	ni, err := s.NodeInfo(path)
	if err != nil {
		t.Fatalf("NodeInfo(%q): %v", path, err)
	}
	if ni.Name != name || ni.Type != "hole" {
		t.Fatalf("NodeInfo(%q) = %+v, want Name=%q Type=hole", path, ni, name)
	}
}

// ListNames and ListTypes share decodeNodeList/decodeTypeList with
// ListNodes but not their response layout, so they need their own
// exercise against a live kernel.
func TestListNamesAndListTypes(t *testing.T) {
	needRoot(t)
	s := openForTest(t)

	if err := s.MkPeer(".:", "hole", "ln", "left"); err != nil {
		t.Fatalf("MkPeer: %v", err)
	}
	if err := s.NameNode(".:ln", "ngtestnamed"); err != nil {
		t.Fatalf("NameNode: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown("ngtestnamed:") })

	names, err := s.ListNames()
	if err != nil {
		t.Fatalf("ListNames: %v", err)
	}
	var found bool
	for _, n := range names {
		if n.Name == "" {
			t.Errorf("ListNames returned an unnamed node: %+v", n)
		}
		if n.Name == "ngtestnamed" {
			if n.Type != "hole" {
				t.Errorf("ngtestnamed has type %q, want hole", n.Type)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("ngtestnamed missing from ListNames: %+v", names)
	}

	types, err := s.ListTypes()
	if err != nil {
		t.Fatalf("ListTypes: %v", err)
	}
	var sawSocket, sawHole bool
	for _, ty := range types {
		if ty.Name == "" {
			t.Errorf("ListTypes returned a nameless type: %+v", ty)
		}
		switch ty.Name {
		case "socket":
			sawSocket = true
			if ty.NumNodes == 0 {
				t.Error("type socket reports 0 live nodes, but this test holds one open")
			}
		case "hole":
			sawHole = true
		}
	}
	if !sawSocket || !sawHole {
		t.Fatalf("ListTypes missing socket=%v hole=%v: %+v", sawSocket, sawHole, types)
	}
}
