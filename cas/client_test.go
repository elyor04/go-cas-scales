package cas

import (
	"context"
	"errors"
	"testing"
)

func TestOpenRejectsUnpopulatedCustomFormat(t *testing.T) {
	// A caller who built their own FrameFormat but forgot Parse should get
	// a clear error before Open ever touches the OS port, not a confusing
	// nil-pointer panic the first time a frame arrives.
	_, err := Open(DialOptions{
		Port:   "COM_DOES_NOT_MATTER",
		Format: FrameFormat{Name: "custom-but-incomplete"},
	})
	if !errors.Is(err, ErrUnpopulatedFormat) {
		t.Errorf("Open error = %v, want it to wrap ErrUnpopulatedFormat", err)
	}
}

func TestClientCloseIsIdempotent(t *testing.T) {
	c, port := newTestClient(DialOptions{})
	if err := c.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if !port.closed {
		t.Error("underlying port was not closed")
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
}

type fakeSession struct{ closed bool }

func (s *fakeSession) Close() error {
	s.closed = true
	return nil
}

func TestClientCloseTearsDownTrackedSessions(t *testing.T) {
	c, _ := newTestClient(DialOptions{})
	sess := &fakeSession{}
	c.track(sess)

	if err := c.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !sess.closed {
		t.Error("Close() did not close the tracked child session")
	}
}

func TestSendCommandBuildsFrameMatchingManualExample(t *testing.T) {
	// The manual's own worked example: zeroing device 11 sends the hex
	// bytes 44 31 31 4B 5A 0D 0A, i.e. ASCII "D11KZ\r\n".
	c, port := newTestClient(DialOptions{DeviceID: 11})
	port.feed("ignored-response\r\n")

	if err := c.Zero(context.Background()); err != nil {
		t.Fatalf("Zero() error = %v", err)
	}
	if got, want := port.written(), "D11KZ\r\n"; got != want {
		t.Errorf("written frame = %q, want %q", got, want)
	}
}

func TestRequestOneWritesRawBinaryDeviceIDByte(t *testing.T) {
	// F31/F35=3 "on request" mode: the manual says the request signal is
	// the raw device-ID value as a single byte (device 10 -> byte 0x0A),
	// not an ASCII digit string — distinct from command-mode framing.
	c, port := newTestClient(DialOptions{DeviceID: 10})
	port.feed("ST,GS,10,000013.5" + string([]byte{lamp22(false, false, false)}) + "kg\r\n")

	r, err := c.RequestOne(context.Background())
	if err != nil {
		t.Fatalf("RequestOne() error = %v", err)
	}
	if r.Value != 13.5 {
		t.Errorf("Value = %v, want 13.5", r.Value)
	}
	if got, want := port.written(), string([]byte{10}); got != want {
		t.Errorf("written request byte = %q, want %q", got, want)
	}
}

func TestHighLimitUnsupportedByModelDoesNotTouchThePort(t *testing.T) {
	c, port := newTestClient(DialOptions{Model: ModelCI200A})
	_, err := c.HighLimit(context.Background())
	if !errors.Is(err, ErrUnsupportedByModel) {
		t.Errorf("error = %v, want it to wrap ErrUnsupportedByModel", err)
	}
	if got := port.written(); got != "" {
		t.Errorf("HighLimit on an unsupported model wrote %q to the port, want nothing", got)
	}
}

func TestHighLimitSupportedByCI201A(t *testing.T) {
	c, port := newTestClient(DialOptions{Model: ModelCI201A, DeviceID: 1})
	port.feed("HI00123\r\n")

	v, err := c.HighLimit(context.Background())
	if err != nil {
		t.Fatalf("HighLimit() error = %v", err)
	}
	if v != 123 {
		t.Errorf("HighLimit() = %v, want 123", v)
	}
	if got, want := port.written(), "D01HI00000\r\n"; got != want {
		t.Errorf("written frame = %q, want %q", got, want)
	}
}

func TestSetKeyTareValueFormatsFiveDigitField(t *testing.T) {
	c, port := newTestClient(DialOptions{})
	port.feed("ok\r\n")

	if err := c.SetKeyTareValue(context.Background(), 45); err != nil {
		t.Fatalf("SetKeyTareValue() error = %v", err)
	}
	if got, want := port.written(), "D00HY00045\r\n"; got != want {
		t.Errorf("written frame = %q, want %q", got, want)
	}
}

func TestDeviceID(t *testing.T) {
	c, _ := newTestClient(DialOptions{DeviceID: 7})
	if got := c.DeviceID(); got != 7 {
		t.Errorf("DeviceID() = %d, want 7", got)
	}
}
