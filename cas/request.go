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
// The read is bounded by DialOptions.ReadTimeout, shortened to ctx's
// deadline if ctx has one and it's sooner.
func (c *Client) RequestOne(ctx context.Context) (Reading, error) {
	if c.isClosed() {
		return Reading{}, opErr("RequestOne", ErrClosed)
	}
	if err := c.acquirePort(ctx); err != nil {
		return Reading{}, opErr("RequestOne", err)
	}
	defer c.releasePort()
	if c.isClosed() {
		return Reading{}, opErr("RequestOne", ErrClosed)
	}

	timeout := c.opts.ReadTimeout
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d > 0 && d < timeout {
			timeout = d
		}
	}
	if err := c.port.SetReadTimeout(timeout); err != nil {
		return Reading{}, opErr("RequestOne", err)
	}

	if _, err := c.port.Write([]byte{byte(c.opts.DeviceID)}); err != nil {
		return Reading{}, opErr("RequestOne", fmt.Errorf("write request byte: %w", err))
	}

	line, err := c.reader.ReadString('\n')
	if err != nil && line == "" {
		return Reading{}, opErr("RequestOne", fmt.Errorf("no data (timeout after %s): %w", timeout, err))
	}

	r, err := c.opts.Format.Parse([]byte(line))
	if err != nil {
		return Reading{}, opErr("RequestOne", err)
	}
	return r, nil
}
