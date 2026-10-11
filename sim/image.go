package sim

import (
	"encoding/binary"

	"github.com/cB-Sigma/mbus/wired"
)

// ImageDevice is a simulated image transfer receiver (EN 13757-3:2025
// annex I): it takes a prepare, collects blocks, reports lost blocks,
// validates and activates.
type ImageDevice struct {
	// Refuse is a preparation result other than 0 to answer with.
	Refuse byte
	// Drop lists blocks lost on their first transmission.
	Drop map[uint16]bool
	// FailValidation makes the validation fail.
	FailValidation bool

	Prepared  *wired.PrepareCmd
	Blocks    map[uint16][]byte
	Total     uint16
	BlockSize uint16
	Sends     int // transfer commands received
	State     int
	Activated bool
	Commands  []byte // functions received, in order
}

// Image is the received image, in block order.
func (d *ImageDevice) Image() []byte {
	var out []byte
	for i := uint16(0); i < d.Total; i++ {
		out = append(out, d.Blocks[i]...)
	}
	return out
}

func (d *ImageDevice) handle(p []byte) []byte {
	segs, err := wired.ParseSegments(p)
	if err != nil {
		return nil
	}
	var out []wired.Segment
	for _, s := range segs {
		d.Commands = append(d.Commands, s.Function)
		switch s.Function {
		case wired.ImgPrepare:
			d.Blocks, d.State = map[uint16][]byte{}, wired.StateIdle
			res := d.Refuse
			q := s.Params
			if s.Sub&wired.PrepMCAS != 0 && len(q) >= 5 {
				q = q[5:]
			}
			if s.Sub&wired.PrepSZIN != 0 && len(q) >= 5 {
				d.Total, d.BlockSize = binary.LittleEndian.Uint16(q), binary.LittleEndian.Uint16(q[2:])
			}
			d.Prepared = &wired.PrepareCmd{}
			sub := byte(0)
			if res != 0 {
				sub |= wired.PrepRspRDY
			}
			out = append(out, wired.Segment{ID: s.ID, Function: wired.ImgPrepare | wired.ImgResponse, Sub: sub, Params: []byte{res}})
		case wired.ImgTransfer:
			d.Sends++
			c, err := wired.ParseTransferCmd(s)
			if err != nil || c.Block >= d.Total {
				continue // discarded (annex I.2.7.2)
			}
			if d.Drop[c.Block] {
				delete(d.Drop, c.Block)
				continue
			}
			d.Blocks[c.Block] = append([]byte(nil), c.Data...)
			d.State = 3
			if len(d.Blocks) == int(d.Total) {
				d.State = wired.StateTransferSuccessful
			}
		case wired.ImgCompletion:
			var lost []uint16
			for i := uint16(0); i < d.Total; i++ {
				if _, ok := d.Blocks[i]; !ok {
					lost = append(lost, i)
				}
			}
			out = append(out, wired.EncodeCompletionRsp(s.ID, lost, len(d.Blocks) == 0))
		case wired.ImgValidate, wired.ImgState:
			if s.Function == wired.ImgValidate {
				d.State = wired.StateValidationActive
			} else if d.State == wired.StateValidationActive {
				d.State = wired.StateValidationSuccessful
				if d.FailValidation {
					d.State = wired.StateValidationFailed
				}
			}
			out = append(out, wired.Segment{ID: s.ID, Function: s.Function | wired.ImgResponse, Sub: byte(d.State)})
		case wired.ImgActivate:
			d.Activated, d.State = true, wired.StateActivationSuccessful
			out = append(out, wired.Segment{ID: s.ID, Function: s.Function | wired.ImgResponse, Sub: byte(d.State)})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return wired.EncodeSegments(out...)
}
