package main

import (
	"bufio"
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TrendStore records every tag's values to disk for the trend charts.
//
// Layout: <path>/<YYYY-MM-DD>/<tag>.csv, one file per tag per local day,
// with lines "unix_ms,local time,value". An empty value marks a gap (no good
// value: link down, read error). The files open directly in Excel.
//
// To keep files small, a tag is written at most once per Interval, and only
// when its value changed, or Heartbeat has passed since the last line.
type TrendStore struct {
	cfg HistoryConfig
	ch  chan trendSample

	mu    sync.Mutex            // guards files (writer goroutine vs. readers)
	files map[string]*trendFile // by day/file name
	last  map[string]trendLast  // by tag; writer goroutine only

	dropped int
	done    chan struct{}
}

type trendSample struct {
	tag string
	t   time.Time
	v   float64 // NaN = gap
}

type trendLast struct {
	t   time.Time
	v   float64
	gap bool
}

type trendFile struct {
	f    *os.File
	w    *bufio.Writer
	used time.Time
}

const dayLayout = "2006-01-02"

func NewTrendStore(cfg HistoryConfig) (*TrendStore, error) {
	if err := os.MkdirAll(cfg.Path, 0o755); err != nil {
		return nil, fmt.Errorf("history folder: %w", err)
	}
	return &TrendStore{
		cfg: cfg, ch: make(chan trendSample, 8192),
		files: map[string]*trendFile{}, last: map[string]trendLast{}, done: make(chan struct{}),
	}, nil
}

// Record queues one polled value (NaN for "no good value"). It never blocks
// polling: if the disk can't keep up, samples are dropped and counted.
func (s *TrendStore) Record(tag string, t time.Time, v float64) {
	select {
	case s.ch <- trendSample{tag, t, v}:
	default:
		s.mu.Lock()
		s.dropped++
		s.mu.Unlock()
	}
}

// Run writes queued samples until ctx is cancelled, then flushes and closes.
func (s *TrendStore) Run(ctx context.Context) {
	defer close(s.done)
	flush := time.NewTicker(time.Second)
	defer flush.Stop()
	housekeeping := time.NewTicker(10 * time.Minute)
	defer housekeeping.Stop()
	s.cleanup()
	for {
		select {
		case <-ctx.Done():
			// Drain what is queued, then close every file.
			for {
				select {
				case smp := <-s.ch:
					s.handle(smp)
					continue
				default:
				}
				break
			}
			s.mu.Lock()
			for k, f := range s.files {
				f.w.Flush()
				f.f.Close()
				delete(s.files, k)
			}
			s.mu.Unlock()
			return
		case smp := <-s.ch:
			s.handle(smp)
		case <-flush.C:
			s.mu.Lock()
			for _, f := range s.files {
				f.w.Flush()
			}
			if s.dropped > 0 {
				log.Printf("history: disk too slow, %d sample(s) not recorded", s.dropped)
				s.dropped = 0
			}
			s.mu.Unlock()
		case now := <-housekeeping.C:
			s.closeIdle(now)
			s.cleanup()
		}
	}
}

// Wait blocks until Run has flushed and closed all files.
func (s *TrendStore) Wait() { <-s.done }

func (s *TrendStore) handle(smp trendSample) {
	gap := math.IsNaN(smp.v)
	lw, seen := s.last[smp.tag]
	switch {
	case gap && seen && lw.gap:
		return // already marked
	case gap, !seen, lw.gap:
		// always write gaps and the first good value after one
	case smp.t.Sub(lw.t) < s.cfg.Interval:
		return
	case smp.v == lw.v && smp.t.Sub(lw.t) < s.cfg.Heartbeat:
		return
	}
	s.last[smp.tag] = trendLast{t: smp.t, v: smp.v, gap: gap}
	if !seen && !gap {
		// First value since PLC Monitor started: mark the time it wasn't
		// running as a gap, so a chart doesn't draw a line across it.
		s.writeLine(smp.tag, smp.t.Add(-time.Millisecond), math.NaN())
	}
	s.writeLine(smp.tag, smp.t, smp.v)
}

func (s *TrendStore) writeLine(tag string, t time.Time, v float64) {
	smp := trendSample{tag, t, v}
	gap := math.IsNaN(v)
	day := smp.t.Format(dayLayout)
	key := day + "/" + trendFileName(smp.tag)
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.files[key]
	if f == nil {
		dir := filepath.Join(s.cfg.Path, day)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("history: %v", err)
			return
		}
		path := filepath.Join(dir, trendFileName(smp.tag))
		fh, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			log.Printf("history: %v", err)
			return
		}
		f = &trendFile{f: fh, w: bufio.NewWriterSize(fh, 16<<10)}
		if st, err := fh.Stat(); err == nil && st.Size() == 0 {
			fmt.Fprintf(f.w, "unix_ms,time,%s\n", csvField(smp.tag))
		}
		s.files[key] = f
	}
	f.used = smp.t
	val := ""
	if !gap {
		val = strconv.FormatFloat(smp.v, 'g', -1, 64)
	}
	fmt.Fprintf(f.w, "%d,%s,%s\n", smp.t.UnixMilli(), smp.t.Format("2006-01-02 15:04:05.000"), val)
}

func csvField(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// closeIdle closes files not written for a while (e.g. yesterday's).
func (s *TrendStore) closeIdle(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, f := range s.files {
		if now.Sub(f.used) > 10*time.Minute {
			f.w.Flush()
			f.f.Close()
			delete(s.files, k)
		}
	}
}

// cleanup deletes day folders older than KeepDays.
func (s *TrendStore) cleanup() {
	if s.cfg.KeepDays < 0 {
		return
	}
	entries, err := os.ReadDir(s.cfg.Path)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -s.cfg.KeepDays).Format(dayLayout)
	for _, e := range entries {
		if _, err := time.Parse(dayLayout, e.Name()); err != nil || !e.IsDir() {
			continue // not ours
		}
		if e.Name() < cutoff {
			if err := os.RemoveAll(filepath.Join(s.cfg.Path, e.Name())); err == nil {
				log.Printf("history: removed %s (older than %d days)", e.Name(), s.cfg.KeepDays)
			}
		}
	}
}

// trendFileName makes a file name from a tag name. Names that need
// changing get a short hash so two tags can't share a file.
func trendFileName(tag string) string {
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, tag)
	if safe != tag || strings.Trim(safe, ".") == "" {
		safe = fmt.Sprintf("%s~%08x", safe, crc32.ChecksumIEEE([]byte(tag)))
	}
	return safe + ".csv"
}

// Read returns a tag's recorded samples in [from, to], oldest first, plus
// the last sample before from (so a chart line starts at the left edge).
func (s *TrendStore) Read(tag string, from, to time.Time) ([]Sample, error) {
	name := trendFileName(tag)
	s.mu.Lock() // make sure everything written so far is on disk
	for k, f := range s.files {
		if strings.HasSuffix(k, "/"+name) {
			f.w.Flush()
		}
	}
	s.mu.Unlock()

	var out []Sample
	var before *Sample
	fromMs, toMs := from.UnixMilli(), to.UnixMilli()
	// One day earlier too, for the sample just before from.
	for d := truncDay(from).AddDate(0, 0, -1); !d.After(to); d = d.AddDate(0, 0, 1) {
		err := readTrendFile(filepath.Join(s.cfg.Path, d.Format(dayLayout), name), func(smp Sample) {
			switch {
			case smp.T < fromMs:
				before = &smp
			case smp.T <= toMs:
				out = append(out, smp)
			}
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	if before != nil {
		out = append([]Sample{*before}, out...)
	}
	return out, nil
}

func truncDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func readTrendFile(path string, fn func(Sample)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		i := strings.IndexByte(line, ',')
		j := strings.LastIndexByte(line, ',')
		if i <= 0 || j <= i {
			continue
		}
		ms, err := strconv.ParseInt(line[:i], 10, 64)
		if err != nil {
			continue // header
		}
		v := math.NaN()
		if val := line[j+1:]; val != "" {
			if v, err = strconv.ParseFloat(val, 64); err != nil {
				continue
			}
		}
		fn(Sample{T: ms, V: v})
	}
	return sc.Err()
}

// Downsample reduces samples to about 2*n points for a chart by keeping the
// minimum and maximum of each of n time buckets (so spikes stay visible) and
// any gap.
func Downsample(samples []Sample, from, to int64, n int) []Sample {
	if n <= 0 || len(samples) <= 2*n || to <= from {
		return samples
	}
	width := float64(to-from) / float64(n)
	var out []Sample
	for i := 0; i < len(samples); {
		b := int(float64(samples[i].T-from) / width)
		var lo, hi, gap *Sample
		for ; i < len(samples) && int(float64(samples[i].T-from)/width) == b; i++ {
			smp := &samples[i]
			switch {
			case math.IsNaN(smp.V):
				if gap == nil {
					gap = smp
				}
			case lo == nil:
				lo, hi = smp, smp
			case smp.V < lo.V:
				lo = smp
			case smp.V > hi.V:
				hi = smp
			}
		}
		pts := []*Sample{}
		for _, p := range []*Sample{lo, hi, gap} {
			if p != nil && (len(pts) == 0 || pts[len(pts)-1] != p) {
				pts = append(pts, p)
			}
		}
		sort.Slice(pts, func(a, b int) bool { return pts[a].T < pts[b].T })
		for _, p := range pts {
			out = append(out, *p)
		}
	}
	return out
}

// ExportCSV writes a tag's samples in [from, to] as CSV (local time, value).
// ExportCSVMulti writes several tags side by side (a combined chart): one
// row per moment any of them changed, each column holding that tag's value
// at that moment (the last one recorded, empty if none yet or no data).
func (s *TrendStore) ExportCSVMulti(w io.Writer, tags []string, from, to time.Time) error {
	if len(tags) == 1 {
		return s.ExportCSV(w, tags[0], from, to)
	}
	all := make([][]Sample, len(tags))
	var times []int64
	for i, tag := range tags {
		samples, err := s.Read(tag, from, to)
		if err != nil {
			return err
		}
		all[i] = samples
		hadValue := false
		for _, smp := range samples {
			nan := math.IsNaN(smp.V)
			// A gap before the tag's first value (the start-of-recording
			// marker) would only add an empty row.
			if smp.T >= from.UnixMilli() && (!nan || hadValue) {
				times = append(times, smp.T)
			}
			hadValue = hadValue || !nan
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	bw := bufio.NewWriter(w)
	bw.WriteString("time")
	for _, tag := range tags {
		bw.WriteString("," + csvField(tag))
	}
	bw.WriteString("\n")
	pos := make([]int, len(tags)) // next sample per tag
	cur := make([]string, len(tags))
	for i, t := range times {
		if i > 0 && t == times[i-1] {
			continue
		}
		for k := range tags {
			for pos[k] < len(all[k]) && all[k][pos[k]].T <= t {
				v := all[k][pos[k]].V
				cur[k] = ""
				if !math.IsNaN(v) {
					cur[k] = strconv.FormatFloat(v, 'g', -1, 64)
				}
				pos[k]++
			}
		}
		fmt.Fprintf(bw, "%s,%s\n", time.UnixMilli(t).Format("2006-01-02 15:04:05.000"), strings.Join(cur, ","))
	}
	return bw.Flush()
}

func (s *TrendStore) ExportCSV(w io.Writer, tag string, from, to time.Time) error {
	samples, err := s.Read(tag, from, to)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "time,%s\n", csvField(tag))
	for _, smp := range samples {
		if smp.T < from.UnixMilli() {
			continue
		}
		val := ""
		if !math.IsNaN(smp.V) {
			val = strconv.FormatFloat(smp.V, 'g', -1, 64)
		}
		fmt.Fprintf(bw, "%s,%s\n", time.UnixMilli(smp.T).Format("2006-01-02 15:04:05.000"), val)
	}
	return bw.Flush()
}
