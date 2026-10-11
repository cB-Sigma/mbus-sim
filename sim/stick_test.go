// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package sim_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/cB-Sigma/mbus-sim/sim"
	"github.com/cB-Sigma/mbus/wireless"
)

// mbus sets the simulated stick's mode in each stick's protocol, then
// reads the meter's telegram in that stick's framing.
func TestStickModesAndFramings(t *testing.T) {
	for _, tc := range []struct {
		framer, mode string
		set          func(context.Context, net.Conn) error
	}{
		{wireless.FramerIM871A, "im871a:06", func(ctx context.Context, c net.Conn) error {
			return wireless.ConfigureIM871A(ctx, c, wireless.IM871AModeC1A, false)
		}},
		{wireless.FramerAmber, "amber:09", func(ctx context.Context, c net.Conn) error {
			return wireless.ConfigureAmber(ctx, c, wireless.AmberModeC2T2)
		}},
		{wireless.FramerCUL, "cul:brt", func(ctx context.Context, c net.Conn) error {
			return wireless.ConfigureCUL(ctx, c, "T1")
		}},
		{wireless.FramerRTLWMBUS, "", nil},
	} {
		m := &sim.Meter{Manufacturer: "ABC", ID: "12345678", Version: 1, DeviceType: 7, Key: testKey, Mode: 5,
			Records: []byte{0x04, 0x13, 0x39, 0x30, 0x00, 0x00}}
		st := &sim.Stick{Framer: tc.framer, Meters: []*sim.Meter{m}}
		addr, err := st.Listen("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if tc.set != nil {
			if err := tc.set(ctx, c); err != nil {
				t.Fatalf("%s: %v", tc.framer, err)
			}
		}
		cancel()
		if got := st.Mode(); got != tc.mode {
			t.Errorf("%s: mode %q, want %q", tc.framer, got, tc.mode)
		}
		for range 50 {
			if st.Transmit(0) == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		r, err := wireless.NewReader(c, tc.framer).Read()
		if err != nil {
			t.Fatalf("%s: %v", tc.framer, err)
		}
		tg, err := wireless.Decode(r.Frame, wireless.FormatAuto, func(wireless.Header) []byte { return testKey })
		if err != nil || len(tg.Records) != 1 || tg.Records[0].Value != 12.345 {
			t.Fatalf("%s: %v %+v", tc.framer, err, tg)
		}
		if tc.framer == wireless.FramerCUL && (r.RSSI == nil || *r.RSSI != -60) {
			t.Errorf("CUL RSSI %v", r.RSSI)
		}
		_ = c.Close()
		_ = st.Close()
	}
}
