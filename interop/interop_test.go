// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package interop

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cB-Sigma/mbus-sim/sim"
	"github.com/cB-Sigma/mbus/wireless"
)

// The wmbusmeters image the tests run, pinned by tag and by the digest
// that tag named when the tests were written (the arm64 and amd64 images
// are both under this index digest).
const (
	wmImage  = "wmbusmeters/wmbusmeters:release-3.0.0"
	wmDigest = "sha256:90839319b0512730fb3aae43837f5db8219a82b3c0e1267ab0907e767f3d4847"
	wmBinary = "/wmbusmeters/wmbusmeters"
)

func needDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("SIGMA_MBUS_INTEROP") != "1" {
		t.Skip("runs wmbusmeters in Docker; set SIGMA_MBUS_INTEROP=1 to run")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not on PATH")
	}
	if exec.Command("docker", "image", "inspect", wmImage).Run() != nil {
		if out, err := exec.Command("docker", "pull", wmImage).CombinedOutput(); err != nil {
			t.Skipf("cannot pull %s (offline?): %v %s", wmImage, err, out)
		}
	}
	out, err := exec.Command("docker", "image", "inspect", "--format", "{{json .RepoDigests}}", wmImage).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), wmDigest) {
		t.Fatalf("%s is %s, the tests are pinned to %s", wmImage, out, wmDigest)
	}
}

// runWmbusmeters decodes telegrams (CRC-less hex) with wmbusmeters'
// driver for one meter and returns the JSON objects it prints.
func runWmbusmeters(t *testing.T, telegrams [][]byte, driver, id, key string) []map[string]any {
	t.Helper()
	dir := t.TempDir()
	var sim bytes.Buffer
	for _, tg := range telegrams {
		fmt.Fprintf(&sim, "telegram=|%X|\n", tg)
	}
	if err := os.WriteFile(filepath.Join(dir, "simulation.txt"), sim.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if key == "" {
		key = "NOKEY"
	}
	cmd := exec.Command("docker", "run", "--rm", "--network", "none", "--read-only",
		"-v", dir+":/data:ro", "--entrypoint", wmBinary, wmImage,
		"--format=json", "/data/simulation.txt", "SimMeter", driver, id, key)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("wmbusmeters: %v\n%s", err, stderr.String())
	}
	if s := strings.TrimSpace(stderr.String()); s != "" {
		t.Logf("wmbusmeters stderr: %s", s)
	}
	var out []map[string]any
	sc := bufio.NewScanner(&stdout)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("wmbusmeters printed %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// waterRecords are an instantaneous volume (VIF 13h, litres, 32-bit
// integer) and a volume flow (VIF 3Bh, l/h, 16-bit signed integer), the records
// wmbusmeters' iperl driver reads.
func waterRecords(litres uint32, flowLH int16) []byte {
	b := []byte{0x04, 0x13}
	b = binary.LittleEndian.AppendUint32(b, litres)
	b = append(b, 0x02, 0x3B)
	return binary.LittleEndian.AppendUint16(b, uint16(flowLH))
}

// TestWmbusmetersDecodesSimulatedMeter: interop of mbus-sim's wireless meter (modes 0, 5, 7; with and without ELL) with wmbusmeters 3.0.0
func TestWmbusmetersDecodesSimulatedMeter(t *testing.T) {
	needDocker(t)
	key := []byte{0x5A, 0x17, 0xC3, 0x08, 0x9E, 0x41, 0x26, 0xB7, 0xD0, 0x6F, 0x83, 0x2C, 0xE5, 0x19, 0x74, 0xAB} // a test key
	type reading struct {
		litres uint32
		flow   int16 // type B: signed
	}
	readings := []reading{{123529, 0}, {123530, 291}, {987654321, -1}}
	total, equal := 0, 0
	for _, mode := range []int{0, 5, 7} {
		for _, bidir := range []bool{false, true} {
			for _, driver := range []string{"iperl", "auto"} {
				name := fmt.Sprintf("mode%d/ell=%v/%s", mode, bidir, driver)
				t.Run(name, func(t *testing.T) {
					m := &sim.Meter{Manufacturer: "SEN", ID: "12345678", Version: 0x68, DeviceType: 0x07, Mode: mode, Bidirectional: bidir}
					keyHex := ""
					if mode != 0 {
						m.Key = key
						keyHex = hex.EncodeToString(key)
					}
					var frames [][]byte
					for _, r := range readings {
						m.Records = waterRecords(r.litres, r.flow)
						f, err := m.Telegram()
						if err != nil {
							t.Fatal(err)
						}
						frames = append(frames, f)
					}
					got := runWmbusmeters(t, frames, driver, m.ID, keyHex)
					if len(got) != len(readings) {
						t.Fatalf("wmbusmeters printed %d readings for %d telegrams: %v", len(got), len(readings), got)
					}
					for i, r := range readings {
						total++
						j := got[i]
						wantM3, wantFlow := float64(r.litres)/1000, float64(r.flow)/1000
						m3, _ := j["total_m3"].(float64)
						flow, _ := j["max_flow_m3h"].(float64)
						if j["id"] != m.ID || j["meter"] != "iperl" || j["media"] != "water" ||
							math.Abs(m3-wantM3) > 1e-9 || math.Abs(flow-wantFlow) > 1e-9 {
							t.Errorf("telegram %d %X: wmbusmeters %v, want id %s, iperl, water, %v m3, %v m3/h", i+1, frames[i], j, m.ID, wantM3, wantFlow)
							continue
						}
						// Our decoder reads the same frame to the same values.
						tg, err := wireless.Decode(frames[i], wireless.FormatNoCRC, func(wireless.Header) []byte { return m.Key })
						if err != nil || len(tg.Records) != 2 || tg.Records[0].Value != wantM3 || tg.Records[1].Value != wantFlow {
							t.Errorf("telegram %d: our decoder %v %+v", i+1, err, tg)
							continue
						}
						equal++
					}
				})
			}
		}
	}
	// A control: with a wrong key wmbusmeters prints no reading (it does
	// check mode 5's 2F2F and mode 7's AFL CMAC).
	for _, mode := range []int{5, 7} {
		m := &sim.Meter{Manufacturer: "SEN", ID: "12345678", Version: 0x68, DeviceType: 0x07, Mode: mode, Key: key, Records: waterRecords(1, 0)}
		f, err := m.Telegram()
		if err != nil {
			t.Fatal(err)
		}
		wrong := hex.EncodeToString(append([]byte{key[0] ^ 1}, key[1:]...))
		if got := runWmbusmeters(t, [][]byte{f}, "iperl", m.ID, wrong); len(got) != 0 {
			t.Errorf("mode %d with a wrong key: wmbusmeters printed %v", mode, got)
		}
	}
	t.Logf("%s (%s): %d simulated telegrams, %d decoded by wmbusmeters, and by our decoder, to the values the simulator encoded", wmImage, wmDigest, total, equal)
}
