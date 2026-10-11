// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package sim

import (
	"encoding/binary"
	"fmt"

	"github.com/cB-Sigma/mbus/records"
	"github.com/cB-Sigma/mbus/wireless"
)

// Radio is the meter's next telegram as it goes over the air: with the
// link-layer CRCs of frame format A or B.
func (m *Meter) Radio(f wireless.FrameFormat) ([]byte, error) {
	t, err := m.Telegram()
	if err != nil {
		return nil, err
	}
	switch f {
	case wireless.FormatA:
		return wireless.AddCRCA(t), nil
	case wireless.FormatB:
		return wireless.AddCRCB(t), nil
	case wireless.FormatNoCRC:
		return t, nil
	}
	return nil, fmt.Errorf("frame format %v: A, B or none", f)
}

// compact gives the CI field and application data of a compact-sending
// meter's telegram: the full frame (its records) first and every
// FullEvery telegrams, else the compact frame: format signature, Full-Frame-CRC and the records' data
// without their DIF and VIF bytes.
func (m *Meter) compact(_ byte) (byte, []byte, error) {
	if m.Mode != 0 && m.Mode != 5 {
		return 0, nil, fmt.Errorf("compact frames: the simulator sends them in modes 0 and 5")
	}
	every := m.FullEvery
	if every <= 0 {
		every = 4
	}
	m.mu.Lock()
	n := m.sentN
	m.sentN++
	m.mu.Unlock()
	if n%every == 0 {
		return wireless.CIShortHeader, m.Records, nil
	}
	format, err := wireless.BuildFormat(m.Records)
	if err != nil {
		return 0, nil, err
	}
	recs, err := records.ParseRecords(m.Records)
	if err != nil {
		return 0, nil, err
	}
	var data []byte
	for _, r := range recs {
		if records.DataLength(r.DIF) < 0 {
			return 0, nil, fmt.Errorf("compact frames: variable-length records are not simulated")
		}
		data = append(data, r.Raw...)
	}
	apl := binary.LittleEndian.AppendUint16(nil, wireless.FormatSignature(format))
	apl = binary.LittleEndian.AppendUint16(apl, wireless.FullFrameCRC(m.Records))
	apl = append(apl, data...)
	return wireless.CICompactShort, apl, nil
}
