// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package sim_test

import (
	"fmt"

	"github.com/cB-Sigma/mbus-sim/sim"
	"github.com/cB-Sigma/mbus/wireless"
)

// A simulated wireless meter's telegram in security mode 7 (KDF-A, AFL
// CMAC), decoded with its key.
func ExampleMeter_Telegram() {
	key := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	m := &sim.Meter{Manufacturer: "ABC", ID: "12345678", Version: 1, DeviceType: 7, Key: key, Mode: 7,
		Records: []byte{0x04, 0x13, 0x39, 0x30, 0x00, 0x00}}
	f, err := m.Telegram()
	if err != nil {
		panic(err)
	}
	tg, err := wireless.Decode(f, wireless.FormatNoCRC, func(wireless.Header) []byte { return key })
	if err != nil {
		panic(err)
	}
	fmt.Println(tg.Meter.Key(), "mode", tg.Mode, "counter", tg.AFL.Counter, tg.Records[0].Name(), tg.Records[0].Value)
	// Output:
	// ABC-12345678 mode 7 counter 1 volume 12.345
}
