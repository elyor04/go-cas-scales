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
// Wire layout (after the CR/LF that terminates every frame is stripped),
// by byte offset:
//
//	0-1   <US|ST|OL>   status
//	2     ","
//	3-4   <GS|NT>      weight type
//	5     ","
//	6     device ID    one raw binary byte, the F26 value (device 12 = 0x0C)
//	7     lamp byte    see below
//	8     ","
//	9-16  weight       8 ASCII bytes
//	17    " "          the manual's "Empty" byte
//	18-19 unit         "kg" or "t "
//
// Captured from a real CI-200A (device 00, empty platform, stable):
//
//	53 54 2C 47 53 2C 00 C5 2C 20 20 20 20 20 30 2E 30 20 6B 67 0D 0A
//	S  T  ,  G  S  ,  id lamp ,  .  .  .  .  .  0  .  0     k  g  CR LF
//
// The device ID and the lamp byte are binary, so the frame is decoded by
// position rather than by splitting on commas: device 44 is sent as 0x2C,
// which is itself a comma.
//
// The lamp byte is, from bit 7 down: 1 (fixed), Stable, 0 (fixed), Hold,
// Printer, Gross, Tare, Zero point (0xC5 above = fixed + Stable + Gross +
// Zero). Hold, Tare and AtZero are read from it; Stable and Net come from
// the text fields. A byte whose fixed bits are wrong is not trusted, and
// Hold/Tare/AtZero are then left false.
//
// The weight field already includes the decimal point and, for negative
// readings, a leading '-'. The manual's example pads it with zeros (13.5 kg
// as "000013.5"); the hardware above pads it with spaces ("     0.0").
// Either is handed to strconv.ParseFloat once trimmed.
const (
	name22Byte = "22-byte CAS"
	minLen22   = 20 // 2+1+2+1+1+1+1+8+1+2, i.e. the frame minus its CR/LF

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

	if len(content) != minLen22 {
		return Reading{}, fmt.Errorf("%s: expected %d bytes, got %d: %q", name22Byte, minLen22, len(content), content)
	}
	if content[2] != ',' || content[5] != ',' || content[8] != ',' {
		return Reading{}, fmt.Errorf("%s: no separator at byte 2, 5 or 8: %q", name22Byte, content)
	}
	status, wtype := content[0:2], content[3:5]
	idByte, lampByte := content[6], content[7]
	weightStr, unit := content[9:17], content[18:20]

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

	// F26 only goes to 99; a byte above that is line noise rather than an
	// ID, and DeviceID degrades to the -1 used when a format has no ID.
	// The rest of the frame still decodes.
	r.DeviceID = -1
	if idByte <= 99 {
		r.DeviceID = int(idByte)
	}

	v, err := strconv.ParseFloat(strings.TrimSpace(weightStr), 64)
	if err != nil {
		return Reading{}, fmt.Errorf("%s: weight field %q: %w", name22Byte, weightStr, err)
	}
	r.Value = v
	r.Unit = strings.TrimSpace(unit)

	// Stable and Gross are also in the lamp byte, but the text fields above
	// are authoritative for those; only the bits with no other
	// representation are read here.
	if lampByteValid(lampByte) {
		r.Hold = lampByte&(1<<4) != 0
		r.Tare = lampByte&(1<<1) != 0
		r.AtZero = lampByte&(1<<0) != 0
	}

	return r, nil
}

// lampByteValid reports whether b has the lamp byte's two fixed bits as the
// manual documents them: bit 7 set and bit 5 clear. Anything else means the
// byte at that position isn't a lamp byte, and its other bits mean nothing.
func lampByteValid(b byte) bool {
	return b&(1<<7) != 0 && b&(1<<5) == 0
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
