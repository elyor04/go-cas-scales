package cas

import (
	"context"
	"fmt"
	"time"
)

// RequestOne asks the indicator for a single frame and returns it. It's the
// counterpart to Set Mode F31/F35 = 3 ("send upon data request"): per the
// manual, the request signal is one raw binary byte equal to the F26
// device ID (e.g. device 10 is requested with byte 0x0A) — not an ASCII
// digit string. Do not confuse this with the ASCII "D dd CODE CR LF"
// framing used by the command-mode methods (Zero, Gross, Net, ...), which
// requires F31/F35 = 4 instead and is a different indicator mode.
//
// The call is bounded by DialOptions.ReadTimeout, shortened to ctx's
// deadline if ctx has one and it's sooner: RequestOne always returns
// control to the caller by then. This is enforced independently of the
// underlying port's read timeout, which real-hardware testing found some
// platform/driver combinations don't honor reliably when the indicator
// stays silent (observed blocking for minutes instead of the configured
// duration). If the read is still outstanding when the deadline passes,
// the port remains held until it eventually completes in the background —
// the OS read call itself can't be forcibly interrupted without closing
// the port out from under any other caller — so a queued RequestOne,
// Stream, or command-mode call may itself wait that long for the port.
func (c *Client) RequestOne(ctx context.Context) (Reading, error) {
	if c.isClosed() {
		return Reading{}, opErr("RequestOne", ErrClosed)
	}
	if err := c.acquirePort(ctx); err != nil {
		return Reading{}, opErr("RequestOne", err)
	}
	if c.isClosed() {
		c.releasePort()
		return Reading{}, opErr("RequestOne", ErrClosed)
	}

	timeout := c.opts.ReadTimeout
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d > 0 && d < timeout {
			timeout = d
		}
	}
	if err := c.port.SetReadTimeout(timeout); err != nil {
		c.releasePort()
		return Reading{}, opErr("RequestOne", err)
	}

	if _, err := c.port.Write([]byte{byte(c.opts.DeviceID)}); err != nil {
		c.releasePort()
		return Reading{}, opErr("RequestOne", fmt.Errorf("write request byte: %w", err))
	}

	line, err := c.readLineBounded(ctx, timeout)
	if err != nil {
		return Reading{}, opErr("RequestOne", err)
	}

	r, err := c.opts.Format.Parse([]byte(line))
	if err != nil {
		return Reading{}, opErr("RequestOne", err)
	}
	return r, nil
}

// readLineBounded reads one CR/LF-terminated line from c.reader, returning
// control to the caller no later than timeout (or ctx's own cancellation,
// if earlier) regardless of whether the underlying read has actually
// completed. It always releases the port — either immediately if the read
// already lost the race, or from the background goroutine once the read
// finally does complete — so callers must not also call c.releasePort.
func (c *Client) readLineBounded(ctx context.Context, timeout time.Duration) (string, error) {
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := c.reader.ReadString('\n')
		done <- result{line, err}
		c.releasePort()
	}()

	select {
	case res := <-done:
		if res.err != nil && res.line == "" {
			return "", fmt.Errorf("no data (timeout after %s): %w", timeout, res.err)
		}
		return res.line, nil
	case <-ctx.Done():
		return "", fmt.Errorf("no data (gave up after %s, port still busy until the read completes): %w", timeout, ctx.Err())
	case <-time.After(timeout):
		return "", fmt.Errorf("no data (timeout after %s, port still busy until the read completes): %w", timeout, context.DeadlineExceeded)
	}
}
