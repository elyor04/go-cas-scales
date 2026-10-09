package cas

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
)

// RequestOne asks the indicator for a single frame and returns it. It's the
// counterpart to Set Mode F31 = 3 ("send upon data request"), which exists
// on COM1 only (F35, COM2's output mode, goes up to 2): per the manual, the
// request signal is one raw binary byte equal to the F26 device ID (e.g.
// device 10 is requested with byte 0x0A) — not an ASCII digit string. Do
// not confuse this with the ASCII "D dd CODE CR LF" framing used by the
// command-mode methods (Zero, Gross, Net, ...), which requires F31 = 4
// instead and is a different indicator mode.
//
// The call is bounded by DialOptions.ReadTimeout, shortened to ctx's
// deadline if ctx has one and it's sooner: RequestOne always returns
// control to the caller by then. The port's own read timeout is set to the
// same bound, so the port is free again at that point too. A Transport
// whose Read ignores SetReadTimeout still returns control on time, but
// keeps the port until its read completes in the background (it can't be
// interrupted without closing the port), so a queued call waits for that.
//
// Anything received before the request is written is discarded first (see
// discardInput), so a reply that arrives after its request timed out is
// never taken as the next request's answer.
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
	if err := c.discardInput(); err != nil {
		c.releasePort()
		return Reading{}, opErr("RequestOne", fmt.Errorf("discard stale input: %w", err))
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
		line, err := readCRLF(c.reader)
		done <- result{line, err}
		c.releasePort()
	}()

	select {
	case res := <-done:
		err := res.err
		if errors.Is(err, errReadTimeout) {
			err = context.DeadlineExceeded
		}
		switch {
		case err == nil:
			return res.line, nil
		case res.line != "":
			return "", fmt.Errorf("incomplete frame %q: %w", res.line, err)
		case err == context.DeadlineExceeded:
			return "", fmt.Errorf("no data (timeout after %s): %w", timeout, err)
		default:
			return "", fmt.Errorf("no data: %w", err)
		}
	case <-ctx.Done():
		return "", fmt.Errorf("no data: %w", ctx.Err())
	case <-time.After(timeout):
		return "", fmt.Errorf("no data (timeout after %s): %w", timeout, context.DeadlineExceeded)
	}
}

// readCRLF reads up to and including the next CR LF pair, the terminator of
// every documented frame. A lone LF is not enough: Format22Byte's device-ID
// byte is binary, so device 10 puts a 0x0A in the middle of the frame. It
// can't fake the pair, because the byte before it is always a comma and
// the lamp byte after a device-13 0x0D always has bit 7 set.
func readCRLF(r *bufio.Reader) (string, error) {
	var line []byte
	for {
		chunk, err := r.ReadBytes('\n')
		line = append(line, chunk...)
		if err != nil || bytes.HasSuffix(line, []byte("\r\n")) {
			return string(line), err
		}
	}
}
