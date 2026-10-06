package cas

import (
	"testing"

	"go.bug.st/serial"
)

// cas.Parity is serial.Parity, so a caller can use either package's constants -- existing code that
// sets serial.EvenParity keeps compiling, and a new one need not import go.bug.st/serial at all.
func TestParity_IsSerialParity(t *testing.T) {
	var opts DialOptions
	opts.Parity = serial.EvenParity
	if opts.Parity != EvenParity {
		t.Fatalf("EvenParity = %v, serial.EvenParity = %v", EvenParity, serial.EvenParity)
	}
	if NoParity != serial.NoParity || OddParity != serial.OddParity {
		t.Fatal("the constants must be serial's own values")
	}
	if (DialOptions{}).Parity != NoParity {
		t.Fatal("the zero value must be NoParity, the indicator's factory default")
	}
}
