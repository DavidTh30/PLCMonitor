package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestMonitor(t *testing.T, tags []Tag) (*Monitor, *Simulator) {
	t.Helper()
	cfg := &Config{Simulate: true, PollInterval: 50 * time.Millisecond, Tags: tags}
	cfg.fileSimulate = true
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	sim := NewSimulator(cfg.Tags, SimConfig{})
	mon := NewMonitor(cfg, func(*Config) Dialer { return sim.Dialer() }, nil, "")
	mon.OnApply = func(c *Config) { sim.SetTags(c.Tags) }
	return mon, sim
}

func TestFileMenu(t *testing.T) {
	dir := t.TempDir()
	mon, _ := newTestMonitor(t, []Tag{{Name: "a", Area: "M", Offset: 0, Type: "INT"}})
	if st, _ := mon.Snapshot(); st.NetworkDirty || st.TagsDirty {
		t.Fatal("fresh untitled config shows unsaved changes")
	}

	// Save without a file asks for Save as.
	if err := mon.FileSave(KindTags, ""); err == nil {
		t.Error("Save with no file succeeded")
	}
	tagPath := filepath.Join(dir, "line1.tags.yaml")
	if err := mon.FileSave(KindTags, tagPath); err != nil {
		t.Fatal(err)
	}
	if st, _ := mon.Snapshot(); st.TagsFile != tagPath || st.TagsDirty {
		t.Errorf("after Save as: file=%q dirty=%v", st.TagsFile, st.TagsDirty)
	}

	// An edit makes it dirty; Save clears it and writes the edit.
	mon.Update(func(c *Config) error {
		c.Tags = append(c.Tags, Tag{Name: "b", Area: "DB", DB: 2, Offset: 4, Type: "REAL"})
		return nil
	})
	if st, _ := mon.Snapshot(); !st.TagsDirty {
		t.Error("edit not shown as unsaved")
	}
	if err := mon.FileSave(KindTags, ""); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(tagPath); !strings.Contains(string(data), "name: b") {
		t.Errorf("saved tag file lacks the edit:\n%s", data)
	}

	// New empties the list; Open brings the saved one back.
	if err := mon.FileNew(KindTags); err != nil {
		t.Fatal(err)
	}
	if c := mon.Config(); len(c.Tags) != 0 || c.TagsFile != "" {
		t.Errorf("after New: %d tags, file %q", len(c.Tags), c.TagsFile)
	}
	if err := mon.FileOpen(KindTags, tagPath); err != nil {
		t.Fatal(err)
	}
	if c := mon.Config(); len(c.Tags) != 2 || c.Tags[1].Address() != "DB2.DBD4" {
		t.Errorf("after Open: %+v", c.Tags)
	}

	// Network: Save as, switch to Modbus, open the S7 file again.
	netPath := filepath.Join(dir, "plant.net.yaml")
	if err := mon.FileSave(KindNetwork, netPath); err != nil {
		t.Fatal(err)
	}
	err := mon.Update(func(c *Config) error {
		c.Protocol, c.Modbus.Address = ProtoModbus, "10.0.0.5"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	st, states := mon.Snapshot()
	if !st.NetworkDirty || st.Protocol != ProtoModbus {
		t.Errorf("protocol switch: dirty=%v protocol=%s", st.NetworkDirty, st.Protocol)
	}
	if !strings.Contains(states[0].Error, "not polled") {
		t.Errorf("S7 tag under Modbus not flagged: %q", states[0].Error)
	}
	if err := mon.FileOpen(KindNetwork, netPath); err != nil {
		t.Fatal(err)
	}
	if c := mon.Config(); c.Protocol != ProtoS7 || !c.Simulate {
		t.Errorf("network file not restored: %+v", c)
	}

	// Opening the wrong kind of file is refused and changes nothing.
	if err := mon.FileOpen(KindNetwork, tagPath); err == nil {
		t.Error("tag file accepted as network file")
	}
	if err := mon.FileOpen(KindTags, netPath); err == nil {
		t.Error("network file accepted as tag file")
	}
	if c := mon.Config(); len(c.Tags) != 2 {
		t.Error("failed open changed the tag list")
	}
}

func TestFileAPIAndHistory(t *testing.T) {
	dir := t.TempDir()
	mon, _ := newTestMonitor(t, []Tag{{Name: "lvl", Area: "DB", DB: 1, Offset: 0, Type: "REAL"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mon.Run(ctx)
	h := NewWebHandler(mon, false)

	call := func(method, path, body, remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	p := filepath.ToSlash(filepath.Join(dir, "x"))
	if rec := call("POST", "/api/file/tags/saveas", `{"path":"`+p+`"}`, "127.0.0.1:5000"); rec.Code != 200 {
		t.Fatalf("saveas: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.tags.yaml")); err != nil {
		t.Error("default extension not added")
	}
	if rec := call("POST", "/api/file/tags/open", `{"path":"`+p+`.tags.yaml"}`, "10.1.2.3:5000"); rec.Code != http.StatusForbidden {
		t.Errorf("remote file access allowed: %d", rec.Code)
	}
	// A Siemens tag table picked in File → Open is read by path, from this computer only.
	xmlPath := filepath.Join(dir, "PLCTags.xml")
	os.WriteFile(xmlPath, []byte(tiaTagtableXML), 0o644)
	tia := `{"path":"` + filepath.ToSlash(xmlPath) + `"}`
	if rec := call("POST", "/api/tags/tia", tia, "10.1.2.3:5000"); rec.Code != http.StatusForbidden {
		t.Errorf("remote tag table by path: %d", rec.Code)
	}
	if rec := call("POST", "/api/tags/tia", tia, "127.0.0.1:5000"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"Silo_Level_AI"`) {
		t.Errorf("tag table by path: %d %s", rec.Code, rec.Body)
	}
	if rec := call("POST", "/api/tags/tia", `{"path":"`+p+`.tags.yaml"}`, "127.0.0.1:5000"); rec.Code == 200 {
		t.Error("YAML file read as a tag table")
	}
	if rec := call("POST", "/api/file/dialog", `{"kind":"tags"}`, "127.0.0.1:5000"); rec.Code != http.StatusNotImplemented {
		t.Errorf("dialog without native support: %d", rec.Code)
	}

	waitFor(t, "history", func() bool {
		_, s, _ := mon.History("lvl", 0)
		return len(s) >= 3
	})
	rec := call("GET", "/api/history/lvl?since=0", "", "127.0.0.1:5000")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"points":[[`) {
		t.Errorf("history: %d %s", rec.Code, rec.Body)
	}
	if rec := call("GET", "/api/history/nope", "", "127.0.0.1:5000"); rec.Code != 404 {
		t.Errorf("history of unknown tag: %d", rec.Code)
	}
}

func TestExitAPI(t *testing.T) {
	mon, _ := newTestMonitor(t, nil)
	called := make(chan struct{}, 1)
	old := exitApp
	exitApp = func() { called <- struct{}{} }
	defer func() { exitApp = old }()
	h := NewWebHandler(mon, true) // read-only must not block Exit

	post := func(remote string) int {
		req := httptest.NewRequest("POST", "/api/exit", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := post("10.0.0.7:4000"); c != http.StatusForbidden {
		t.Errorf("remote exit: %d", c)
	}
	if c := post("[::1]:4000"); c != 200 {
		t.Fatalf("local exit: %d", c)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Error("exit was not triggered")
	}
}

// TestConnectDisconnect: the Disconnect button stops all traffic, Connect
// starts again at once.
func TestConnectDisconnect(t *testing.T) {
	mon, sim := newTestMonitor(t, []Tag{{Name: "a", Area: "M", Offset: 0, Type: "INT"}})
	go sim.Run(t.Context())
	go mon.Run(t.Context())
	waitFor(t, "connected", func() bool { st, _ := mon.Snapshot(); return st.Connected })
	h := NewWebHandler(mon, false)
	post := func(body string) int {
		req := httptest.NewRequest("POST", "/api/connection", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := post(`{"connect":false}`); c != 200 {
		t.Fatalf("disconnect: %d", c)
	}
	waitFor(t, "disconnected", func() bool { st, _ := mon.Snapshot(); return !st.Connected && st.Paused })
	st, tags := mon.Snapshot()
	polls := st.Polls
	if st.LastError != "" || tags[0].Good {
		t.Errorf("after disconnect: error %q, tag good %v", st.LastError, tags[0].Good)
	}
	time.Sleep(300 * time.Millisecond) // poll interval is 50 ms: no polls may happen now
	if st, _ := mon.Snapshot(); st.Polls != polls || st.Reconnects != 0 {
		t.Errorf("traffic while disconnected: polls %d→%d, reconnects %d", polls, st.Polls, st.Reconnects)
	}
	if err := mon.Write("a", int64(5)); err == nil {
		t.Error("write accepted while disconnected")
	}
	if c := post(`{"connect":true}`); c != 200 {
		t.Fatalf("connect: %d", c)
	}
	waitFor(t, "connected again", func() bool { st, tags := mon.Snapshot(); return st.Connected && !st.Paused && tags[0].Good })
	if st, _ := mon.Snapshot(); st.Reconnects != 0 {
		t.Errorf("a deliberate connect counted as a reconnect: %d", st.Reconnects)
	}
	if c := NewWebHandler(mon, true); c != nil {
		req := httptest.NewRequest("POST", "/api/connection", strings.NewReader(`{"connect":false}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("disconnect on a read-only dashboard: %d", rec.Code)
		}
	}
}

// The sample config is embedded and written on first run (with the simulator
// on), so it must load and come with example tags.
func TestSampleConfigLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := ensureConfig(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulator off and no address: nothing is contacted until the user
	// enters the PLC address (or turns the simulator on).
	if cfg.Protocol != ProtoS7 || cfg.Modbus.Mode != "tcp" || len(cfg.Tags) != 15 || cfg.Simulate ||
		cfg.PLC.Address != "" || cfg.Export.InfluxDB.Version != 2 || !strings.Contains(missingTarget(cfg), "no PLC address") {
		t.Errorf("sample config: protocol %q, modbus mode %q, %d tags, simulate %v, address %q",
			cfg.Protocol, cfg.Modbus.Mode, len(cfg.Tags), cfg.Simulate, cfg.PLC.Address)
	}
	// File → Network configuration → New: also simulator off.
	mon, _ := newTestMonitor(t, nil)
	if err := mon.FileNew(KindNetwork); err != nil || mon.Config().Simulate {
		t.Errorf("new network: simulate=%v err=%v", mon.Config().Simulate, err)
	}
}

func TestHistoryRing(t *testing.T) {
	h := newHistory()
	for i := 0; i < historySize+10; i++ {
		h.add(Sample{int64(i), float64(i)})
	}
	all := h.since(-1)
	if len(all) != historySize || all[0].T != 10 || all[len(all)-1].T != historySize+9 {
		t.Errorf("ring: len %d first %d", len(all), all[0].T)
	}
	if got := h.since(historySize + 5); len(got) != 4 {
		t.Errorf("since: %d samples", len(got))
	}
}
