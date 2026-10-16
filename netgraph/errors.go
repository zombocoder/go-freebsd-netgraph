package netgraph

import (
	"errors"
	"fmt"
	"syscall"
)

// Sentinel errors. Use errors.Is to test for these; an Error value returned
// by this package always wraps one of them (or an unmapped errno) so that
// works regardless of how the error was constructed.
var (
	// ErrTimeout is returned when a receive did not complete before its
	// deadline. No call in this package blocks past the timeout it is given.
	ErrTimeout = errors.New("netgraph: timeout")

	// ErrNotFound corresponds to ENOENT: no such node, hook or path.
	ErrNotFound = errors.New("netgraph: no such node or hook")

	// ErrExists corresponds to EEXIST: the name or hook is already in use.
	ErrExists = errors.New("netgraph: already exists")

	// ErrStale corresponds to ESTALE: the node existed when the path was
	// resolved but is gone by the time the operation reached it.
	ErrStale = errors.New("netgraph: stale")

	// ErrTruncated is returned when a response's arglen describes more data
	// than the receive buffer actually holds, rather than handing back a
	// short buffer a caller would read out of bounds.
	ErrTruncated = errors.New("netgraph: response truncated")

	// ErrNotSupported is returned by every operation on platforms other
	// than FreeBSD, where netgraph does not exist.
	ErrNotSupported = errors.New("netgraph: not supported on this platform")
)

// Error carries the failing operation's context alongside the underlying
// error, so a caller sees which call and which path failed rather than a
// bare "no such file or directory" that could have come from any of them.
type Error struct {
	Op   string // the operation being performed, e.g. "mkpeer"
	Path string // the netgraph path involved, e.g. "foo:"
	Err  error  // the underlying error: a sentinel above, or a raw errno
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("netgraph: %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("netgraph: %s %s: %v", e.Op, e.Path, e.Err)
}

// Unwrap makes Error transparent to errors.Is and errors.As, so callers can
// test against the sentinel errors above or against a raw syscall.Errno for
// anything this package does not map to one of them.
func (e *Error) Unwrap() error {
	return e.Err
}

// errnoToError wraps errno in an *Error that names the operation and path,
// mapping the errnos netgraph callers care about onto this package's
// sentinel errors. An errno with no specific mapping is still wrapped, so it
// remains inspectable with errors.Is(err, thatErrno).
func errnoToError(op, path string, errno syscall.Errno) error {
	var mapped error = errno
	switch errno {
	case syscall.ENOENT:
		mapped = ErrNotFound
	case syscall.EEXIST:
		mapped = ErrExists
	case syscall.ESTALE:
		mapped = ErrStale
	}
	return &Error{Op: op, Path: path, Err: mapped}
}
