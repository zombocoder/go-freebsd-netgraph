# go-freebsd-netgraph

[![Go Reference](https://pkg.go.dev/badge/github.com/zombocoder/go-freebsd-netgraph.svg)](https://pkg.go.dev/github.com/zombocoder/go-freebsd-netgraph)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![FreeBSD](https://img.shields.io/badge/FreeBSD-14.3--RELEASE--p3-AB2B28?logo=freebsd&logoColor=white)](https://www.freebsd.org/releases/)
[![License](https://img.shields.io/badge/license-BSD--2--Clause-blue.svg)](LICENSE)

Go bindings for FreeBSD's [netgraph(4)](https://man.freebsd.org/netgraph/4)
subsystem: control sockets, control messages, the generic node operations,
hook data, and ASCII conversion.

It binds netgraph **as a subsystem**, not as a catalogue of node types. A
node type's own control messages are that type's business — this package
carries their payloads as opaque bytes and lets you marshal them, the same
level of abstraction `libnetgraph` itself offers.

---

## Requirements

| | |
|---|---|
| OS | FreeBSD 13.2+ — **only 14.3-RELEASE-p3 has actually been tested** |
| Go | **1.26** (`go.mod` requires it; see [Toolchain](#toolchain) below) |
| C toolchain | required — this package uses cgo against the system `libnetgraph` |
| Privileges | **root**, for anything that touches the kernel |

## Install

```sh
go get github.com/zombocoder/go-freebsd-netgraph
```

## Quick start

```go
package main

import (
	"fmt"
	"log"

	"github.com/zombocoder/go-freebsd-netgraph/netgraph"
)

func main() {
	// "" creates an anonymous socket node. Close tears it down.
	s, err := netgraph.Open("")
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	nodes, err := s.ListNodes()
	if err != nil {
		log.Fatal(err)
	}
	for _, n := range nodes {
		fmt.Printf("%-16s type=%-10s id=0x%08x hooks=%d\n", n.Name, n.Type, n.ID, n.NumHooks)
	}
}
```

Building a graph, in the two equivalent forms:

```go
// Binary: arguments marshalled in Go.
err := s.MkPeer(".:", "hole", "a", "left")

// ASCII: handed to the kernel's ng_parse. The braces are mandatory —
// see "The ASCII brace trap" below.
_, err := s.CallAscii(".:", `mkpeer { type="hole" ourhook="b" peerhook="left" }`, 2*time.Second)
```

## What this package guarantees

Each of these is a failure that happened for real while building `ng_policy`
on top of raw `libnetgraph`. They are in the library so nobody rediscovers
them.

**1. Replies are matched to their request by token.**
A netgraph control socket hands you whatever message is next in its queue,
not the answer to the call you just made — a late reply to an earlier,
abandoned request is indistinguishable on the wire. `Call` sends a request
carrying a fresh token and reads until a reply's token matches, discarding
anything stale. When this bit us for real it presented as *"the node stopped
responding"*, which pointed squarely at the kernel, which was fine.

**2. No *receive* blocks past its deadline.**
Every receiving call takes a `time.Duration` and returns `ErrTimeout` at it.
`SO_RCVTIMEO` is re-armed before every read and the remaining time
recomputed each iteration, so discarding a stale message cannot extend the
deadline. This package also declines to use `NgSendMsg`, `NgSendAsciiMsg`,
`NgRecvAsciiMsg` and `NgAllocRecvMsg`, all of which either reach a
`poll(..., INFTIM)` or receive without checking the token.

> **This guarantee covers receives, not sends, and the difference is the
> kernel's, not this package's.** `ngc_send` queues the item and then calls
> `msleep(priv, &priv->mtx, 0, "ngsock", 0)` — the 5th argument `0` is *no
> timeout* and the 3rd `0` means *no `PCATCH`*, so the sleep is neither
> bounded nor interruptible by a signal. `SO_SNDTIMEO` cannot reach it:
> `ngcontrol_protosw` sets no `pr_sosend`, and the value it sets is only
> consulted when a sender waits for send-buffer space, which a netgraph
> control socket never lacks. Delivery is synchronous in the calling thread
> when the target node is idle and the stack has room — which is why nothing
> here has ever been seen to block — but control messages are *writers*
> unless they carry `NGM_READONLY`, and a writer needs a completely idle
> node, so queueing is easy to hit under contention. No userland netgraph
> client on FreeBSD can promise otherwise.

**3. Response buffers are sized to the socket's receive space.**
A control socket's receive space is 20480 bytes by default
(`net.graph.recvspace`), and a legitimate response can fill it. A smaller
buffer does not overflow — the kernel truncates the datagram — but the
`arglen` in the surviving 56-byte header still describes the *whole*
response, so a caller that trusts it reads out of bounds. Buffers are sized
from the sysctl at `Open`, and a mismatch returns `ErrTruncated` rather than
a short slice.

**4. The ASCII brace trap is documented and demonstrated.**
See below, and `examples/mkpeer`.

**Also, quietly:** names, hooks, types and paths are validated in Go before
submission, so you get a specific message instead of a bare `EINVAL`; errors
carry the operation and path that produced them and map onto `ErrNotFound`,
`ErrExists`, `ErrStale` and friends; and the maximum control path is **32
bytes**, not the `NG_PATHSIZ` 512 that the header invites you to assume.

## The ASCII brace trap

This works:

```
mkpeer { type="hole" ourhook="a" peerhook="b" }
```

This does not:

```
mkpeer hole a b
```

...even though the second is exactly what you type at `ngctl(8)`. The
kernel's `NGM_ASCII2BINARY` runs `ng_parse`, whose struct parser demands an
opening brace (`ng_parse.c:1220`) and returns a bare `EINVAL` with no hint
when it does not get one. `ngctl` accepts the positional form because it
never sends it: `mkpeer.c:61,81` `snprintf()`s the arguments into a
`struct ngm_mkpeer` in userland and sends the **binary** message. The
positional form is `ngctl`'s command-line syntax, not netgraph's ASCII
syntax, and nothing about either one says so.

## Safety

**netgraph is live networking.** These calls take effect immediately on the
running kernel; there is no dry-run mode and no transaction to roll back.
`Shutdown` on the wrong node, or `RmHook` on a hook carrying real traffic,
will drop packets — including, if the node is in the path of your session,
the packets carrying your session. Test on a console or a disposable host.

Two specific traps:

- **A leaked node outlives the process.** Netgraph nodes are kernel objects.
  A program that exits without tearing down what it built leaves it running,
  and the next run then fails with `EEXIST` on the name and looks like a
  flaky test rather than yesterday's mess. Every example here cleans up on
  every path out, including error paths; the run below shows what happens
  when it does not.
- **`ng_hole` and `ng_echo` self-destruct when their last hook goes**, which
  makes closing your socket *look* like it tidied up after you. That stops
  being true the moment a node has a second hook. Shut nodes down
  explicitly.

## Examples

Three runnable programs. All need root. The output below is **captured from
real runs on the test host**, not written by hand.

```sh
make examples          # builds ./bin/ng-list, ./bin/ng-mkpeer, ./bin/ng-data
doas ./bin/ng-list
```

### `examples/list` — enumerate nodes and types

```console
$ doas ./bin/ng-list
this program's own socket node: [383d]

2 nodes:
  em0                  type=ether        id=0x00003237 hooks=0
  [383d]               type=socket       id=0x0000383d hooks=0

globally named nodes (1):
  em0                  type=ether        id=0x00003237 hooks=0

4 node types loaded:
  echo             nodes=0
  hole             nodes=0
  ether            nodes=1
  socket           nodes=1
```

`[383d]` is this program's own anonymous socket node, shown in the `[hex-id]`
form the kernel and `ngctl` both use for a node with no global name. It is
gone by the time the process exits.

### `examples/mkpeer` — build a graph, both syntaxes, then tear it down

```console
$ doas ./bin/ng-mkpeer
socket node: [383e]

MkPeer(".:", "hole", "a", "left") -> named ngexhole1
CallAscii(".:", `mkpeer { type="hole" ourhook="b" peerhook="left" }`) -> named ngexhole2
CallAscii(".:", `mkpeer hole c left`) -> rejected, as it must be:
    netgraph: send .:: invalid argument

Connect("ngexhole1:", "toB", "ngexhole2:", "toA")

ngexhole1: type=hole id=0x0000383f hooks=2
    toB    -> ngexhole2:toA (type hole)
    left   -> [383e]:a (type socket)
ngexhole2: type=hole id=0x00003840 hooks=2
    toA    -> ngexhole1:toB (type hole)
    left   -> [383e]:b (type socket)

shut down ngexhole1, confirmed gone
shut down ngexhole2, confirmed gone
```

The third line is the trap, live: `mkpeer hole c left` — the form `ngctl`
accepts — comes back as an unadorned `invalid argument`.

**And the cleanup is not decoration.** With a failure injected right after
`Connect` (so both nodes have two hooks and neither self-destructs), the
deferred teardown still runs and the graph is gone:

```console
$ doas ./bin/ng-mkpeer            # failure injected after Connect
...
ng-mkpeer: INJECTED failure after Connect — both nodes now have two hooks

$ doas ngctl list
There are 2 total nodes:
  Name: ngctl23011      Type: socket          ID: 00003857   Num hooks: 0
  Name: em0             Type: ether           ID: 00003237   Num hooks: 0
```

Remove the two `defer shutdown(...)` lines and run the same injected failure
again, and the nodes survive the process:

```console
$ doas ngctl list
There are 4 total nodes:
  Name: em0             Type: ether           ID: 00003237   Num hooks: 0
  Name: ngexhole1       Type: hole            ID: 00003859   Num hooks: 1
  Name: ngexhole2       Type: hole            ID: 0000385a   Num hooks: 1
  Name: ngctl38773      Type: socket          ID: 0000385b   Num hooks: 0

$ doas ./bin/ng-mkpeer             # the very next run, unmodified
ng-mkpeer: name ngexhole1: netgraph: send .:a: address already in use
```

That last line is the whole reason the examples clean up on error paths.

### `examples/data` — write and read frames on a hook

```console
$ doas ./bin/ng-data
socket node: [3841]
attached an ng_echo node on hook "e0"

frame 0: wrote 19 bytes, read 19 bytes back on hook "e0" — identical ("netgraph round trip")
frame 1: wrote 44 bytes, read 44 bytes back on hook "e0" — identical ("second frame, a little l"...)
frame 2: wrote 64 bytes, read 64 bytes back on hook "e0" — identical (64 binary bytes, first 0xab)

ReadData with nothing to read returned after 263ms: netgraph: timeout
```

The last line is guarantee 2 in action: a 250 ms deadline on a socket with
nothing to read returns `ErrTimeout` instead of blocking.

Running all three twice back to back and then checking:

```console
$ doas ngctl list
There are 2 total nodes:
  Name: ngctl92666      Type: socket          ID: 00003850   Num hooks: 0
  Name: em0             Type: ether           ID: 00003237   Num hooks: 0
```

`em0` is the host's physical interface. `ngctlNNNNN` is the transient socket
node `ngctl list` creates for itself, under a new PID each invocation.
Nothing else is left behind.

## Building and testing

```sh
make freebsd-check    # host is FreeBSD, toolchain is usable
make lint             # gofmt -l over the tree; any output fails
make vet              # go vet, with and without the integration tag
make test             # unit tests: no kernel, no root
make test-integration # integration tests: builds real graphs, needs root
make examples         # build ./bin/ng-{list,mkpeer,data}
make                  # lint + vet + test
```

`make test-integration` runs through `doas` and creates and destroys real
netgraph nodes. Confirm afterwards with `doas ngctl list` that only your own
pre-existing nodes remain.

### Toolchain

`go.mod` requires **go 1.26**, and an older `go` does not simply refuse it:
since 1.21, the toolchain switch silently *downloads* a matching toolchain
(~71 MB) and builds with that instead. On the test host,
`/usr/local/bin/go` is 1.21.13:

```console
$ cd /tmp && go version
go version go1.21.13 freebsd/amd64

$ cd ~/dev/go-freebsd-netgraph && go version
go version go1.26.0 freebsd/amd64
```

Same binary, different answer, no warning — and it fetched 1.26.0, not the
1.26.7 this project is built and tested with. Every `go` invocation in the
`Makefile` therefore sets `GOTOOLCHAIN=local`, which fails immediately and
says what it needs:

```console
$ GOTOOLCHAIN=local go build ./...
go: go.mod requires go >= 1.26 (running go 1.21.13; GOTOOLCHAIN=local)
```

Point `make` at a specific toolchain with `make GO=/path/to/go`.

### Off FreeBSD

The package builds anywhere: `netgraph/unsupported.go` provides the same API
with every call returning `ErrNotSupported`, so cross-platform code can
import it and `go doc` works on any host. Verified with
`GOOS=linux go build ./...`. That is a build guarantee only — there is no
netgraph outside FreeBSD.

## Project structure

```
netgraph/
  doc.go            package documentation, including the ASCII trap
  client.go         Socket, Open/Close, Send/Recv/Call, token matching
  cgo_freebsd.go    the only file with `import "C"`
  generic.go        NGM_* command constants (no build tag: documents everywhere)
  types.go          Msg, NodeInfo, HookInfo, TypeInfo, Token
  nodes.go          the generic node operations and their wire decoders
  data.go           WriteData / ReadData
  ascii.go          ToAscii / ToAsciiAt / FromAscii / CallAscii
  errors.go         error types and errno mapping
  validate.go       name, hook, type and path validation
  unsupported.go    !freebsd stubs returning ErrNotSupported
internal/testutil/  root check for tests that need a live kernel
examples/
  list/             enumerate nodes and types
  mkpeer/           build a small graph, both syntaxes, and tear it down
  data/             send and receive frames on a hook
```

## Documentation

API reference: [`go doc github.com/zombocoder/go-freebsd-netgraph/netgraph`](https://pkg.go.dev/github.com/zombocoder/go-freebsd-netgraph/netgraph).
The package documentation (`netgraph/doc.go`) also covers the ASCII brace trap.

## License

BSD-2-Clause. See [LICENSE](LICENSE).
