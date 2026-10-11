// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package wiredtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cB-Sigma/mbus-sim/sim"
	"github.com/cB-Sigma/mbus/wired"
)

// A command to a meter named by its identification number alone goes
// only when exactly one slave answers that selection, and then to the
// slave's full secondary address; two slaves sharing the number refuse
// it, so neither changes.
func TestSelectOneFullAddress(t *testing.T) {
	ctx := context.Background()
	id := wired.SecondaryAddress{ID: "87654321", Manufacturer: 0xFFFF, Version: 0xFF, Medium: 0xFF}
	a := &sim.Slave{Primary: 1, Address: wired.SecondaryAddress{ID: "87654321", Manufacturer: 0x2C2D, Version: 1, Medium: 7}, Telegrams: [][]byte{{0x04, 0x13, 1, 0, 0, 0}}}
	bus := startBus(t, a)
	m := linkMaster(t, bus, 100*time.Millisecond, 0)
	addr, full, err := m.SelectOne(ctx, wired.Target{Secondary: &id, Name: "x"})
	if err != nil || addr != wired.AddrSecondary || full == nil || *full != a.Address {
		t.Fatalf("one slave: %v %v %v", addr, full, err)
	}
	b := &sim.Slave{Primary: 2, Address: wired.SecondaryAddress{ID: "87654321", Manufacturer: 0x2C2D, Version: 2, Medium: 7}, Telegrams: [][]byte{{0x04, 0x13, 2, 0, 0, 0}}}
	bus2 := startBus(t, a, b)
	m2 := linkMaster(t, bus2, 100*time.Millisecond, 0)
	if _, _, err := m2.SelectOne(ctx, wired.Target{Secondary: &id, Name: "x"}); err == nil || !strings.Contains(err.Error(), "more than one meter") {
		t.Errorf("two slaves: %v", err)
	}
	if _, err := m2.SetPrimary(ctx, wired.Target{Secondary: &id, Name: "x"}, 9); err == nil {
		t.Error("set-primary went to two slaves")
	}
	if a.Primary != 1 || b.Primary != 2 {
		t.Errorf("primary addresses changed: %d %d", a.Primary, b.Primary)
	}
}

// SetPrimary refuses an address a slave answers at, and recognises a lost
// acknowledgement by the slave answering at its new address.
func TestSetPrimary(t *testing.T) {
	ctx := context.Background()
	a := &sim.Slave{Primary: 5, Address: wired.SecondaryAddress{ID: "12345678", Manufacturer: 0x4024, Version: 1, Medium: 7}, Telegrams: [][]byte{{0x04, 0x13, 1, 0, 0, 0}}}
	b := &sim.Slave{Primary: 6, Address: wired.SecondaryAddress{ID: "22345678", Manufacturer: 0x4024, Version: 1, Medium: 7}, Telegrams: [][]byte{{0x04, 0x13, 1, 0, 0, 0}}}
	bus := startBus(t, a, b)
	m := linkMaster(t, bus, 100*time.Millisecond, 1)
	if _, err := m.SetPrimary(ctx, wired.Target{Primary: 5}, 6); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("taken address: %v", err)
	}
	bus.Lock()
	a.LoseAddressACK = true
	bus.Unlock()
	lost, err := m.SetPrimary(ctx, wired.Target{Primary: 5}, 7)
	bus.Lock()
	got := a.Primary
	bus.Unlock()
	if err != nil || !lost || got != 7 {
		t.Errorf("lost ACK: %v %v, slave at %d", lost, err, got)
	}
}
