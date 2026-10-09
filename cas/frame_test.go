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

// cas22 builds a Format22Byte frame (without its CR/LF) in the manual's
// layout: the device ID and lamp byte are single raw bytes between the
// second and third commas, and a space separates the weight from the unit.
func cas22(status, wtype string, id, lamp byte, weight, unit string) string {
	return status + "," + wtype + "," + string([]byte{id, lamp}) + "," + weight + " " + unit
}

// realCI200AEmptyStable is a frame captured from a real CI-200A on an empty
// platform (device 00, stable, gross, zero lamp lit), byte for byte.
var realCI200AEmptyStable = []byte{
	0x53, 0x54, 0x2C, 0x47, 0x53, 0x2C, 0x00, 0xC5, 0x2C, 0x20, 0x20,
	0x20, 0x20, 0x20, 0x30, 0x2E, 0x30, 0x20, 0x6B, 0x67, 0x0D, 0x0A,
}

func TestParseCAS22RealHardwareFrame(t *testing.T) {
	r, err := parseCAS22(realCI200AEmptyStable)
	if err != nil {
		t.Fatalf("parseCAS22(% X) error = %v", realCI200AEmptyStable, err)
	}
	want := Reading{Value: 0, Unit: "kg", Stable: true, AtZero: true, DeviceID: 0,
		Raw: string(realCI200AEmptyStable[:20])}
	if r != want {
		t.Errorf("parseCAS22(% X) = %+v, want %+v", realCI200AEmptyStable, r, want)
	}
}

func TestParseCAS22StableGross(t *testing.T) {
	// Built from the manual's own worked example: 13.5 kg is sent as the
	// 8-byte ASCII field "000013.5".
	content := cas22("ST", "GS", 1, lamp22(false, false, false), "000013.5", "kg")

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
	content := cas22("ST", "GS", 1, lamp22(false, false, false), "000013.5", "kg")
	r, err := parseCAS22([]byte(content + "\r\n"))
	if err != nil {
		t.Fatalf("parseCAS22 with CRLF: error = %v", err)
	}
	if r.Raw != content {
		t.Errorf("Raw = %q, want CR/LF stripped %q", r.Raw, content)
	}
}

func TestParseCAS22NetUnstableWithLampBits(t *testing.T) {
	content := cas22("US", "NT", 42, lamp22(true, true, true), "-00120.0", "lb")
	r, err := parseCAS22([]byte(content))
	if err != nil {
		t.Fatalf("parseCAS22(%q) error = %v", content, err)
	}
	want := Reading{Value: -120.0, Unit: "lb", Stable: false, Net: true, Hold: true, Tare: true, AtZero: true, DeviceID: 42, Raw: content}
	if r != want {
		t.Errorf("parseCAS22(%q) = %+v, want %+v", content, r, want)
	}
}

func TestParseCAS22EachLampBitOnItsOwn(t *testing.T) {
	cases := []struct {
		name               string
		lamp               byte
		hold, tare, atZero bool
	}{
		{"none", lamp22(false, false, false), false, false, false},
		{"hold", lamp22(true, false, false), true, false, false},
		{"tare", lamp22(false, true, false), false, true, false},
		{"zero", lamp22(false, false, true), false, false, true},
		// Stable, Printer and Gross are set but are not read from the lamp.
		{"other bits only", 0x80 | 1<<6 | 1<<3 | 1<<2, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := parseCAS22([]byte(cas22("ST", "GS", 0, tc.lamp, "000100.0", "kg")))
			if err != nil {
				t.Fatal(err)
			}
			if r.Hold != tc.hold || r.Tare != tc.tare || r.AtZero != tc.atZero {
				t.Errorf("lamp %#02x: Hold/Tare/AtZero = %v/%v/%v, want %v/%v/%v",
					tc.lamp, r.Hold, r.Tare, r.AtZero, tc.hold, tc.tare, tc.atZero)
			}
		})
	}
}

func TestParseCAS22LampByteWithWrongFixedBitsIsIgnored(t *testing.T) {
	// The manual fixes bit 7 at 1 and bit 5 at 0. A byte that breaks either
	// isn't a lamp byte, so its other bits must not light anything. 0x3F
	// is '?' (bit 7 clear); 0xBF has bit 5 set; 0x20 is the space the
	// previous version of this parser mistook for the lamp byte.
	for _, lamp := range []byte{0x3F, 0xBF, 0x20} {
		r, err := parseCAS22([]byte(cas22("ST", "GS", 0, lamp, "000000.0", "kg")))
		if err != nil {
			t.Fatalf("lamp %#02x: error = %v, want the rest of the frame decoded", lamp, err)
		}
		if r.Hold || r.Tare || r.AtZero {
			t.Errorf("lamp %#02x: Hold/Tare/AtZero = %v/%v/%v, want all false", lamp, r.Hold, r.Tare, r.AtZero)
		}
		if !r.Stable || r.Unit != "kg" {
			t.Errorf("lamp %#02x: %+v, want Stable and kg still decoded", lamp, r)
		}
	}
}

func TestParseCAS22DeviceIDIsOneRawByte(t *testing.T) {
	// 44 is 0x2C, a comma; 10 and 13 are LF and CR. None may throw off the
	// decode, which is positional for exactly this reason.
	for _, id := range []byte{0, 10, 12, 13, 44, 99} {
		r, err := parseCAS22([]byte(cas22("ST", "GS", id, lamp22(false, false, true), "000013.5", "kg")))
		if err != nil {
			t.Fatalf("device %d: error = %v", id, err)
		}
		if r.DeviceID != int(id) || r.Value != 13.5 || !r.AtZero {
			t.Errorf("device %d: %+v, want DeviceID=%d Value=13.5 AtZero", id, r, id)
		}
	}
}

func TestParseCAS22Overload(t *testing.T) {
	content := cas22("OL", "GS", 0, lamp22(false, false, false), "999999.9", "kg")
	r, err := parseCAS22([]byte(content))
	if err != nil {
		t.Fatalf("parseCAS22(%q) error = %v", content, err)
	}
	if !r.Overload {
		t.Errorf("Overload = false, want true for status %q", content[:2])
	}
}

func TestParseCAS22Errors(t *testing.T) {
	lamp := lamp22(false, false, false)
	cases := []struct {
		name    string
		content string
	}{
		{"too short", "ST,GS,01,00"},
		{"too long", cas22("ST", "GS", 1, lamp, "000013.5", "kg") + "x"},
		{"no separator after status", "ST;GS," + string([]byte{1, lamp}) + ",000013.5 kg"},
		{"no separator after type", "ST,GS;" + string([]byte{1, lamp}) + ",000013.5 kg"},
		{"no separator after lamp", "ST,GS," + string([]byte{1, lamp}) + ";000013.5 kg"},
		{"bad status", cas22("XX", "GS", 1, lamp, "000013.5", "kg")},
		{"bad weight type", cas22("ST", "XX", 1, lamp, "000013.5", "kg")},
		{"non numeric weight", cas22("ST", "GS", 1, lamp, "NOTANUM.", "kg")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseCAS22([]byte(tc.content)); err == nil {
				t.Errorf("parseCAS22(%q) succeeded, want error", tc.content)
			}
		})
	}
}

func TestParseCAS22ImplausibleDeviceIDByteFallsBackToNegativeOne(t *testing.T) {
	// A byte outside F26's documented 00-99 range doesn't look like a real
	// device ID at all (more likely line noise), so DeviceID must fall all
	// the way back to -1 rather than reporting a bogus value.
	content := cas22("ST", "GS", 0xFF, lamp22(false, false, false), "000013.5", "kg")
	r, err := parseCAS22([]byte(content))
	if err != nil {
		t.Fatalf("parseCAS22(%q) error = %v, want a successful decode", content, err)
	}
	if r.DeviceID != -1 {
		t.Errorf("DeviceID = %d, want -1 for an implausible (>99) raw device-ID byte", r.DeviceID)
	}
	if r.Value != 13.5 || r.Unit != "kg" || !r.Stable {
		t.Errorf("parseCAS22(%q) = %+v, want the rest decoded despite the bad ID", content, r)
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
