// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cB-Sigma/mbus/wireless"
)

// A gateway connected to the served stick receives the meter's telegrams
// and decrypts them with the printed key.
func TestStart(t *testing.T) {
	var out strings.Builder
	c := config{listen: "127.0.0.1:0", framer: wireless.FramerAmber, key: "000102030405060708090A0B0C0D0E0F", mode: 7, every: 20 * time.Millisecond}
	st, err := start(c, &out)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	addr := strings.TrimPrefix(strings.Fields(out.String())[4], "tcp://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go run(ctx, st, c.every)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	r, err := wireless.NewReader(conn, wireless.FramerAmber).Read()
	if err != nil {
		t.Fatal(err)
	}
	key := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	tg, err := wireless.Decode(r.Frame, wireless.FormatNoCRC, func(wireless.Header) []byte { return key })
	if err != nil || tg.Mode != 7 || tg.Meter.Key() != "ABC-12345678" {
		t.Fatalf("%v %+v", err, tg)
	}
	for _, bad := range []config{{framer: "raw", key: c.key}, {framer: wireless.FramerIM871A, key: "00"}, {framer: wireless.FramerIM871A, key: c.key, mode: 11}} {
		if _, err := start(bad, &out); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}
