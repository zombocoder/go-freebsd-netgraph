//go:build freebsd

package netgraph

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"
)

// defaultTimeout bounds every generic operation below that waits for a
// reply. The design's signatures for these operations take no timeout
// parameter, but this package guarantees that nothing blocks forever, so
// they need one anyway; five seconds is far beyond what a local, in-kernel
// generic message takes (measured: well under a millisecond) while still
// being short enough that a wedged call surfaces as an error rather than a
// hang. Use Call directly if you need a different deadline.
const defaultTimeout = 5 * time.Second

// Wire layout of the generic-message argument structures, in bytes.
//
// Every offset and size below was verified by compiling an offsetof(3)
// probe against /usr/include/netgraph/ng_message.h on this host rather than
// derived on paper (FreeBSD 14.3-RELEASE-p3, amd64); see
// docs/task3-report.md for the probe and its output. NG_NODESIZ,
// NG_TYPESIZ and NG_HOOKSIZ are all 32 and ng_ID_t is uint32_t, so none of
// these structures needs compiler padding — but that is a fact to check,
// not to assume, which is why the probe exists.
const (
	// struct nodeinfo { char name[32]; char type[32]; ng_ID_t id;
	//                   u_int32_t hooks; }
	nodeInfoSize = 72
	niNameOff    = 0
	niTypeOff    = 32
	niIDOff      = 64
	niHooksOff   = 68

	// struct linkinfo { char ourhook[32]; char peerhook[32];
	//                   struct nodeinfo nodeinfo; }
	linkInfoSize  = 136
	liOurHookOff  = 0
	liPeerHookOff = 32
	liNodeInfoOff = 64

	// struct hooklist { struct nodeinfo nodeinfo; struct linkinfo link[]; }
	// The number of link[] entries is nodeinfo.hooks: NGM_LISTHOOKS in
	// ng_base.c resets ni->hooks to 0 and increments it once per hook it
	// actually writes, so it counts entries present, not hooks the node
	// claims to have.
	hlNodeInfoOff = 0
	hlLinkOff     = 72

	// struct namelist { u_int32_t numnames; struct nodeinfo nodeinfo[]; }
	// Used by both NGM_LISTNODES and NGM_LISTNAMES.
	nlCountOff = 0
	nlArrayOff = 4

	// struct typeinfo { char type_name[32]; u_int32_t numnodes; }
	typeInfoSize  = 36
	tiNameOff     = 0
	tiNumNodesOff = 32

	// struct typelist { u_int32_t numtypes; struct typeinfo typeinfo[]; }
	tlCountOff = 0
	tlArrayOff = 4

	// Argument structures this package builds and sends.
	//
	// struct ngm_mkpeer { char type[32]; char ourhook[32];
	//                     char peerhook[32]; }
	mkPeerSize        = 96
	mkPeerTypeOff     = 0
	mkPeerOurHookOff  = 32
	mkPeerPeerHookOff = 64

	// struct ngm_connect { char path[512]; char ourhook[32];
	//                      char peerhook[32]; }
	// Note the path field is 512 bytes (NG_PATHSIZ), not 32: a connect's
	// peer path travels inside the message payload, not inside the
	// sockaddr_ng that addresses it, so it is bounded by NG_PATHSIZ rather
	// than by sg_len. See validateEmbeddedPath.
	connectSize        = 576
	connectPathOff     = 0
	connectOurHookOff  = 512
	connectPeerHookOff = 544

	// struct ngm_name { char name[32]; }
	nameSize = 32

	// struct ngm_rmhook { char ourhook[32]; }
	rmHookSize = 32
)

// cstring converts a fixed-width, NUL-terminated C character array into a
// Go string, stopping at the first NUL.
//
// Trimming matters: without it a name comes back as "hole\x00\x00..." and
// every comparison against it fails in a way that looks like a kernel bug.
// It stops at the FIRST NUL rather than trimming trailing NULs because the
// bytes after the terminator are not guaranteed to be zero — ngctl(8)'s own
// mkpeer.c, for instance, snprintf()s into an uninitialised struct
// ngm_mkpeer and ships whatever stack bytes followed each terminator — so
// "trim the trailing zeros" can leave garbage attached to a valid name.
func cstring(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

// validateEmbeddedPath checks a path that travels inside a message payload
// rather than inside the sockaddr_ng that addresses the message.
//
// ValidatePath applies the sg_len limit, which is correct for a
// destination path but wrong here: NGM_CONNECT's peer path is a
// char[NG_PATHSIZ] field in struct ngm_connect and is resolved by the
// kernel's ng_path2noderef, so NG_PATHSIZ is its real bound. Applying the
// stricter sg_len limit here would reject paths the kernel accepts.
func validateEmbeddedPath(s string) error {
	if s == "" {
		return fmt.Errorf("netgraph: peer path must not be empty")
	}
	if len(s) > ngPathSiz-1 {
		return fmt.Errorf("netgraph: peer path %q is %d bytes, must fit NG_PATHSIZ=%d including the terminator", s, len(s), ngPathSiz)
	}
	return nil
}

// decodeNodeInfo decodes one struct nodeinfo from the front of b.
func decodeNodeInfo(b []byte) (NodeInfo, error) {
	if len(b) < nodeInfoSize {
		return NodeInfo{}, ErrTruncated
	}
	return NodeInfo{
		Name:     cstring(b[niNameOff : niNameOff+ngNodeSiz]),
		Type:     cstring(b[niTypeOff : niTypeOff+ngTypeSiz]),
		ID:       binary.LittleEndian.Uint32(b[niIDOff : niIDOff+4]),
		NumHooks: binary.LittleEndian.Uint32(b[niHooksOff : niHooksOff+4]),
	}, nil
}

// decodeNodeList decodes a struct namelist: a count followed by that many
// struct nodeinfo.
//
// The count is a length field in data the kernel handed us, so it is
// checked against what was actually delivered before any element is
// indexed. A count larger than the payload is precisely the truncation
// ErrTruncated exists for; trusting it would read past the end of the
// buffer. The arithmetic is done in uint64 so a hostile or corrupt count
// cannot overflow the multiplication back into a small, passing value.
func decodeNodeList(arg []byte) ([]NodeInfo, error) {
	if len(arg) < nlArrayOff {
		return nil, ErrTruncated
	}
	count := binary.LittleEndian.Uint32(arg[nlCountOff : nlCountOff+4])
	if uint64(nlArrayOff)+uint64(count)*nodeInfoSize > uint64(len(arg)) {
		return nil, ErrTruncated
	}
	out := make([]NodeInfo, 0, count)
	for i := uint32(0); i < count; i++ {
		off := nlArrayOff + int(i)*nodeInfoSize
		ni, err := decodeNodeInfo(arg[off : off+nodeInfoSize])
		if err != nil {
			return nil, err
		}
		out = append(out, ni)
	}
	return out, nil
}

// decodeHookList decodes a struct hooklist: one nodeinfo describing the
// node itself, followed by nodeinfo.hooks struct linkinfo entries. The
// count is bounds-checked against the delivered length before indexing,
// for the same reason as in decodeNodeList.
func decodeHookList(arg []byte) (NodeInfo, []HookInfo, error) {
	if len(arg) < hlLinkOff {
		return NodeInfo{}, nil, ErrTruncated
	}
	ni, err := decodeNodeInfo(arg[hlNodeInfoOff:])
	if err != nil {
		return NodeInfo{}, nil, err
	}
	count := ni.NumHooks
	if uint64(hlLinkOff)+uint64(count)*linkInfoSize > uint64(len(arg)) {
		return NodeInfo{}, nil, ErrTruncated
	}
	hooks := make([]HookInfo, 0, count)
	for i := uint32(0); i < count; i++ {
		off := hlLinkOff + int(i)*linkInfoSize
		link := arg[off : off+linkInfoSize]
		peer, err := decodeNodeInfo(link[liNodeInfoOff:])
		if err != nil {
			return NodeInfo{}, nil, err
		}
		hooks = append(hooks, HookInfo{
			Name:     cstring(link[liOurHookOff : liOurHookOff+ngHookSiz]),
			PeerHook: cstring(link[liPeerHookOff : liPeerHookOff+ngHookSiz]),
			PeerName: peer.Name,
			PeerType: peer.Type,
			PeerID:   peer.ID,
		})
	}
	return ni, hooks, nil
}

// decodeTypeList decodes a struct typelist: a count followed by that many
// struct typeinfo. Bounds-checked like decodeNodeList.
func decodeTypeList(arg []byte) ([]TypeInfo, error) {
	if len(arg) < tlArrayOff {
		return nil, ErrTruncated
	}
	count := binary.LittleEndian.Uint32(arg[tlCountOff : tlCountOff+4])
	if uint64(tlArrayOff)+uint64(count)*typeInfoSize > uint64(len(arg)) {
		return nil, ErrTruncated
	}
	out := make([]TypeInfo, 0, count)
	for i := uint32(0); i < count; i++ {
		off := tlArrayOff + int(i)*typeInfoSize
		ti := arg[off : off+typeInfoSize]
		out = append(out, TypeInfo{
			Name:     cstring(ti[tiNameOff : tiNameOff+ngTypeSiz]),
			NumNodes: binary.LittleEndian.Uint32(ti[tiNumNodesOff : tiNumNodesOff+4]),
		})
	}
	return out, nil
}

// MkPeer creates a new node of type nodeType and connects it to the node at
// path: ourHook on the node at path, peerHook on the new node. The kernel
// loads the type's module on demand if it is not already present.
//
// NGM_MKPEER carries no NGM_HASREPLY, so there is no reply to wait for; any
// error from the operation comes back synchronously from the underlying
// sendto(2), because ng_socket.c's ngc_send sleeps until the destination
// node has applied the message and then returns that node's error.
func (s *Socket) MkPeer(path, nodeType, ourHook, peerHook string) error {
	if err := ValidatePath(path); err != nil {
		return err
	}
	if err := ValidateTypeName(nodeType); err != nil {
		return err
	}
	if err := ValidateHookName(ourHook); err != nil {
		return err
	}
	if err := ValidateHookName(peerHook); err != nil {
		return err
	}

	arg := make([]byte, mkPeerSize)
	copy(arg[mkPeerTypeOff:], nodeType)
	copy(arg[mkPeerOurHookOff:], ourHook)
	copy(arg[mkPeerPeerHookOff:], peerHook)

	_, err := s.Send(path, GenericCookie, NGM_MKPEER, arg)
	return err
}

// Connect joins an existing node: ourHook on the node at path is connected
// to peerHook on the node at peerPath. Like MkPeer this command has no
// reply and reports failure through the send.
func (s *Socket) Connect(path, ourHook, peerPath, peerHook string) error {
	if err := ValidatePath(path); err != nil {
		return err
	}
	if err := validateEmbeddedPath(peerPath); err != nil {
		return err
	}
	if err := ValidateHookName(ourHook); err != nil {
		return err
	}
	if err := ValidateHookName(peerHook); err != nil {
		return err
	}

	arg := make([]byte, connectSize)
	copy(arg[connectPathOff:], peerPath)
	copy(arg[connectOurHookOff:], ourHook)
	copy(arg[connectPeerHookOff:], peerHook)

	_, err := s.Send(path, GenericCookie, NGM_CONNECT, arg)
	return err
}

// NameNode gives the node at path the global name name, making it
// addressable as "name:". No reply; errors come back from the send.
func (s *Socket) NameNode(path, name string) error {
	if err := ValidatePath(path); err != nil {
		return err
	}
	if err := ValidateNodeName(name); err != nil {
		return err
	}

	arg := make([]byte, nameSize)
	copy(arg, name)

	_, err := s.Send(path, GenericCookie, NGM_NAME, arg)
	return err
}

// RmHook disconnects hook from the node at path. No reply; errors come back
// from the send.
func (s *Socket) RmHook(path, hook string) error {
	if err := ValidatePath(path); err != nil {
		return err
	}
	if err := ValidateHookName(hook); err != nil {
		return err
	}

	arg := make([]byte, rmHookSize)
	copy(arg, hook)

	_, err := s.Send(path, GenericCookie, NGM_RMHOOK, arg)
	return err
}

// Shutdown destroys the node at path. No reply; errors come back from the
// send.
func (s *Socket) Shutdown(path string) error {
	if err := ValidatePath(path); err != nil {
		return err
	}
	_, err := s.Send(path, GenericCookie, NGM_SHUTDOWN, nil)
	return err
}

// NodeInfo returns the name, type, ID and hook count of the node at path.
// It waits up to defaultTimeout for the reply, matched by token.
func (s *Socket) NodeInfo(path string) (NodeInfo, error) {
	m, err := s.Call(path, GenericCookie, NGM_NODEINFO, nil, defaultTimeout)
	if err != nil {
		return NodeInfo{}, err
	}
	ni, err := decodeNodeInfo(m.Arg)
	if err != nil {
		return NodeInfo{}, &Error{Op: "nodeinfo", Path: path, Err: err}
	}
	return ni, nil
}

// ListHooks returns the node at path and one HookInfo per connected hook,
// including each hook's peer. It waits up to defaultTimeout for the reply.
func (s *Socket) ListHooks(path string) (NodeInfo, []HookInfo, error) {
	m, err := s.Call(path, GenericCookie, NGM_LISTHOOKS, nil, defaultTimeout)
	if err != nil {
		return NodeInfo{}, nil, err
	}
	ni, hooks, err := decodeHookList(m.Arg)
	if err != nil {
		return NodeInfo{}, nil, &Error{Op: "listhooks", Path: path, Err: err}
	}
	return ni, hooks, nil
}

// ListNodes returns every node currently in the graph, named or not. The
// query is addressed to this socket's own node (".:"); the generic list
// commands are answered identically by any node, since the kernel handles
// them in ng_base.c before the node's own rcvmsg ever sees them.
func (s *Socket) ListNodes() ([]NodeInfo, error) {
	m, err := s.Call(".:", GenericCookie, NGM_LISTNODES, nil, defaultTimeout)
	if err != nil {
		return nil, err
	}
	nodes, err := decodeNodeList(m.Arg)
	if err != nil {
		return nil, &Error{Op: "listnodes", Path: ".:", Err: err}
	}
	return nodes, nil
}

// ListNames returns only the nodes that have been given a global name.
func (s *Socket) ListNames() ([]NodeInfo, error) {
	m, err := s.Call(".:", GenericCookie, NGM_LISTNAMES, nil, defaultTimeout)
	if err != nil {
		return nil, err
	}
	nodes, err := decodeNodeList(m.Arg)
	if err != nil {
		return nil, &Error{Op: "listnames", Path: ".:", Err: err}
	}
	return nodes, nil
}

// ListTypes returns every node type currently loaded, with the number of
// live nodes of each.
func (s *Socket) ListTypes() ([]TypeInfo, error) {
	m, err := s.Call(".:", GenericCookie, NGM_LISTTYPES, nil, defaultTimeout)
	if err != nil {
		return nil, err
	}
	types, err := decodeTypeList(m.Arg)
	if err != nil {
		return nil, &Error{Op: "listtypes", Path: ".:", Err: err}
	}
	return types, nil
}
