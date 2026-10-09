package cas

import (
	"bufio"
	"context"
	"fmt"
	"sync"

	"go.bug.st/serial"
)

// io_Closer is the minimal interface Client needs to track and tear down
// child sessions (currently just *streamSession). Named with an underscore
// to make clear it's a local bookkeeping type, not io.Closer imported for
// its own sake.
type io_Closer interface {
	Close() error
}

// Client is a connection to one CAS CI-200-series indicator.
//
// A Client is safe for concurrent use by multiple goroutines, including
// concurrent calls to RequestOne, the command-mode methods (Zero, Gross,
// Net, ...), and Stream. The underlying serial line is physically
// half-duplex — only one request/response (or one Stream) can ever be in
// flight on the wire — so Client serializes these internally: concurrent
// callers queue in FIFO order for exclusive access to the port rather than
// interleaving their bytes on it. A queued caller's ctx is honored while it
// waits, not just once its turn arrives, so a busy port surfaces as
// ctx.Err() instead of an indefinite block. Stream holds the port for its
// entire run, so RequestOne/command-mode calls issued while a Stream is
// active simply queue behind it (or time out via ctx) — matching the fact
// that Stream and RequestOne/command mode are different, mutually
// exclusive device configurations (F31/F35) to begin with.
//
// RequestOne and the command-mode methods always return control to their
// own caller by DialOptions.ReadTimeout (or ctx's deadline, if sooner) —
// see RequestOne's doc comment.
type Client struct {
	mu         sync.Mutex
	port       Transport
	reader     *bufio.Reader
	portTokens chan struct{}
	opts       DialOptions
	closed     bool
	sessions   map[io_Closer]struct{}
}

// newPortTokens returns a single-token channel used to serialize exclusive
// access to the port: acquirePort receives the token, releasePort sends it
// back.
func newPortTokens() chan struct{} {
	t := make(chan struct{}, 1)
	t <- struct{}{}
	return t
}

// acquirePort blocks until the port is free for exclusive use, or ctx is
// done first. Every operation that reads or writes c.port must be
// bracketed by acquirePort/releasePort (typically via defer) so concurrent
// callers are serialized into one request/response at a time on the wire.
func (c *Client) acquirePort(ctx context.Context) error {
	// Checked first because select picks at random among ready cases: with
	// the port free, a cancelled ctx would otherwise still win it half the
	// time and go on to write a request.
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.portTokens:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) releasePort() {
	c.portTokens <- struct{}{}
}

// discardInput drops everything received before a request is written: a
// reply that arrived after its own request timed out, or a frame left over
// from the last one, would otherwise be read as this request's answer, and
// every reading after it would be one request stale. It empties the Client's
// own buffer and, when the transport can (serial.Port and OpenTCP's
// connection both can), the transport's.
func (c *Client) discardInput() error {
	_, _ = c.reader.Discard(c.reader.Buffered())
	if r, ok := c.port.(interface{ ResetInputBuffer() error }); ok {
		return r.ResetInputBuffer()
	}
	return nil
}

// Open opens the serial port named by opts.Port and returns a Client ready
// to read from it. See DefaultOptions for the documented zero-value
// defaults applied to opts.
func Open(opts DialOptions) (*Client, error) {
	resolved, err := resolveFormat("Open", opts)
	if err != nil {
		return nil, err
	}

	dataBits := 8
	if resolved.Parity != NoParity {
		dataBits = 7
	}
	mode := &serial.Mode{
		BaudRate: resolved.BaudRate,
		DataBits: dataBits,
		Parity:   resolved.Parity,
		StopBits: serial.OneStopBit,
	}

	port, err := serial.Open(resolved.Port, mode)
	if err != nil {
		return nil, opErr("Open", fmt.Errorf("open %s: %w", resolved.Port, err))
	}

	return newClient(port, resolved), nil
}

// newClient wraps an open transport. opts must already be resolved.
func newClient(port Transport, opts DialOptions) *Client {
	return &Client{
		port:       port,
		reader:     bufio.NewReader(timeoutReader{port}),
		portTokens: newPortTokens(),
		opts:       opts,
		sessions:   make(map[io_Closer]struct{}),
	}
}

// resolveFormat applies Open's rule for DialOptions.Format: the untouched
// zero value means Format22Byte, and a caller-built format must carry a
// Parse func.
func resolveFormat(op string, opts DialOptions) (DialOptions, error) {
	resolved := opts.resolved()
	switch {
	case resolved.Format.Parse != nil:
	case resolved.Format.Name == "" && resolved.Format.MinLen == 0:
		resolved.Format = Format22Byte
	default:
		return resolved, opErr(op, ErrUnpopulatedFormat)
	}
	return resolved, nil
}

// DeviceID returns the F26 device ID this Client was configured with.
func (c *Client) DeviceID() int {
	return c.opts.DeviceID
}

// track registers a child session so Close tears it down too.
func (c *Client) track(s io_Closer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessions != nil {
		c.sessions[s] = struct{}{}
	}
}

func (c *Client) untrack(s io_Closer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, s)
}

// Close closes the underlying serial port. It is idempotent: calling Close
// more than once, or after Open failed to fully initialize, is safe and
// returns nil on the second and later calls. Any Stream sessions started
// on this Client are closed first.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	sessions := c.sessions
	c.sessions = nil
	c.mu.Unlock()

	for s := range sessions {
		_ = s.Close()
	}

	return opErr("Close", c.port.Close())
}

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
