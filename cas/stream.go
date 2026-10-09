package cas

import (
	"bufio"
	"bytes"
	"context"
	"sync"
)

// streamSession backs one Stream call. It exists so Client.Close (or a
// second, unrelated Stream error) can shut a running stream down without
// racing the goroutine that's still delivering readings on it.
type streamSession struct {
	closeMu  sync.Mutex
	closed   bool
	done     chan struct{}
	readings chan Reading
	errs     chan error
}

func (s *streamSession) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.done)
	return nil
}

// deliverReading and deliverErr send without blocking: a consumer that
// isn't keeping up drops frames/errors rather than stalling the read loop.
// Both hold closeMu so they can never send on a channel that Close (running
// concurrently) is about to make un-owned/closed out from under them.
func (s *streamSession) deliverReading(r Reading) {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.readings <- r:
	default:
	}
}

func (s *streamSession) deliverErr(err error) {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.errs <- err:
	default:
	}
}

// splitCRLF is a bufio.SplitFunc that tokenizes on the CR LF sequence every
// documented CAS frame format is terminated with. The pair, not a lone LF:
// see readCRLF for why Format22Byte's binary device ID needs that.
func splitCRLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.Index(data, []byte("\r\n")); i >= 0 {
		return i + 2, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// Stream continuously reads and decodes frames from the indicator. It's the
// counterpart to Set Mode F31/F35 = 1 ("send all the time") or 2 ("send
// when stable") — the indicator must already be configured to push frames
// on its own; Stream never writes to the port.
//
// Both returned channels are closed when the stream ends (ctx cancelled,
// the Client closed, or a read error). A slow consumer does not block the
// read loop: readings and errors are delivered on buffered channels with a
// non-blocking send, so a consumer that falls behind silently drops frames
// rather than stalling the port.
//
// Cancellation via ctx is checked once per successfully scanned frame, so
// it takes effect promptly while the indicator is actively streaming (the
// documented use case for F31/F35=1/2). If the line goes completely idle,
// Stream won't notice ctx cancellation until either more data arrives or
// the Client is closed, because the underlying Read call has no way to be
// interrupted mid-block.
//
// Stream holds the port for exclusive use for its entire run, acquired
// before Stream returns: a concurrent RequestOne or command-mode call
// queues behind it (or times out via its own ctx) rather than interleaving
// reads on the same port. Likewise, Stream itself queues behind any
// in-flight RequestOne/command-mode call, and behind a prior Stream that
// hasn't finished shutting down; if ctx is done before the port becomes
// free, Stream returns immediately-closed channels carrying ctx's error.
// Only one Stream should be running against a Client at a time.
func (c *Client) Stream(ctx context.Context) (<-chan Reading, <-chan error) {
	readings := make(chan Reading, 16)
	errs := make(chan error, 1)

	if c.isClosed() {
		errs <- opErr("Stream", ErrClosed)
		close(readings)
		close(errs)
		return readings, errs
	}

	if err := c.acquirePort(ctx); err != nil {
		errs <- opErr("Stream", err)
		close(readings)
		close(errs)
		return readings, errs
	}
	if c.isClosed() {
		c.releasePort()
		errs <- opErr("Stream", ErrClosed)
		close(readings)
		close(errs)
		return readings, errs
	}

	s := &streamSession{done: make(chan struct{}), readings: readings, errs: errs}
	c.track(s)

	go func() {
		defer func() {
			c.untrack(s)
			c.releasePort()
			close(readings)
			close(errs)
		}()

		scanner := bufio.NewScanner(c.reader)
		scanner.Split(splitCRLF)

		for scanner.Scan() {
			token := append([]byte(nil), scanner.Bytes()...) // Scanner reuses its buffer
			if r, err := c.opts.Format.Parse(token); err != nil {
				s.deliverErr(opErr("Stream", err))
			} else {
				s.deliverReading(r)
			}

			select {
			case <-ctx.Done():
				return
			case <-s.done:
				return
			default:
			}
		}
		if err := scanner.Err(); err != nil {
			s.deliverErr(opErr("Stream", err))
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-s.done:
		}
	}()

	return readings, errs
}
