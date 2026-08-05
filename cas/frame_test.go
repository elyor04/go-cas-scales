package cas

import (
	"errors"
	"testing"
)

// lamp22 builds a Format22Byte lamp-status byte from the bits this package
// actually reads (Hold, Tare, AtZero), with the two fixed bits (7 and 5)
// set as the manual documents them.
func lamp22(hold, tare, atZero bool) byte {
	b := byte(0x80) // bit7 fixed 1
	if hold {
		b |= 1 << 4
	}
	if tare {
		b |= 1 << 1
	}
	if atZero {
		b |= 1 << 0
	}
	return b
}

func TestParseCAS22StableGross(t *testing.T) {
	// Built from the manual's own worked example: 13.5 kg is sent as the
	// 8-byte ASCII field "000013.5".
	content := "ST,GS,01,000013.5" + string([]byte{lamp22(false, false, false)}) + "kg"

	r, err := parseCAS22([]byte(content))
	if err != nil {
		t.Fatalf("parseCAS22(%q) error = %v", content, err)
	}
	want := Reading{Value: 13.5, Unit: "kg", Stable: true, Net: false, DeviceID: 1, Raw: content}
	if r != want {
		t.Errorf("parseCAS22(%q) = %+v, want %+v", content, r, want)
	}
}

func TestParseCAS22TrailingCRLFStripped(t *testing.T) {
	content := "ST,GS,01,000013.5" + string([]byte{lamp22(false, false, false)}) + "kg"
	r, err := parseCAS22([]byte(content + "\r\n"))
	if err != nil {
		t.Fatalf("parseCAS22 with CRLF: error = %v", err)
	}
	if r.Raw != content {
		t.Errorf("Raw = %q, want CR/LF stripped %q", r.Raw, content)
	}
}

func TestParseCAS22NetUnstableWithLampBits(t *testing.T) {
	content := "US,NT,42,-00120.0" + string([]byte{lamp22(true, true, true)}) + "lb"
	r, err := parseCAS22([]byte(content))
	if err != nil {
		t.Fatalf("parseCAS22(%q) error = %v", content, err)
	}
	want := Reading{Value: -120.0, Unit: "lb", Stable: false, Net: true, Hold: true, Tare: true, AtZero: true, DeviceID: 42, Raw: content}
	if r != want {
		t.Errorf("parseCAS22(%q) = %+v, want %+v", content, r, want)
	}
}

func TestParseCAS22Overload(t *testing.T) {
	content := "OL,GS,00,999999.9" + string([]byte{lamp22(false, false, false)}) + "kg"
	r, err := parseCAS22([]byte(content))
	if err != nil {
		t.Fatalf("parseCAS22(%q) error = %v", content, err)
	}
	if !r.Overload {
		t.Errorf("Overload = false, want true for status %q", content[:2])
	}
}

func TestParseCAS22Errors(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"too short", "ST,GS,01,00"},
		{"wrong field count", "ST,GS,000013.5kg"},
		{"bad status", "XX,GS,01,000013.5" + string([]byte{lamp22(false, false, false)}) + "kg"},
		{"bad weight type", "ST,XX,01,000013.5" + string([]byte{lamp22(false, false, false)}) + "kg"},
		{"non numeric weight", "ST,GS,01,NOTANUM." + string([]byte{lamp22(false, false, false)}) + "kg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseCAS22([]byte(tc.content)); err == nil {
				t.Errorf("parseCAS22(%q) succeeded, want error", tc.content)
			}
		})
	}
}

func TestParseCAS22ShortFrameWrapsErrShortFrame(t *testing.T) {
	_, err := parseCAS22([]byte("ST,GS,01"))
	if !errors.Is(err, ErrShortFrame) {
		t.Errorf("error = %v, want it to wrap ErrShortFrame", err)
	}
}

func TestParseCAS10(t *testing.T) {
	r, err := parseCAS10([]byte("000013.5"))
	if err != nil {
		t.Fatalf("parseCAS10 error = %v", err)
	}
	want := Reading{Value: 13.5, DeviceID: -1, Raw: "000013.5"}
	if r != want {
		t.Errorf("parseCAS10 = %+v, want %+v", r, want)
	}
}

func TestParseCAS10TrailingCRLFStripped(t *testing.T) {
	r, err := parseCAS10([]byte("000013.5\r\n"))
	if err != nil {
		t.Fatalf("parseCAS10 with CRLF: error = %v", err)
	}
	if r.Value != 13.5 {
		t.Errorf("Value = %v, want 13.5", r.Value)
	}
}

func TestParseCAS10NonNumeric(t *testing.T) {
	if _, err := parseCAS10([]byte("NOTANUM.")); err == nil {
		t.Error("parseCAS10 succeeded on non-numeric input, want error")
	}
}

func TestParseAND18(t *testing.T) {
	content := "ST,GS,000013.5kg"
	r, err := parseAND18([]byte(content))
	if err != nil {
		t.Fatalf("parseAND18(%q) error = %v", content, err)
	}
	want := Reading{Value: 13.5, Unit: "kg", Stable: true, Net: false, DeviceID: -1, Raw: content}
	if r != want {
		t.Errorf("parseAND18(%q) = %+v, want %+v", content, r, want)
	}
}

func TestParseAND18NoDeviceIDOrLampBits(t *testing.T) {
	// AND format has no device-ID or lamp-byte fields at all; confirm the
	// zero-value/-1 sentinel documented on Reading holds.
	r, err := parseAND18([]byte("US,NT,-00050.2lb"))
	if err != nil {
		t.Fatalf("parseAND18 error = %v", err)
	}
	if r.DeviceID != -1 {
		t.Errorf("DeviceID = %d, want -1 (AND format carries no device ID)", r.DeviceID)
	}
	if r.Hold || r.Tare || r.AtZero {
		t.Errorf("Hold/Tare/AtZero = %v/%v/%v, want all false (AND format carries no lamp byte)", r.Hold, r.Tare, r.AtZero)
	}
}

func TestParseAND18Errors(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"too short", "ST,GS,0000"},
		{"wrong field count", "ST,GS,NT,000013.5kg"},
		{"bad status", "XX,GS,000013.5kg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseAND18([]byte(tc.content)); err == nil {
				t.Errorf("parseAND18(%q) succeeded, want error", tc.content)
			}
		})
	}
}
