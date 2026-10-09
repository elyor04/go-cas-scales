package cas

import (
	"bufio"
	"context"
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

func TestOpenTransport_RejectsNil(t *testing.T) {
	if _, err := OpenTransport(nil, DialOptions{}); err == nil {
		t.Fatal("want an error for a nil transport")
	}
}
