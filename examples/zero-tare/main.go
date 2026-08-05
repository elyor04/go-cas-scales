// Command zero-tare demonstrates the command-mode methods (Zero, Gross,
// Net): it zeroes the indicator, waits, then toggles the display between
// gross and net weight. It expects the indicator's Set Mode F31 to be set
// to 4 ("command mode") on COM1 — the manual documents command mode as
// COM1-only.
//
// Environment variables:
//
//	CAS_PORT       (required) OS serial device name, e.g. COM3 or /dev/ttyUSB0.
//	CAS_BAUD       baud rate; defaults to 9600.
//	CAS_DEVICE_ID  the indicator's F26 device ID; defaults to 0.
package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/elyor04/go-cas-scales/cas"
)

func main() {
	port := os.Getenv("CAS_PORT")
	if port == "" {
		log.Fatal("CAS_PORT environment variable is required, e.g. COM3 or /dev/ttyUSB0")
	}

	opts := cas.DefaultOptions(port)
	if baud := os.Getenv("CAS_BAUD"); baud != "" {
		v, err := strconv.Atoi(baud)
		if err != nil {
			log.Fatalf("invalid CAS_BAUD %q: %v", baud, err)
		}
		opts.BaudRate = v
	}
	if id := os.Getenv("CAS_DEVICE_ID"); id != "" {
		v, err := strconv.Atoi(id)
		if err != nil {
			log.Fatalf("invalid CAS_DEVICE_ID %q: %v", id, err)
		}
		opts.DeviceID = v
	}

	client, err := cas.Open(opts)
	if err != nil {
		log.Fatalf("open %s: %v", port, err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	log.Println("zeroing...")
	if err := client.Zero(ctx); err != nil {
		log.Fatalf("Zero: %v", err)
	}

	time.Sleep(2 * time.Second)

	log.Println("switching to gross weight...")
	if err := client.Gross(ctx); err != nil {
		log.Fatalf("Gross: %v", err)
	}

	time.Sleep(2 * time.Second)

	log.Println("switching to net weight...")
	if err := client.Net(ctx); err != nil {
		log.Fatalf("Net: %v", err)
	}
}
