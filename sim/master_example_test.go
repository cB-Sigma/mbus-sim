// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package sim_test

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/cB-Sigma/mbus-sim/sim"
	"github.com/cB-Sigma/mbus/records"
	"github.com/cB-Sigma/mbus/wired"
)

// The master of the mbus module on a simulated bus with one water meter:
// find it by the secondary address search, select it and read its data
// records.
func Example_wiredMaster() {
	mf, _ := records.ManufacturerField("ABC")
	bus := sim.New(&sim.Slave{Primary: 5, Address: wired.SecondaryAddress{ID: "12345678", Manufacturer: mf, Version: 1, Medium: 7},
		Telegrams: [][]byte{{0x04, 0x13, 0x39, 0x30, 0x00, 0x00}}})
	if err := bus.Listen(); err != nil {
		panic(err)
	}
	defer bus.Stop()
	conn, err := net.Dial("tcp", bus.Addr())
	if err != nil {
		panic(err)
	}
	p := wired.NewPort(conn)
	defer p.Close()
	m := wired.NewMaster(2400)
	m.Timeout, m.Retries = 100*time.Millisecond, 0
	m.SetPort(p, 2400)

	ctx := context.Background()
	found, err := m.Search(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(found[0].Key())
	a, err := m.Select(ctx, wired.Target{Secondary: &found[0]})
	if err != nil {
		panic(err)
	}
	_, err = m.ReadTelegrams(ctx, a, 8, func(t wired.Telegram) error {
		for _, r := range t.Records {
			fmt.Println(r.Name(), r.Value, r.Unit)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	// Output:
	// ABC-12345678
	// volume 12.345 m3
}
