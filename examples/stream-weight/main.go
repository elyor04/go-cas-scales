// Command stream-weight connects to a CAS CI-200-series indicator and
// prints every Reading it streams. It expects the indicator's Set Mode to
// already have F31 (or F35, for COM2) set to 1 or 2 — this program never
// writes to the port, it only reads.
//
// Environment variables:
//
//	CAS_PORT  (required) OS serial device name, e.g. COM3 or /dev/ttyUSB0.
//	CAS_BAUD  baud rate; defaults to 9600 (F28/F32 factory default).
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"

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

	client, err := cas.Open(opts)
	if err != nil {
		log.Fatalf("open %s: %v", port, err)
	}
	defer client.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	readings, errs := client.Stream(ctx)
	for readings != nil || errs != nil {
		select {
		case r, ok := <-readings:
			if !ok {
				readings = nil
				continue
			}
			log.Printf("%.3f %s stable=%v net=%v", r.Value, r.Unit, r.Stable, r.Net)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			log.Printf("stream error: %v", err)
		}
	}
}
