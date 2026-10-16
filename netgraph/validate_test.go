package netgraph

import (
	"strings"
	"testing"
)

func TestValidateNodeName(t *testing.T) {
	if err := ValidateNodeName(""); err == nil {
		t.Error("empty node name must be rejected")
	}
	if err := ValidateNodeName(strings.Repeat("a", 31)); err != nil {
		t.Errorf("31 bytes fits in NG_NODESIZ=32 with the terminator: %v", err)
	}
	if err := ValidateNodeName(strings.Repeat("a", 32)); err == nil {
		t.Error("32 bytes leaves no room for the terminator and must be rejected")
	}
	// A dot separates a node name from a hook in a path; a colon terminates
	// the node part. Either inside a name makes the path unparseable.
	for _, bad := range []string{"a.b", "a:b"} {
		if err := ValidateNodeName(bad); err == nil {
			t.Errorf("node name %q contains a path separator and must be rejected", bad)
		}
	}
}

func TestValidateHookName(t *testing.T) {
	if err := ValidateHookName(strings.Repeat("h", 31)); err != nil {
		t.Errorf("31 bytes fits NG_HOOKSIZ=32: %v", err)
	}
	if err := ValidateHookName(strings.Repeat("h", 32)); err == nil {
		t.Error("32 bytes must be rejected")
	}
	if err := ValidateHookName(""); err == nil {
		t.Error("empty hook name must be rejected")
	}
}

// The real limit is not NG_PATHSIZ (512): the path travels inside a struct
// sockaddr_ng, and ngc_send (ng_socket.c) rejects the send with EINVAL,
// before the path is even parsed, once sg_len exceeds
// NG_NODESIZ+offsetof(sg_data). With cSendMsg's encoding (no NUL counted in
// sg_data — ngc_send appends its own), that allows a path of up to 32
// bytes. See ngMaxPathLen in validate.go.
func TestValidatePathUsesRealLimit(t *testing.T) {
	if err := ValidatePath(strings.Repeat("p", 32)); err != nil {
		t.Errorf("32 bytes is accepted by ngc_send (ng_socket.c): %v", err)
	}
	if err := ValidatePath(strings.Repeat("p", 33)); err == nil {
		t.Error("33 bytes is rejected by ngc_send (ng_socket.c) with EINVAL and must be rejected here too")
	}
}
