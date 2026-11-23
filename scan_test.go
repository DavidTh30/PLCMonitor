package main

import (
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/simonvetter/modbus"
)

func TestScanTargets(t *testing.T) {
	tg, nets, err := scanTargets("10.0.0.0/30, 10.0.0.2 ; 10.0.1.5", true)
	if err != nil {
		t.Fatal(err)
	}
	// 10.0.0.1, 10.0.0.2 (once), 10.0.1.5, 127.0.0.1
	if len(tg) != 4 || fmt.Sprint(nets) != "[10.0.0.0/30 10.0.0.2 10.0.1.5 127.0.0.1]" {
		t.Errorf("%v %v", tg, nets)
	}
	if tg[3].ip.String() != "127.0.0.1" || tg[3].net != localNetName || tg[1].net != "10.0.0.0/30" {
		t.Errorf("targets: %+v", tg)
	}
	if _, _, err := scanTargets("", false); err == nil {
		t.Error("nothing to scan accepted")
	}
	if _, _, err := scanTargets("10.0.0.0/30, nonsense", false); err == nil {
		t.Error("bad entry accepted")
	}
	if _, nets, err := scanTargets("all", false); err != nil || len(nets) != len(LocalNetworks()) {
		t.Errorf("all: %v %v", nets, err)
	}
}

func TestParseS7Clock(t *testing.T) {
	// The answer of a CPU to "read clock" (userdata, function group 7),
	// 2026-11-23 07:41:13.250.
	resp := []byte{3, 0, 0, 43, 2, 240, 128, 50, 7, 0, 0, 56, 0, 0, 12, 0, 14, 0, 1, 18, 8, 18, 135, 1, 0, 0, 0, 0,
		0, 0xFF, 9, 0, 10, 0, 0x20, 0x26, 0x11, 0x23, 0x07, 0x41, 0x13, 0x25, 0x01}
	got, err := parseS7Clock(resp)
	if err != nil || !got.Equal(time.Date(2026, 11, 23, 7, 41, 13, 250*int(time.Millisecond), time.UTC)) {
		t.Errorf("%v %v", got, err)
	}
	// "function not supported" (a virtual PLC): no clock.
	bad := []byte{3, 0, 0, 33, 2, 240, 128, 50, 7, 0, 0, 56, 0, 0, 12, 0, 4, 0, 1, 18, 8, 18, 135, 1, 0, 0, 0, 129, 4, 10, 0, 0, 0}
	if _, err := parseS7Clock(bad); err == nil {
		t.Error("error answer read as a clock")
	}
	resp[36] = 0x13 // month 13
	if _, err := parseS7Clock(resp); err == nil {
		t.Error("month 13 accepted")
	}

	// How far off: a PLC 2 minutes ahead in local time; one in 2012.
	c := clockOf(time.Now().Add(2*time.Minute), false)
	if c.Diff < 110 || c.Diff > 130 {
		t.Errorf("2 min ahead: %+v", c)
	}
	if c := clockOf(time.Date(2012, 1, 5, 6, 2, 24, 0, time.UTC), true); c.Diff > -1e8 || c.Ref != "UTC" {
		t.Errorf("2012: %+v", c)
	}
}

func TestHostsOf(t *testing.T) {
	if ips, err := hostsOf("192.168.10.0/24"); err != nil || len(ips) != 254 || ips[0].String() != "192.168.10.1" || ips[253].String() != "192.168.10.254" {
		t.Errorf("/24: %d %v", len(ips), err)
	}
	if ips, err := hostsOf("10.0.0.7"); err != nil || len(ips) != 1 {
		t.Errorf("single address: %v %v", ips, err)
	}
	if ips, err := hostsOf("10.0.0.0/30"); err != nil || len(ips) != 2 {
		t.Errorf("/30: %v %v", ips, err)
	}
	for _, bad := range []string{"10.0.0.0/21", "nonsense", "::1/128", ""} {
		if _, err := hostsOf(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// gatewayDevice answers like an RS485 gateway with slaves 1 and 5 behind it:
// other unit IDs get "gateway target device failed to respond".
type gatewayDevice struct{ fakeDevice }

func (g *gatewayDevice) HandleHoldingRegisters(r *modbus.HoldingRegistersRequest) ([]uint16, error) {
	if r.UnitId != 1 && r.UnitId != 5 {
		return nil, modbus.ErrGWTargetFailedToRespond
	}
	return g.fakeDevice.HandleHoldingRegisters(r)
}

func startModbusServer(t *testing.T, h modbus.RequestHandler) int {
	t.Helper()
	port := freePort(t)
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: "tcp://127.0.0.1:" + port, Timeout: 5 * time.Second, MaxClients: 4}, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Stop() })
	n, _ := strconv.Atoi(port)
	return n
}

func waitScan(t *testing.T, s *Scanner) ScanState {
	t.Helper()
	var st ScanState
	waitForLong(t, "scan finished", 30*time.Second, func() bool { st = s.State(); return !st.Running })
	return st
}

func waitForLong(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestScanFindsModbusGateway(t *testing.T) {
	gwPort := startModbusServer(t, &gatewayDevice{})
	mon, _ := newTestMonitor(t, nil)
	s := NewScanner(mon)
	if st := s.State(); st.Devices == nil {
		t.Error("devices is null before the first scan (the page expects a list)")
	}
	s.modbusPort = gwPort
	s.s7Port = 1 // nothing listens there

	if err := s.Start(ScanOptions{Subnet: "127.0.0.1", TimeoutMs: 300, Units: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(ScanOptions{Subnet: "127.0.0.1"}); err == nil {
		t.Error("second scan started while one is running")
	}
	st := waitScan(t, s)
	if len(st.Devices) != 1 {
		t.Fatalf("devices: %+v", st.Devices)
	}
	d := st.Devices[0]
	if d.IP != "127.0.0.1" || d.Kind != "modbus" || d.Modbus == nil || d.Modbus.AllUnits ||
		fmt.Sprint(d.Modbus.Units) != "[1 5]" {
		t.Errorf("gateway: %+v modbus=%+v", d, d.Modbus)
	}
}

func TestScanPlainModbusDevice(t *testing.T) {
	port := startModbusServer(t, &fakeDevice{}) // answers on every unit ID
	mon, _ := newTestMonitor(t, nil)
	s := NewScanner(mon)
	s.modbusPort, s.s7Port = port, 1
	s.Start(ScanOptions{Subnet: "127.0.0.1", TimeoutMs: 300, Units: true})
	st := waitScan(t, s)
	if len(st.Devices) != 1 || st.Devices[0].Modbus == nil || !st.Devices[0].Modbus.AllUnits {
		t.Errorf("plain device: %+v", st.Devices)
	}
}

// TestBusScan scans slave IDs through the monitor's own connection, the way
// it works on an RS485 COM port that is already open for polling.
func TestBusScan(t *testing.T) {
	gwPort := startModbusServer(t, &gatewayDevice{})
	cfg := &Config{Protocol: ProtoModbus, Modbus: ModbusConfig{Mode: "tcp", Address: "127.0.0.1:" + strconv.Itoa(gwPort), UnitID: 1},
		PollInterval: 50 * time.Millisecond, Tags: []Tag{{Name: "x", Area: "HR", Offset: 0, Type: "INT"}}}
	mon := startMonitor(t, cfg, func(c *Config) Dialer { return ModbusDialer(c.Modbus) })
	s := NewScanner(mon)
	if err := s.StartBus(1, 10); err != nil {
		t.Fatal(err)
	}
	waitForLong(t, "bus scan", 30*time.Second, func() bool { return !s.State().Bus.Running })
	bus := s.State().Bus
	if fmt.Sprint(bus.Units) != "[1 5]" || bus.Error != "" {
		t.Errorf("bus scan: %+v", bus)
	}
	// Polling went on during the scan and still works afterwards.
	if st, tags := mon.Snapshot(); !st.Connected || !tags[0].Good {
		t.Errorf("polling broken after bus scan: %+v", tags[0])
	}
	if err := s.StartBus(0, 300); err == nil {
		t.Error("slave IDs outside 1..247 accepted")
	}
}

func TestLocalNetworks(t *testing.T) {
	for _, n := range LocalNetworks() {
		if _, _, err := net.ParseCIDR(n.Subnet); err != nil || net.ParseIP(n.IP) == nil {
			t.Errorf("bad local network %+v", n)
		}
	}
}
