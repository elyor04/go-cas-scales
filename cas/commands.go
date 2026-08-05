package cas

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SendCommand is the low-level command-mode primitive: it builds a
// "D<deviceID><code><data>\r\n" request frame (Set Mode F31/F35 = 4,
// COM1 only), writes it, and returns whatever the indicator sends back
// before DialOptions.ReadTimeout (or ctx's deadline, if sooner) expires.
//
// It exists as an escape hatch for command codes not wrapped by a named
// method below — most notably KT, which the manual lists as "Zero Point
// Key" with an identical description to KZ. That looks like a
// documentation error, but since it's not clear what KT actually does on
// real hardware, this package doesn't guess by exposing a Client.KT method;
// send it yourself via SendCommand if you need to find out.
//
// The manual only documents the response to every command generically as
// "Received Data Return," without giving an exact byte layout distinct
// from the normal weight frame for the read-value commands (HI, HL, ID,
// HY) — the named wrappers for those (KeyTareValue, HighLimit, LowLimit)
// make a best-effort attempt to find a trailing numeric value in whatever
// comes back. Verify against real hardware before relying on it.
func (c *Client) SendCommand(ctx context.Context, code, data string) ([]byte, error) {
	if c.isClosed() {
		return nil, opErr("SendCommand", ErrClosed)
	}
	if err := c.acquirePort(ctx); err != nil {
		return nil, opErr("SendCommand", err)
	}
	defer c.releasePort()
	if c.isClosed() {
		return nil, opErr("SendCommand", ErrClosed)
	}

	timeout := c.opts.ReadTimeout
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d > 0 && d < timeout {
			timeout = d
		}
	}
	if err := c.port.SetReadTimeout(timeout); err != nil {
		return nil, opErr("SendCommand", err)
	}

	frame := fmt.Sprintf("D%02d%s%s\r\n", c.opts.DeviceID, code, data)
	if _, err := c.port.Write([]byte(frame)); err != nil {
		return nil, opErr("SendCommand", fmt.Errorf("write %q: %w", frame, err))
	}

	line, err := c.reader.ReadString('\n')
	if err != nil && line == "" {
		return nil, opErr("SendCommand", fmt.Errorf("no response to %q (timeout after %s): %w", code, timeout, err))
	}
	return []byte(line), nil
}

// Zero emulates a press of the indicator's ZERO key (command code KZ).
func (c *Client) Zero(ctx context.Context) error {
	_, err := c.SendCommand(ctx, "KZ", "")
	return opErr("Zero", err)
}

// Gross switches the indicator to display gross weight (command code KG).
func (c *Client) Gross(ctx context.Context) error {
	_, err := c.SendCommand(ctx, "KG", "")
	return opErr("Gross", err)
}

// Net switches the indicator to display net weight (command code KN).
func (c *Client) Net(ctx context.Context) error {
	_, err := c.SendCommand(ctx, "KN", "")
	return opErr("Net", err)
}

// Hold emulates a press of the indicator's HOLD key (command code HD).
func (c *Client) Hold(ctx context.Context) error {
	_, err := c.SendCommand(ctx, "HD", "")
	return opErr("Hold", err)
}

// Print emulates a press of the indicator's PRINT key (command code KB).
func (c *Client) Print(ctx context.Context) error {
	_, err := c.SendCommand(ctx, "KB", "")
	return opErr("Print", err)
}

// TotalPrint requests a grand-total print (command code KC).
func (c *Client) TotalPrint(ctx context.Context) error {
	_, err := c.SendCommand(ctx, "KC", "")
	return opErr("TotalPrint", err)
}

// RequestWeight requests one weight frame in command mode (command code
// KW) and parses it with the Client's configured FrameFormat.
func (c *Client) RequestWeight(ctx context.Context) (Reading, error) {
	resp, err := c.SendCommand(ctx, "KW", "")
	if err != nil {
		return Reading{}, opErr("RequestWeight", err)
	}
	r, err := c.opts.Format.Parse(resp)
	if err != nil {
		return Reading{}, opErr("RequestWeight", err)
	}
	return r, nil
}

// KeyTareValue reads the indicator's stored key-tare value (command code
// HY). See the SendCommand doc comment for the caveat on response parsing.
func (c *Client) KeyTareValue(ctx context.Context) (float64, error) {
	resp, err := c.SendCommand(ctx, "HY", "00000")
	if err != nil {
		return 0, opErr("KeyTareValue", err)
	}
	v, err := trailingNumber(resp)
	return v, opErr("KeyTareValue", err)
}

// SetKeyTareValue writes the indicator's key-tare value (command code HY).
// The manual doesn't fully specify the write encoding for this field (a
// related command table for it notes only "DATA (Not include decimal
// point)"); this sends v rounded to the nearest whole unit as a 5-digit,
// zero-padded field. Verify against real hardware before relying on it for
// non-integer tare values.
func (c *Client) SetKeyTareValue(ctx context.Context, v float64) error {
	_, err := c.SendCommand(ctx, "HY", fmt.Sprintf("%05d", int64(v+0.5)))
	return opErr("SetKeyTareValue", err)
}

// HighLimit reads the indicator's high-limit value (command code HI).
// Returns ErrUnsupportedByModel unless the configured Model reports
// SupportsLimits (CI-201A, CI-200SC — the manual documents this command as
// "LCD, SC Only").
func (c *Client) HighLimit(ctx context.Context) (float64, error) {
	if !c.opts.Model.SupportsLimits() {
		return 0, opErr("HighLimit", ErrUnsupportedByModel)
	}
	resp, err := c.SendCommand(ctx, "HI", "00000")
	if err != nil {
		return 0, opErr("HighLimit", err)
	}
	v, err := trailingNumber(resp)
	return v, opErr("HighLimit", err)
}

// LowLimit reads the indicator's low-limit value (command code HL). See
// HighLimit for the Model restriction.
func (c *Client) LowLimit(ctx context.Context) (float64, error) {
	if !c.opts.Model.SupportsLimits() {
		return 0, opErr("LowLimit", ErrUnsupportedByModel)
	}
	resp, err := c.SendCommand(ctx, "HL", "00000")
	if err != nil {
		return 0, opErr("LowLimit", err)
	}
	v, err := trailingNumber(resp)
	return v, opErr("LowLimit", err)
}

// trailingNumber extracts the trailing run of digits/sign/decimal-point
// characters from a command response and parses it as a float, ignoring
// whatever prefix (echoed device ID, command code, frame status fields)
// precedes it.
func trailingNumber(resp []byte) (float64, error) {
	s := strings.TrimSpace(string(trimFrame(resp)))
	i := strings.LastIndexFunc(s, func(r rune) bool {
		return !(r == '-' || r == '.' || (r >= '0' && r <= '9'))
	})
	numStr := s[i+1:]
	v, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("no numeric value found in response %q: %w", s, err)
	}
	return v, nil
}
