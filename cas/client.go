package cas

import (
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
// A Client is safe for concurrent use by multiple goroutines, except that
// RequestOne and the command-mode methods (Zero, Gross, Net, ...) share the
// underlying port's read/write timeline with each other and with any
// running Stream — issuing them concurrently will interleave their bytes
// on the wire. Run at most one of Stream/RequestOne/a command method at a
// time against a given Client.
type Client struct {
	mu       sync.Mutex
	port     serial.Port
	opts     DialOptions
	closed   bool
	sessions map[io_Closer]struct{}
}

// Open opens the serial port named by opts.Port and returns a Client ready
// to read from it. See DefaultOptions for the documented zero-value
// defaults applied to opts.
func Open(opts DialOptions) (*Client, error) {
	resolved := opts.resolved()
	switch {
	case resolved.Format.Parse != nil:
		// caller supplied a preset or a fully-built custom format
	case resolved.Format.Name == "" && resolved.Format.MinLen == 0:
		resolved.Format = Format22Byte // untouched zero value: use the default
	default:
		return nil, opErr("Open", ErrUnpopulatedFormat)
	}

	dataBits := 8
	if resolved.Parity != serial.NoParity {
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

	return &Client{
		port:     port,
		opts:     resolved,
		sessions: make(map[io_Closer]struct{}),
	}, nil
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
