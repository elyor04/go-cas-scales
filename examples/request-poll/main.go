// Command request-poll polls a CAS CI-200-series indicator for one weight
// frame at a fixed interval, using Client.RequestOne. It expects the
// indicator's Set Mode F31 (or F35, for COM2) to be set to 3 ("send upon
// data request").
//
// Environment variables:
//
//	CAS_PORT     (required) OS serial device name, e.g. COM3 or /dev/ttyUSB0.
//	CAS_BAUD     baud rate; defaults to 9600.
//	CAS_DEVICE_ID  the indicator's F26 device ID; defaults to 0.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r, err := client.RequestOne(ctx)
			if err != nil {
				log.Printf("request failed: %v", err)
				continue
			}
			log.Printf("%.3f %s stable=%v net=%v", r.Value, r.Unit, r.Stable, r.Net)
		}
	}
}
