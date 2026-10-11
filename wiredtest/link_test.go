package wiredtest

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cB-Sigma/mbus-sim/sim"
	"github.com/cB-Sigma/mbus/wired"
)

// linkMaster is a master with its line open to the simulated bus at 2400
// Bd, driven frame by frame through the link layer.
func linkMaster(t *testing.T, bus *sim.Bus, timeout time.Duration, retries int) *wired.Master {
	t.Helper()
	rw, err := net.Dial("tcp", bus.Addr())
	if err != nil {
		t.Fatal(err)
	}
	p := wired.NewPort(rw)
	t.Cleanup(func() { _ = p.Close() })
	m := wired.NewMaster(2400)
	m.Timeout, m.Retries = timeout, retries
	m.SetPort(p, 2400)
	return m
}

func startBus(t *testing.T, slaves ...*sim.Slave) *sim.Bus {
	t.Helper()
	bus := sim.New(slaves...)
	if err := bus.Listen(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Stop)
	return bus
}

// wireC lists the C and A fields of the frames the master sent from
// index from on.
func wireC(bus *sim.Bus, from int) string {
	var out []string
	for _, f := range bus.Frames()[from:] {
		out = append(out, hex.EncodeToString([]byte{f.C, f.A}))
	}
	return strings.Join(out, " ")
}

// bits8E1 is a byte as it goes on the line (EN 13757-2 5.4): start bit,
// eight data bits least significant first, even parity, stop bit.
func bits8E1(b byte) string {
	s := "0"
	ones := 0
	for i := 0; i < 8; i++ {
		if b>>i&1 == 1 {
			s += "1"
			ones++
		} else {
			s += "0"
		}
	}
	return s + fmt.Sprint(ones%2) + "1"
}

// EN 13757-2:2018+A1:2023: the annex F frames, ACK and NACK (3.1.3,
// 3.1.4), SND_UD2 with FCV clear (5.7.3.4, 5.7.7), the one-byte L field,
// and the answer window of 5.7.6.
//
// Covers: SIG-IPE-3686, SIG-IPE-3687, SIG-IPE-3688
func TestMBusLinkFramesA1(t *testing.T) {
	// F.1: SND_NKE to the broadcast address.
	nke := wired.Frame{C: wired.CSndNke, A: wired.AddrBroadcast}.Encode()
	if hex.EncodeToString(nke) != "1040ff3f16" {
		t.Errorf("broadcast SND_NKE %x", nke)
	}
	// Its bits on the line; the annex prints the stop byte's group with
	// ten bits, the eleven below are 16h in 8E1 (F-2050).
	var groups []string
	for _, b := range nke {
		groups = append(groups, bits8E1(b))
	}
	if got := strings.Join(groups, " "); got != "00000100011 00000001011 01111111101 01111110001 00110100011" {
		t.Errorf("bits %s", got)
	}
	// F.2: REQ_UD2 to address 1 with FCB 0, and an RSP_UD with three
	// application bytes: L = 6 counts C, A and CI (the annex leaves out
	// the A field, F-2051).
	if got := hex.EncodeToString(wired.Frame{C: wired.CReqUD2, A: 1}.Encode()); got != "105b015c16" {
		t.Errorf("REQ_UD2 %s", got)
	}
	rsp := wired.Frame{Long: true, C: wired.CRspUD, A: 1, CI: 0x78, Data: []byte{0xAA, 0xBB, 0xCC}}.Encode()
	if rsp[1] != 6 || rsp[2] != 6 || rsp[4] != 0x08 || rsp[5] != 1 || rsp[6] != 0x78 {
		t.Errorf("RSP_UD %x", rsp)
	}
	// ACK E5h and NACK A2h are single characters.
	for _, c := range []struct {
		b    byte
		want wired.Frame
	}{{0xE5, wired.Frame{Single: true}}, {0xA2, wired.Frame{Nack: true}}} {
		f, err := wired.ReadFrame(bytes.NewReader([]byte{c.b}))
		if err != nil || f.Single != c.want.Single || f.Nack != c.want.Nack || !bytes.Equal(f.Encode(), []byte{c.b}) {
			t.Errorf("%02X: %+v %v", c.b, f, err)
		}
	}
	if wired.CSndUD2&wired.FCV != 0 || wired.CSndUD&wired.FCV == 0 || wired.CReqUD1&wired.FCV == 0 || wired.CReqUD2&wired.FCV == 0 || wired.CSndNke&(wired.FCV|wired.FCB) != 0 {
		t.Error("FCV bits of the C fields")
	}
	// The longest long frame: L = 255.
	full := wired.Frame{Long: true, C: wired.CSndUD, A: 5, CI: 0x51, Data: make([]byte, wired.MaxLongData)}
	if err := full.Check(); err != nil {
		t.Fatal(err)
	}
	back, err := wired.ReadFrame(bytes.NewReader(full.Encode()))
	if err != nil || len(back.Data) != wired.MaxLongData {
		t.Errorf("L=255 frame: %v", err)
	}
	over := full
	over.Data = make([]byte, wired.MaxLongData+1)
	if err := over.Check(); err == nil {
		t.Error("a long frame with 253 data bytes passes Check")
	}
	bus := startBus(t, &sim.Slave{Primary: 5, Address: wired.SecondaryAddress{ID: "12345678", Manufacturer: 0x4024, Version: 1, Medium: 7}, Telegrams: [][]byte{{0x04, 0x13, 1, 0, 0, 0}}})
	d := linkMaster(t, bus, 200*time.Millisecond, 2)
	if _, err := d.Exchange(context.Background(), over, true); err == nil || !strings.Contains(err.Error(), "at most 252") {
		t.Errorf("an overlong frame was sent: %v", err)
	}
	if n := len(bus.Frames()); n != 0 {
		t.Errorf("%d frames reached the bus", n)
	}
	// 5.7.6: 330 bit times + 50 ms.
	if wired.MaxAnswerDelay(300) != 1150*time.Millisecond || wired.MaxAnswerDelay(2400) != 187500*time.Microsecond {
		t.Errorf("answer windows %v %v", wired.MaxAnswerDelay(300), wired.MaxAnswerDelay(2400))
	}
}

// EN 13757-2:2018+A1:2023 5.7.7 with slaves of both kinds: one frame
// count bit per address, preset by SND_NKE, toggled by each new REQ_UD1
// or REQ_UD2, carried unchanged by SND_UD (the last request's bit, so the
// A1 slave that counts SND_UD and the earlier slave that does not both
// see every new request as new), clear in SND_UD2, and repeated
// unchanged when a request is sent again.
//
// Covers: SIG-IPE-3684, SIG-IPE-3681
func TestMBusFCBSequenceA1(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"shared", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			s := &sim.Slave{Primary: 5, Address: wired.SecondaryAddress{ID: "12345678", Manufacturer: 0x4024, Version: 1, Medium: 7}, FCB: mode, SndUD2: "rsp",
				Telegrams: [][]byte{{0x04, 0x13, 1, 0, 0, 0}, {0x04, 0x13, 2, 0, 0, 0}, {0x04, 0x13, 3, 0, 0, 0}, {0x04, 0x13, 4, 0, 0, 0}}}
			bus := startBus(t, s)
			d := linkMaster(t, bus, 200*time.Millisecond, 2)
			volume := func(f wired.Frame, err error) int {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
				r, err := wired.ParseResponse(f)
				if err != nil {
					t.Fatal(err)
				}
				switch v := r.Records[0].Value.(type) {
				case int64:
					return int(v)
				case float64:
					return int(math.Round(v * 1000)) // VIF 13h: litres
				}
				t.Fatalf("record %+v", r.Records[0])
				return 0
			}
			ack := func() {
				t.Helper()
				if f, err := d.Exchange(ctx, wired.Frame{Long: true, C: wired.CSndUD, A: 5, CI: wired.CISynchronize}, true); err != nil || !f.Single {
					t.Fatalf("SND_UD: %+v %v", f, err)
				}
			}
			if _, err := d.Exchange(ctx, wired.Frame{C: wired.CSndNke, A: 5}, true); err != nil {
				t.Fatal(err)
			}
			req := wired.Frame{C: wired.CReqUD2, A: 5}
			got := []int{volume(d.Exchange(ctx, req, true))}
			ack()
			got = append(got, volume(d.Exchange(ctx, req, true)))
			ack()
			got = append(got, volume(d.Exchange(ctx, req, true)))
			// A lost answer: the request goes again with the same bit and the
			// slave repeats its telegram instead of moving on.
			bus.Lock()
			bus.DropNext = 1
			bus.Unlock()
			got = append(got, volume(d.Exchange(ctx, req, true)))
			// SND_UD2 has FCV and FCB clear whatever the sequence.
			if f, err := d.Exchange(ctx, wired.Frame{Long: true, C: wired.CSndUD2 | wired.FCB, A: 5, CI: 0x51}, true); err != nil || !f.Long {
				t.Errorf("SND_UD2: %+v %v", f, err)
			}
			if fmt.Sprint(got) != "[1 2 3 4]" {
				t.Errorf("telegrams %v, want 1 to 4 in order", got)
			}
			if w := wireC(bus, 0); w != "4005 7b05 7305 5b05 5305 7b05 5b05 5b05 4305" {
				t.Errorf("frames %s", w)
			}
			if rep, _ := bus.Repeats(s); rep != 1 {
				t.Errorf("%d requests taken as repetitions, want only the resent one", rep)
			}
		})
	}
}

// EN 13757-2:2018+A1:2023 5.7.6: a request whose answer is damaged is
// sent again, three attempts in all like an unanswered one.
//
// Covers: SIG-IPE-3681
func TestMBusDamagedAnswerRetries(t *testing.T) {
	ctx := context.Background()
	s := &sim.Slave{Primary: 5, Address: wired.SecondaryAddress{ID: "12345678", Manufacturer: 0x4024, Version: 1, Medium: 7}, FCB: "shared",
		Telegrams: [][]byte{{0x04, 0x13, 1, 0, 0, 0}}}
	bus := startBus(t, s)
	d := linkMaster(t, bus, 200*time.Millisecond, 2)
	bus.Lock()
	bus.CorruptNext = 2
	bus.Unlock()
	f, err := d.Exchange(ctx, wired.Frame{C: wired.CReqUD2, A: 5}, true)
	if err != nil || !f.Long {
		t.Fatalf("after two damaged answers: %+v %v", f, err)
	}
	if w := wireC(bus, 0); w != "7b05 7b05 7b05" {
		t.Errorf("frames %s, want the same REQ_UD2 three times", w)
	}
	n := len(bus.Frames())
	bus.Lock()
	bus.CorruptNext = 3
	bus.Unlock()
	if _, err := d.Exchange(ctx, wired.Frame{C: wired.CReqUD2, A: 5}, true); !errors.Is(err, wired.ErrFrame) {
		t.Errorf("three damaged answers: %v, want the framing error", err)
	}
	if got := len(bus.Frames()) - n; got != 3 {
		t.Errorf("%d attempts, want 3", got)
	}
	n = len(bus.Frames())
	if _, err := d.Exchange(ctx, wired.Frame{C: wired.CReqUD2, A: 9}, true); !errors.Is(err, wired.ErrNoAnswer) {
		t.Errorf("no meter at 9: %v", err)
	}
	if got := len(bus.Frames()) - n; got != 3 {
		t.Errorf("%d attempts at an empty address, want 3", got)
	}
}

// EN 13757-2:2018+A1:2023 5.7.6 and 5.5.3: the timeout bounds the start
// of an answer, not its end, so a long answer arriving slowly is read
// whole; and after a datagram nobody answers the master leaves the line idle before
// the next.
//
// Covers: SIG-IPE-3682, SIG-IPE-3683
func TestMBusAnswerTimingA1(t *testing.T) {
	ctx := context.Background()
	s := &sim.Slave{Primary: 5, Address: wired.SecondaryAddress{ID: "12345678", Manufacturer: 0x4024, Version: 1, Medium: 7},
		Telegrams: [][]byte{bytes.Repeat([]byte{0x04, 0x13, 1, 0, 0, 0}, 8)}}
	bus := startBus(t, s)
	d := linkMaster(t, bus, 150*time.Millisecond, 0)
	bus.Lock()
	bus.ByteDelay = 4 * time.Millisecond // about 72 bytes: some 290 ms, as at 2400 Bd
	bus.Unlock()
	f, err := d.Exchange(ctx, wired.Frame{C: wired.CReqUD2, A: 5}, true)
	if err != nil || len(f.Data) != 12+48 {
		t.Fatalf("slow answer: %v", err)
	}
	bus.Lock()
	bus.ByteDelay = 0
	bus.Unlock()
	n := len(bus.Frames())
	if _, err := d.Exchange(ctx, wired.Frame{C: wired.CSndNke, A: wired.AddrBroadcast}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exchange(ctx, wired.Frame{C: wired.CSndNke, A: 5}, true); err != nil {
		t.Fatal(err)
	}
	ts := bus.Times()
	if gap := ts[n+1].Sub(ts[n]); gap < wired.QuietAfter(5, 2400)-5*time.Millisecond {
		t.Errorf("the next datagram came %v after the broadcast, want at least %v", gap, wired.QuietAfter(5, 2400))
	}
}

// The wildcard search (EN 13757-3 selection behind address 253, EN
// 13757-2 5.7.5) against slaves that keep strict frame count bits for
// address 253 (5.7.7): the bus is normalised first and again after any
// read at 253, so no probe is taken for a repetition; collisions at the
// selection or at the read go a digit deeper; meters sharing all eight
// digits are reported, not listed.
//
// Covers: SIG-IPE-3685
func TestMBusSearchCollisionsA1(t *testing.T) {
	ctx := context.Background()
	mk := func(id string, man uint16, mode string) *sim.Slave {
		return &sim.Slave{Address: wired.SecondaryAddress{ID: id, Manufacturer: man, Version: 1, Medium: 7}, FCB: mode,
			Telegrams: [][]byte{{0x04, 0x13, 1, 0, 0, 0}}}
	}
	sl := []*sim.Slave{mk("12345678", 0x2C2D, "shared"), mk("12349999", 0x2C2D, "legacy"), mk("12340000", 0x2C2D, "shared"),
		mk("87654321", 0x2C2D, "legacy"), mk("55555555", 0x2C2D, "shared"), mk("55555555", 0x4024, "shared")}
	bus := startBus(t, sl...)
	d := linkMaster(t, bus, 60*time.Millisecond, 0)
	// 12345678 was read at 253 before the search: its last bit there is 1.
	b, _ := sl[0].Address.Bytes()
	if _, err := d.Exchange(ctx, wired.Frame{Long: true, C: wired.CSndUD, A: wired.AddrSecondary, CI: wired.CISelect, Data: b}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exchange(ctx, wired.Frame{C: wired.CReqUD2, A: wired.AddrSecondary}, true); err != nil {
		t.Fatal(err)
	}
	start := len(bus.Frames())
	found, err := d.Search(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range found {
		ids = append(ids, a.ID)
	}
	if got := strings.Join(ids, " "); got != "12340000 12345678 12349999 87654321" {
		t.Errorf("found %s", got)
	}
	for _, s := range sl {
		if rep, _ := bus.Repeats(s); rep != 0 {
			t.Errorf("%s took %d probes for repetitions", s.Address.ID, rep)
		}
	}
	// Every selection after a read at 253 is preceded by a broadcast
	// SND_NKE; the search starts with one.
	fs := bus.Frames()[start:]
	if fs[0].Long || fs[0].C != wired.CSndNke || fs[0].A != wired.AddrBroadcast {
		t.Errorf("the search starts with %+v", fs[0])
	}
	read := false
	for _, f := range fs {
		switch {
		case !f.Long && f.C == wired.CSndNke && f.A == wired.AddrBroadcast:
			read = false
		case !f.Long && f.A == wired.AddrSecondary:
			read = true
		case f.Long && f.CI == wired.CISelect && read:
			t.Fatalf("a selection after a read at 253 without SND_NKE: %x", f.Data)
		}
	}
}
