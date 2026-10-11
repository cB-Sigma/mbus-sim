// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package sim

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/cB-Sigma/mbus/records"
	"github.com/cB-Sigma/mbus/wireless"
)

// Meter is a simulated Wireless M-Bus meter (EN 13757-4 link layer, EN
// 13757-7 transport layer): it builds its telegrams with data records in
// security mode 0, 5 or 7, and, when bidirectional, takes the frames a
// gateway sends in its receive window and acknowledges them.
type Meter struct {
	Manufacturer string // three letters
	ID           string // eight digits
	Version      byte
	DeviceType   byte // EN 13757-3 device type (7: water)
	// Key is the AES-128 key: mode 5's key or modes 7's persistent key
	// (KDF-A). Mode is 0, 5 or 7.
	Key  []byte
	Mode int
	// Records are the data records each telegram carries.
	Records []byte
	Status  byte
	// Bidirectional adds an ELL whose CC field opens a receive window.
	Bidirectional bool
	// Relayed marks the telegram as relayed by a repeater (the hop bit H
	// of the ELL's CC field; an ELL is added for it).
	Relayed bool
	// Compact sends compact frames (EN 13757-3 annex G) after the first
	// full frame; a full frame follows every FullEvery telegrams (default
	// 4) so a receiver that heard compact frames first learns the format.
	// Security modes 0 and 5.
	Compact   bool
	FullEvery int

	mu       sync.Mutex
	acc      byte
	sentN    int // telegrams of a compact-sending meter so far
	counter  uint32
	dec      wireless.Decoder
	received []*wireless.Telegram
}

// Header is the meter's link-layer address.
func (m *Meter) Header() (wireless.Header, error) {
	mf, err := records.ManufacturerField(m.Manufacturer)
	if err != nil {
		return wireless.Header{}, err
	}
	id, err := hex.DecodeString(m.ID)
	if err != nil || len(id) != 4 {
		return wireless.Header{}, fmt.Errorf("meter id %q is not eight digits", m.ID)
	}
	h := wireless.Header{MField: mf, Manufacturer: strings.ToUpper(m.Manufacturer), ID: m.ID, Version: m.Version, DeviceType: m.DeviceType}
	for i := range 4 {
		h.A[i] = id[3-i]
	}
	h.A[4], h.A[5] = m.Version, m.DeviceType
	return h, nil
}

// Telegram builds the meter's next SND-NR: a CRC-less frame starting with
// its L field, with a new access number (and, in mode 7, a new message
// counter).
func (m *Meter) Telegram() ([]byte, error) {
	h, err := m.Header()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.acc++
	m.counter++
	acc, ctr := m.acc, m.counter
	m.mu.Unlock()
	var body []byte
	if m.Bidirectional || m.Relayed {
		var cc byte
		if m.Bidirectional {
			cc |= wireless.CCBidirectional
		}
		if m.Relayed {
			cc |= wireless.CCHop
		}
		body = append(body, wireless.CIELL2, cc, acc)
	}
	apl := m.Records
	ci := byte(wireless.CIShortHeader)
	if m.Compact {
		if ci, apl, err = m.compact(acc); err != nil {
			return nil, err
		}
	}
	var tpdu []byte
	switch m.Mode {
	case 0:
		tpdu, err = wireless.SealTPL(ci, false, h, acc, m.Status, 0, wireless.Seal{KeyVersion: -1}, apl)
	case 5:
		tpdu, err = wireless.SealTPL(ci, false, h, acc, m.Status, 0, wireless.Seal{Mode: 5, Key: m.Key, KeyVersion: -1}, apl)
	case 7:
		tpdu, err = wireless.SealTPL(wireless.CIShortHeader, false, h, acc, m.Status, 0,
			wireless.Seal{Mode: 7, Key: m.Key, KDF: 1, KeyVersion: -1, Counter: ctr}, apl)
		if err == nil {
			tpdu, err = wireless.SealAFL(tpdu, h, m.Key, 1, ctr, 5, nil, false)
		}
	case 8, 9, 10:
		if m.Compact {
			return nil, fmt.Errorf("compact frames: modes 0 and 5 only")
		}
		tpdu, err = wireless.SealTPL(wireless.CIShortHeader, false, h, acc, m.Status, 0,
			wireless.Seal{Mode: m.Mode, Key: m.Key, KDF: 1, KeyVersion: -1, Counter: ctr, CounterInTPL: true}, apl)
	default:
		return nil, fmt.Errorf("security mode %d: the simulator sends modes 0, 5, 7, 8, 9 and 10", m.Mode)
	}
	if err != nil {
		return nil, err
	}
	return frame(wireless.CSndNR, h, append(body, tpdu...)), nil
}

// frame puts L, C, M and A in front of the rest.
func frame(c byte, h wireless.Header, rest []byte) []byte {
	b := []byte{0, c}
	b = binary.LittleEndian.AppendUint16(b, h.MField)
	b = append(append(b, h.A[:]...), rest...)
	b[0] = byte(len(b) - 1)
	return b
}

// Accept takes a frame a gateway sent (CRC-less, with L). A frame for
// this meter, decrypted with its key, is kept (Received) and, when it
// asks for an acknowledgement (SND-UD with FCV set), the meter's ACK
// is returned: C 00h with an ELL that echoes the frame's ELL access
// number. Other frames return nil.
func (m *Meter) Accept(f []byte) ([]byte, error) {
	h, err := m.Header()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.dec.MeterSide = true
	tg, err := m.dec.Decode(f, wireless.FormatNoCRC, func(wireless.Header) []byte { return m.Key })
	m.mu.Unlock()
	if tg == nil {
		return nil, err
	}
	if tg.Meter.Key() != h.Key() {
		return nil, nil // for another meter
	}
	m.mu.Lock()
	m.received = append(m.received, tg)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if tg.C&^wireless.CFCB != wireless.CSndUD {
		return nil, nil
	}
	return frame(wireless.CACK, h, []byte{wireless.CIELL2, wireless.CCBidirectional, tg.ELL.ACCOr(0)}), nil
}

// Received lists the frames the meter accepted, decoded (APL in plain).
func (m *Meter) Received() []*wireless.Telegram {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*wireless.Telegram(nil), m.received...)
}

// Stick is a simulated wireless M-Bus receiver stick reached over TCP (as
// through a serial-to-TCP bridge): it delivers the telegrams of its
// meters to every connection in its framing, iM871A or Amber (command
// mode), and passes the frames a gateway sends back to the meters, whose
// acknowledgements it delivers in turn.
type Stick struct {
	// Framer is wireless.FramerIM871A (default), FramerAmber, FramerCUL or
	// FramerRTLWMBUS (receive only).
	Framer string
	Meters []*Meter
	mode   string // the mode a gateway set, as the stick's code ("im871a:03", "amber:09", "cul:brc")

	mu    sync.Mutex
	ln    net.Listener
	conns map[net.Conn]bool
	wg    sync.WaitGroup
	sent  [][]byte // the frames the gateway sent (CRC-less, with L)
}

// Listen serves on addr ("127.0.0.1:0" for any port) and returns the
// address.
func (s *Stick) Listen(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.ln, s.conns = ln, map[net.Conn]bool{}
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns[c] = true
			s.mu.Unlock()
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.serve(c)
			}()
		}
	}()
	return ln.Addr().String(), nil
}

// Close stops the stick and its connections.
func (s *Stick) Close() error {
	s.mu.Lock()
	ln := s.ln
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	var err error
	if ln != nil {
		err = ln.Close()
	}
	s.wg.Wait()
	return err
}

// Transmit sends meter i's next telegram to every connection.
func (s *Stick) Transmit(i int) error {
	if i < 0 || i >= len(s.Meters) {
		return fmt.Errorf("no meter %d", i)
	}
	t, err := s.Meters[i].Telegram()
	if err != nil {
		return err
	}
	return s.deliver(t)
}

// Sent lists the frames the gateway sent (CRC-less, with L).
func (s *Stick) Sent() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.sent...)
}

// deliver writes a telegram as the stick's indication of a received
// frame.
func (s *Stick) deliver(t []byte) error {
	ind := EncodeIndication(s.Framer, t)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conns) == 0 {
		return errors.New("no gateway is connected")
	}
	for c := range s.conns {
		if _, err := c.Write(ind); err != nil {
			delete(s.conns, c)
			_ = c.Close()
		}
	}
	return nil
}

// EncodeIndication frames a CRC-less telegram (with L) as a receiver
// stick delivers it: an iM871A radio-link message indication with the
// HCI CRC or an Amber command-mode data indication (neither carries L), a
// culfw text line or an rtl-wmbus text line.
func EncodeIndication(framer string, t []byte) []byte {
	p := t[1:]
	switch framer {
	case wireless.FramerCUL:
		// culfw after X21: "b", the format A frame with its CRCs in hex,
		// then LQI and RSSI (a fixed -60 dBm here).
		return []byte("b" + strings.ToUpper(hex.EncodeToString(wireless.AddCRCA(t))) + "28" + "1C" + "\r\n")
	case wireless.FramerRTLWMBUS:
		return []byte("T1;1;1;2026-01-01 00:00:00.000;100;90;" + fmt.Sprintf("%X", t[4:8]) + ";0x" + strings.ToLower(hex.EncodeToString(t)) + "\n")
	}
	if framer == wireless.FramerAmber {
		b := append([]byte{wireless.AmberStart, wireless.AmberDataInd, byte(len(p))}, p...)
		var cs byte
		for _, x := range b {
			cs ^= x
		}
		return append(b, cs)
	}
	b := append([]byte{wireless.IM871ARadioLink | wireless.IM871ACRC, wireless.IM871AMsgInd, byte(len(p))}, p...)
	c := wireless.CRCX25(b)
	return append(append([]byte{wireless.IM871ASOF}, b...), byte(c), byte(c>>8))
}

// serve reads the gateway's data requests.
func (s *Stick) serve(c net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		_ = c.Close()
	}()
	r := bufio.NewReader(c)
	for {
		f, cmd, err := s.readRequest(r)
		if err != nil {
			return
		}
		if cmd != nil {
			// A configuration command: the stick confirms it.
			s.mu.Lock()
			_, _ = c.Write(cmd)
			s.mu.Unlock()
			continue
		}
		s.mu.Lock()
		s.sent = append(s.sent, f)
		s.mu.Unlock()
		for _, m := range s.Meters {
			if ack, err := m.Accept(f); err == nil && ack != nil {
				_ = s.deliver(ack)
			}
		}
	}
}

// Mode is the mode a gateway last set on the stick, as the stick's own
// code ("im871a:03", "amber:09", "cul:brc"), or "".
func (s *Stick) Mode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func (s *Stick) setMode(m string) {
	s.mu.Lock()
	s.mode = m
	s.mu.Unlock()
}

// readRequest reads the gateway's next data request (a frame to send,
// returned with its L field) or configuration command (the stick's
// confirmation is returned instead).
func (s *Stick) readRequest(r *bufio.Reader) (frame, reply []byte, err error) {
	switch s.Framer {
	case wireless.FramerCUL:
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return nil, nil, err
			}
			switch cmd := strings.TrimSpace(line); cmd {
			case "brt", "brs", "brc":
				s.setMode("cul:" + cmd)
				return nil, []byte(map[string]string{"brt": "TMODE", "brs": "SMODE", "brc": "CMODE"}[cmd] + "\r\n"), nil
			}
		}
	case wireless.FramerRTLWMBUS:
		// A receiver only: whatever arrives is ignored.
		for {
			if _, err := r.ReadByte(); err != nil {
				return nil, nil, err
			}
		}
	}
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, nil, err
		}
		if s.Framer == wireless.FramerAmber {
			if b != wireless.AmberStart {
				continue
			}
			hdr := make([]byte, 2)
			if _, err := io.ReadFull(r, hdr); err != nil {
				return nil, nil, err
			}
			body := make([]byte, int(hdr[1])+1)
			if _, err := io.ReadFull(r, body); err != nil {
				return nil, nil, err
			}
			cs := byte(wireless.AmberStart) ^ hdr[0] ^ hdr[1]
			for _, x := range body[:len(body)-1] {
				cs ^= x
			}
			if cs != body[len(body)-1] {
				continue
			}
			p := body[:len(body)-1]
			switch hdr[0] {
			case wireless.AmberDataReq:
				return append([]byte{byte(len(p))}, p...), nil, nil
			case wireless.AmberSetModeReq:
				if len(p) != 1 {
					continue
				}
				s.setMode(fmt.Sprintf("amber:%02X", p[0]))
				c := []byte{wireless.AmberStart, wireless.AmberSetModeCnf, 1, 0}
				return nil, append(c, wireless.AmberStart^wireless.AmberSetModeCnf^1^0), nil
			}
			continue
		}
		if b != wireless.IM871ASOF {
			continue
		}
		hdr := make([]byte, 3)
		if _, err := io.ReadFull(r, hdr); err != nil {
			return nil, nil, err
		}
		n := int(hdr[2])
		extra := 0
		if hdr[0]&wireless.IM871ACRC != 0 {
			extra = 2
		}
		body := make([]byte, n+extra)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, nil, err
		}
		if extra == 2 {
			got := uint16(body[n]) | uint16(body[n+1])<<8
			if wireless.CRCX25(append(append([]byte(nil), hdr...), body[:n]...)) != got {
				continue
			}
		}
		switch {
		case hdr[0]&0x0F == wireless.IM871ARadioLink && hdr[1] == wireless.IM871ADataReq:
			return append([]byte{byte(n)}, body[:n]...), nil, nil
		case hdr[0]&0x0F == wireless.IM871ADevMgmt && hdr[1] == wireless.IM871ASetConfigReq:
			// Byte 1's bit 1 says the radio mode follows.
			if n >= 3 && body[1]&0x02 != 0 {
				s.setMode(fmt.Sprintf("im871a:%02X", body[2]))
			}
			return nil, wireless.IM871AFrame(wireless.IM871ADevMgmt, wireless.IM871ASetConfigRsp, []byte{0}), nil
		case hdr[0]&0x0F == wireless.IM871ADevMgmt && hdr[1] == wireless.IM871APingReq:
			return nil, wireless.IM871AFrame(wireless.IM871ADevMgmt, wireless.IM871APingRsp, nil), nil
		}
	}
}
