package cas

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStreamDeliversFramesUntilInputExhausted(t *testing.T) {
	c, port := newTestClient(DialOptions{})
	frame := cas22("ST", "GS", 1, lamp22(false, false, false), "000013.5", "kg") + "\r\n"
	port.feed(frame + frame + frame)

	readings, errs := c.Stream(context.Background())

	got := 0
	for r := range readings {
		if r.Value != 13.5 {
			t.Errorf("Value = %v, want 13.5", r.Value)
		}
		got++
	}
	for err := range errs {
		t.Errorf("unexpected error: %v", err)
	}
	if got != 3 {
		t.Errorf("got %d readings, want 3", got)
	}
}

func TestStreamSplitsFramesWithBinaryDeviceIDs(t *testing.T) {
	// 10 = LF, 13 = CR, 44 = ','. Each must stay inside its own frame.
	c, port := newTestClient(DialOptions{})
	ids := []byte{10, 13, 44}
	for _, id := range ids {
		port.feed(cas22("ST", "GS", id, lamp22(false, false, true), "000013.5", "kg") + "\r\n")
	}
	port.feed(string(realCI200AEmptyStable))

	readings, errs := c.Stream(context.Background())

	var got []Reading
	for r := range readings {
		got = append(got, r)
	}
	for err := range errs {
		t.Errorf("unexpected error: %v", err)
	}
	if len(got) != len(ids)+1 {
		t.Fatalf("got %d readings, want %d", len(got), len(ids)+1)
	}
	for i, id := range ids {
		if got[i].DeviceID != int(id) || got[i].Value != 13.5 || !got[i].AtZero {
			t.Errorf("frame %d: %+v, want DeviceID=%d Value=13.5 AtZero", i, got[i], id)
		}
	}
	if last := got[len(ids)]; last.DeviceID != 0 || last.Value != 0 || !last.AtZero || !last.Stable {
		t.Errorf("real hardware frame: %+v, want DeviceID=0 Value=0 AtZero Stable", last)
	}
}

func TestStreamInvalidFrameReportedOnErrorChannel(t *testing.T) {
	c, port := newTestClient(DialOptions{})
	port.feed("this is not a valid frame\r\n")

	readings, errs := c.Stream(context.Background())

	sawErr := false
	for range readings {
		t.Error("did not expect any successful readings")
	}
	for err := range errs {
		if err == nil {
			t.Error("received nil error on errs channel")
		}
		sawErr = true
	}
	if !sawErr {
		t.Error("expected a parse error on the errs channel, got none")
	}
}

// TestStreamCloseDoesNotPanicOrRace is a regression test for the two
// hazards a producer/consumer streaming design like this is prone to: a
// "send on closed channel" panic if the read goroutine tries to deliver a
// Reading/error after Close has already closed the channels, and a data
// race on streamSession.closed between the read goroutine and Close. Run
// with -race; a bare `go test` will not reliably catch either bug.
func TestStreamCloseDoesNotPanicOrRace(t *testing.T) {
	c, port := newTestClient(DialOptions{})
	frame := cas22("ST", "GS", 1, lamp22(false, false, false), "000013.5", "kg") + "\r\n"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readings, errs := c.Stream(ctx)

	stopFeed := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopFeed:
				return
			default:
				port.feed(frame)
				time.Sleep(time.Millisecond)
			}
		}
	}()
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		_ = c.Close() // races against the feed goroutine and the read loop above
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for readings != nil || errs != nil {
			select {
			case _, ok := <-readings:
				if !ok {
					readings = nil
				}
			case _, ok := <-errs:
				if !ok {
					errs = nil
				}
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not shut down within 5s of Close")
	}

	close(stopFeed)
	wg.Wait()
}

func TestStreamOnClosedClientReturnsClosedChannels(t *testing.T) {
	c, _ := newTestClient(DialOptions{})
	if err := c.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	readings, errs := c.Stream(context.Background())

	if _, ok := <-readings; ok {
		t.Error("expected readings channel to be closed immediately")
	}
	err, ok := <-errs
	if !ok {
		t.Fatal("expected one error on errs before it closed")
	}
	if err == nil {
		t.Error("expected a non-nil ErrClosed-wrapping error")
	}
}
