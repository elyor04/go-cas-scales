# go-cas-scales

A Go package for talking to CAS CI-200-series weighing indicators
(CI-200A, CI-200S, CI-200SC, CI-201A) over their RS-232C ASCII serial
interface, as documented in the CAS "CI-200 SERIES Weighing Indicator"
owner's manual, section 11.

```go
import "github.com/elyor04/go-cas-scales/cas"
```

## Scope

CI-200A is the primary target (it's the flagship model this package is
built against), with CI-200S, CI-200SC, and CI-201A supported as the same
family sharing an identical serial protocol. The frame-decoding layer
(`cas.FrameFormat`) isn't hardcoded to the CI-200 series, though: the
10-byte and AND-compatible 18-byte formats it implements are generic CAS
serial-interface formats, and a caller can plug in a fully custom
`FrameFormat` for a different indicator entirely.

## Over TCP/IP

`cas.OpenTCP("192.168.1.50:4001", opts)` reads the same frames from a TCP
connection instead of a serial port: an indicator behind a serial-to-Ethernet
converter in transparent (raw TCP server) mode, or one with its own Ethernet
option. `opts` is used exactly as `Open` uses it, except that `Port`,
`BaudRate` and `Parity` are ignored -- those are the converter's settings.
The connection's read timeout behaves like a serial port's (a timed-out read
returns no data rather than an error), so `Stream`, `RequestOne` and the
command-mode methods work unchanged. A dropped connection surfaces as an error
on `Stream`'s error channel, as an unplugged cable does; reconnecting is the
caller's job. `cas.OpenTransport` takes any other `cas.Transport`.

## Protocol summary

The indicator's Set Mode exposes the function codes that matter here:

| Code | Meaning |
|---|---|
| F26 | Device ID (00-99) |
| F27 | Parity: 0 = 8N1 (default), 1 = 7E1, 2 = 7O1 — `cas.NoParity` / `cas.EvenParity` / `cas.OddParity` |
| F28 / F32 | Baud rate for COM1 / COM2 (default 9600) |
| F30 / F34 | Frame format for COM1 / COM2: 0 = 22-byte CAS, 1 = 10-byte CAS, 2 = 18-byte AND |
| F31 / F35 | Output mode for COM1 / COM2: 0 = off, 1 = stream always, 2 = stream when stable; COM1 only (F31): 3 = send on request, 4 = command mode |

This package maps onto those modes as:

- **`Client.Stream`** — F31/F35 = 1 or 2. The indicator pushes frames on
  its own; this only reads.
- **`Client.RequestOne`** — F31 = 3, COM1 only. Writes one raw byte (the
  device ID) to trigger a single frame.
- **`Client.Zero` / `Gross` / `Net` / `Hold` / `Print` / `TotalPrint` /
  `RequestWeight` / `SetKeyTareValue` / `SetHighLimit` / `SetLowLimit` /
  `SendCommand`** — F31 = 4, COM1 only. ASCII request/response framing.

## The 22-byte frame

The default format (F30/F34 = 0), from the manual's §11-3-1 diagram and
confirmed byte for byte against a real CI-200A (device 00, empty platform):

```
53 54 2C 47 53 2C 00 C5 2C 20 20 20 20 20 30 2E 30 20 6B 67 0D 0A
S  T  ,  G  S  ,  id lamp ,  <-- weight, 8 bytes -->  sp k  g  CR LF
```

The device ID is **one raw binary byte** (the F26 value: device 12 is
`0x0C`), immediately followed by the **lamp byte**: bit 7 = 1 (fixed),
6 = Stable, 5 = 0 (fixed), 4 = Hold, 3 = Printer, 2 = Gross, 1 = Tare,
0 = Zero point (`0xC5` = fixed + Stable + Gross + Zero). A space separates
the weight from the unit. `Reading.Hold`/`Tare`/`AtZero` come from the lamp
byte, and stay false if its fixed bits are wrong.

Because the ID is binary, frames are decoded by position, never by
splitting on commas (device 44 is `0x2C`, a comma), and lines are ended
only by the CR LF pair (device 10 is `0x0A`, a bare LF).

Versions before 1.2.0 assumed a 2-byte ID and read the lamp byte from the
space before the unit, so Hold/Tare/AtZero were always false on real
hardware.

`DefaultOptions` returns a `DialOptions` matching a CI-200A's factory
defaults (9600 8N1, 22-byte CAS frame, device 0); override fields for a
different model or Set Mode configuration.

## Concurrency

A `Client` is safe for concurrent use by multiple goroutines, including
mixed concurrent calls to `Stream`, `RequestOne`, and the command-mode
methods. The serial line itself is physically half-duplex — only one
request/response (or one `Stream`) can ever be in flight on the wire — so
`Client` serializes these internally: concurrent callers queue in FIFO
order for exclusive access to the port rather than interleaving their
bytes on it. A queued caller's `ctx` is honored while it waits, not just
once its turn arrives, so a busy port surfaces as `ctx.Err()` instead of
an indefinite block. `Stream` holds the port for its entire run, so
`RequestOne`/command-mode calls issued while a `Stream` is active simply
queue behind it (or time out via `ctx`).

`RequestOne` and the command-mode methods always return control to the
caller by `DialOptions.ReadTimeout` (or `ctx`'s deadline, if sooner), and
the port is free again at the same point. Before each request they discard
anything already received, so a reply that arrives after its request timed
out is never taken as the next request's answer. With a custom `Transport`
whose `Read` ignores `SetReadTimeout`, the caller still gets control back on
time, but the port stays held until that read completes.

Before 1.2.1 a silent indicator held the port for 100 × `ReadTimeout`
(200 s at the default) after the caller had already timed out: `bufio`
retried each timed-out read, which returns no data and no error, 100 times.
That is what earlier versions described as a driver that doesn't honor the
read timeout. The same retries ended a `Stream` started after a request
once the line had been idle that long.

## Notes worth reading before wiring this up

- **The on-request byte and the command-mode frame are not the same
  thing.** `RequestOne` (F31=3) sends the device ID as a single raw
  *binary* byte (device 10 → byte `0x0A`). The command methods (F31=4)
  send an ASCII frame, `D` + 2-digit device ID + code + `CR LF` (device 11's
  zero command is literally the bytes `44 31 31 4B 5A 0D 0A`, straight from
  the manual). Mixing these up will just get you garbage back.
- **`SetHighLimit`/`SetLowLimit` are LCD/SC-only**, per the manual — calling
  them against a `ModelCI200A`/`ModelCI200S` client returns
  `ErrUnsupportedByModel` without touching the wire.
- **The manual lists a `KT` command code as "Zero Point Key,"** with a
  description identical to `KZ`. That reads like a documentation error, so
  this package doesn't expose a `Client.KT` method that might do the wrong
  thing — reach it via `Client.SendCommand(ctx, "KT", "")` yourself if you
  need to find out what it actually does on real hardware.
- **A separate "NT-200 Command Mode Table" appears elsewhere in the
  manual**, describing a different framing (1-byte hex device ID + 2-char
  command) for an overlapping command set. This looks like a legacy/related
  scheme rather than what CI-200 firmware actually speaks, so it isn't
  implemented — but it's worth trying via `SendCommand` if the primary
  framing doesn't get a response from your hardware.
- **The key tare and the limits can be set but not read.** The manual gives
  `HY`/`HI`/`HL` (and `ID`) a 5-digit value field and documents every reply
  only as "Received Data Return", an echo. Sending `HY00000` to "read" the
  key tare is the same bytes as setting it to 0. `KeyTareValue`, `HighLimit`
  and `LowLimit` did exactly that before 1.2.0; they are now deprecated and
  return `ErrNoReadCommand` without touching the wire. The 5-digit encoding
  the setters use (the value rounded to whole units) isn't fully specified
  either — verify it on real hardware for non-integer values. A value that
  rounds outside 0-99999 is an error.
- **The manual has a few typos worth knowing about**: §11-3-1 says "F30 and
  F35" for the output mode where it means F31/F35, and the F47/F48 tables
  are labelled "F45".

## Package layout

```
cas/
  cas.go        Model type and per-model capability flags
  frame.go      FrameFormat + the three documented frame parsers
  reading.go    Reading, the decoded-frame value type
  config.go     DialOptions, DefaultOptions
  client.go     Client, Open, Close
  transport.go  Transport, OpenTransport, OpenTCP
  stream.go     Client.Stream (continuous read)
  request.go    Client.RequestOne (on-request read)
  commands.go   Command-mode methods (Zero, Gross, Net, ..., SendCommand)
  errors.go     Error type and sentinel errors
examples/
  stream-weight/   continuous-streaming demo
  request-poll/    on-request polling demo
  zero-tare/       command-mode demo
```

## Examples

Each example reads its connection settings from environment variables so
nothing hardcodes a COM port:

| Example | Demonstrates | Required env |
|---|---|---|
| `examples/stream-weight` | `Client.Stream` | `CAS_PORT` (`CAS_BAUD` optional) |
| `examples/request-poll` | `Client.RequestOne` | `CAS_PORT` (`CAS_BAUD`, `CAS_DEVICE_ID` optional) |
| `examples/zero-tare` | Command-mode `Zero`/`Gross`/`Net` | `CAS_PORT` (`CAS_BAUD`, `CAS_DEVICE_ID` optional) |

```
CAS_PORT=COM3 go run ./examples/stream-weight
```

## Testing

```
go test ./cas/... -race
```

Every test runs against an in-memory fake `serial.Port` — no hardware or
live connection is needed. Hardware validation happens by running the
examples against a real indicator: set F30=0 (22-byte format) and F31=1
(stream) via the indicator's Set Mode, then run `stream-weight` pointed at
the correct port and confirm the decoded values match the indicator's own
display.
