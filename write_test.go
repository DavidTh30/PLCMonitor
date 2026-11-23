package main

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/simonvetter/modbus"
)

func startMonitor(t *testing.T, cfg *Config, dial func(*Config) Dialer) *Monitor {
	t.Helper()
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	mon := NewMonitor(cfg, dial, nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go mon.Run(ctx)
	waitFor(t, "connected", func() bool { st, _ := mon.Snapshot(); return st.Connected })
	return mon
}

func display(m *Monitor, name string) string {
	_, tags := m.Snapshot()
	for _, s := range tags {
		if s.Name == name {
			return s.Display
		}
	}
	return "?"
}

func mustParse(t *testing.T, m *Monitor, name, s string) any {
	t.Helper()
	tag, ok := m.Tag(name)
	if !ok {
		t.Fatalf("no tag %s", name)
	}
	v, err := tag.ParseValue(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestModbusWrite writes every writable kind of Modbus tag to a real Modbus
// TCP server (the library's own, on loopback) and checks the device memory.
func TestModbusWrite(t *testing.T) {
	dev := &fakeDevice{}
	dev.hr[10] = 0x0001 // bit 0 already set; writing bit 3 must keep it
	port := freePort(t)
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: "tcp://127.0.0.1:" + port, Timeout: 5 * time.Second, MaxClients: 2}, dev)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	cfg := &Config{
		Protocol: ProtoModbus, Modbus: ModbusConfig{Mode: "tcp", Address: "127.0.0.1:" + port, UnitID: 1},
		PollInterval: 50 * time.Millisecond,
		Tags: []Tag{
			{Name: "int", Area: "HR", Offset: 0, Type: "INT"},
			{Name: "real", Area: "HR", Offset: 2, Type: "REAL"},
			{Name: "bit3", Area: "HR", Offset: 10, Bit: 3, Type: "BOOL"},
			{Name: "coil", Area: "COIL", Offset: 5, Type: "BOOL"},
			{Name: "ir", Area: "IR", Offset: 0, Type: "WORD"},
		},
	}
	mon := startMonitor(t, cfg, func(c *Config) Dialer { return ModbusDialer(c.Modbus) })

	for _, w := range []struct{ tag, val string }{{"int", "-1234"}, {"real", "12.5"}, {"bit3", "true"}, {"coil", "1"}} {
		if err := mon.Write(w.tag, mustParse(t, mon, w.tag, w.val)); err != nil {
			t.Fatalf("write %s: %v", w.tag, err)
		}
	}
	dev.mu.Lock()
	gotInt, gotReal := int16(dev.hr[0]), math.Float32frombits(uint32(dev.hr[2])<<16|uint32(dev.hr[3]))
	gotBits, gotCoil := dev.hr[10], dev.coil[5]
	dev.mu.Unlock()
	if gotInt != -1234 || gotReal != 12.5 || gotBits != 0x0009 || !gotCoil {
		t.Errorf("device after writes: int=%d real=%g hr10=%#04x coil=%v", gotInt, gotReal, gotBits, gotCoil)
	}
	waitFor(t, "written values read back", func() bool {
		return display(mon, "int") == "-1234" && display(mon, "real") == "12.5" && display(mon, "bit3") == "TRUE"
	})
	if err := mon.Write("ir", int64(1)); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("input register write: %v", err)
	}
}

func TestParseValue(t *testing.T) {
	tags := mustTags(t, []Tag{
		{Name: "b", Area: "M", Type: "BOOL"}, {Name: "by", Area: "M", Type: "BYTE"},
		{Name: "i", Area: "M", Type: "INT"}, {Name: "dw", Area: "M", Type: "DWORD"}, {Name: "r", Area: "M", Type: "REAL"},
	})
	good := []struct {
		i    int
		in   string
		want any
	}{{0, "TRUE", true}, {0, "0", false}, {1, "255", int64(255)}, {1, "0x10", int64(16)},
		{2, "-32768", int64(-32768)}, {3, "4294967295", int64(4294967295)}, {4, "-1.5e3", -1500.0}}
	for _, c := range good {
		if v, err := tags[c.i].ParseValue(c.in); err != nil || v != c.want {
			t.Errorf("%s %q: %v %v", tags[c.i].Type, c.in, v, err)
		}
	}
	bad := []struct {
		i  int
		in string
	}{{0, "maybe"}, {1, "256"}, {1, "-1"}, {2, "40000"}, {2, "1.5"}, {3, "-1"}, {4, "abc"}, {4, "1e40"}}
	for _, c := range bad {
		if _, err := tags[c.i].ParseValue(c.in); err == nil {
			t.Errorf("%s accepted %q", tags[c.i].Type, c.in)
		}
	}
}

func TestGeneratorShapes(t *testing.T) {
	real := mustTags(t, []Tag{{Name: "r", Area: "M", Type: "REAL"}})[0]
	word := mustTags(t, []Tag{{Name: "w", Area: "M", Type: "WORD"}})[0]
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	near := func(a any, b float64) bool { return math.Abs(toFloat(a)-b) < 1e-9 }

	sine := GenSpec{Kind: "sine", Min: 0, Max: 100, PeriodMs: 1000, IntervalMs: 100}
	if !near(sine.value(&real, 0), 50) || !near(sine.value(&real, ms(250)), 100) || !near(sine.value(&real, ms(750)), 0) {
		t.Error("sine wave shape")
	}
	saw := GenSpec{Kind: "sawtooth", Min: 10, Max: 20, PeriodMs: 1000, IntervalMs: 100}
	if !near(saw.value(&real, ms(500)), 15) || !near(saw.value(&real, ms(1000)), 10) {
		t.Error("sawtooth shape")
	}
	sq := GenSpec{Kind: "square", Min: 1, Max: 9, PeriodMs: 1000, IntervalMs: 100}
	if sq.value(&word, ms(100)) != int64(9) || sq.value(&word, ms(600)) != int64(1) {
		t.Error("square shape / integer output")
	}
	if v := sine.value(&word, ms(100)); v != int64(math.Round(50+50*math.Sin(2*math.Pi*0.1))) {
		t.Errorf("integer sine not rounded: %v", v)
	}
	for i := 0; i < 200; i++ {
		v := GenSpec{Kind: "random", Min: 5, Max: 7, IntervalMs: 100}.value(&word, 0).(int64)
		if v < 5 || v > 7 {
			t.Fatalf("random out of range: %d", v)
		}
	}
	b := mustTags(t, []Tag{{Name: "b", Area: "M", Type: "BOOL"}})[0]
	tog := GenSpec{Kind: "toggle", OnMs: 300, OffMs: 100}
	if tog.value(&b, ms(299)) != true || tog.value(&b, ms(350)) != false || tog.value(&b, ms(400)) != true {
		t.Error("toggle timing")
	}
	tick := GenSpec{Kind: "tick", WidthMs: 50, PeriodMs: 1000}
	if tick.value(&b, ms(10)) != true || tick.value(&b, ms(60)) != false || tick.value(&b, ms(1020)) != true {
		t.Error("tick timing")
	}

	invalid := []struct {
		tag  *Tag
		spec GenSpec
	}{
		{&word, GenSpec{Kind: "toggle", OnMs: 100, OffMs: 100}},                      // BOOL kind on a number
		{&b, GenSpec{Kind: "sine", Min: 0, Max: 1, PeriodMs: 1000, IntervalMs: 100}}, // wave on BOOL
		{&word, GenSpec{Kind: "sine", Min: 0, Max: 70000, PeriodMs: 1000, IntervalMs: 100}},
		{&word, GenSpec{Kind: "sine", Min: 5, Max: 5, PeriodMs: 1000, IntervalMs: 100}},
		{&word, GenSpec{Kind: "sine", Min: 0, Max: 10, PeriodMs: 1000, IntervalMs: 10}},
		{&b, GenSpec{Kind: "tick", WidthMs: 500, PeriodMs: 400}},
	}
	for _, c := range invalid {
		if err := c.spec.validate(c.tag); err == nil {
			t.Errorf("accepted %+v on %s", c.spec, c.tag.Type)
		}
	}
}

// TestGeneratorsOnSimulator runs real generators through the poll loop's
// write path and watches the values the monitor reads back.
func TestGeneratorsOnSimulator(t *testing.T) {
	cfg := &Config{Simulate: true, PollInterval: 20 * time.Millisecond, Tags: []Tag{
		{Name: "level", Area: "DB", DB: 1, Offset: 0, Type: "REAL"},
		{Name: "count", Area: "M", Offset: 0, Type: "INT"},
		{Name: "run", Area: "M", Offset: 4, Bit: 2, Type: "BOOL"},
	}}
	cfg.applyDefaults()
	cfg.validate()
	sim := NewSimulator(cfg.Tags, SimConfig{})
	mon := startMonitor(t, cfg, func(*Config) Dialer { return sim.Dialer() })
	mon.OnApply = func(c *Config) { sim.SetTags(c.Tags) }

	// A manual write sticks: the simulator stops animating that tag.
	if err := mon.Write("count", int64(1234)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if d := display(mon, "count"); d != "1234" {
		t.Errorf("manual write not kept: %s", d)
	}

	if err := mon.StartGenerator("level", GenSpec{Kind: "sine", Min: 10, Max: 20, PeriodMs: 400, IntervalMs: 50}); err != nil {
		t.Fatal(err)
	}
	if err := mon.StartGenerator("run", GenSpec{Kind: "toggle", OnMs: 100, OffMs: 100}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond) // let the first generated values be written and polled
	lo, hi := math.Inf(1), math.Inf(-1)
	seen := map[string]bool{}
	for end := time.Now().Add(900 * time.Millisecond); time.Now().Before(end); time.Sleep(15 * time.Millisecond) {
		_, tags := mon.Snapshot()
		if f, ok := tags[0].Value.(float64); ok && tags[0].Gen != "" {
			lo, hi = math.Min(lo, f), math.Max(hi, f)
		}
		seen[tags[2].Display] = true
	}
	if lo < 10-1e-6 || hi > 20+1e-6 || hi-lo < 5 {
		t.Errorf("sine generator range %g..%g, want spread inside 10..20", lo, hi)
	}
	if !seen["TRUE"] || !seen["FALSE"] {
		t.Errorf("toggle generator never produced both states: %v", seen)
	}
	if _, tags := mon.Snapshot(); !strings.HasPrefix(tags[0].Gen, "sine") || tags[2].Gen == "" {
		t.Errorf("generator not shown in state: %q %q", tags[0].Gen, tags[2].Gen)
	}

	// Changing the network target stops every generator.
	mon.Update(func(c *Config) error { c.PLC.Address = "10.9.9.9"; return nil })
	waitFor(t, "generators stopped", func() bool {
		_, tags := mon.Snapshot()
		return tags[0].Gen == "" && tags[2].Gen == ""
	})
}

func TestWritePermissions(t *testing.T) {
	cfg := &Config{Simulate: true, PollInterval: 20 * time.Millisecond, Tags: []Tag{{Name: "x", Area: "M", Offset: 0, Type: "INT"}}}
	cfg.applyDefaults()
	cfg.validate()
	sim := NewSimulator(cfg.Tags, SimConfig{})
	mon := startMonitor(t, cfg, func(*Config) Dialer { return sim.Dialer() })

	call := func(h http.Handler, path, body, remote string) int {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	h := NewWebHandler(mon, false)
	if c := call(h, "/api/write/x", `{"value": 7}`, "127.0.0.1:1"); c != 200 {
		t.Errorf("local write: %d", c)
	}
	if c := call(h, "/api/write/x", `{"value": "99999"}`, "127.0.0.1:1"); c != 400 {
		t.Errorf("out-of-range write: %d", c)
	}
	if c := call(h, "/api/write/x", `{"value": 7}`, "10.0.0.5:1"); c != http.StatusForbidden {
		t.Errorf("remote write without allow_remote_write: %d", c)
	}
	if c := call(h, "/api/gen/x", `{"kind":"square","min":0,"max":5,"period_ms":1000,"interval_ms":100}`, "10.0.0.5:1"); c != http.StatusForbidden {
		t.Errorf("remote generator without allow_remote_write: %d", c)
	}
	mon.Update(func(c *Config) error { c.Web.AllowRemoteWrite = true; return nil })
	if c := call(h, "/api/write/x", `{"value": 8}`, "10.0.0.5:1"); c != 200 {
		t.Errorf("remote write with allow_remote_write: %d", c)
	}
	if c := call(NewWebHandler(mon, true), "/api/write/x", `{"value": 9}`, "127.0.0.1:1"); c != http.StatusForbidden {
		t.Errorf("write on read-only dashboard: %d", c)
	}
	if c := call(h, "/api/gen/x", `{"kind":"toggle","on_ms":100,"off_ms":100}`, "127.0.0.1:1"); c != 400 {
		t.Errorf("BOOL generator on INT tag: %d", c)
	}
}
