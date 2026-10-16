// Command ng-list enumerates the netgraph nodes and node types the kernel
// currently has, using nothing but read-only generic control messages.
//
// It creates exactly one thing — the anonymous socket node every netgraph
// client needs in order to talk to the kernel at all — and Close tears that
// down again, including on every error path below. Run it twice; the second
// run must look like the first.
//
// Needs root: netgraph control sockets do.
//
//	doas go run ./examples/list
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/zombocoder/go-freebsd-netgraph/netgraph"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ng-list:", err)
		if errors.Is(err, os.ErrPermission) {
			fmt.Fprintln(os.Stderr, "hint: netgraph needs root — try doas")
		}
		os.Exit(1)
	}
}

func run() error {
	// "" asks for an anonymous node. Nothing else in this program creates a
	// node, so this single deferred Close is the whole cleanup story.
	s, err := netgraph.Open("")
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer s.Close()

	fmt.Printf("this program's own socket node: %s\n\n", s.NodeName())

	nodes, err := s.ListNodes()
	if err != nil {
		return fmt.Errorf("listnodes: %w", err)
	}
	fmt.Printf("%d nodes:\n", len(nodes))
	for _, n := range nodes {
		name := n.Name
		if name == "" {
			// An unnamed node is addressable only by its numeric ID, in
			// the [hex] form the kernel and ngctl(8) both use.
			name = fmt.Sprintf("[%x]", n.ID)
		}
		fmt.Printf("  %-20s type=%-12s id=0x%08x hooks=%d\n", name, n.Type, n.ID, n.NumHooks)
	}

	named, err := s.ListNames()
	if err != nil {
		return fmt.Errorf("listnames: %w", err)
	}
	fmt.Printf("\nglobally named nodes (%d):\n", len(named))
	for _, n := range named {
		fmt.Printf("  %-20s type=%-12s id=0x%08x hooks=%d\n", n.Name, n.Type, n.ID, n.NumHooks)
	}

	types, err := s.ListTypes()
	if err != nil {
		return fmt.Errorf("listtypes: %w", err)
	}
	fmt.Printf("\n%d node types loaded:\n", len(types))
	for _, t := range types {
		fmt.Printf("  %-16s nodes=%d\n", t.Name, t.NumNodes)
	}

	return nil
}
