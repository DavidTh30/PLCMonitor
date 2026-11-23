package main

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func mustTags(t *testing.T, tags []Tag) []Tag {
	t.Helper()
	for i := range tags {
		if err := tags[i].normalize(ProtoS7); err != nil {
			t.Fatal(err)
		}
	}
	return tags
}

func TestDecode(t *testing.T) {
	buf := []byte{
		0b0000_0101, // 0: bits 0 and 2
		0xFF,        // 1: SINT -1 / BYTE 255
		0xFC, 0x18,  // 2: INT -1000
		0xFF, 0xFF, 0xFF, 0xFE, // 4: DINT -2
		0x42, 0x48, 0x00, 0x00, // 8: REAL 50.0
		0x40, 0x09, 0x21, 0xFB, 0x54, 0x44, 0x2D, 0x18, // 12: LREAL pi
	}
	cases := []struct {
		tag  Tag
		want any
	}{
		{Tag{Name: "b0", Area: "M", Offset: 0, Bit: 0, Type: "BOOL"}, true},
		{Tag{Name: "b1", Area: "M", Offset: 0, Bit: 1, Type: "BOOL"}, false},
		{Tag{Name: "b2", Area: "M", Offset: 0, Bit: 2, Type: "bool"}, true},
		{Tag{Name: "sint", Area: "M", Offset: 1, Type: "SINT"}, int64(-1)},
		{Tag{Name: "byte", Area: "M", Offset: 1, Type: "BYTE"}, int64(255)},
		{Tag{Name: "int", Area: "M", Offset: 2, Type: "INT"}, int64(-1000)},
		{Tag{Name: "word", Area: "M", Offset: 2, Type: "WORD"}, int64(0xFC18)},
		{Tag{Name: "dint", Area: "M", Offset: 4, Type: "DINT"}, int64(-2)},
		{Tag{Name: "dword", Area: "M", Offset: 4, Type: "DWORD"}, int64(0xFFFFFFFE)},
		{Tag{Name: "real", Area: "M", Offset: 8, Type: "REAL"}, 50.0},
		{Tag{Name: "lreal", Area: "M", Offset: 12, Type: "LREAL"}, math.Pi},
	}
	for _, c := range cases {
		tags := mustTags(t, []Tag{c.tag})
		got, err := tags[0].Decode(buf, 0)
		if err != nil || got != c.want {
			t.Errorf("%s: got %v (%v), want %v", c.tag.Name, got, err, c.want)
		}
		// Round-trip through Encode.
		out := make([]byte, len(buf))
		copy(out, buf)
		tags[0].Encode(out, got)
		if again, _ := tags[0].Decode(out, 0); again != c.want {
			t.Errorf("%s: encode round-trip got %v", c.tag.Name, again)
		}
	}
}

func TestAddress(t *testing.T) {
	tags := mustTags(t, []Tag{
		{Name: "a", Area: "DB", DB: 1, Offset: 0, Bit: 3, Type: "BOOL"},
		{Name: "b", Area: "DB", DB: 5, Offset: 4, Type: "REAL"},
		{Name: "c", Area: "M", Offset: 10, Type: "INT"},
		{Name: "d", Area: "E", Offset: 0, Bit: 1, Type: "BOOL"},
		{Name: "e", Area: "PA", Offset: 2, Type: "BYTE"},
	})
	want := []string{"DB1.DBX0.3", "DB5.DBD4", "MW10", "I0.1", "QB2"}
	for i, w := range want {
		if got := tags[i].Address(); got != w {
			t.Errorf("tag %s: got %s want %s", tags[i].Name, got, w)
		}
	}
}

func TestBuildBlocks(t *testing.T) {
	tags := mustTags(t, []Tag{
		{Name: "a", Area: "DB", DB: 1, Offset: 0, Bit: 0, Type: "BOOL"},
		{Name: "b", Area: "DB", DB: 1, Offset: 4, Type: "REAL"},
		{Name: "c", Area: "DB", DB: 1, Offset: 500, Type: "INT"}, // too far: new block
		{Name: "d", Area: "DB", DB: 2, Offset: 0, Type: "INT"},
		{Name: "e", Area: "M", Offset: 10, Type: "DINT"},
	})
	blocks := BuildBlocks(tags, 0)
	if len(blocks) != 4 {
		t.Fatalf("got %d blocks, want 4", len(blocks))
	}
	if b := blocks[0]; b.DB != 1 || b.Start != 0 || b.Size != 8 || len(b.Tags) != 2 {
		t.Errorf("first block: %+v", b)
	}
}

func TestChangedDeadband(t *testing.T) {
	tag := mustTags(t, []Tag{{Name: "r", Area: "M", Type: "REAL", Deadband: 0.5}})[0]
	if tag.Changed(10.0, 10.2) {
		t.Error("change below deadband reported")
	}
	if !tag.Changed(10.0, 10.6) {
		t.Error("change above deadband missed")
	}
	if tag.Changed(math.NaN(), math.NaN()) {
		t.Error("NaN -> NaN reported as a change")
	}
}

// TestMonitorWithSimulator runs the full poll loop against the simulator,
// forces a link drop and checks that the monitor reconnects.
func TestMonitorWithSimulator(t *testing.T) {
	cfg := &Config{
		PollInterval: 50 * time.Millisecond, ReconnectDelay: 50 * time.Millisecond, ReconnectMax: 100 * time.Millisecond,
		Simulate: true,
		Tags: []Tag{
			{Name: "flag", Area: "M", Offset: 0, Bit: 0, Type: "BOOL"},
			{Name: "count", Area: "DB", DB: 1, Offset: 0, Type: "DINT"},
			{Name: "level", Area: "DB", DB: 1, Offset: 4, Type: "REAL"},
		},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	sim := NewSimulator(cfg.Tags, SimConfig{})
	mon := NewMonitor(cfg, func(*Config) Dialer { return sim.Dialer() }, nil, "")
	mon.OnApply = func(c *Config) { sim.SetTags(c.Tags) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mon.Run(ctx)

	waitFor(t, "connected", func() bool { st, _ := mon.Snapshot(); return st.Connected })
	waitFor(t, "values good", func() bool {
		_, tags := mon.Snapshot()
		return tags[0].Good && tags[1].Good && tags[2].Good
	})

	sim.mu.Lock()
	sim.downUntil = time.Now().Add(300 * time.Millisecond)
	sim.mu.Unlock()
	waitFor(t, "disconnect detected", func() bool { st, tags := mon.Snapshot(); return !st.Connected && !tags[1].Good })
	waitFor(t, "reconnected", func() bool { st, tags := mon.Snapshot(); return st.Connected && tags[1].Good && st.Reconnects > 0 })

	// Live tag edits: add a tag in a new DB, retype one, delete one.
	st0, _ := mon.Snapshot()
	err := mon.Update(func(c *Config) error {
		c.Tags = append(c.Tags, Tag{Name: "added", Area: "DB", DB: 9, Offset: 2, Type: "INT"})
		c.Tags[2].Type = "DINT"
		c.Tags = c.Tags[1:] // drop "flag"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "edited tags polled", func() bool {
		_, tags := mon.Snapshot()
		return len(tags) == 3 && tags[0].Name == "count" && tags[1].Type == "DINT" &&
			tags[2].Name == "added" && tags[2].Good && tags[2].Address == "DB9.DBW2"
	})
	if st, _ := mon.Snapshot(); st.Reconnects != st0.Reconnects {
		t.Error("tag-only change caused a reconnect")
	}
	if err := mon.Update(func(c *Config) error { c.Tags[0].Name = "added"; return nil }); err == nil {
		t.Error("duplicate tag name accepted")
	}
}

func TestSaveConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	orig := "# hand-written\nplc: {address: 10.0.0.5, rack: 0, slot: 2}\npoll_interval: 250ms\nsimulate: false\n" +
		"web: {listen: ':8080'}\ntags:\n  - {name: A, area: M, offset: 1, bit: 3, type: BOOL}\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Simulate, cfg.Web.Listen = true, ":9999" // command-line overrides must not be saved
	cfg.Tags = append(cfg.Tags, Tag{Name: "B", Area: "DB", DB: 3, Offset: 4, Type: "REAL", Deadband: 0.5})
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	back, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Simulate || back.Web.Listen != ":8080" || back.PollInterval != 250*time.Millisecond ||
		back.PLC.Slot != 2 || len(back.Tags) != 2 || back.Tags[0].Bit != 3 || back.Tags[1].Address() != "DB3.DBD4" {
		t.Errorf("round trip lost data: %+v", back)
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != orig {
		t.Error("original not kept as .bak")
	}
}

func TestWebEditAPI(t *testing.T) {
	cfg := &Config{Simulate: true, Tags: []Tag{{Name: "a", Area: "M", Offset: 0, Type: "INT"}}}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	mon := NewMonitor(cfg, nil, nil, "")
	h := NewWebHandler(mon, false)
	do := func(method, path, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do("POST", "/api/tags", `{"name":"b","area":"DB","db":2,"offset":4,"type":"REAL"}`); c != 200 {
		t.Errorf("add tag: %d", c)
	}
	if c := do("POST", "/api/tags", `{"name":"bad","area":"DB","db":0,"offset":0,"type":"INT"}`); c != 400 {
		t.Errorf("invalid tag accepted: %d", c)
	}
	if c := do("PUT", "/api/tags/a", `{"name":"a2","area":"Q","offset":1,"bit":2,"type":"BOOL"}`); c != 200 {
		t.Errorf("edit tag: %d", c)
	}
	if c := do("DELETE", "/api/tags/nope", ""); c != 404 {
		t.Errorf("delete missing: %d", c)
	}
	if c := do("PUT", "/api/network", `{"address":"10.1.1.1","rack":0,"slot":1,"connection_type":"op","timeout":"3s","poll_interval":"500ms","reconnect_delay":"1s","reconnect_max":"20s"}`); c != 200 {
		t.Errorf("network: %d", c)
	}
	if c := do("PUT", "/api/network", `{"address":"x","connection_type":"pg","timeout":"soon","poll_interval":"1s","reconnect_delay":"1s","reconnect_max":"1s"}`); c != 400 {
		t.Errorf("bad duration accepted: %d", c)
	}
	c := mon.Config()
	if len(c.Tags) != 2 || c.Tags[0].Address() != "Q1.2" || c.PLC.Address != "10.1.1.1" || c.PollInterval != 500*time.Millisecond {
		t.Errorf("config after edits: %+v", c)
	}
	// Whole list at once (paste / undo): order kept, all-or-nothing.
	if code := do("PUT", "/api/tags", `[{"name":"z","area":"M","offset":4,"type":"INT"},{"name":"a2","area":"Q","offset":1,"bit":2,"type":"BOOL"},{"name":"b","area":"DB","db":2,"offset":4,"type":"REAL"}]`); code != 200 {
		t.Errorf("replace list: %d", code)
	}
	if c := mon.Config(); len(c.Tags) != 3 || c.Tags[0].Name != "z" || c.Tags[2].Name != "b" {
		t.Errorf("list after replace: %+v", c.Tags)
	}
	if code := do("PUT", "/api/tags", `[{"name":"x","area":"M","type":"INT"},{"name":"x","area":"M","offset":2,"type":"INT"}]`); code != 400 {
		t.Errorf("duplicate names accepted: %d", code)
	}
	if code := do("PUT", "/api/tags", `[{"name":"h","area":"HR","type":"INT"}]`); code != 400 {
		t.Errorf("new Modbus tag accepted on S7: %d", code)
	}
	if c := mon.Config(); len(c.Tags) != 3 {
		t.Errorf("a refused list changed the tags: %+v", c.Tags)
	}

	// Cross-site form posts (no JSON content type) are refused.
	req := httptest.NewRequest("POST", "/api/tags", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain post: %d", rec.Code)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestInfluxSink(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" { // start-up reachability check
			return
		}
		if r.URL.Path != "/api/v2/write" || r.URL.Query().Get("bucket") != "plc" || r.Header.Get("Authorization") != "Token tok" {
			t.Errorf("bad request: %s %s auth=%q", r.Method, r.URL, r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		got <- string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tags := mustTags(t, []Tag{
		{Name: "Tank Level", Area: "DB", DB: 1, Offset: 4, Type: "REAL"},
		{Name: "Run", Area: "M", Offset: 0, Bit: 0, Type: "BOOL"},
	})
	s := NewInfluxSink(InfluxDBConfig{URL: srv.URL, Bucket: "plc", Token: "tok", Measurement: "s7", FlushInterval: time.Hour})
	ts := time.UnixMilli(1700000000000)
	s.Write([]Change{
		{Time: ts, Tag: &tags[0], New: 12.5, Good: true, Address: tags[0].Address()},
		{Time: ts, Tag: &tags[1], New: true, Good: true, Address: tags[1].Address()},
		{Time: ts, Tag: &tags[1], New: true, Good: false, Address: tags[1].Address()}, // skipped
	})
	s.Close() // flushes

	body := <-got
	want := "s7,tag=Tank\\ Level,address=DB1.DBD4,type=REAL value=12.5 1700000000000\n" +
		"s7,tag=Run,address=M0.0,type=BOOL value=1 1700000000000\n"
	if body != want {
		t.Errorf("line protocol:\n got %q\nwant %q", body, want)
	}
	if strings.Count(body, "\n") != 2 {
		t.Error("bad-quality point was written")
	}
}

// TestInfluxVersions checks the endpoint, query and authentication used for
// InfluxDB 1.x, 2.x and 3.x.
func TestInfluxVersions(t *testing.T) {
	tag := mustTags(t, []Tag{{Name: "T", Area: "M", Offset: 0, Type: "INT"}})[0]
	cases := []struct {
		cfg              InfluxDBConfig
		path, query, hdr string
		ping             string
	}{
		{InfluxDBConfig{Version: 1, Database: "plant", RetentionPolicy: "week", Username: "u", Password: "p"},
			"/write", "db=plant&precision=ms&rp=week", "Basic dTpw", "/ping"},
		{InfluxDBConfig{Version: 2, Org: "acme", Bucket: "plc", Token: "t2"},
			"/api/v2/write", "bucket=plc&org=acme&precision=ms", "Token t2", "/health"},
		{InfluxDBConfig{Version: 3, Database: "plant", Token: "t3"},
			"/api/v3/write_lp", "db=plant&precision=millisecond", "Bearer t3", "/health"},
	}
	for _, c := range cases {
		writes, pings := make(chan *http.Request, 1), make(chan string, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				pings <- r.URL.Path
				return
			}
			writes <- r
			w.WriteHeader(http.StatusNoContent)
		}))
		c.cfg.URL, c.cfg.Measurement, c.cfg.FlushInterval = srv.URL, "s7", time.Hour
		s := NewInfluxSink(c.cfg)
		s.Write([]Change{{Time: time.UnixMilli(1700000000000), Tag: &tag, New: int64(5), Good: true, Address: "MW0"}})
		s.Close()
		r := <-writes
		if r.URL.Path != c.path || r.URL.RawQuery != c.query || r.Header.Get("Authorization") != c.hdr {
			t.Errorf("v%d: %s?%s auth=%q", c.cfg.Version, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"))
		}
		select {
		case p := <-pings:
			if p != c.ping {
				t.Errorf("v%d health check on %s, want %s", c.cfg.Version, p, c.ping)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("v%d: no health check", c.cfg.Version)
		}
		srv.Close()
	}

	bad := []InfluxDBConfig{
		{Enabled: true, Version: 1, URL: "http://x"},              // no database
		{Enabled: true, Version: 2, URL: "http://x"},              // no bucket
		{Enabled: true, Version: 3, URL: "http://x"},              // no database
		{Enabled: true, Version: 4, URL: "http://x", Bucket: "b"}, // no such version
	}
	for _, ix := range bad {
		cfg := &Config{Simulate: true}
		cfg.Export.InfluxDB = ix
		cfg.applyDefaults()
		if err := cfg.validate(); err == nil {
			t.Errorf("accepted %+v", ix)
		}
	}
	old := &Config{Simulate: true}
	old.Export.InfluxDB = InfluxDBConfig{Enabled: true, URL: "http://x", Bucket: "b", Token: "t"} // pre-version config
	old.applyDefaults()
	if err := old.validate(); err != nil || old.Export.InfluxDB.Version != 2 {
		t.Errorf("old config: version %d, %v", old.Export.InfluxDB.Version, err)
	}
}
