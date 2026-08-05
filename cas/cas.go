// Package cas talks to CAS CI-200-series weighing indicators (CI-200A,
// CI-200S, CI-200SC, CI-201A) over their RS-232C ASCII serial interface, as
// documented in the CAS "CI-200 SERIES Weighing Indicator" owner's manual,
// section 11 (RS-232C Interface in Detail).
//
// # Protocol summary
//
// The indicator's Set Mode exposes a handful of function codes that govern
// the serial interface:
//
//   - F26: device ID (00-99), used to address a specific unit when several
//     share a line, and to build command-mode/on-request frames.
//   - F27: parity (0 = 8 data bits, no parity, the default; 1 = 7E1; 2 = 7O1).
//   - F28/F32: baud rate for COM1/COM2 (index 0-8, 600-115200 bps; default
//     index 4 = 9600).
//   - F30/F34: frame format for COM1/COM2 (0 = 22-byte CAS, 1 = 10-byte CAS,
//     2 = 18-byte AND-compatible). See FrameFormat.
//   - F31/F35: output mode for COM1/COM2 (0 = off, 1 = stream continuously,
//     2 = stream only while stable, 3 = send one frame per request byte,
//     4 = interactive command mode, COM1 only). See Client.Stream,
//     Client.RequestOne, and the command methods on Client.
//
// This package assumes the indicator is configured with F27=0 (8N1) and one
// of the three documented frame formats; it does not attempt to change the
// indicator's own Set Mode configuration over the wire (there is no
// documented remote way to do that beyond the front panel).
//
// # Models
//
// CI-200A and CI-200S are LED-display models with the base command set.
// CI-200SC adds checker/limit output lamps but no additional serial
// commands. CI-201A is an LCD model that additionally supports the
// High/Low limit commands. All four models share the same F26-F35 register
// scheme and frame formats; Model only affects which optional commands are
// considered supported (see Model.SupportsLimits).
package cas

// Model identifies which CI-200-series indicator a Client is talking to.
// The zero value, ModelCI200A, is a safe default: it is the flagship model
// this package targets and imposes no extra restrictions.
type Model int

const (
	// ModelCI200A is the base LED-display indicator.
	ModelCI200A Model = iota
	// ModelCI200S is the LED-display panel/wall-mount variant; same
	// protocol as CI-200A.
	ModelCI200S
	// ModelCI200SC adds checker (limit) output lamps; same serial
	// protocol as CI-200A.
	ModelCI200SC
	// ModelCI201A is the LCD-display model. It additionally supports the
	// High/Low limit commands (see Client.HighLimit, Client.LowLimit).
	ModelCI201A
)

// String returns the model's marketing name, e.g. "CI-200A".
func (m Model) String() string {
	switch m {
	case ModelCI200A:
		return "CI-200A"
	case ModelCI200S:
		return "CI-200S"
	case ModelCI200SC:
		return "CI-200SC"
	case ModelCI201A:
		return "CI-201A"
	default:
		return "unknown model"
	}
}

// SupportsLimits reports whether the model exposes the High/Low limit
// command-mode codes (HI/HL). Per the manual, these are documented as
// "LCD, SC Only" — true for ModelCI201A and ModelCI200SC, false otherwise.
func (m Model) SupportsLimits() bool {
	return m == ModelCI201A || m == ModelCI200SC
}
