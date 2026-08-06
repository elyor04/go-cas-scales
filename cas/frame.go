package cas

import (
	"fmt"
	"strconv"
	"strings"
)

// FrameFormat describes how to decode one line of indicator output into a
// Reading. The three package-level presets (Format22Byte, Format10Byte,
// Format18ByteAND) cover every format documented in manual section 11-3;
// a caller with a different/custom device can build their own FrameFormat
// by supplying Parse.
type FrameFormat struct {
	// Name identifies the format for error messages, e.g. "22-byte CAS".
	Name string
	// MinLen is the shortest frame (CR/LF already stripped) this format
	// can produce; frames shorter than this are rejected before Parse
	// is even called.
	MinLen int
	// Parse decodes one CR/LF-stripped frame into a Reading.
	Parse func(raw []byte) (Reading, error)
}

// Format22Byte is the 22-byte CAS frame (manual section 11-3-1), used when
// F30/F34 = 0. This is the default and the only format that carries a
// device ID and the Hold/Tare/Zero lamp bits.
//
// Wire layout (after the CR/LF that terminates every frame is stripped):
//
//	<US|ST|OL>(2) "," <GS|NT>(2) "," <deviceID>(2) "," <weight>(8) <lampByte>(1) <unit>(2)
//
// The weight field is 8 ASCII bytes that already include the decimal point
// and, for negative readings, a leading '-' (e.g. "13.5" kg is sent as
// "000013.5" — see the manual's own worked example), so it can be handed
// straight to strconv.ParseFloat once trimmed.
//
// The device ID sub-field is documented as 2 bytes but real hardware has
// been observed sending it as raw binary rather than 2 ASCII digits (see
// Reading.DeviceID); when that happens DeviceID recovers a best-effort
// value from the raw byte if it's plausible, or -1 otherwise, while Value,
// Unit, Stable, Net, and the lamp bits are still populated normally either
// way.
const (
	name22Byte = "22-byte CAS"
	minLen22   = 20 // 2+1+2+1+2+1+8+1+2, i.e. the frame minus its CR/LF

	name10Byte = "10-byte CAS"
	minLen10   = 8

	name18ByteAND = "18-byte AND"
	minLen18      = 16
)

var Format22Byte = FrameFormat{
	Name:   name22Byte,
	MinLen: minLen22,
	Parse:  parseCAS22,
}

// Format10Byte is the 10-byte CAS frame (manual section 11-3-2), used when
// F30/F34 = 1. It carries nothing but the raw weight digits — no status,
// device ID, or lamp byte — so Reading.Unit, Stable, Overload, Net, Hold,
// Tare, and AtZero are left at their zero value and DeviceID is -1.
var Format10Byte = FrameFormat{
	Name:   name10Byte,
	MinLen: minLen10,
	Parse:  parseCAS10,
}

// Format18ByteAND is the 18-byte AND-compatible frame (manual section
// 11-3-3), used when F30/F34 = 2. Note the indicator switches to 7 data
// bits + parity for this format (F27 = 1 or 2); DialOptions.Parity must be
// set to match. It carries status and weight type but no device ID or lamp
// byte, so DeviceID is -1 and Hold/Tare/AtZero are left false.
var Format18ByteAND = FrameFormat{
	Name:   name18ByteAND,
	MinLen: minLen18,
	Parse:  parseAND18,
}

// trimFrame strips a trailing CR and/or LF, matching how frames are
// delimited on the wire (every documented format ends with CR LF).
func trimFrame(raw []byte) []byte {
	return []byte(strings.TrimRight(string(raw), "\r\n"))
}

func parseCAS22(raw []byte) (Reading, error) {
	content := string(trimFrame(raw))
	if len(content) < minLen22 {
		return Reading{}, fmt.Errorf("%s: %w (got %d bytes, want at least %d): %q",
			name22Byte, ErrShortFrame, len(content), minLen22, content)
	}

	parts := strings.SplitN(content, ",", 4)
	if len(parts) != 4 {
		return Reading{}, fmt.Errorf("%s: expected 4 comma-separated fields, got %d: %q", name22Byte, len(parts), content)
	}
	status, wtype, idStr, rest := parts[0], parts[1], parts[2], parts[3]
	if len(status) != 2 || len(wtype) != 2 || len(idStr) != 2 {
		return Reading{}, fmt.Errorf("%s: malformed status/type/device-id fields: %q", name22Byte, content)
	}
	if len(rest) != 11 {
		return Reading{}, fmt.Errorf("%s: expected 11 bytes after device ID (weight+lamp+unit), got %d: %q", name22Byte, len(rest), content)
	}

	r := Reading{Raw: content}

	switch status {
	case "ST":
		r.Stable = true
	case "US":
		r.Stable = false
	case "OL":
		r.Overload = true
	default:
		return Reading{}, fmt.Errorf("%s: unexpected status field %q", name22Byte, status)
	}

	switch wtype {
	case "GS":
		r.Net = false
	case "NT":
		r.Net = true
	default:
		return Reading{}, fmt.Errorf("%s: unexpected weight-type field %q", name22Byte, wtype)
	}

	// Real-hardware testing found at least one CI-200A that sends this
	// field as raw binary rather than the 2 ASCII digits the manual's
	// byte-count implies: the first byte tracked F26 exactly across every
	// value tried (e.g. device 12 as the single byte 0x0C), while the
	// second appeared to carry unrelated status-like information rather
	// than being part of the ID. If the field doesn't parse as ASCII
	// decimal, fall back to that raw-binary reading of the first byte when
	// it falls within F26's documented 00-99 range — otherwise it's more
	// likely line noise than a real ID, so DeviceID degrades to the same
	// -1 sentinel used when a format carries no device ID at all. Either
	// way the rest of an otherwise fully decodable Reading isn't discarded
	// over this secondary field.
	id, err := strconv.Atoi(idStr)
	switch {
	case err == nil:
		r.DeviceID = id
	case int(idStr[0]) <= 99:
		r.DeviceID = int(idStr[0])
	default:
		r.DeviceID = -1
	}

	weightStr, lampByte, unit := rest[0:8], rest[8], rest[9:11]
	v, err := strconv.ParseFloat(strings.TrimSpace(weightStr), 64)
	if err != nil {
		return Reading{}, fmt.Errorf("%s: weight field %q: %w", name22Byte, weightStr, err)
	}
	r.Value = v
	r.Unit = strings.TrimSpace(unit)

	// Lamp status byte: bit7=1 (fixed), bit6=Stable, bit5=0 (fixed),
	// bit4=Hold, bit3=Printer, bit2=Gross, bit1=Tare, bit0=ZeroPoint.
	// Stable/Gross are already derived from the text fields above, which
	// are treated as authoritative; only the bits with no other
	// representation are read from the lamp byte.
	r.Hold = lampByte&(1<<4) != 0
	r.Tare = lampByte&(1<<1) != 0
	r.AtZero = lampByte&(1<<0) != 0

	return r, nil
}

func parseCAS10(raw []byte) (Reading, error) {
	content := string(trimFrame(raw))
	if len(content) < minLen10 {
		return Reading{}, fmt.Errorf("%s: %w (got %d bytes, want at least %d): %q",
			name10Byte, ErrShortFrame, len(content), minLen10, content)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(content), 64)
	if err != nil {
		return Reading{}, fmt.Errorf("%s: weight field %q: %w", name10Byte, content, err)
	}
	return Reading{Value: v, DeviceID: -1, Raw: content}, nil
}

func parseAND18(raw []byte) (Reading, error) {
	content := string(trimFrame(raw))
	if len(content) < minLen18 {
		return Reading{}, fmt.Errorf("%s: %w (got %d bytes, want at least %d): %q",
			name18ByteAND, ErrShortFrame, len(content), minLen18, content)
	}

	parts := strings.SplitN(content, ",", 3)
	if len(parts) != 3 {
		return Reading{}, fmt.Errorf("%s: expected 3 comma-separated fields, got %d: %q", name18ByteAND, len(parts), content)
	}
	status, wtype, rest := parts[0], parts[1], parts[2]
	if len(status) != 2 || len(wtype) != 2 {
		return Reading{}, fmt.Errorf("%s: malformed status/type fields: %q", name18ByteAND, content)
	}
	if len(rest) != 10 {
		return Reading{}, fmt.Errorf("%s: expected 10 bytes after weight type (weight+unit), got %d: %q", name18ByteAND, len(rest), content)
	}

	r := Reading{DeviceID: -1, Raw: content}

	switch status {
	case "ST":
		r.Stable = true
	case "US":
		r.Stable = false
	case "OL":
		r.Overload = true
	default:
		return Reading{}, fmt.Errorf("%s: unexpected status field %q", name18ByteAND, status)
	}

	switch wtype {
	case "GS":
		r.Net = false
	case "NT":
		r.Net = true
	default:
		return Reading{}, fmt.Errorf("%s: unexpected weight-type field %q", name18ByteAND, wtype)
	}

	weightStr, unit := rest[0:8], rest[8:10]
	v, err := strconv.ParseFloat(strings.TrimSpace(weightStr), 64)
	if err != nil {
		return Reading{}, fmt.Errorf("%s: weight field %q: %w", name18ByteAND, weightStr, err)
	}
	r.Value = v
	r.Unit = strings.TrimSpace(unit)

	return r, nil
}
