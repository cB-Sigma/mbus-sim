# Changelog

## v0.2.0 (2026-10-11)

Added, all backwards compatible: new fields default to the v0.1.0
behaviour.

- Wireless meter: security modes 8, 9 and 10 (KDF-A, counter in the
  TPL); `Meter.Radio` returns a telegram with the CRCs of frame format A
  or B; `Meter.Relayed` sets the repeater hop bit; `Meter.Compact` sends
  compact frames (modes 0 and 5) with a full frame every `FullEvery`
  telegrams.
- Receiver stick: confirms the mode commands of iM871A (SET_CONFIG, and
  PING), Amber (set mode) and CUL (brt, brs, brc), and records the mode
  (`Stick.Mode`); new framings `cul` (culfw lines with LQI and RSSI) and
  `rtlwmbus` (receive only). `wmbussim -framer` takes them, `-mode`
  takes 8, 9 and 10.
- Wired bus: a primary master repeater at address 251 (`Bus.Repeater`,
  `Bus.RepeaterFrames`); without one the address stays silent.
- Interop: libmbus 0.10.3, built from its tag in Docker, as the master
  of the simulated bus (primary and secondary scans, reads by primary and
  secondary address, multi-telegram reads).

## v0.1.0 (2026-10-11)

First release.
