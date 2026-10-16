// Package netgraph binds FreeBSD's netgraph(4) subsystem: control sockets,
// control messages, the generic node operations, hook data, and ASCII
// conversion. It is a binding for the subsystem, not a model of individual
// node types — a node type's control messages are that type's own business,
// so this package carries their payloads as opaque bytes and lets the
// caller marshal them, the same level of abstraction libnetgraph itself
// provides.
//
// # Requirements
//
// FreeBSD 13.2+, a C compiler (this package uses cgo against the system
// libnetgraph), and root privileges for anything that actually touches the
// kernel. On any other platform every exported function returns
// [ErrNotSupported] so the package still builds and its documentation is
// still reachable.
//
// # Replies are matched to their request by token
//
// A netgraph control socket delivers whatever message is next in its
// receive queue, not necessarily the answer to the call just made: a late
// reply to an earlier, abandoned request is indistinguishable on the wire
// from the reply to the current one. [Socket.Call] sends a request carrying
// a fresh [Token] and then reads until a reply's token matches it,
// discarding anything stale rather than handing it to the caller.
//
// # Every receive has a deadline
//
// No function in this package can block forever. A receive that does not
// complete before its deadline returns [ErrTimeout].
//
// # Response buffers are sized to the socket's receive space
//
// A netgraph control socket's receive space defaults to 20480 bytes
// (net.graph.recvspace). A legitimate response can fill it entirely. A
// smaller receive buffer does not make the kernel truncate the reported
// arglen along with the data — arglen keeps describing the full response,
// so a caller that trusts arglen after a short read reads out of bounds.
// This package sizes its buffers from the sysctl at [Open] and returns
// [ErrTruncated] rather than handing back a message whose payload does not
// match its own header.
//
// # The ASCII brace trap
//
// [Socket.CallAscii] and [Socket.FromAscii] hand text to the kernel's
// NGM_ASCII2BINARY, which runs the ng_parse struct parser. That parser
// requires brace/field syntax:
//
//	mkpeer { type="hole" ourhook="a" peerhook="b" }
//
// The ngctl(8) command accepts the positional form
//
//	mkpeer hole a b
//
// but only because ngctl parses its own arguments positionally in
// userland before it ever constructs a message for the kernel. Handed
// directly to NGM_ASCII2BINARY, that same positional text is rejected. The
// difference is invisible from outside a debugger — both look like
// "netgraph's ASCII form" — and it costs an afternoon the first time it is
// hit. Every ASCII path in this package, and every example that builds one,
// uses the brace form.
package netgraph
