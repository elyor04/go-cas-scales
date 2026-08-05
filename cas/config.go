package cas

import (
	"time"

	"go.bug.st/serial"
)

// Default values applied by Open when the corresponding DialOptions field
// is left at its zero value.
const (
	DefaultBaudRate    = 9600 // F28/F32 index 4
	DefaultReadTimeout = 2 * time.Second
)

// DialOptions configures a Client. Every field's zero value is a documented,
// sane default — there is no functional-options pattern here, just a plain
// struct.
type DialOptions struct {
	// Port is the OS device name, e.g. "COM3" or "/dev/ttyUSB0".
	Port string

	// Model identifies the indicator model. The zero value, ModelCI200A,
	// is the flagship model this package targets.
	Model Model

	// BaudRate is the serial baud rate. Zero means DefaultBaudRate
	// (9600, the indicator's F28/F32 factory default).
	BaudRate int

	// Parity is the serial parity setting. The zero value,
	// serial.NoParity, matches the indicator's F27=0 factory default
	// (8 data bits, no parity, 1 stop bit). Set serial.EvenParity or
	// serial.OddParity to match F27=1/2 (7 data bits) — required when
	// using Format18ByteAND.
	Parity serial.Parity

	// Format selects how received frames are decoded. The zero value is
	// treated as Format22Byte (F30/F34=0, the indicator's default
	// format), which is the only preset with a non-nil Parse — Open
	// rejects any other unpopulated FrameFormat.
	Format FrameFormat

	// DeviceID is the indicator's F26 device ID (0-99). Zero means
	// device 00, the factory default.
	DeviceID int

	// ReadTimeout bounds RequestOne and the command-mode methods. Zero
	// means DefaultReadTimeout (2s).
	ReadTimeout time.Duration
}

// resolved returns a copy of opts with every zero-value field, other than
// Format, replaced by its documented default. Format is handled separately
// by Open, which needs to distinguish "left at the zero value" (use
// Format22Byte) from "a caller-built FrameFormat with no Parse func" (an
// error) — see ErrUnpopulatedFormat.
func (opts DialOptions) resolved() DialOptions {
	if opts.BaudRate == 0 {
		opts.BaudRate = DefaultBaudRate
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = DefaultReadTimeout
	}
	return opts
}

// DefaultOptions returns DialOptions for a CI-200A on its factory-default
// serial settings (9600 8N1, 22-byte CAS frame, device ID 0) on the given
// port. Override individual fields — most commonly Model and Format — for
// a different indicator or Set Mode configuration:
//
//	opts := cas.DefaultOptions("COM3")
//	opts.Model = cas.ModelCI201A
//	opts.Format = cas.Format18ByteAND
//	opts.Parity = serial.EvenParity
func DefaultOptions(port string) DialOptions {
	return DialOptions{
		Port:     port,
		Model:    ModelCI200A,
		BaudRate: DefaultBaudRate,
		Format:   Format22Byte,
	}
}
