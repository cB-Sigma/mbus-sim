// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package sim_test

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/mbus-sim/sim"
	"github.com/cB-Sigma/mbus/wireless"
)

var testKey = []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// Each security mode the simulated meter sends decodes with its key.
func TestMeterTelegrams(t *testing.T) {
	for _, mode := range []int{0, 5, 7} {
		m := &sim.Meter{Manufacturer: "ABC", ID: "12345678", Version: 1, DeviceType: 7, Key: testKey, Mode: mode,
			Records: []byte{0x04, 0x13, 0x39, 0x30, 0x00, 0x00}}
		for i := 0; i < 2; i++ {
			f, err := m.Telegram()
			if err != nil {
				t.Fatal(err)
			}
			var d wireless.Decoder
			tg, err := d.Decode(f, wireless.FormatNoCRC, func(wireless.Header) []byte { return testKey })
			if err != nil || tg.Mode != mode || len(tg.Records) != 1 || tg.Records[0].Value != 12.345 {
				t.Fatalf("mode %d: %v %+v", mode, err, tg)
			}
		}
	}
}

// A gateway on the simulated stick receives a bidirectional meter's
// telegrams in both framings, sends an SND-UD in the window the next one
// opens (one frame per window), and the meter's ACK settles it.
func TestStickDownlink(t *testing.T) {
	for _, framer := range []string{wireless.FramerIM871A, wireless.FramerAmber} {
		m := &sim.Meter{Manufacturer: "ABC", ID: "12345678", Version: 1, DeviceType: 7, Key: testKey, Mode: 5,
			Records: []byte{0x04, 0x13, 0x39, 0x30, 0x00, 0x00}, Bidirectional: true}
		st := &sim.Stick{Framer: framer, Meters: []*sim.Meter{m}}
		addr, err := st.Listen("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		gw := wireless.Gateway{Manufacturer: "XYZ", ID: "33445566", Version: 1}
		if err := gw.Validate(); err != nil {
			t.Fatal(err)
		}
		var states []string
		s := &wireless.Scheduler{Gateway: gw, Framer: framer, Timeout: time.Minute,
			Seal: func(string, *wireless.Telegram, *wireless.Pending) (wireless.DownlinkSeal, error) {
				return wireless.DownlinkSeal{Mode: 5, Key: testKey}, nil
			},
			State: func(_ string, _ *wireless.Pending, state, _ string) { states = append(states, state) }}
		s.SetWriter(c)
		rd := wireless.NewReader(c, framer)
		var dec wireless.Decoder
		next := func() *wireless.Telegram {
			t.Helper()
			_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
			r, err := rd.Read()
			if err != nil {
				t.Fatal(err)
			}
			tg, err := dec.Decode(r.Frame, wireless.FormatNoCRC, func(wireless.Header) []byte { return testKey })
			if err != nil {
				t.Fatal(err)
			}
			return tg
		}
		for range 50 { // the connection is registered asynchronously
			if st.Transmit(0) == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		tg := next()
		dev := tg.Meter.Key()
		s.Enqueue(dev, &wireless.Pending{ID: "a", Frame: wireless.Downlink{C: wireless.CSndUD, CI: wireless.CIToMeterLong, APL: []byte{0x01, 0xFD, 0x1A, 0x01}},
			Queued: time.Now(), Expect: wireless.ExpectFor(wireless.CSndUD)})
		s.Enqueue(dev, &wireless.Pending{ID: "b", Frame: wireless.Downlink{C: wireless.CSndUD, CI: wireless.CIToMeterLong, APL: []byte{0x01, 0xFD, 0x1A, 0x00}},
			Queued: time.Now(), Expect: wireless.ExpectFor(wireless.CSndUD)})
		_ = st.Transmit(0)
		for _, e := range s.After(dev, next(), true) { // the window: frame a only
			e()
		}
		ack := next()
		if ack.C != wireless.CACK {
			t.Fatalf("%s: answer C %02X", framer, ack.C)
		}
		for _, e := range s.After(dev, ack, true) {
			e()
		}
		if want := []string{wireless.StateSent, wireless.StateAcknowledged}; len(states) != 2 || states[0] != want[0] || states[1] != want[1] {
			t.Errorf("%s: states %v, want %v", framer, states, want)
		}
		if got := m.Received(); len(got) != 1 || !bytes.Contains(got[0].APL, []byte{0x01, 0xFD, 0x1A, 0x01}) {
			t.Errorf("%s: the meter received %d frames", framer, len(got))
		}
		if n := len(st.Sent()); n != 1 {
			t.Errorf("%s: %d frames sent in one window", framer, n)
		}
		_ = c.Close()
		_ = st.Close()
	}
}
