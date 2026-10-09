package cas

import (
	"errors"
	"fmt"
)

// Error wraps a failure from a Client operation with the name of the
// operation that failed, so callers can tell (for example) a Stream parse
// failure from an Open failure without string-matching the message.
type Error struct {
	Op  string
	Err error
}

func (e *Error) Error() string {
	return fmt.Sprintf("cas: %s: %v", e.Op, e.Err)
}

func (e *Error) Unwrap() error {
	return e.Err
}

func opErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Op: op, Err: err}
}

// Sentinel errors a caller can match with errors.Is.
var (
	// ErrClosed is returned by any Client method called after Close.
	ErrClosed = errors.New("cas: client is closed")

	// ErrShortFrame is returned when a received frame is shorter than
	// the configured FrameFormat requires.
	ErrShortFrame = errors.New("cas: frame shorter than expected")

	// ErrUnsupportedByModel is returned by command methods that the
	// configured Model does not support (see Model.SupportsLimits).
	ErrUnsupportedByModel = errors.New("cas: command not supported by this model")

	// ErrNoReadCommand is returned by the deprecated KeyTareValue,
	// HighLimit and LowLimit: the CI-200 command set can set these values
	// but has no command that reads them back.
	ErrNoReadCommand = errors.New("cas: the indicator has no command to read this value")

	// ErrUnpopulatedFormat is returned by Open when opts.Format looks
	// like an unpopulated zero-value FrameFormat rather than one of the
	// documented presets or a fully-specified custom format.
	ErrUnpopulatedFormat = errors.New("cas: DialOptions.Format is unpopulated; use Format22Byte/Format10Byte/Format18ByteAND or set Parse")
)
