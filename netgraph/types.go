package netgraph

// Token identifies a request/response pair on a control socket. The kernel
// echoes the token from a request back in its reply so that a caller can
// match an answer to the call that produced it; see doc.go for why that
// matters.
type Token uint32

// Msg is a decoded netgraph control message: either a request this package
// is about to send or a response it has received.
type Msg struct {
	Version    uint8
	Flags      uint32
	Token      Token
	TypeCookie int32
	Cmd        int32
	CmdStr     string
	Arg        []byte
}

// NodeInfo describes a single netgraph node, as returned by NGM_NODEINFO,
// NGM_LISTNODES and NGM_LISTNAMES.
type NodeInfo struct {
	Name     string
	Type     string
	ID       uint32
	NumHooks uint32
}

// HookInfo describes one hook on a node and, where connected, its peer.
type HookInfo struct {
	Name     string
	PeerName string // peer node name, empty if unnamed
	PeerType string
	PeerID   uint32
	PeerHook string
}

// TypeInfo describes a loaded netgraph node type, as returned by
// NGM_LISTTYPES.
type TypeInfo struct {
	Name     string
	NumNodes uint32
}
