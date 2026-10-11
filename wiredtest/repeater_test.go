// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package wiredtest

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/cB-Sigma/mbus-sim/sim"
	"github.com/cB-Sigma/mbus/wired"
)

// Management frames to the primary master repeater (address 251) reach
// the repeater and its answer comes back; slaves ignore the address, and
// a bus without a repeater stays silent.
func TestRepeaterAddress(t *testing.T) {
	ctx := context.Background()
	s := &sim.Slave{Primary: 1, Address: wired.SecondaryAddress{ID: "12345678", Manufacturer: 0x4024, Version: 1, Medium: 7}, Telegrams: [][]byte{{0x04, 0x13, 1, 0, 0, 0}}}
	bus := startBus(t, s)
	bus.Lock()
	bus.Repeater = func(f wired.Frame) *wired.Frame {
		if f.Long {
			return &wired.Frame{Long: true, C: wired.CRspUD, A: wired.AddrRepeater, CI: 0x78, Data: []byte{0x01, 0xFD, 0x0C, 0x02}}
		}
		return &wired.Frame{Single: true}
	}
	bus.Unlock()
	m := linkMaster(t, bus, 100*time.Millisecond, 0)
	if r, err := m.Repeater(ctx, wired.CSndNke, 0, nil, true); err != nil || !r.Single {
		t.Fatalf("SND_NKE: %+v %v", r, err)
	}
	r, err := m.Repeater(ctx, wired.CReqUD2, 0x51, []byte{0x7F}, true)
	if err != nil || r.CI != 0x78 || !bytes.Equal(r.Data, []byte{0x01, 0xFD, 0x0C, 0x02}) {
		t.Fatalf("REQ_UD2: %+v %v", r, err)
	}
	if got := bus.RepeaterFrames(); len(got) != 2 || got[1].CI != 0x51 {
		t.Errorf("repeater got %+v", got)
	}

	quiet := startBus(t, s)
	m2 := linkMaster(t, quiet, 50*time.Millisecond, 0)
	if _, err := m2.Repeater(ctx, wired.CSndNke, 0, nil, true); err == nil {
		t.Error("an answer at 251 without a repeater")
	}
}
