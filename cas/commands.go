package cas

import (
	"context"
	"fmt"
	"time"
)

// SendCommand is the low-level command-mode primitive: it builds a
// "D<deviceID><code><data>\r\n" request frame (Set Mode F31 = 4, COM1
// only), writes it, and returns whatever the indicator sends back
// before DialOptions.ReadTimeout (or ctx's deadline, if sooner) expires.
// As with RequestOne, that bound is enforced independently of the
// underlying port's own read timeout — see RequestOne's doc comment for
// why, and for the port-stays-busy tradeoff that implies if the indicator
// never answers.
//
// It exists as an escape hatch for command codes not wrapped by a named
// method below — most notably KT, which the manual lists as "Zero Point
// Key" with an identical description to KZ. That looks like a
// documentation error, but since it's not clear what KT actually does on
// real hardware, this package doesn't guess by exposing a Client.KT method;
// send it yourself via SendCommand if you need to find out.
//
// The manual documents the response to every command only as "Received
// Data Return," without a byte layout. The value-carrying codes (HY, HI,
// HL, ID) take a 5-digit data field and set that value; none of them reads
// one back (see KeyTareValue).
func (c *Client) SendCommand(ctx context.Context, code, data string) ([]byte, error) {
	if c.isClosed() {
		return nil, opErr("SendCommand", ErrClosed)
	}
	if err := c.acquirePort(ctx); err != nil {
		return nil, opErr("SendCommand", err)
	}
	if c.isClosed() {
		c.releasePort()
		return nil, opErr("SendCommand", ErrClosed)
	}

	timeout := c.opts.ReadTimeout
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d > 0 && d < timeout {
			timeout = d
		}
	}
	if err := c.port.SetReadTimeout(timeout); err != nil {
		c.releasePort()
		return nil, opErr("SendCommand", err)
	}

	frame := fmt.Sprintf("D%02d%s%s\r\n", c.opts.DeviceID, code, data)
	if _, err := c.port.Write([]byte(frame)); err != nil {
		c.releasePort()
		return nil, opErr("SendCommand", fmt.Errorf("write %q: %w", frame, err))
	}

	line, err := c.readLineBounded(ctx, timeout)
	if err != nil {
		return nil, opErr("SendCommand", fmt.Errorf("no response to %q: %w", code, err))
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

// KeyTareValue used to read the key-tare value by sending "HY00000". That
// is byte for byte the frame SetKeyTareValue(0) sends: the manual's command
// table gives HY, HI and HL a 5-digit value field and documents every reply
// only as "Received Data Return", i.e. an echo. So the "read" could only
// ever clear the stored value and hand back the echoed zero. It now returns
// ErrNoReadCommand without touching the port.
//
// Deprecated: the CI-200 command set has no way to read this value.
func (c *Client) KeyTareValue(ctx context.Context) (float64, error) {
	return 0, opErr("KeyTareValue", ErrNoReadCommand)
}

// SetKeyTareValue writes the indicator's key-tare value (command code HY).
// See limitField for how v is encoded.
func (c *Client) SetKeyTareValue(ctx context.Context, v float64) error {
	_, err := c.SendCommand(ctx, "HY", limitField(v))
	return opErr("SetKeyTareValue", err)
}

// HighLimit used to read the high limit by sending "HI00000"; like
// KeyTareValue, that frame can only set the limit to 0. It now returns
// ErrNoReadCommand without touching the port (ErrUnsupportedByModel first,
// on a model without limits).
//
// Deprecated: the CI-200 command set has no way to read this value.
func (c *Client) HighLimit(ctx context.Context) (float64, error) {
	if !c.opts.Model.SupportsLimits() {
		return 0, opErr("HighLimit", ErrUnsupportedByModel)
	}
	return 0, opErr("HighLimit", ErrNoReadCommand)
}

// LowLimit is HighLimit's counterpart for the low limit (HL).
//
// Deprecated: the CI-200 command set has no way to read this value.
func (c *Client) LowLimit(ctx context.Context) (float64, error) {
	if !c.opts.Model.SupportsLimits() {
		return 0, opErr("LowLimit", ErrUnsupportedByModel)
	}
	return 0, opErr("LowLimit", ErrNoReadCommand)
}

// SetHighLimit writes the indicator's high limit (command code HI). Returns
// ErrUnsupportedByModel unless the configured Model reports SupportsLimits
// (CI-201A, CI-200SC — the manual documents this command as "LCD, SC
// Only"). See limitField for how v is encoded.
func (c *Client) SetHighLimit(ctx context.Context, v float64) error {
	if !c.opts.Model.SupportsLimits() {
		return opErr("SetHighLimit", ErrUnsupportedByModel)
	}
	_, err := c.SendCommand(ctx, "HI", limitField(v))
	return opErr("SetHighLimit", err)
}

// SetLowLimit writes the indicator's low limit (command code HL). See
// SetHighLimit.
func (c *Client) SetLowLimit(ctx context.Context, v float64) error {
	if !c.opts.Model.SupportsLimits() {
		return opErr("SetLowLimit", ErrUnsupportedByModel)
	}
	_, err := c.SendCommand(ctx, "HL", limitField(v))
	return opErr("SetLowLimit", err)
}

// limitField encodes a value for the 5-digit field of HY/HI/HL. The manual
// doesn't fully specify it (a related command table notes only "DATA (Not
// include decimal point)"); this sends v rounded to the nearest whole unit,
// zero-padded. Verify against real hardware before relying on it for
// non-integer values.
func limitField(v float64) string {
	return fmt.Sprintf("%05d", int64(v+0.5))
}
