package cas

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Transport is what a Client reads frames from and writes requests to. A
// serial.Port satisfies it (Open), and so does the TCP connection OpenTCP
// makes to an indicator's Ethernet option or a serial-to-Ethernet converter
// in front of its RS-232/RS-485 port.
//
// SetReadTimeout follows go.bug.st/serial's semantics, which the rest of
// this package is written against: a Read that waits longer than the
// timeout returns (0, nil) rather than an error, and a negative timeout
// means "block until data arrives".
type Transport interface {
	io.ReadWriteCloser
	SetReadTimeout(t time.Duration) error
}

// DefaultDialTimeout bounds OpenTCP's connect.
const DefaultDialTimeout = 5 * time.Second

// noReadTimeout is the SetReadTimeout value that makes Read block until data
// arrives (go.bug.st/serial's NoTimeout).
const noReadTimeout time.Duration = -1

// errReadTimeout is what the Client's reader sees in place of a timed-out
// Read's (0, nil). bufio.Reader retries an empty read 100 times before giving
// up with io.ErrNoProgress, so passed through as is, one ReadTimeout became a
// hundred: RequestOne against a silent indicator held the port for 200s at
// the default 2s. That was the read "blocking for minutes" seen on real
// hardware, which this package used to put down to the serial driver.
var errReadTimeout = errors.New("read timed out")

// timeoutReader reports a Transport's timed-out read as errReadTimeout.
type timeoutReader struct{ t Transport }

func (r timeoutReader) Read(p []byte) (int, error) {
	n, err := r.t.Read(p)
	if n == 0 && err == nil && len(p) > 0 {
		return 0, errReadTimeout
	}
	return n, err
}

// OpenTransport returns a Client reading from an already-open transport.
// Format is resolved exactly as Open resolves it; Port, BaudRate and
// Parity are ignored (the transport is already set up). The Client owns
// t from here on: Close closes it.
func OpenTransport(t Transport, opts DialOptions) (*Client, error) {
	if t == nil {
		return nil, opErr("OpenTransport", errors.New("nil transport"))
	}
	resolved, err := resolveFormat("OpenTransport", opts)
	if err != nil {
		return nil, err
	}
	return newClient(t, resolved), nil
}

// OpenTCP connects to address ("host:port") and returns a Client reading
// the indicator's frames from that connection -- the same frames, in the
// same format, as on its serial port. Use it for an indicator behind a
// serial-to-Ethernet converter (transparent/raw TCP server mode) or with
// its own Ethernet option. Port, BaudRate and Parity in opts are ignored;
// those are the converter's settings.
//
// A dropped connection surfaces as an error on Stream's error channel, as
// an unplugged serial cable does; reconnecting is the caller's job, as it
// is for a serial port.
func OpenTCP(address string, opts DialOptions) (*Client, error) {
	resolved, err := resolveFormat("OpenTCP", opts)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("tcp", address, DefaultDialTimeout)
	if err != nil {
		return nil, opErr("OpenTCP", fmt.Errorf("connect %s: %w", address, err))
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		// A converter that dies without a FIN would otherwise leave Stream
		// blocked forever on a connection that no longer exists.
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(15 * time.Second)
	}
	return newClient(&tcpTransport{conn: conn, timeout: noReadTimeout}, resolved), nil
}

// tcpTransport adapts a net.Conn to Transport's serial-port semantics.
type tcpTransport struct {
	conn net.Conn

	mu      sync.Mutex
	timeout time.Duration // < 0: block
}

func (t *tcpTransport) SetReadTimeout(d time.Duration) error {
	t.mu.Lock()
	t.timeout = d
	t.mu.Unlock()
	return nil
}

func (t *tcpTransport) Read(p []byte) (int, error) {
	t.mu.Lock()
	d := t.timeout
	t.mu.Unlock()
	var deadline time.Time
	if d >= 0 {
		deadline = time.Now().Add(d)
	}
	if err := t.conn.SetReadDeadline(deadline); err != nil {
		return 0, err
	}
	n, err := t.conn.Read(p)
	var ne net.Error
	if err != nil && errors.As(err, &ne) && ne.Timeout() {
		// A serial port's read timeout is not an error; match it.
		return n, nil
	}
	return n, err
}

// ResetInputBuffer discards whatever the connection has already received, as
// serial.Port's ResetInputBuffer purges its receive buffer (see
// Client.discardInput).
func (t *tcpTransport) ResetInputBuffer() error {
	buf := make([]byte, 512)
	for {
		if err := t.conn.SetReadDeadline(time.Now().Add(time.Millisecond)); err != nil {
			return err
		}
		n, err := t.conn.Read(buf)
		if n > 0 {
			continue
		}
		var ne net.Error
		if err == nil || errors.As(err, &ne) && ne.Timeout() {
			return nil
		}
		return err
	}
}

func (t *tcpTransport) Write(p []byte) (int, error) { return t.conn.Write(p) }

func (t *tcpTransport) Close() error { return t.conn.Close() }
