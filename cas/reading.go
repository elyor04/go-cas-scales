package cas

// Reading is one parsed weight sample from the indicator.
//
// Which fields are populated depends on the FrameFormat that produced the
// Reading:
//
//   - Format22Byte populates every field.
//   - Format10Byte populates only Value and Raw (Unit, Stable, Overload,
//     Net, Hold, Tare, AtZero are left at their zero value, and DeviceID is
//     -1) — the 10-byte frame carries nothing but the weight digits.
//   - Format18ByteAND populates Value, Unit, Stable, Overload, Net, and Raw;
//     it has no device ID or lamp-status byte, so DeviceID is -1 and Hold,
//     Tare, AtZero are left false.
type Reading struct {
	// Value is the signed weight value, in Unit.
	Value float64
	// Unit is the wire unit string, e.g. "kg", "lb", "t ". Empty for
	// Format10Byte, which carries no unit.
	Unit string
	// Stable reports whether the indicator's motion detector considers
	// the reading settled (wire status "ST") as opposed to still
	// changing ("US").
	Stable bool
	// Overload reports whether the wire status was "OL".
	Overload bool
	// Net reports whether the value is a net weight ("NT"); false means
	// gross weight ("GS").
	Net bool
	// Hold reports the indicator's Hold lamp bit. Only populated by
	// Format22Byte, and false if the frame's lamp byte has the wrong
	// fixed bits (see Format22Byte).
	Hold bool
	// Tare reports the indicator's Tare lamp bit. Same conditions as Hold.
	Tare bool
	// AtZero reports the indicator's Zero-point lamp bit. Same conditions
	// as Hold.
	AtZero bool
	// DeviceID is the indicator's F26 device ID as sent in the frame (one
	// raw binary byte in Format22Byte), or -1 if the active FrameFormat
	// doesn't carry one. Format22Byte also gives -1 for a byte above 99,
	// F26's maximum; the rest of the Reading is still populated.
	DeviceID int
	// Raw is the frame (with any trailing CR/LF stripped) that produced
	// this Reading, kept around for logging/debugging. In Format22Byte it
	// holds the binary device-ID and lamp bytes as received, so it is not
	// necessarily valid UTF-8.
	Raw string
}
