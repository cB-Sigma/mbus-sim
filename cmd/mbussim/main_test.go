// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cB-Sigma/mbus/wired"
)

// The served bus is found by a search and read by a master.
func TestStart(t *testing.T) {
	var out strings.Builder
	bus, err := start("127.0.0.1:0", 3, &out)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Stop()
	if !strings.Contains(out.String(), "secondary 12345673.ABC") {
		t.Errorf("output %q", out.String())
	}
	rw, err := net.Dial("tcp", bus.Addr())
	if err != nil {
		t.Fatal(err)
	}
	p := wired.NewPort(rw)
	defer p.Close()
	m := wired.NewMaster(2400)
	m.Timeout, m.Retries = 60*time.Millisecond, 0
	m.SetPort(p, 2400)
	ctx := context.Background()
	found, err := m.Search(ctx)
	if err != nil || len(found) != 3 {
		t.Fatalf("search: %v %v", found, err)
	}
	a, err := m.Select(ctx, wired.Target{Primary: 2})
	if err != nil {
		t.Fatal(err)
	}
	var vol any
	if _, err := m.ReadTelegrams(ctx, a, 4, func(tg wired.Telegram) error {
		vol = tg.Records[0].Value
		return nil
	}); err != nil || vol != 12.345 {
		t.Errorf("read: %v %v", vol, err)
	}
	if _, err := slaves(10); err == nil {
		t.Error("10 meters accepted")
	}
}
