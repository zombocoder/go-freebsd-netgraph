package netgraph

// Generic (NGM_GENERIC_COOKIE) message constants, verified against
// /usr/include/netgraph/ng_message.h on FreeBSD 14.3-RELEASE-p3. These are
// plain numeric constants with no OS dependency, so this file carries no
// build tag: it compiles everywhere, even though only the freebsd build of
// this package can actually send any of them.
//
// The generic command enum bakes NGM_READONLY and NGM_HASREPLY into the
// command values themselves: NGM_NODEINFO is 6|NGM_READONLY|NGM_HASREPLY,
// not the bare ordinal 6. A binding that writes the bare ordinal addresses
// a different command, or one the kernel silently rejects.
const (
	ngmReadOnly = 0x10000000 // NGM_READONLY
	ngmHasReply = 0x20000000 // NGM_HASREPLY
)

// GenericCookie is NGM_GENERIC_COOKIE: the type cookie for the generic
// control messages every netgraph node understands.
const GenericCookie int32 = 1137070366

// Generic message commands, for use as the cmd argument to Send and Call.
const (
	NGM_SHUTDOWN int32 = 1 // Shut down node.
	NGM_MKPEER   int32 = 2 // Create and attach a peer node.
	NGM_CONNECT  int32 = 3 // Connect two nodes.
	NGM_NAME     int32 = 4 // Give a node a name.
	NGM_RMHOOK   int32 = 5 // Break a connection between two nodes.

	NGM_NODEINFO     int32 = 6 | ngmReadOnly | ngmHasReply  // Get nodeinfo for target.
	NGM_LISTHOOKS    int32 = 7 | ngmReadOnly | ngmHasReply  // Get list of hooks on node.
	NGM_LISTNAMES    int32 = 8 | ngmReadOnly | ngmHasReply  // List globally named nodes.
	NGM_LISTNODES    int32 = 9 | ngmReadOnly | ngmHasReply  // List all nodes.
	NGM_LISTTYPES    int32 = 10 | ngmReadOnly | ngmHasReply // List installed node types.
	NGM_TEXT_STATUS  int32 = 11 | ngmReadOnly | ngmHasReply // Get text status.
	NGM_BINARY2ASCII int32 = 12 | ngmReadOnly | ngmHasReply // Convert ng_mesg to ASCII.
	NGM_ASCII2BINARY int32 = 13 | ngmReadOnly | ngmHasReply // Convert ASCII to ng_mesg.
	NGM_TEXT_CONFIG  int32 = 14                             // Get/set text config.
)

// NGF_RESP marks a message as a response rather than an original request, in
// Msg.Flags.
const NGF_RESP = 0x00000001
