// Package sim simulates M-Bus devices for tests and trials.
//
// Bus is an in-process wired M-Bus segment: slaves with primary and
// secondary addresses answering SND_NKE, selection, application reset,
// primary address changes and REQ_UD2 with one or more telegrams
// (FCB-driven), reached over TCP like a transparent serial-to-IP
// converter. When several slaves answer a long frame at once the bus
// carries garbage, as a real collision does.
//
// Meter is a Wireless M-Bus meter and Stick a receiver stick (iM871A or
// Amber framing) that carries its telegrams to a gateway over TCP and the
// gateway's frames back to it (wireless.go).
package sim

import (
	"bufio"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/cB-Sigma/mbus/tpdu"
	"github.com/cB-Sigma/mbus/wired"
)

// Slave is one meter.
type Slave struct {
	Primary byte
	Address wired.SecondaryAddress
	// Telegrams are the data records of each telegram; all but the last
	// get DIF 1F (more follows) appended.
	Telegrams [][]byte
	Status    byte
	Silent    bool
	// Fixed, when set, is a fixed data structure's 16 bytes: REQ_UD2 is
	// answered with CI 73h instead of the telegrams.
	Fixed []byte
	// Alarm, when set, is answered to REQ_UD1 (CI 71h) and raises the
	// access demand bit in RSP_UD; nil answers REQ_UD1 with E5.
	Alarm []byte
	// OneShot is a one-time alarm (EN 13757-3:2025 annex D.1): repeated
	// until a REQ_UD1 with the frame count bit toggled confirms it.
	OneShot []byte
	// Applications are the blocks (data records) of each selectable
	// message application (clause 7): after an application select,
	// REQ_UD2 answers CI 68h with the selected block, then the next.
	Applications map[int][][]byte
	// AppError, when set, answers REQ_UD2 with an application error (CI
	// 70h) until the next command.
	AppError []byte
	// DLMS, when set, answers REQ_UD2 with this DLMS/COSEM APDU behind
	// CI 7Ch (long header, EN 13757-1:2021 9.2.1).
	DLMS []byte
	// DLMSHandler, when set, answers DLMS/COSEM requests sent to the slave
	// (SND_UD with CI 60h/61h, or EN 13757-1:2021 8.2.4 transport PDUs,
	// CI 00h to 1Fh): the answer APDU is returned to the next REQ_UD2s.
	DLMSHandler func(apdu []byte) []byte
	// DLMSSegmented sends DLMS answers and DLMS as transport PDUs (8.2.4)
	// instead of behind CI 7Ch.
	DLMSSegmented bool
	// Baud is the slave's rate; 0 hears every rate.
	Baud int
	// Image is an image transfer receiver (annex I); nil refuses CI C0h.
	Image *ImageDevice
	// FCB is how the slave keeps frame count bits (EN 13757-2:2018+A1:2023
	// 5.7.7). "" accepts any bit (the lenient default). "shared" is the A1
	// slave: one last bit per address it answers (its primary address and
	// 253 apart), preset to 0 by SND_NKE and updated by every frame with
	// FCV set, SND_UD included. "legacy" is an earlier slave: only
	// REQ_UD1 and REQ_UD2 update the last bit. In both, a request whose
	// bit equals the last one is a repetition and gets the last answer
	// again.
	FCB string
	// SndUD2 is the answer to SND_UD2 (C 43h, A1 5.7.3.4): "rsp" answers
	// with the telegram (or the DLMS answer) at once, "nack" with NACK,
	// "ack" with ACK (a 2018 slave), "" not at all (a 2004 slave).
	SndUD2 string
	// LoseAddressACK loses the acknowledgement of the next primary
	// address change on the line: the slave takes the new address and
	// answers E5, which the master does not receive.
	LoseAddressACK bool
	// ClockError, when set, is the application error (CI 70h data) the
	// slave reports at the next REQ_UD2 after a clock synchronisation or a
	// synchronize action it acknowledged but could not carry out.
	ClockError []byte

	access   byte
	dlmsIn   tpdu.Reassembler
	dlmsOut  [][]byte // DLMS answer frames for the next REQ_UD2s
	dlmsReqs [][]byte // DLMS request APDUs received
	oneShotF int8
	selApp   int
	selBlock int
	appSel   bool // an application is selected
	selAll   bool
	pending  []byte
	clock    [][]byte
	syncs    int
	selected bool
	idx      int
	lastFCB  int8
	resets   int
	reads    int
	alarms   int
	last     map[byte]int8 // strict FCB: the last bit per address
	lastOut  []byte        // the last answer to a request
	repeats  int
	ud2      int
}

// Bus is the simulated segment.
type Bus struct {
	mu     sync.Mutex
	slaves []*Slave
	ln     net.Listener
	addr   string
	conn   net.Conn
	frames []wired.Frame
	times  []time.Time
	baud   int // the master's current rate (0: every slave hears it)
	// DropNext answers are lost, CorruptNext answers arrive with a wrong
	// checksum, and every answer byte is written ByteDelay apart (set
	// under Lock).
	DropNext    int
	CorruptNext int
	ByteDelay   time.Duration
}

// SetMasterBaud tells the bus the master's rate; slaves with another
// rate do not hear it.
func (b *Bus) SetMasterBaud(baud int) { b.mu.Lock(); b.baud = baud; b.mu.Unlock() }

// Clock returns the clock synchronisation data a slave received (TC
// field onwards) and how many synchronize actions.
func (b *Bus) Clock(s *Slave) ([][]byte, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]byte(nil), s.clock...), s.syncs
}

// New creates a bus.
func New(slaves ...*Slave) *Bus {
	for _, s := range slaves {
		s.lastFCB = -1
		s.oneShotF = -1
	}
	return &Bus{slaves: slaves}
}

// Listen accepts one master at a time on 127.0.0.1.
func (b *Bus) Listen() error { return b.ListenOn("127.0.0.1:0") }

// ListenOn accepts one master at a time on addr.
func (b *Bus) ListenOn(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.ln, b.addr = ln, ln.Addr().String()
	b.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			b.mu.Lock()
			if b.conn != nil {
				_ = b.conn.Close()
			}
			b.conn = c
			b.mu.Unlock()
			go b.serve(c)
		}
	}()
	return nil
}

// Addr is the listening address.
func (b *Bus) Addr() string { b.mu.Lock(); defer b.mu.Unlock(); return b.addr }

// Stop closes everything.
func (b *Bus) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ln != nil {
		_ = b.ln.Close()
	}
	if b.conn != nil {
		_ = b.conn.Close()
	}
}

// Drop closes the current connection.
func (b *Bus) Drop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		_ = b.conn.Close()
	}
}

// Lock and Unlock guard the slaves' fields.
func (b *Bus) Lock() { b.mu.Lock() }

// Unlock ends Lock.
func (b *Bus) Unlock() { b.mu.Unlock() }

// Frames returns the frames the master sent.
func (b *Bus) Frames() []wired.Frame {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]wired.Frame(nil), b.frames...)
}

// Alarms returns how many REQ_UD1 a slave answered.
func (b *Bus) Alarms(s *Slave) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return s.alarms
}

// Times returns when each frame of Frames arrived.
func (b *Bus) Times() []time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]time.Time(nil), b.times...)
}

// Repeats returns how many requests a slave took for repetitions (strict
// FCB) and how many SND_UD2 it received.
func (b *Bus) Repeats(s *Slave) (repeats, sndUD2 int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return s.repeats, s.ud2
}

// Counts returns a slave's application resets and telegrams sent.
func (b *Bus) Counts(s *Slave) (resets, reads int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return s.resets, s.reads
}

func (b *Bus) serve(c net.Conn) {
	r := bufio.NewReader(c)
	for {
		f, err := wired.ReadFrame(r)
		if errors.Is(err, wired.ErrFrame) {
			continue
		}
		if err != nil {
			return
		}
		b.mu.Lock()
		b.frames = append(b.frames, f)
		b.times = append(b.times, time.Now())
		out := b.answer(f)
		delay := b.ByteDelay
		if out != nil && b.DropNext > 0 {
			b.DropNext--
			out = nil
		}
		if out != nil && b.CorruptNext > 0 && len(out) > 2 {
			b.CorruptNext--
			out = append([]byte(nil), out...)
			out[len(out)-2]++ // checksum (or the C field of a short frame)
		}
		b.mu.Unlock()
		if out != nil && delay > 0 {
			for _, x := range out {
				time.Sleep(delay)
				_, _ = c.Write([]byte{x})
			}
		} else if out != nil {
			_, _ = c.Write(out)
		}
	}
}

var collision = []byte{0x68, 0x31, 0x13, 0x68, 0x08}

func (b *Bus) addressed(a byte) []*Slave {
	var out []*Slave
	for _, s := range b.slaves {
		if s.Silent || s.Baud != 0 && b.baud != 0 && s.Baud != b.baud {
			continue
		}
		if a == wired.AddrSecondary && s.selected || a == s.Primary && a != wired.AddrSecondary || a == wired.AddrBroadcastAns {
			out = append(out, s)
		}
	}
	return out
}

// fcbKey is the address whose last frame count bit a frame to a uses:
// 253 has its own, the test address 254 shares the primary address's.
func (s *Slave) fcbKey(a byte) byte {
	if a == wired.AddrSecondary {
		return a
	}
	return s.Primary
}

// repeated applies the strict FCB rules to a request with frame count
// bit fcb to address a: true when it repeats the last one.
func (s *Slave) repeated(a byte, fcb int8) bool {
	if s.FCB == "" {
		return false
	}
	if s.last == nil {
		s.last = map[byte]int8{}
	}
	k := s.fcbKey(a)
	if s.last[k] == fcb && s.lastOut != nil {
		s.repeats++
		return true
	}
	s.last[k] = fcb
	return false
}

// answer builds the bus's answer to a frame (under mu).
func (b *Bus) answer(f wired.Frame) []byte {
	ack := []byte{wired.ACK}
	switch {
	case !f.Long && f.C == wired.CSndNke:
		if f.A == wired.AddrBroadcast {
			for _, s := range b.slaves {
				s.selected, s.idx, s.lastFCB = false, 0, -1
				s.last, s.lastOut = nil, nil
			}
			return nil
		}
		to := b.addressed(f.A)
		for _, s := range to {
			s.idx, s.lastFCB = 0, -1
			if s.last != nil {
				delete(s.last, s.fcbKey(f.A))
			}
			s.lastOut = nil
			if f.A == wired.AddrSecondary {
				s.selected = false
			}
		}
		if len(to) == 0 {
			return nil
		}
		return ack
	case !f.Long && f.C&^wired.FCB == wired.CReqUD1:
		to := b.addressed(f.A)
		if len(to) == 0 {
			return nil
		}
		if len(to) > 1 {
			return collision
		}
		s := to[0]
		s.alarms++
		fcb := int8(0)
		if f.C&wired.FCB != 0 {
			fcb = 1
		}
		if s.repeated(f.A, fcb) {
			return s.lastOut
		}
		out := s.reqUD1(ack, fcb)
		if s.FCB != "" {
			s.lastOut = out
		}
		return out
	case !f.Long && f.C&^wired.FCB == wired.CReqUD2:
		to := b.addressed(f.A)
		if len(to) == 0 {
			return nil
		}
		fcb := int8(0)
		if f.C&wired.FCB != 0 {
			fcb = 1
		}
		if len(to) > 1 {
			// Each slave answers as if alone; the answers collide.
			for _, s := range to {
				if !s.repeated(f.A, fcb) {
					out := s.reqUD2(fcb)
					if s.FCB != "" {
						s.lastOut = out
					}
				}
			}
			return collision
		}
		s := to[0]
		if s.repeated(f.A, fcb) {
			return s.lastOut
		}
		out := s.reqUD2(fcb)
		if s.FCB != "" {
			s.lastOut = out
		}
		return out
	case f.Long && f.C == wired.CSndUD2:
		to := b.addressed(f.A)
		if len(to) != 1 {
			return nil
		}
		s := to[0]
		s.ud2++
		switch s.SndUD2 {
		case "rsp":
			s.command(f)
			out := s.reqUD2(-1)
			if s.FCB != "" {
				s.lastOut = out
			}
			return out
		case "nack":
			return []byte{wired.NACK}
		case "ack":
			return ack
		}
		return nil
	case f.Long && f.C&^wired.FCB == wired.CSndUD:
		if f.A == wired.AddrSecondary && f.CI == wired.CISelect && len(f.Data) >= 8 {
			pat := wired.ParseSecondary(f.Data)
			n := 0
			for _, s := range b.slaves {
				s.selected = !s.Silent && s.Address.Matches(pat)
				if s.selected {
					n++
				}
			}
			if n == 0 {
				return nil
			}
			return ack // simultaneous single characters look like one
		}
		to := b.addressed(f.A)
		if len(to) == 0 {
			return nil
		}
		if len(to) > 1 {
			// Every addressed slave (several selected by one secondary
			// address) carries the command out and acknowledges; the
			// simultaneous E5s look like one.
			acked := false
			for _, s := range to {
				if s.command(f) {
					acked = true
				}
			}
			if acked {
				return ack
			}
			return nil
		}
		s := to[0]
		if f.C == wired.CSndUD && wired.CIBaud(f.CI) != 0 {
			// Clause 11: acknowledged at the old rate, then the new one
			// (FEh on a point-to-point line, or the meter's own address).
			s.Baud = wired.CIBaud(f.CI)
			return ack
		}
		if s.FCB == "shared" {
			// A1: the last bit is shared by every frame with FCV set.
			if s.last == nil {
				s.last = map[byte]int8{}
			}
			s.last[s.fcbKey(f.A)] = int8(f.C&wired.FCB) >> 5
		}
		if s.command(f) {
			return ack
		}
		return nil
	}
	return nil
}

// reqUD1 answers REQ_UD1 (under mu).
func (s *Slave) reqUD1(ack []byte, fcb int8) []byte {
	if s.OneShot != nil {
		if s.oneShotF == -1 || s.oneShotF == fcb {
			s.oneShotF = fcb
			return wired.Frame{Long: true, C: wired.CRspUD, A: s.Primary, CI: wired.CIRspAlarm, Data: append([]byte(nil), s.OneShot...)}.Encode()
		}
		s.OneShot, s.oneShotF = nil, -1 // confirmed
	}
	if s.Alarm == nil {
		return ack
	}
	return wired.Frame{Long: true, C: wired.CRspUD, A: s.Primary, CI: wired.CIRspAlarm, Data: append([]byte(nil), s.Alarm...)}.Encode()
}

// reqUD2 answers REQ_UD2 with frame count bit fcb (-1: the answer to
// SND_UD2, which does not move the telegram sequence) (under mu).
func (s *Slave) reqUD2(fcb int8) []byte {
	if s.pending != nil {
		out := s.pending
		s.pending = nil
		return out
	}
	if len(s.dlmsOut) > 0 {
		out := s.dlmsOut[0]
		s.dlmsOut = s.dlmsOut[1:]
		return out
	}
	if s.AppError != nil {
		return wired.Frame{Long: true, C: wired.CRspUD, A: s.Primary, CI: wired.CIAppErrNone, Data: append([]byte(nil), s.AppError...)}.Encode()
	}
	if fcb >= 0 && s.lastFCB != -1 && s.lastFCB != fcb {
		if s.appSel {
			s.selBlock++
		} else if len(s.Telegrams) > 0 {
			s.idx = (s.idx + 1) % len(s.Telegrams)
		}
	}
	if fcb >= 0 {
		s.lastFCB = fcb
	}
	if s.appSel {
		return s.selectedBlock()
	}
	return s.telegram()
}

// command carries out an SND_UD or SND_UD2 to the slave and reports
// whether it is acknowledged (under mu).
func (s *Slave) command(f wired.Frame) bool {
	s.AppError = nil
	switch {
	case f.CI == 0x60 && len(f.Data) >= 12, f.CI == 0x61 && len(f.Data) >= 4:
		hdr := 4
		if f.CI == 0x60 {
			hdr = 12
		}
		s.dlmsRequest(append([]byte(nil), f.Data[hdr:]...))
		return true
	case f.CI <= 0x1F:
		tp, err := tpdu.ParseTPDU(append([]byte{f.CI}, f.Data...))
		if err != nil {
			return false
		}
		apdu, err := s.dlmsIn.Add(tp)
		if err != nil {
			return false
		}
		if apdu != nil {
			s.dlmsRequest(apdu)
		}
		return true
	}
	switch f.CI {
	case wired.CIAppReset:
		s.resets++
		s.idx, s.lastFCB = 0, -1
		s.appSel = false
		if len(f.Data) > 0 {
			app, block, _ := wired.ParseSubcode(f.Data)
			if _, ok := s.Applications[app]; ok {
				// Block 0 asks for every block (from the first, with DIF
				// 1Fh while more follow); block n is the n-th.
				s.appSel, s.selApp, s.selBlock, s.selAll = true, app, max(block-1, 0), block == 0
			}
			// Clause 7.4.2: an unsupported application is a reset.
		}
	case wired.CISynchronize:
		s.syncs++
		if s.ClockError != nil {
			s.AppError = append([]byte(nil), s.ClockError...)
		}
	case wired.CIClockSync:
		if len(f.Data) > 12 {
			s.clock = append(s.clock, append([]byte(nil), f.Data[12:]...))
		}
		if s.ClockError != nil {
			s.AppError = append([]byte(nil), s.ClockError...)
		}
	case wired.CIImageCmdLong:
		if s.Image == nil || len(f.Data) < 12 {
			return false
		}
		hdr, _ := s.Address.Bytes()
		if string(f.Data[:8]) != string(hdr) {
			return false // addressed to another device
		}
		if out := s.Image.handle(f.Data[12:]); out != nil {
			data := append(hdr, s.access, s.Status, 0, 0)
			s.pending = wired.Frame{Long: true, C: wired.CRspUD, A: s.Primary, CI: wired.CIImageRspLong, Data: append(data, out...)}.Encode()
		}
	case wired.CISetData:
		if len(f.Data) == 3 && f.Data[0] == 0x01 && f.Data[1] == 0x7A {
			s.Primary = f.Data[2]
			if s.LoseAddressACK {
				s.LoseAddressACK = false
				return false
			}
		}
	}
	return true
}

func (s *Slave) telegram() []byte {
	s.reads++
	s.access++
	c := byte(wired.CRspUD)
	if s.Alarm != nil {
		c |= 0x20 // ACD: class 1 data waiting
	}
	if s.Fixed != nil {
		data := append([]byte(nil), s.Fixed...)
		data[4] = s.access
		return wired.Frame{Long: true, C: c, A: s.Primary, CI: wired.CIRspFixed, Data: data}.Encode()
	}
	if s.DLMS != nil && s.DLMSSegmented {
		frames := s.dlmsFrames(s.DLMS)
		s.dlmsOut = append(s.dlmsOut, frames[1:]...)
		return frames[0]
	}
	hdr, _ := s.Address.Bytes()
	data := append(hdr, s.access, s.Status, 0, 0)
	if s.DLMS != nil {
		return wired.Frame{Long: true, C: c, A: s.Primary, CI: wired.CIRspDLMSLong, Data: append(data, s.DLMS...)}.Encode()
	}
	data = append(data, s.Telegrams[s.idx]...)
	if s.idx < len(s.Telegrams)-1 {
		data = append(data, 0x1F)
	}
	return wired.Frame{Long: true, C: c, A: s.Primary, CI: wired.CIRspVariable, Data: data}.Encode()
}

// selectedBlock answers with the selected application's block (CI 68h),
// wrapping to block 0 after the last (clause 7.5); a block that does not
// exist also gives block 0.
func (s *Slave) selectedBlock() []byte {
	s.reads++
	s.access++
	blocks := s.Applications[s.selApp]
	i := s.selBlock
	if i < 0 || i >= len(blocks) {
		i, s.selBlock = 0, 0
	}
	hdr, _ := s.Address.Bytes()
	data := append(hdr, s.access, s.Status, 0, 0)
	data = append(data, blocks[i]...)
	if s.selAll && i < len(blocks)-1 {
		data = append(data, 0x1F)
	}
	return wired.Frame{Long: true, C: wired.CRspUD, A: s.Primary, CI: wired.CIRspSelLong, Data: data}.Encode()
}

// dlmsRequest records a DLMS request and queues the handler's answer.
func (s *Slave) dlmsRequest(apdu []byte) {
	s.dlmsReqs = append(s.dlmsReqs, apdu)
	if s.DLMSHandler == nil {
		return
	}
	if resp := s.DLMSHandler(apdu); resp != nil {
		s.dlmsOut = append(s.dlmsOut, s.dlmsFrames(resp)...)
	}
}

// dlmsFrames are the RSP_UD frames carrying a DLMS APDU: one CI 7Ch frame,
// or transport PDUs (EN 13757-1:2021 8.2.4, from the management logical
// device to the public client) when segmented or too long for one frame.
func (s *Slave) dlmsFrames(apdu []byte) [][]byte {
	if !s.DLMSSegmented && len(apdu) <= 240 {
		hdr, _ := s.Address.Bytes()
		data := append(append(hdr, s.access, s.Status, 0, 0), apdu...)
		return [][]byte{wired.Frame{Long: true, C: wired.CRspUD, A: s.Primary, CI: wired.CIRspDLMSLong, Data: data}.Encode()}
	}
	var out [][]byte
	for _, tp := range tpdu.SegmentAPDU(apdu, tpdu.SAPManagement, tpdu.SAPPublicClient) {
		b := tp.Encode()
		out = append(out, wired.Frame{Long: true, C: wired.CRspUD, A: s.Primary, CI: b[0], Data: b[1:]}.Encode())
	}
	return out
}

// DLMSRequests returns the DLMS request APDUs a slave received.
func (b *Bus) DLMSRequests(s *Slave) [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]byte(nil), s.dlmsReqs...)
}
