package main

import (
	"context"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestTrend(t *testing.T) (*TrendStore, func()) {
	t.Helper()
	cfg := HistoryConfig{Path: t.TempDir(), Interval: time.Second, Heartbeat: time.Minute, KeepDays: 30}
	s, err := NewTrendStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	return s, func() { cancel(); s.Wait() }
}

func TestTrendRecordAndRead(t *testing.T) {
	s, stop := newTestTrend(t)
	base := time.Date(2026, 11, 19, 23, 59, 0, 0, time.Local)
	at := func(sec float64) time.Time { return base.Add(time.Duration(sec * float64(time.Second))) }

	s.Record("Tank Level", at(0), 1)           // written (first)
	s.Record("Tank Level", at(0.5), 2)         // skipped: within 1 s interval
	s.Record("Tank Level", at(1.2), 2)         // written: changed, interval passed
	s.Record("Tank Level", at(5), 2)           // skipped: unchanged, heartbeat not due
	s.Record("Tank Level", at(10), math.NaN()) // gap: written
	s.Record("Tank Level", at(11), math.NaN()) // gap again: skipped
	s.Record("Tank Level", at(11.5), 3)        // first value after a gap: written
	s.Record("Tank Level", at(75), 3)          // unchanged but heartbeat (60 s) due: written, next day
	stop()

	day1 := filepath.Join(s.cfg.Path, "2026-11-19", trendFileName("Tank Level"))
	data, err := os.ReadFile(day1)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// header, start-of-recording gap, 1, 2, gap, 3
	if !strings.HasPrefix(lines[0], "unix_ms,time,Tank Level") || len(lines) != 1+5 {
		t.Fatalf("day file:\n%s", data)
	}
	if !strings.HasSuffix(lines[1], ",") || !strings.HasSuffix(lines[4], ",") { // gap lines have an empty value
		t.Errorf("gap lines %q %q", lines[1], lines[4])
	}
	if _, err := os.Stat(filepath.Join(s.cfg.Path, "2026-11-20", trendFileName("Tank Level"))); err != nil {
		t.Errorf("sample after midnight not in the next day's file: %v", err)
	}

	// Read across midnight, starting after the first sample: the sample just
	// before "from" is included so a chart line starts at the edge.
	got, err := s.Read("Tank Level", at(1), at(80))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got[0].V != 1 || !math.IsNaN(got[2].V) || got[4].V != 3 {
		t.Errorf("read: %+v", got)
	}
	if got, _ := s.Read("Nope", at(0), at(80)); len(got) != 0 {
		t.Error("unknown tag returned data")
	}

	var csv strings.Builder
	if err := s.ExportCSV(&csv, "Tank Level", at(0), at(80)); err != nil {
		t.Fatal(err)
	}
	if out := csv.String(); !strings.HasPrefix(out, "time,Tank Level\n2026-11-19 23:59:00.000,1\n") || strings.Count(out, "\n") != 6 {
		// the start-of-recording gap is 1 ms before "from", so not exported
		t.Errorf("csv export:\n%s", out)
	}
}

// TestTrendExportMulti: several tags side by side, each column carrying its
// last value forward.
func TestTrendExportMulti(t *testing.T) {
	s, stop := newTestTrend(t)
	base := time.Date(2026, 11, 19, 10, 0, 0, 0, time.Local)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	s.Record("A", at(0), 1)
	s.Record("B", at(2), 10)
	s.Record("A", at(4), 2)
	s.Record("B", at(4), 20)
	stop()
	var out strings.Builder
	if err := s.ExportCSVMulti(&out, []string{"A", "B"}, at(0), at(10)); err != nil {
		t.Fatal(err)
	}
	want := "time,A,B\n" +
		"2026-11-19 10:00:00.000,1,\n" +
		"2026-11-19 10:00:02.000,1,10\n" +
		"2026-11-19 10:00:04.000,2,20\n"
	if out.String() != want {
		t.Errorf("multi export:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestTrendFileNamesAndCleanup(t *testing.T) {
	if n := trendFileName("Motor1_Speed"); n != "Motor1_Speed.csv" {
		t.Errorf("plain name changed: %s", n)
	}
	a, b := trendFileName("Tank/Level"), trendFileName("Tank:Level")
	if a == b || strings.ContainsAny(a, `/\:`) {
		t.Errorf("unsafe names not made unique and safe: %s %s", a, b)
	}

	dir := t.TempDir()
	old := time.Now().AddDate(0, 0, -40).Format(dayLayout)
	recent := time.Now().AddDate(0, 0, -2).Format(dayLayout)
	for _, d := range []string{old, recent, "not-a-day"} {
		os.MkdirAll(filepath.Join(dir, d), 0o755)
	}
	s := &TrendStore{cfg: HistoryConfig{Path: dir, KeepDays: 30}}
	s.cleanup()
	for d, want := range map[string]bool{old: false, recent: true, "not-a-day": true} {
		if _, err := os.Stat(filepath.Join(dir, d)); (err == nil) != want {
			t.Errorf("%s: exists=%v, want %v", d, err == nil, want)
		}
	}
}

func TestDownsample(t *testing.T) {
	var in []Sample
	for i := 0; i < 10000; i++ {
		v := math.Sin(float64(i) / 100)
		if i == 5000 {
			v = 50 // a spike
		}
		if i == 7000 {
			v = math.NaN() // a gap
		}
		in = append(in, Sample{int64(i), v})
	}
	out := Downsample(in, 0, 10000, 100)
	if len(out) > 300 || len(out) < 100 {
		t.Errorf("downsampled to %d points", len(out))
	}
	spike, gap := false, false
	for i, p := range out {
		if p.V == 50 {
			spike = true
		}
		if math.IsNaN(p.V) {
			gap = true
		}
		if i > 0 && p.T < out[i-1].T {
			t.Fatal("output not in time order")
		}
	}
	if !spike || !gap {
		t.Errorf("spike kept=%v gap kept=%v", spike, gap)
	}
	if small := Downsample(in[:50], 0, 50, 100); len(small) != 50 {
		t.Error("short input changed")
	}
}

// TestTrendThroughMonitor: values polled from the simulator end up in the
// files and come back through the chart API and the CSV download.
func TestTrendThroughMonitor(t *testing.T) {
	trend, stopTrend := newTestTrend(t)
	defer stopTrend()
	trend.cfg.Interval = 10 * time.Millisecond
	cfg := &Config{Simulate: true, PollInterval: 20 * time.Millisecond, Tags: []Tag{{Name: "level", Area: "DB", DB: 1, Offset: 0, Type: "REAL"}}}
	cfg.applyDefaults()
	cfg.validate()
	sim := NewSimulator(cfg.Tags, SimConfig{})
	mon := NewMonitor(cfg, func(*Config) Dialer { return sim.Dialer() }, nil, "")
	mon.Trend = trend
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sim.Run(ctx) // values change every 100 ms
	go mon.Run(ctx)
	start := time.Now()
	time.Sleep(500 * time.Millisecond)

	h := NewWebHandler(mon, false)
	q := "?from=" + itoa(start.Add(-time.Second).UnixMilli()) + "&to=" + itoa(time.Now().UnixMilli())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/trend/level"+q+"&max=500", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"files":true`) || strings.Count(rec.Body.String(), "],[") < 3 {
		t.Errorf("trend API: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/trend/level/csv"+q, nil))
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "time,level\n") ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("csv download: %d %q", rec.Code, rec.Body.String()[:min(80, rec.Body.Len())])
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/trend/level?from=5&to=1", nil))
	if rec.Code != 400 {
		t.Errorf("bad range accepted: %d", rec.Code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
