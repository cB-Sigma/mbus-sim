// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

// Command wmbussim serves a simulated Wireless M-Bus receiver stick over
// TCP (as a serial-to-TCP bridge would carry it) with one meter that
// transmits every -every, so a gateway can be tried without radio
// hardware.
//
//	wmbussim -listen 127.0.0.1:9001 -framer im871a -mode 5 -every 10s
//
// The meter is ABC-12345678 (water) with the key -key (a test value),
// sending a volume record in security mode 0, 5 or 7. It is
// bidirectional: a frame the gateway sends in its window (iM871A or Amber
// data request) is decrypted and acknowledged.
package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cB-Sigma/mbus-sim/sim"
	"github.com/cB-Sigma/mbus/wireless"
)

type config struct {
	listen, framer, key string
	mode                int
	every               time.Duration
}

func main() {
	var c config
	flag.StringVar(&c.listen, "listen", "127.0.0.1:9001", "TCP address of the simulated stick")
	flag.StringVar(&c.framer, "framer", wireless.FramerIM871A, "stick framing: im871a or amber")
	flag.StringVar(&c.key, "key", "000102030405060708090A0B0C0D0E0F", "the meter's AES-128 key (hex; a test value)")
	flag.IntVar(&c.mode, "mode", 5, "security mode: 0, 5 or 7")
	flag.DurationVar(&c.every, "every", 10*time.Second, "how often the meter transmits")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := start(c, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wmbussim:", err)
		os.Exit(2)
	}
	defer st.Close()
	run(ctx, st, c.every)
}

// start serves the stick and prints the meter.
func start(c config, out io.Writer) (*sim.Stick, error) {
	key, err := hex.DecodeString(c.key)
	if err != nil || len(key) != 16 {
		return nil, fmt.Errorf("key %q is not 16 bytes of hex", c.key)
	}
	if c.framer != wireless.FramerIM871A && c.framer != wireless.FramerAmber {
		return nil, fmt.Errorf("framer %q is not im871a or amber", c.framer)
	}
	m := &sim.Meter{Manufacturer: "ABC", ID: "12345678", Version: 1, DeviceType: 7, Key: key, Mode: c.mode,
		Records: []byte{0x04, 0x13, 0x39, 0x30, 0x00, 0x00}, Bidirectional: true}
	if _, err := m.Telegram(); err != nil {
		return nil, err
	}
	st := &sim.Stick{Framer: c.framer, Meters: []*sim.Meter{m}}
	addr, err := st.Listen(c.listen)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "wM-Bus %s stick on tcp://%s\n  meter ABC-12345678, mode %d, key %X\n", c.framer, addr, c.mode, key)
	return st, nil
}

// run transmits until ctx ends (nothing is sent while no gateway is
// connected).
func run(ctx context.Context, st *sim.Stick, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = st.Transmit(0)
		}
	}
}
