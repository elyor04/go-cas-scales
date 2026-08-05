package cas

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestConcurrentRequestOneEachGetsExactlyOneFrame fires many RequestOne
// calls at the same Client concurrently. Before the internal port lock, this
// would race the shared bufio.Reader wrapping the port and could split,
// merge, drop, or duplicate frames across goroutines. With the lock, each
// call must observe exactly one complete, distinct frame.
func TestConcurrentRequestOneEachGetsExactlyOneFrame(t *testing.T) {
	const n = 8
	c, port := newTestClient(DialOptions{})

	want := make([]float64, n)
	var feed strings.Builder
	for i := range n {
		v := float64((i + 1) * 10)
		want[i] = v
		fmt.Fprintf(&feed, "ST,GS,01,%08.1f", v)
		feed.WriteByte(lamp22(false, false, false))
		feed.WriteString("kg\r\n")
	}
	port.feed(feed.String())

	var wg sync.WaitGroup
	var mu sync.Mutex
	var got []float64
	var errs []error
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.RequestOne(context.Background())
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			got = append(got, r.Value)
		}()
	}
	wg.Wait()

	for _, err := range errs {
		t.Errorf("RequestOne() error = %v", err)
	}
	sort.Float64s(got)
	sort.Float64s(want)
	if len(got) != len(want) {
		t.Fatalf("got %d readings, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("readings = %v, want %v (frame split/merged/lost under concurrency)", got, want)
			break
		}
	}
}

// TestConcurrentCommandsSerializeOntoDistinctFrames fires many Zero() calls
// (built on SendCommand) concurrently and checks every call gets its own
// response and the wire only ever saw complete, well-formed request frames.
func TestConcurrentCommandsSerializeOntoDistinctFrames(t *testing.T) {
	const n = 8
	c, port := newTestClient(DialOptions{DeviceID: 3})

	var feed strings.Builder
	for range n {
		feed.WriteString("ok\r\n")
	}
	port.feed(feed.String())

	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- c.Zero(context.Background())
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Errorf("Zero() error = %v", err)
		}
	}

	want := strings.Repeat("D03KZ\r\n", n)
	if got := port.written(); got != want {
		t.Errorf("written = %q, want %q (%d complete frames, none interleaved)", got, want, n)
	}
}

// TestRequestOneRespectsContextWhilePortBusy checks that a caller queued
// behind a busy port is released by its own ctx deadline instead of
// blocking indefinitely — the reason Option B (a ctx-aware token) was
// chosen over a plain sync.Mutex.
func TestRequestOneRespectsContextWhilePortBusy(t *testing.T) {
	c, _ := newTestClient(DialOptions{})
	<-c.portTokens // simulate another operation holding the port

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.RequestOne(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("RequestOne() error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("RequestOne() blocked for %s, want it released promptly by ctx", elapsed)
	}
}

// TestStreamRespectsContextWhilePortBusy mirrors the RequestOne case for
// Stream, which now acquires the port for its whole run before returning.
func TestStreamRespectsContextWhilePortBusy(t *testing.T) {
	c, _ := newTestClient(DialOptions{})
	<-c.portTokens // simulate another operation holding the port

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	readings, errs := c.Stream(ctx)
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("Stream() blocked for %s, want it released promptly by ctx", elapsed)
	}
	if _, ok := <-readings; ok {
		t.Error("expected readings channel to be closed immediately")
	}
	err, ok := <-errs
	if !ok {
		t.Fatal("expected one error on errs before it closed")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Stream() error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

// TestRequestOneQueuesBehindActiveStreamThenRuns checks that a RequestOne
// issued while a Stream holds the port doesn't error or corrupt the wire —
// it simply waits its turn, matching the documented "Stream holds the port
// for its entire run" behavior.
func TestRequestOneQueuesBehindActiveStreamThenRuns(t *testing.T) {
	c, port := newTestClient(DialOptions{})

	streamCtx, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	readings, errs := c.Stream(streamCtx)
	go func() {
		for range readings {
		}
	}()
	go func() {
		for range errs {
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		stopStream()
		// Give the stream goroutine a moment to observe cancellation and
		// release the port before RequestOne tries to acquire it. This
		// isn't required for correctness (RequestOne would simply queue
		// longer otherwise) but keeps the test fast and deterministic.
		time.Sleep(20 * time.Millisecond)
		port.feed("ST,GS,01,000013.5" + string([]byte{lamp22(false, false, false)}) + "kg\r\n")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		r, err := c.RequestOne(ctx)
		if err != nil {
			t.Errorf("RequestOne() error = %v", err)
			return
		}
		if r.Value != 13.5 {
			t.Errorf("Value = %v, want 13.5", r.Value)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RequestOne never completed after Stream released the port")
	}
}
