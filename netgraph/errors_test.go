package netgraph

import (
	"errors"
	"strings"
	"syscall"
	"testing"
)

func TestErrnoMapping(t *testing.T) {
	cases := []struct {
		errno syscall.Errno
		want  error
	}{
		{syscall.ENOENT, ErrNotFound},
		{syscall.EEXIST, ErrExists},
		{syscall.ESTALE, ErrStale},
	}
	for _, c := range cases {
		got := errnoToError("mkpeer", "foo:", c.errno)
		if !errors.Is(got, c.want) {
			t.Errorf("errno %v mapped to %v, want %v", c.errno, got, c.want)
		}
		// The context must survive: an error that says only "no such file"
		// is useless when four different paths could have produced it.
		if !strings.Contains(got.Error(), "mkpeer") || !strings.Contains(got.Error(), "foo:") {
			t.Errorf("error lost its operation or path context: %v", got)
		}
	}
}

func TestUnmappedErrnoIsStillWrapped(t *testing.T) {
	got := errnoToError("connect", "x:", syscall.EPERM)
	if !errors.Is(got, syscall.EPERM) {
		t.Errorf("an unmapped errno must remain inspectable: %v", got)
	}
}
