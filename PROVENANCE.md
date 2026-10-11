# Provenance

## How the code was written

Written for SigmaM2M, a oneM2M platform, in Go, by the author of the mbus
module and from the same standards as its master and decoder. No code
was ported or copied from another implementation; the repository's
history records no porting from wmbusmeters, libmbus, jMBus, pyMeterBus
or any other M-Bus stack, and a search of this module for their names,
identifiers and porting markers finds none.

The wired bus (`sim.Bus`, `sim.Slave`) was first written next to
SigmaM2M's wired M-Bus adapter, to test it without meters; the wireless
meter and the receiver stick (`sim.Meter`, `sim.Stick`) were added when
the protocol code became the mbus module (2026-10-06). On 2026-10-11 the
simulators moved into this separate module with their commands and the
tests that drive the mbus master against them (`wiredtest`), so that mbus
does not depend on its simulator.

## Standards followed

None is kept in the repository or redistributed. No specification text
is reproduced; comments cite clause, table and annex numbers in our own
words.

| Package | Followed |
|---|---|
| `sim` (wired) | EN 13757-2:2018+A1:2023 (the slave side of the link layer: FT1.2, the frame count bit, collisions), EN 13757-3:2025 (selection, application reset and select, primary address changes, baud rate switching, alarms, fixed data, image transfer), EN 13757-1:2021 8.2.4 (DLMS transport PDUs) |
| `sim` (wireless) | EN 13757-4:2025 (frame formats A and B, the receive window of bidirectional meters, the hop bit), EN 13757-7:2025 (security modes 0, 5, 7 to 10, KDF-A, AFL CMAC), EN 13757-3:2025 annex G (compact frames), and the receiver framings and mode commands of the iM871A HCI, Amber command mode, culfw and rtl-wmbus as mbus's `wireless` package reads and sends them (from the makers' published interface documents) |
| `sim` (repeater) | EN 13757-2:2018+A1:2023 5.7.5 (address 251; its commands are the test's own, as the standard defines none) |

It is a test peer, not a conformance reference: it was written by the
same author from the same documents as the master and decoder, so tests
against it show that the two agree, not that either matches a real
meter. See the README for the checks against independent
implementations.

## Test values

All meter IDs, manufacturer codes, keys and readings are test values.
The tests carry `// Covers: SIG-...` comments: requirement IDs of the
SigmaM2M project's compliance report, which mean nothing else here.
