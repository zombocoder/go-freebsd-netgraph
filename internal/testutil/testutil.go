// Package testutil holds helpers shared by this module's integration
// tests. It currently holds one: the root check every netgraph integration
// test needs, since creating a netgraph socket node touches the kernel and
// is refused otherwise. Cleanup helpers for the graph-building tests in
// later tasks belong here too, so that test files depend on one shared
// implementation instead of each redeclaring their own.
package testutil

import (
	"os"
	"testing"
)

// NeedRoot skips the calling test unless the process is running as root.
func NeedRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("netgraph needs root")
	}
}
