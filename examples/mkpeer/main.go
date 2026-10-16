// Command ng-mkpeer builds a small netgraph graph, inspects it, and tears
// it down again — and demonstrates the ASCII brace syntax the kernel's
// parser actually requires.
//
// Two ng_hole nodes are created, one through the binary MkPeer call and one
// through ASCII text, then wired to each other and shut down. Every node
// this program creates is shut down again on every path out of run(),
// because a leaked netgraph node outlives the process: the next run would
// fail with EEXIST on the name and look like a flaky test rather than
// yesterday's mess.
//
// Needs root.
//
//	doas go run ./examples/mkpeer
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/zombocoder/go-freebsd-netgraph/netgraph"
)

const (
	nodeA = "ngexhole1"
	nodeB = "ngexhole2"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ng-mkpeer:", err)
		os.Exit(1)
	}
}

func run() error {
	s, err := netgraph.Open("")
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	// Registered first, so under LIFO it runs last: every node-level
	// teardown below gets to use the socket before it is closed.
	defer s.Close()

	fmt.Printf("socket node: %s\n\n", s.NodeName())

	// ---------------------------------------------------------------
	// 1. The binary form: a typed call, arguments marshalled in Go.
	// ---------------------------------------------------------------
	if err := s.MkPeer(".:", "hole", "a", "left"); err != nil {
		return fmt.Errorf("mkpeer (binary): %w", err)
	}
	if err := s.NameNode(".:a", nodeA); err != nil {
		_ = s.RmHook(".:", "a")
		return fmt.Errorf("name %s: %w", nodeA, err)
	}
	defer shutdown(s, nodeA)
	fmt.Printf("MkPeer(\".:\", \"hole\", \"a\", \"left\") -> named %s\n", nodeA)

	// ---------------------------------------------------------------
	// 2. The ASCII form. Note the braces.
	//
	// The kernel converts ASCII arguments with ng_parse. Every generic
	// command whose argument is a structure — mkpeer, connect, name,
	// rmhook — goes through ng_struct_parse, and the first thing that
	// does is demand an opening brace (ng_parse.c:1220):
	//
	//	if (ng_parse_get_token(...) != T_LBRACE) { error = EINVAL; }
	//
	// So this works:
	//
	//	mkpeer { type="hole" ourhook="b" peerhook="left" }
	//
	// and this does not:
	//
	//	mkpeer hole b left
	//
	// even though the second is exactly what you type at ngctl(8). ngctl
	// accepts it because ngctl never sends it: its mkpeer command
	// (usr.sbin/ngctl/mkpeer.c:61,81) snprintf()s av[1..3] into a
	// struct ngm_mkpeer in userland and sends the *binary* message with
	// NgSendMsg. The positional form is ngctl's command-line syntax, not
	// netgraph's ASCII syntax — and the kernel's only complaint about the
	// difference is a bare EINVAL with no hint. Step 3 below shows it.
	// ---------------------------------------------------------------
	const asciiMkPeer = `mkpeer { type="hole" ourhook="b" peerhook="left" }`
	if _, err := s.CallAscii(".:", asciiMkPeer, 2*time.Second); err != nil {
		return fmt.Errorf("mkpeer (ascii, brace form): %w", err)
	}
	if err := s.NameNode(".:b", nodeB); err != nil {
		_ = s.RmHook(".:", "b")
		return fmt.Errorf("name %s: %w", nodeB, err)
	}
	defer shutdown(s, nodeB)
	fmt.Printf("CallAscii(\".:\", `%s`) -> named %s\n", asciiMkPeer, nodeB)

	// ---------------------------------------------------------------
	// 3. The same command without braces, for contrast. This is expected
	//    to fail; a success here would mean the kernel parser changed and
	//    the documentation needs revisiting.
	// ---------------------------------------------------------------
	const asciiPositional = `mkpeer hole c left`
	_, err = s.CallAscii(".:", asciiPositional, 2*time.Second)
	if err == nil {
		_ = s.RmHook(".:", "c")
		return fmt.Errorf("positional ASCII form `%s` unexpectedly succeeded — "+
			"the kernel parser no longer requires braces and the docs are stale", asciiPositional)
	}
	fmt.Printf("CallAscii(\".:\", `%s`) -> rejected, as it must be:\n    %v\n", asciiPositional, err)

	// ---------------------------------------------------------------
	// 4. Wire the two peers together and read the graph back.
	// ---------------------------------------------------------------
	if err := s.Connect(nodeA+":", "toB", nodeB+":", "toA"); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	fmt.Printf("\nConnect(%q, \"toB\", %q, \"toA\")\n\n", nodeA+":", nodeB+":")

	for _, name := range []string{nodeA, nodeB} {
		ni, hooks, err := s.ListHooks(name + ":")
		if err != nil {
			return fmt.Errorf("listhooks %s: %w", name, err)
		}
		fmt.Printf("%s: type=%s id=0x%08x hooks=%d\n", ni.Name, ni.Type, ni.ID, ni.NumHooks)
		for _, h := range hooks {
			peer := h.PeerName
			if peer == "" {
				peer = fmt.Sprintf("[%x]", h.PeerID)
			}
			fmt.Printf("    %-6s -> %s:%s (type %s)\n", h.Name, peer, h.PeerHook, h.PeerType)
		}
	}

	// ---------------------------------------------------------------
	// 5. Explicit teardown, so the normal path does not rely on the
	//    deferred cleanup. The deferred calls are still registered and
	//    still run; shutdown() tolerates an already-gone node.
	// ---------------------------------------------------------------
	fmt.Println()
	for _, name := range []string{nodeA, nodeB} {
		if err := s.Shutdown(name + ":"); err != nil {
			return fmt.Errorf("shutdown %s: %w", name, err)
		}
		if _, err := s.NodeInfo(name + ":"); !errors.Is(err, netgraph.ErrNotFound) {
			return fmt.Errorf("%s should be gone after shutdown, NodeInfo gave: %v", name, err)
		}
		fmt.Printf("shut down %s, confirmed gone\n", name)
	}

	return nil
}

// shutdown tears a node down and ignores ErrNotFound, so it is safe both as
// an error-path cleanup and as a no-op after the explicit teardown above.
func shutdown(s *netgraph.Socket, name string) {
	if err := s.Shutdown(name + ":"); err != nil && !errors.Is(err, netgraph.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "ng-mkpeer: cleanup: shutdown %s: %v\n", name, err)
	}
}
