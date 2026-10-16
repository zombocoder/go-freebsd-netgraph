// Command ng-data sends frames on a netgraph hook and reads them back.
//
// It attaches an ng_echo node — which returns on the same hook whatever it
// is sent — to this program's own socket node, writes a few frames, and
// reads each one back. Then it shows the other half of the guarantee: a
// read with nothing to read returns ErrTimeout at its deadline instead of
// blocking.
//
// The echo node is removed on every path out of run(). ng_echo shuts itself
// down when its last hook goes, so removing the hook is enough; the
// deferred Close would eventually do it too, but relying on that would mean
// relying on luck the moment a node grows a second hook.
//
// Needs root.
//
//	doas go run ./examples/data
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/zombocoder/go-freebsd-netgraph/netgraph"
)

// The hook on our own socket node that the echo node hangs off. Frames are
// addressed to this name on the way out and arrive tagged with it coming
// back, because a socket node's data frames are addressed by *our* hook, not
// the peer's.
const ourHook = "e0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ng-data:", err)
		os.Exit(1)
	}
}

func run() error {
	s, err := netgraph.Open("")
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer s.Close()

	fmt.Printf("socket node: %s\n", s.NodeName())

	// ng_echo needs no configuration: it reflects whatever arrives.
	if err := s.MkPeer(".:", "echo", ourHook, "in"); err != nil {
		return fmt.Errorf("mkpeer echo: %w", err)
	}
	defer func() {
		if err := s.RmHook(".:", ourHook); err != nil && !errors.Is(err, netgraph.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "ng-data: cleanup: rmhook %s: %v\n", ourHook, err)
		}
	}()
	fmt.Printf("attached an ng_echo node on hook %q\n\n", ourHook)

	frames := [][]byte{
		[]byte("netgraph round trip"),
		[]byte("second frame, a little longer than the first"),
		bytes.Repeat([]byte{0xAB}, 64), // binary, not text
	}

	buf := make([]byte, 512)
	for i, want := range frames {
		if err := s.WriteData(ourHook, want); err != nil {
			return fmt.Errorf("writedata frame %d: %w", i, err)
		}
		n, hook, err := s.ReadData(buf, 2*time.Second)
		if err != nil {
			return fmt.Errorf("readdata frame %d: %w", i, err)
		}
		got := buf[:n]
		if hook != ourHook || !bytes.Equal(got, want) {
			return fmt.Errorf("frame %d came back as %d bytes on hook %q, want %d bytes on %q",
				i, n, hook, len(want), ourHook)
		}
		fmt.Printf("frame %d: wrote %2d bytes, read %2d bytes back on hook %q — identical (%s)\n",
			i, len(want), n, hook, preview(got))
	}

	// The deadline guarantee, shown rather than asserted. Nothing is going
	// to arrive: the echo node only speaks when spoken to.
	fmt.Println()
	start := time.Now()
	_, _, err = s.ReadData(buf, 250*time.Millisecond)
	elapsed := time.Since(start)
	if !errors.Is(err, netgraph.ErrTimeout) {
		return fmt.Errorf("idle ReadData returned %v, want ErrTimeout", err)
	}
	fmt.Printf("ReadData with nothing to read returned after %v: %v\n", elapsed.Round(time.Millisecond), err)

	return nil
}

// preview renders a frame compactly: as a quoted string if it is printable
// ASCII, otherwise as a byte count and its first byte in hex.
func preview(b []byte) string {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return fmt.Sprintf("%d binary bytes, first 0x%02x", len(b), b[0])
		}
	}
	if len(b) > 24 {
		return fmt.Sprintf("%q...", b[:24])
	}
	return fmt.Sprintf("%q", b)
}
