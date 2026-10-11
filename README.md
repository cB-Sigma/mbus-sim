# mbus-sim

A simulated wired M-Bus segment, Wireless M-Bus meters and receiver sticks in Go, for testing M-Bus masters and wM-Bus gateways without hardware.

[![Go Reference](https://pkg.go.dev/badge/github.com/cB-Sigma/mbus-sim.svg)](https://pkg.go.dev/github.com/cB-Sigma/mbus-sim) [![test](https://github.com/cB-Sigma/mbus-sim/actions/workflows/test.yml/badge.svg)](https://github.com/cB-Sigma/mbus-sim/actions/workflows/test.yml) [![Licence: Apache-2.0](https://img.shields.io/badge/licence-Apache--2.0-blue.svg)](LICENSE) [![Go](https://img.shields.io/badge/go-1.27.1-00ADD8.svg)](go.mod)

The wired bus is reached over TCP as through a transparent
serial-to-IP converter. Its slaves answer with primary and secondary
addresses, keep a frame count bit per address, collide in the search as
real meters do, and support multi-telegram reads, alarms, application
select and reset, address changes, baud rate switching, fixed data,
image transfer and DLMS APDUs, and a primary master repeater can be
plugged in at address 251. The wireless meter builds telegrams in
security modes 0, 5, 7, 8, 9 and 10, in frame format A or B, relayed by
a repeater or as compact frames, and, when bidirectional, takes and
acknowledges the frames a gateway sends in its receive window; the stick
carries them in iM871A, Amber, CUL or rtl-wmbus framing over TCP and
confirms the mode commands of the first three. It is built on
[mbus](https://github.com/cB-Sigma/mbus), without cgo. Written for
SigmaM2M, a oneM2M platform; it has no dependency on it.

## What is simulated

| Package | What it does |
|---|---|
| `sim` | `Bus` and `Slave`: a wired segment over TCP (SND_NKE, selection by primary or secondary address with wildcards, REQ_UD2 with one or more telegrams by FCB, REQ_UD1 alarms, application select and reset, SND_UD changes, baud rates, fixed data, image transfer, DLMS behind CI 60h/61h and 7Ch/7Dh, collisions, a silent or lenient frame count bit meter, a repeater at address 251 (`Bus.Repeater`)); counters of what the master sent (`Frames`, `Repeats`, `Counts`, `Alarms`, `Times`, `RepeaterFrames`). `Meter` and `Stick`: a Wireless M-Bus meter (security modes 0, 5, 7, 8, 9, 10; `Radio` with format A or B CRCs; `Relayed` with the hop bit; `Compact` frames with a full frame every `FullEvery`; bidirectional) behind an iM871A, Amber, CUL or rtl-wmbus stick over TCP that confirms mode commands (`Stick.Mode`) |
| `cmd/mbussim` | Serves a wired segment with a few water meters |
| `cmd/wmbussim` | Serves a receiver stick with one bidirectional meter |
| `wiredtest` | No code: the tests of mbus's master against the simulated bus |

Not simulated: the physical layer (levels, real baud rates), radio
timing, security mode 13, AFL fragments, SITP answers, mode C and F
encodings (FEC) on the wireless meter, compact frames in modes 7 to 10,
and Radiocrafts or Embit sticks.

## Install

```
go get github.com/cB-Sigma/mbus-sim@latest
```

Requires Go 1.27.1 or later (mbus's TLS for security mode 13 needs it).
The commands:

```
go install github.com/cB-Sigma/mbus-sim/cmd/mbussim@latest
go install github.com/cB-Sigma/mbus-sim/cmd/wmbussim@latest
```

## Command line

```
mbussim -listen 127.0.0.1:10001 -meters 3
wmbussim -listen 127.0.0.1:9001 -framer im871a -mode 5 -every 10s
```

`mbussim`'s meter i (from 1) has primary address i and secondary address
1234567i, manufacturer ABC, version 1, water, and answers with a volume
and a flow temperature that grow with each read. `wmbussim`'s meter is
ABC-12345678 (water) with the key `-key`, sending a volume record.

| Command | Flag | Default | Meaning |
|---|---|---|---|
| mbussim | `-listen` | `127.0.0.1:10001` | TCP address of the simulated converter |
| mbussim | `-meters` | `2` | meters on the bus (1 to 9) |
| wmbussim | `-listen` | `127.0.0.1:9001` | TCP address of the simulated stick |
| wmbussim | `-framer` | `im871a` | stick framing: `im871a`, `amber`, `cul` or `rtlwmbus` |
| wmbussim | `-key` | `000102030405060708090A0B0C0D0E0F` | the meter's AES-128 key (a test value) |
| wmbussim | `-mode` | `5` | security mode: 0, 5, 7, 8, 9 or 10 |
| wmbussim | `-every` | `10s` | how often the meter transmits |

## In Go tests

A bus with one water meter, found and read by mbus's master (from
`Example_wiredMaster`):

```go
mf, _ := records.ManufacturerField("ABC")
bus := sim.New(&sim.Slave{Primary: 5, Address: wired.SecondaryAddress{ID: "12345678", Manufacturer: mf, Version: 1, Medium: 7},
	Telegrams: [][]byte{{0x04, 0x13, 0x39, 0x30, 0x00, 0x00}}}) // volume 12.345 m3
if err := bus.Listen(); err != nil {
	t.Fatal(err)
}
defer bus.Stop()
conn, _ := net.Dial("tcp", bus.Addr())
// wired.NewMaster(2400) with wired.NewPort(conn): Search, Select, ReadTelegrams
```

A wireless meter's telegram in security mode 7, decoded with its key
(from `ExampleMeter_Telegram`):

```go
m := &sim.Meter{Manufacturer: "ABC", ID: "12345678", Version: 1, DeviceType: 7, Key: key, Mode: 7,
	Records: []byte{0x04, 0x13, 0x39, 0x30, 0x00, 0x00}}
f, err := m.Telegram()
tg, err := wireless.Decode(f, wireless.FormatNoCRC, func(wireless.Header) []byte { return key })
```

## Demo values

Every meter ID, manufacturer code, key and reading is a test value. Do
not use the keys on real devices.

## Documentation

Reference: https://pkg.go.dev/github.com/cB-Sigma/mbus-sim

## Testing

```
go vet ./...
go test -race ./...
```

`wiredtest` runs mbus's master against the bus (link frames, frame count
bit sequences, repeats, answer timing, the search with collisions,
selection, address changes); `sim`'s tests decode the wireless meter's
telegrams and the stick's framings with mbus's decoder and `Reader`. The
simulators were written by the author of mbus from the same standards,
so these tests show that the two agree, not that either matches a real
meter.

### Interop tested against

Against wmbusmeters 3.0.0, written independently of this module, on
2026-10-11 (`interop/`, a nested test-only module;
`cd interop && SIGMA_MBUS_INTEROP=1 go test -v ./...`). The simulated
wireless meter's telegrams, in security modes 0, 5 and 7, with and
without the extended link layer of a bidirectional meter, were decoded
by wmbusmeters in its published image
(`wmbusmeters/wmbusmeters:release-3.0.0`, pinned by digest, no network)
with its iperl driver: 36 of 36 telegrams decoded, by wmbusmeters and by
[mbus](https://github.com/cB-Sigma/mbus)'s decoder, to the values the
simulator encoded. As a control, with a wrong key wmbusmeters rejects
the telegrams and prints no reading.

Against libmbus 0.10.3 (commit d617bd2, BSD-3-Clause) as the master, on
2026-10-11: built from its tag in Docker (`interop/libmbus.Dockerfile`,
with its TCP answer timeout lowered from 4 s to 1 s so a primary scan
ends in about four minutes; image removed after the run), its own
commands against a simulated bus of three water meters
(`cd interop && SIGMA_MBUS_INTEROP=1 go test -run Libmbus -timeout 25m
./...`): `mbus-tcp-scan` found 3 of 3 slaves at their primary
addresses; `mbus-tcp-scan-secondary` found 3 of 3 by the wildcard
search, through the simulated collisions; `mbus-tcp-request-data` read
each slave by primary address and one by secondary address, with the
ID, manufacturer, volume and flow temperature the simulator holds;
`mbus-tcp-request-data-multi-reply` followed "more records follow" to
each slave's second telegram.

Not yet: a real level converter, gateway, stick or meter (the stick
mode commands and the CUL and rtl-wmbus framings were checked only
against mbus's own reader); repeater commands of a real repeater (the
standard defines none).

## Related repositories

- [mbus](https://github.com/cB-Sigma/mbus): the wired master, the
  wireless decoder, data records, OMS security and MBAL.
- [dlms-sim](https://github.com/cB-Sigma/dlms-sim): simulated DLMS/COSEM
  meters.

## Status

v0.x. The API may change before v1. Tests carry `// Covers: SIG-...`
comments: requirement IDs of the SigmaM2M project's compliance report,
which mean nothing else here.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md).

## Licence

Apache-2.0 (see [LICENSE](LICENSE)). Copyright 2026 Abhinav Gautam.
[NOTICE](NOTICE) lists the dependencies and names the trademarks used.
