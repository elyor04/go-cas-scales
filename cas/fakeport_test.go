package cas

import (
	"bytes"
	"io"
	"sync"
	"time"

	"go.bug.st/serial"
)

// fakePort is an in-memory serial.Port test double. toClient is what the
// "indicator" sends and Client.Read reads from; fromClient captures what
// Client.Write sends, so tests can assert on outgoing command frames
// without any real hardware.
//
// A reply queued with feed before the request is sent survives it:
// ResetInputBuffer here is a no-op, unlike a real port's. Tests with more
// than one request in flight set respond instead, which answers each write
// as an indicator does, after the request.
type fakePort struct {
	mu         sync.Mutex
	toClient   *bytes.Buffer
	fromClient bytes.Buffer
	closed     bool
	mode       *serial.Mode
	respond    func(request []byte) string
}

func newFakePort(toClient string) *fakePort {
	return &fakePort{toClient: bytes.NewBufferString(toClient)}
}

func (p *fakePort) SetMode(mode *serial.Mode) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = mode
	return nil
}

func (p *fakePort) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	return p.toClient.Read(b)
}

func (p *fakePort) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	if p.respond != nil {
		p.toClient.WriteString(p.respond(b))
	}
	return p.fromClient.Write(b)
}

func (p *fakePort) written() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fromClient.String()
}

// feed appends more bytes for Read to hand back, as if the indicator kept
// transmitting (used by streaming tests).
func (p *fakePort) feed(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.toClient.WriteString(s)
}

func (p *fakePort) Drain() error             { return nil }
func (p *fakePort) ResetInputBuffer() error  { return nil }
func (p *fakePort) ResetOutputBuffer() error { return nil }
func (p *fakePort) SetDTR(bool) error        { return nil }
func (p *fakePort) SetRTS(bool) error        { return nil }
func (p *fakePort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return &serial.ModemStatusBits{}, nil
}
func (p *fakePort) SetReadTimeout(time.Duration) error { return nil }
func (p *fakePort) Break(time.Duration) error          { return nil }

func (p *fakePort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

// newTestClient builds a Client wired to a fakePort, bypassing Open (which
// insists on a real OS port name) so tests can run without hardware.
func newTestClient(opts DialOptions) (*Client, *fakePort) {
	port := newFakePort("")
	return newResolvedTestClient(port, opts), port
}

func newResolvedTestClient(port Transport, opts DialOptions) *Client {
	resolved := opts.resolved()
	if resolved.Format.Parse == nil {
		resolved.Format = Format22Byte
	}
	return newClient(port, resolved)
}

// blockingReadPort wraps a fakePort but makes Read block for a fixed
// duration regardless of whatever SetReadTimeout configured: a Transport
// that doesn't honor its read timeout. (Real-hardware reads that seemed to
// do this were bufio's empty-read retries; see errReadTimeout.)
type blockingReadPort struct {
	*fakePort
	blockFor time.Duration
}

func (p *blockingReadPort) Read(b []byte) (int, error) {
	time.Sleep(p.blockFor)
	return p.fakePort.Read(b)
}

// newBlockingTestClient is newTestClient's counterpart for tests that need
// a Read call the configured ReadTimeout can't actually bound at the port
// level, to verify Client enforces it independently at the ctx/Go level.
func newBlockingTestClient(opts DialOptions, blockFor time.Duration) (*Client, *blockingReadPort) {
	port := &blockingReadPort{fakePort: newFakePort(""), blockFor: blockFor}
	return newResolvedTestClient(port, opts), port
}
