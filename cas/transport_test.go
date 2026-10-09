package cas

import (
	"bufio"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// tcpIndicator is a fake indicator behind a serial-to-Ethernet converter:
// it streams frames on connect, and answers a request byte with one frame.
func tcpIndicator(t *testing.T, stream []string) (addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		for _, f := range stream {
			if _, err := conn.Write([]byte(f + "\r\n")); err != nil {
				return
			}
		}
		r := bufio.NewReader(conn)
		for {
			if _, err := r.ReadByte(); err != nil {
				return
			}
			_, _ = conn.Write([]byte(frame22(21.5) + "\r\n"))
		}
	}()
	return ln.Addr().String()
}

func frame22(kg float64) string {
	v := []byte("000000.0")
	str := []byte(formatField(kg))
	copy(v[len(v)-len(str):], str)
	return cas22("ST", "GS", 1, lamp22(false, false, false), string(v), "kg")
}

func formatField(kg float64) string {
	b := []byte{}
	whole := int(kg)
	frac := int((kg-float64(whole))*10 + 0.5)
	digits := []byte{}
	if whole == 0 {
		digits = append(digits, '0')
	}
	for whole > 0 {
		digits = append([]byte{byte('0' + whole%10)}, digits...)
		whole /= 10
	}
	b = append(b, digits...)
	b = append(b, '.', byte('0'+frac))
	return string(b)
}

func TestOpenTCP_StreamsFramesLikeASerialPort(t *testing.T) {
	addr := tcpIndicator(t, []string{frame22(13.5), frame22(14.0)})
	c, err := OpenTCP(addr, DialOptions{})
	if err != nil {
		t.Fatalf("OpenTCP: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	readings, _ := c.Stream(ctx)
	got := []float64{}
	for r := range readings {
		got = append(got, r.Value)
		if len(got) == 2 {
			// A stream ends on Client.Close (or the next frame after cancel); a
			// real indicator streams continuously, this one has stopped.
			c.Close()
		}
	}
	if len(got) < 2 || got[0] != 13.5 || got[1] != 14.0 {
		t.Fatalf("readings = %v, want [13.5 14]", got)
	}
}

func TestOpenTCP_RequestOneHonoursTheReadTimeout(t *testing.T) {
	addr := tcpIndicator(t, nil)
	c, err := OpenTCP(addr, DialOptions{ReadTimeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("OpenTCP: %v", err)
	}
	defer c.Close()
	r, err := c.RequestOne(context.Background())
	if err != nil {
		t.Fatalf("RequestOne: %v", err)
	}
	if r.Value != 21.5 {
		t.Fatalf("value = %v, want 21.5", r.Value)
	}
}

func TestOpenTCP_ConnectFailureIsAnError(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	if _, err := OpenTCP(addr, DialOptions{}); err == nil {
		t.Fatal("OpenTCP to a closed port: want an error")
	}
}

func TestTCPTransport_TimeoutReadsLikeSerial(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	tr := &tcpTransport{conn: a, timeout: -1}
	_ = tr.SetReadTimeout(20 * time.Millisecond)
	n, err := tr.Read(make([]byte, 8))
	if n != 0 || err != nil {
		t.Fatalf("Read after timeout = (%d, %v), want (0, nil) as a serial port gives", n, err)
	}
}

// scriptedIndicator accepts one connection and runs serve on it.
func scriptedIndicator(t *testing.T, serve func(conn net.Conn)) (addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		serve(conn)
	}()
	return ln.Addr().String()
}

// silentUntil keeps a connection open without sending anything until done.
func silentUntil(done <-chan struct{}) func(net.Conn) {
	return func(net.Conn) { <-done }
}

// TestRequestOne_TimeoutFreesThePort is the regression test for a timed-out
// read reaching bufio as (0, nil): bufio retried it 100 times, so the port
// stayed busy for 100x ReadTimeout (200s at the default) after the caller
// had already been told it timed out, and the next request waited that long.
func TestRequestOne_TimeoutFreesThePort(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	c, err := OpenTCP(scriptedIndicator(t, silentUntil(done)), DialOptions{ReadTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("OpenTCP: %v", err)
	}
	defer c.Close()

	for i := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := time.Now()
		_, err := c.RequestOne(ctx)
		elapsed := time.Since(start)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("RequestOne #%d error = %v, want a timeout", i+1, err)
		}
		if elapsed > 500*time.Millisecond {
			t.Fatalf("RequestOne #%d took %s, want about the 20ms ReadTimeout", i+1, elapsed)
		}
	}
}

// TestRequestOne_IgnoresALateReply: once a timed-out read really ends at the
// timeout, a reply arriving after it waits in the input buffer, and without
// the discard before each request it was read as the next request's answer.
func TestRequestOne_IgnoresALateReply(t *testing.T) {
	addr := scriptedIndicator(t, func(conn net.Conn) {
		r := bufio.NewReader(conn)
		for i := 1; ; i++ {
			if _, err := r.ReadByte(); err != nil {
				return
			}
			if i == 1 {
				time.Sleep(150 * time.Millisecond) // answers after RequestOne gave up
			}
			if _, err := conn.Write([]byte(frame22(float64(i)) + "\r\n")); err != nil {
				return
			}
		}
	})
	c, err := OpenTCP(addr, DialOptions{ReadTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("OpenTCP: %v", err)
	}
	defer c.Close()

	if _, err := c.RequestOne(context.Background()); err == nil {
		t.Fatal("RequestOne #1 succeeded, want a timeout")
	}
	time.Sleep(250 * time.Millisecond) // the late reply to #1 is in by now

	r, err := c.RequestOne(context.Background())
	if err != nil {
		t.Fatalf("RequestOne #2: %v", err)
	}
	if r.Value != 2 {
		t.Fatalf("RequestOne #2 = %v, want 2 (got the late reply to #1)", r.Value)
	}
}

// TestStream_SurvivesAnIdleLineAfterARequest: Stream used to keep the read
// timeout RequestOne had set, and an idle line then ended it with
// io.ErrNoProgress after 100 empty reads.
func TestStream_SurvivesAnIdleLineAfterARequest(t *testing.T) {
	send := make(chan string)
	addr := scriptedIndicator(t, func(conn net.Conn) {
		for f := range send {
			if _, err := conn.Write([]byte(f + "\r\n")); err != nil {
				return
			}
		}
	})
	c, err := OpenTCP(addr, DialOptions{ReadTimeout: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("OpenTCP: %v", err)
	}
	defer c.Close()
	if _, err := c.RequestOne(context.Background()); err == nil {
		t.Fatal("RequestOne succeeded against a silent indicator")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	readings, errs := c.Stream(ctx)

	time.Sleep(1200 * time.Millisecond) // over 100 empty reads at 5ms, twice over
	send <- frame22(13.5)
	close(send)

	select {
	case r, ok := <-readings:
		if !ok || r.Value != 13.5 {
			t.Fatalf("reading = %+v (open %v), want 13.5", r, ok)
		}
	case err := <-errs:
		t.Fatalf("stream ended on the idle line: %v", err)
	case <-ctx.Done():
		t.Fatal("no reading within 5s")
	}
}

func TestOpenTransport_RejectsNil(t *testing.T) {
	if _, err := OpenTransport(nil, DialOptions{}); err == nil {
		t.Fatal("want an error for a nil transport")
	}
}
