// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package sim_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cB-Sigma/mbus-sim/sim"
	"github.com/cB-Sigma/mbus/wireless"
)

func newMeter(mode int) *sim.Meter {
	return &sim.Meter{Manufacturer: "ABC", ID: "12345678", Version: 1, DeviceType: 7, Key: testKey, Mode: mode,
		Records: []byte{0x04, 0x13, 0x39, 0x30, 0x00, 0x00, 0x02, 0x5B, 0x15, 0x00}}
}

func keyOf(wireless.Header) []byte { return testKey }

// Security modes 8, 9 and 10 decode with the meter's key.
func TestMeterModes8to10(t *testing.T) {
	for _, mode := range []int{8, 9, 10} {
		f, err := newMeter(mode).Telegram()
		if err != nil {
			t.Fatalf("mode %d: %v", mode, err)
		}
		var d wireless.Decoder
		tg, err := d.Decode(f, wireless.FormatNoCRC, keyOf)
		if err != nil || tg.Mode != mode || len(tg.Records) != 2 || tg.Records[0].Value != 12.345 {
			t.Fatalf("mode %d: %v %+v", mode, err, tg)
		}
	}
}

// Frame formats A and B carry their CRCs; a changed byte fails them.
func TestMeterFrameFormats(t *testing.T) {
	for _, ff := range []wireless.FrameFormat{wireless.FormatA, wireless.FormatB} {
		f, err := newMeter(5).Radio(ff)
		if err != nil {
			t.Fatal(err)
		}
		tg, err := wireless.Decode(f, ff, keyOf)
		if err != nil || len(tg.Records) != 2 {
			t.Fatalf("%v: %v", ff, err)
		}
		f[len(f)-3] ^= 1
		if _, err := wireless.Decode(f, ff, keyOf); !errors.Is(err, wireless.ErrCRC) {
			t.Errorf("%v: changed byte: %v", ff, err)
		}
	}
}

// A relayed telegram carries the hop bit.
func TestMeterRelayed(t *testing.T) {
	m := newMeter(0)
	m.Relayed = true
	f, _ := m.Telegram()
	tg, err := wireless.Decode(f, wireless.FormatNoCRC, nil)
	if err != nil || tg.ELL == nil || tg.ELL.CC&wireless.CCHop == 0 || tg.ELL.CC&wireless.CCBidirectional != 0 {
		t.Fatalf("%v %+v", err, tg.ELL)
	}
}

// A compact-sending meter: the full frame first, then compact frames that
// a FormatCache rebuilds into the same records.
func TestMeterCompactFrames(t *testing.T) {
	for _, mode := range []int{0, 5} {
		m := newMeter(mode)
		m.Compact, m.FullEvery = true, 3
		cache := wireless.NewFormatCache(4)
		var d wireless.Decoder
		rebuilt := 0
		for i := 0; i < 6; i++ {
			f, err := m.Telegram()
			if err != nil {
				t.Fatal(err)
			}
			tg, err := d.Decode(f, wireless.FormatNoCRC, keyOf)
			full := i%3 == 0
			if full != (tg != nil && tg.CI == wireless.CIShortHeader) {
				t.Fatalf("mode %d telegram %d: CI %02X", mode, i, tg.CI)
			}
			if !full && !errors.Is(err, wireless.ErrCompactFrame) {
				t.Fatalf("mode %d telegram %d: %v", mode, i, err)
			}
			// The cache learns the format from the full frame after the
			// first compact one (telegram 3); 4 and 5 are rebuilt.
			if _, err := cache.Complete(tg, err); err == nil && !full {
				if len(tg.Records) != 2 || fmt.Sprint(tg.Records[1].Value) != "21" {
					t.Fatalf("mode %d telegram %d: %+v", mode, i, tg.Records)
				}
				rebuilt++
			}
		}
		if rebuilt != 2 {
			t.Errorf("mode %d: %d compact frames rebuilt, want 2", mode, rebuilt)
		}
	}
	m := newMeter(7)
	m.Compact = true
	if _, err := m.Telegram(); err == nil {
		t.Error("compact in mode 7 accepted")
	}
}
