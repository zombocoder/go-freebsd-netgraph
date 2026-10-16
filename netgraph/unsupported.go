//go:build !freebsd

package netgraph

import "time"

// Socket is a stub on platforms other than FreeBSD, where netgraph does not
// exist. Every method returns ErrNotSupported.
type Socket struct{}

// Open always fails on this platform: netgraph does not exist here.
func Open(name string) (*Socket, error) {
	return nil, ErrNotSupported
}

func (s *Socket) Close() error {
	return ErrNotSupported
}

func (s *Socket) NodeName() string {
	return ""
}

func (s *Socket) Send(path string, cookie, cmd int32, arg []byte) (Token, error) {
	return 0, ErrNotSupported
}

func (s *Socket) Recv(tok Token, timeout time.Duration) (*Msg, error) {
	return nil, ErrNotSupported
}

func (s *Socket) Call(path string, cookie, cmd int32, arg []byte, timeout time.Duration) (*Msg, error) {
	return nil, ErrNotSupported
}

// Generic node operations. Stubs on this platform, like everything else
// here; see the freebsd build's nodes.go for the real implementations.

func (s *Socket) MkPeer(path, nodeType, ourHook, peerHook string) error {
	return ErrNotSupported
}

func (s *Socket) Connect(path, ourHook, peerPath, peerHook string) error {
	return ErrNotSupported
}

func (s *Socket) NameNode(path, name string) error {
	return ErrNotSupported
}

func (s *Socket) RmHook(path, hook string) error {
	return ErrNotSupported
}

func (s *Socket) Shutdown(path string) error {
	return ErrNotSupported
}

func (s *Socket) NodeInfo(path string) (NodeInfo, error) {
	return NodeInfo{}, ErrNotSupported
}

func (s *Socket) ListHooks(path string) (NodeInfo, []HookInfo, error) {
	return NodeInfo{}, nil, ErrNotSupported
}

func (s *Socket) ListNodes() ([]NodeInfo, error) {
	return nil, ErrNotSupported
}

func (s *Socket) ListNames() ([]NodeInfo, error) {
	return nil, ErrNotSupported
}

func (s *Socket) ListTypes() ([]TypeInfo, error) {
	return nil, ErrNotSupported
}

// Hook data.

func (s *Socket) WriteData(hook string, b []byte) error {
	return ErrNotSupported
}

func (s *Socket) ReadData(b []byte, timeout time.Duration) (n int, hook string, err error) {
	return 0, "", ErrNotSupported
}

// ASCII conversion.

func (s *Socket) CallAscii(path, cmd string, timeout time.Duration) (*Msg, error) {
	return nil, ErrNotSupported
}

func (s *Socket) FromAscii(path, text string) (*Msg, error) {
	return nil, ErrNotSupported
}

func (s *Socket) ToAscii(m *Msg) (string, error) {
	return "", ErrNotSupported
}

func (s *Socket) ToAsciiAt(path string, m *Msg) (string, error) {
	return "", ErrNotSupported
}
