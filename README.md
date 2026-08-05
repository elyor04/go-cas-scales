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

## Protocol summary

The indicator's Set Mode exposes the function codes that matter here:

| Code | Meaning |
|---|---|
| F26 | Device ID (00-99) |
| F27 | Parity: 0 = 8N1 (default), 1 = 7E1, 2 = 7O1 |
| F28 / F32 | Baud rate for COM1 / COM2 (default 9600) |
| F30 / F34 | Frame format for COM1 / COM2: 0 = 22-byte CAS, 1 = 10-byte CAS, 2 = 18-byte AND |
| F31 / F35 | Output mode for COM1 / COM2: 0 = off, 1 = stream always, 2 = stream when stable, 3 = send on request, 4 = command mode (COM1 only) |

This package maps onto those modes as:

- **`Client.Stream`** — F31/F35 = 1 or 2. The indicator pushes frames on
  its own; this only reads.
- **`Client.RequestOne`** — F31/F35 = 3. Writes one raw byte (the device
  ID) to trigger a single frame.
- **`Client.Zero` / `Gross` / `Net` / `Hold` / `Print` / `TotalPrint` /
  `RequestWeight` / `KeyTareValue` / `HighLimit` / `LowLimit` /
  `SendCommand`** — F31/F35 = 4, COM1 only. ASCII request/response framing.

`DefaultOptions` returns a `DialOptions` matching a CI-200A's factory
defaults (9600 8N1, 22-byte CAS frame, device 0); override fields for a
different model or Set Mode configuration.

## Notes worth reading before wiring this up

- **The on-request byte and the command-mode frame are not the same
  thing.** `RequestOne` (F31/F35=3) sends the device ID as a single raw
  *binary* byte (device 10 → byte `0x0A`). The command methods (F31/F35=4)
  send an ASCII frame, `D` + 2-digit device ID + code + `CR LF` (device 11's
  zero command is literally the bytes `44 31 31 4B 5A 0D 0A`, straight from
  the manual). Mixing these up will just get you garbage back.
- **`HighLimit`/`LowLimit` are LCD/SC-only**, per the manual — calling them
  against a `ModelCI200A`/`ModelCI200S` client returns
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
- **The read-value commands' response format isn't fully specified.** The
  manual only says "Received Data Return" for every command-mode code, with
  no exact byte layout given for `HI`/`HL`/`ID`/`HY` distinct from a normal
  weight frame. `KeyTareValue`/`HighLimit`/`LowLimit` cope by extracting the
  trailing numeric run from whatever comes back — verify this against real
  hardware before depending on it.

## Package layout

```
cas/
  cas.go        Model type and per-model capability flags
  frame.go      FrameFormat + the three documented frame parsers
  reading.go    Reading, the decoded-frame value type
  config.go     DialOptions, DefaultOptions
  client.go     Client, Open, Close
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
