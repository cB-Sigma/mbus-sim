// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Command mbussim serves a simulated wired M-Bus segment over TCP, as a
// transparent serial-to-IP converter would, so a bus master can be tried
// without hardware.
//
//	mbussim -listen 127.0.0.1:10001 -meters 3
//
// Meter i (from 1) has primary address i and secondary address
// 1234567i.ABC (version 1, water), and answers REQ_UD2 with a volume and
// a flow temperature that grow with each read. The search, selection,
// application reset and primary address change work as on a real bus.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/cB-Sigma/mbus-sim/sim"
	"github.com/cB-Sigma/mbus/records"
	"github.com/cB-Sigma/mbus/wired"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:10001", "TCP address of the simulated converter")
	n := flag.Int("meters", 2, "number of meters on the bus (1 to 9)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bus, err := start(*listen, *n, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mbussim:", err)
		os.Exit(2)
	}
	<-ctx.Done()
	bus.Stop()
}

// slaves are n water meters.
func slaves(n int) ([]*sim.Slave, error) {
	if n < 1 || n > 9 {
		return nil, fmt.Errorf("meters %d is not 1 to 9", n)
	}
	m, _ := records.ManufacturerField("ABC")
	var out []*sim.Slave
	for i := 1; i <= n; i++ {
		out = append(out, &sim.Slave{Primary: byte(i),
			Address: wired.SecondaryAddress{ID: fmt.Sprintf("1234567%d", i), Manufacturer: m, Version: 1, Medium: 7},
			// Volume 12.345 m³ (DIF 04, VIF 13) and flow temperature 21.5 °C
			// (DIF 02, VIF 5A).
			Telegrams: [][]byte{{0x04, 0x13, 0x39, 0x30, 0x00, 0x00, 0x02, 0x5A, 0xD7, 0x00}}})
	}
	return out, nil
}

// start serves the bus and prints its meters.
func start(listen string, n int, out io.Writer) (*sim.Bus, error) {
	ss, err := slaves(n)
	if err != nil {
		return nil, err
	}
	bus := sim.New(ss...)
	if err := bus.ListenOn(listen); err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "M-Bus segment on tcp://%s (2400 Bd, 8E1 at the converter)\n", bus.Addr())
	for _, s := range ss {
		fmt.Fprintf(out, "  primary %d, secondary %s.%s\n", s.Primary, s.Address.ID, records.ManufacturerCode(s.Address.Manufacturer))
	}
	return bus, nil
}
