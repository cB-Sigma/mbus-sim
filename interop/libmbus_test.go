// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package interop

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/cB-Sigma/mbus-sim/sim"
	"github.com/cB-Sigma/mbus/records"
	"github.com/cB-Sigma/mbus/wired"
)

// libmbus 0.10.3 (BSD-3-Clause), an independent C implementation, as the
// master of the simulated wired bus: its own commands scan the bus by
// primary and by secondary address and read the slaves, in a container
// built from its tag (libmbus.Dockerfile) that reaches the bus over TCP.
const libmbusImage = "sigma-libmbus:0.10.3"

func needLibmbus(t *testing.T) {
	t.Helper()
	if os.Getenv("SIGMA_MBUS_INTEROP") != "1" {
		t.Skip("builds and runs libmbus in Docker; set SIGMA_MBUS_INTEROP=1 to run")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not on PATH")
	}
	if exec.Command("docker", "image", "inspect", libmbusImage).Run() != nil {
		out, err := exec.Command("docker", "build", "-f", "libmbus.Dockerfile", "-t", libmbusImage, ".").CombinedOutput()
		if err != nil {
			t.Skipf("cannot build libmbus (offline?): %v %s", err, out)
		}
	}
}

// libmbus runs one of libmbus's TCP commands against the bus.
func libmbus(t *testing.T, port string, args ...string) string {
	t.Helper()
	run := []string{"run", "--rm"}
	if runtime.GOOS == "linux" {
		// Docker Desktop names the host itself; Linux needs the gateway.
		run = append(run, "--add-host", "host.docker.internal:host-gateway")
	}
	all := append(append(run, libmbusImage, args[0], "host.docker.internal", port), args[1:]...)
	var out, errb bytes.Buffer
	cmd := exec.Command("docker", all...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\n%s%s", args[0], err, out.String(), errb.String())
	}
	return out.String()
}

type lmXML struct {
	Slave struct {
		ID           string `xml:"Id"`
		Manufacturer string
		Version      string
		Medium       string
	} `xml:"SlaveInformation"`
	Records []struct {
		Function, Unit, Value string
		StorageNumber         string
	} `xml:"DataRecord"`
}

func TestLibmbusMasterOnSimulatedBus(t *testing.T) {
	needLibmbus(t)
	abc, _ := records.ManufacturerField("ABC")
	var slaves []*sim.Slave
	for i := 1; i <= 3; i++ {
		slaves = append(slaves, &sim.Slave{Primary: byte(i),
			Address: wired.SecondaryAddress{ID: fmt.Sprintf("1234567%d", i), Manufacturer: abc, Version: 1, Medium: 7},
			// Volume 12.345 m3 + i, flow temperature 40+i Cel; a second
			// telegram with a stored volume.
			Telegrams: [][]byte{
				{0x04, 0x13, byte(12345 + 1000*i), byte((12345 + 1000*i) >> 8), 0, 0, 0x02, 0x5B, byte(40 + i), 0},
				{0x44, 0x13, 0x10, 0x27, 0, 0},
			}})
	}
	bus := sim.New(slaves...)
	if err := bus.ListenOn("0.0.0.0:0"); err != nil {
		t.Fatal(err)
	}
	defer bus.Stop()
	_, port, _ := net.SplitHostPort(bus.Addr())

	// Primary scan: every address answers or not.
	out := libmbus(t, port, "mbus-tcp-scan")
	found := 0
	for i := 1; i <= 3; i++ {
		if strings.Contains(out, "address "+strconv.Itoa(i)) {
			found++
		}
	}
	t.Logf("mbus-tcp-scan: %d of 3 slaves found", found)
	if found != 3 {
		t.Errorf("mbus-tcp-scan found %d of 3:\n%s", found, out)
	}

	// Secondary scan: the wildcard search with collisions.
	out = libmbus(t, port, "mbus-tcp-scan-secondary")
	sec := 0
	for i := 1; i <= 3; i++ {
		if strings.Contains(strings.ToUpper(out), fmt.Sprintf("1234567%d", i)) {
			sec++
		}
	}
	t.Logf("mbus-tcp-scan-secondary: %d of 3 slaves found", sec)
	if sec != 3 {
		t.Errorf("mbus-tcp-scan-secondary found %d of 3:\n%s", sec, out)
	}

	// Reads by primary address, and multi-telegram reads.
	for i := 1; i <= 3; i++ {
		var x lmXML
		out := libmbus(t, port, "mbus-tcp-request-data", strconv.Itoa(i))
		dec := xml.NewDecoder(strings.NewReader(out))
		// libmbus declares ISO-8859-1; its output here is ASCII.
		dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
		if err := dec.Decode(&x); err != nil {
			t.Fatalf("slave %d: %v\n%s", i, err, out)
		}
		if x.Slave.ID != fmt.Sprintf("1234567%d", i) || x.Slave.Manufacturer != "ABC" || len(x.Records) < 2 {
			t.Errorf("slave %d: %+v", i, x)
			continue
		}
		v, _ := strconv.ParseFloat(x.Records[0].Value, 64)
		if want := float64(12345 + 1000*i); v != want || !strings.Contains(x.Records[0].Unit, "m^3") {
			t.Errorf("slave %d: volume %v %s, want %v (1e-3 m^3)", i, x.Records[0].Value, x.Records[0].Unit, want)
		}
		if x.Records[1].Value != strconv.Itoa(40+i) {
			t.Errorf("slave %d: temperature %s", i, x.Records[1].Value)
		}
		out = libmbus(t, port, "mbus-tcp-request-data-multi-reply", strconv.Itoa(i))
		// libmbus follows "more records follow" to the second telegram
		// (the stored volume 10000 L) and prints that last telegram.
		if !strings.Contains(out, "<Value>10000</Value>") || !strings.Contains(out, "<StorageNumber>1</StorageNumber>") {
			t.Errorf("slave %d multi-reply:\n%s", i, out)
		}
	}
	// Reads by secondary address.
	out = libmbus(t, port, "mbus-tcp-request-data", "1234567243040107")
	t.Logf("by secondary address: %s", firstLine(out))
	if !strings.Contains(out, "<Id>12345672</Id>") {
		t.Errorf("secondary read:\n%s", out)
	}
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "<Id>") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}
