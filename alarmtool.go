package main

// Alarm messages: alarm texts that come and go with tag values – a Bool
// that is TRUE (or FALSE), a bit of an alarm word, an analog value above /
// below a limit or outside a range – with a class (fault, alarm, warning,
// info) and acknowledgement, as an HMI shows them. The window
// (web/alarms.html) edits them and runs them against live tags or
// simulated values; this file keeps the files (.plcalarm, CSV, Excel).

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// AlarmSet is a .plcalarm file.
type AlarmSet struct {
	Format  string     `json:"format"` // plcmonitor-alarms
	Version int        `json:"version"`
	Name    string     `json:"name"`
	Alarms  []AlarmDef `json:"alarms"`
}

// AlarmDef is one alarm message.
type AlarmDef struct {
	ID      int          `json:"id"`
	Class   string       `json:"class"` // fault, alarm, warning, info
	Group   string       `json:"group,omitempty"`
	Tag     string       `json:"tag"`
	Trigger AlarmTrigger `json:"trigger"`
	Text    string       `json:"text"` // {value} {tag} {limit} {min} {max} are filled in
	Ack     bool         `json:"ack"`  // must be acknowledged
}

// AlarmTrigger: true, false, bit (bit n set), gt / ge / lt / le / eq / ne
// (against Value), outside / inside (Min … Max). DelayMs: how long the
// condition must hold before the alarm comes.
type AlarmTrigger struct {
	Type    string  `json:"type"`
	Bit     int     `json:"bit,omitempty"`
	Value   float64 `json:"value,omitempty"`
	Min     float64 `json:"min,omitempty"`
	Max     float64 `json:"max,omitempty"`
	DelayMs int     `json:"delay_ms,omitempty"`
}

const alarmFormat = "plcmonitor-alarms"

var alarmClasses = map[string]bool{"fault": true, "alarm": true, "warning": true, "info": true}

func (t AlarmTrigger) String() string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	switch t.Type {
	case "true":
		return "TRUE"
	case "false":
		return "FALSE"
	case "bit":
		return "bit " + strconv.Itoa(t.Bit)
	case "gt":
		return "> " + f(t.Value)
	case "ge":
		return ">= " + f(t.Value)
	case "lt":
		return "< " + f(t.Value)
	case "le":
		return "<= " + f(t.Value)
	case "eq":
		return "= " + f(t.Value)
	case "ne":
		return "<> " + f(t.Value)
	case "outside":
		return "outside " + f(t.Min) + ".." + f(t.Max)
	case "inside":
		return "inside " + f(t.Min) + ".." + f(t.Max)
	}
	return t.Type
}

var (
	condOpRe    = regexp.MustCompile(`^(>=|<=|<>|!=|>|<|=)\s*(-?[\d.]+(?:[eE][-+]?\d+)?)$`)
	condRangeRe = regexp.MustCompile(`(?i)^(outside|inside|out|in)?\s*\(?\s*(-?[\d.]+)\s*(?:\.\.|…|-|to)\s*(-?[\d.]+)\s*\)?$`)
	condBitRe   = regexp.MustCompile(`(?i)^(?:bit|x)\s*\.?\s*(\d+)$`)
)

// parseAlarmCondition reads a condition as written in a sheet: TRUE, FALSE,
// bit 3, > 80, <= 10, = 5, <> 0, outside 10..90, inside 20..30.
func parseAlarmCondition(s string) (AlarmTrigger, error) {
	s = strings.TrimSpace(s)
	u := strings.ToLower(s)
	switch u {
	case "", "true", "1", "on", "high", "set":
		return AlarmTrigger{Type: "true"}, nil
	case "false", "0", "off", "low", "reset":
		return AlarmTrigger{Type: "false"}, nil
	}
	if m := condBitRe.FindStringSubmatch(s); m != nil {
		b, _ := strconv.Atoi(m[1])
		return AlarmTrigger{Type: "bit", Bit: b}, nil
	}
	if m := condOpRe.FindStringSubmatch(s); m != nil {
		v, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			return AlarmTrigger{}, err
		}
		t := map[string]string{">": "gt", ">=": "ge", "<": "lt", "<=": "le", "=": "eq", "<>": "ne", "!=": "ne"}[m[1]]
		return AlarmTrigger{Type: t, Value: v}, nil
	}
	if m := condRangeRe.FindStringSubmatch(s); m != nil {
		a, _ := strconv.ParseFloat(m[2], 64)
		b, _ := strconv.ParseFloat(m[3], 64)
		t := "outside"
		if strings.HasPrefix(strings.ToLower(m[1]), "in") {
			t = "inside"
		}
		return AlarmTrigger{Type: t, Min: math2min(a, b), Max: math2max(a, b)}, nil
	}
	return AlarmTrigger{}, fmt.Errorf("condition %q not understood (TRUE, FALSE, bit 3, > 80, < 10, outside 10..90 …)", s)
}

func math2min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func math2max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// check validates a set; alarms without an ID get the next free one.
func (s *AlarmSet) check() error {
	if s.Format != "" && s.Format != alarmFormat {
		return fmt.Errorf("not an alarm messages file (format %q)", s.Format)
	}
	s.Format, s.Version = alarmFormat, 1
	if len(s.Alarms) > 10000 {
		return errors.New("at most 10000 alarms")
	}
	used := map[int]bool{}
	next := 1
	for i := range s.Alarms {
		a := &s.Alarms[i]
		if a.ID <= 0 {
			for used[next] {
				next++
			}
			a.ID = next
		}
		if used[a.ID] {
			return fmt.Errorf("alarm number %d is used twice", a.ID)
		}
		used[a.ID] = true
		a.Class = strings.ToLower(strings.TrimSpace(a.Class))
		if a.Class == "" {
			a.Class = "alarm"
		}
		if !alarmClasses[a.Class] {
			return fmt.Errorf("alarm %d: class %q (fault, alarm, warning or info)", a.ID, a.Class)
		}
		switch a.Trigger.Type {
		case "true", "false", "bit", "gt", "ge", "lt", "le", "eq", "ne", "outside", "inside":
		case "":
			a.Trigger.Type = "true"
		default:
			return fmt.Errorf("alarm %d: trigger %q", a.ID, a.Trigger.Type)
		}
		if a.Trigger.Bit < 0 || a.Trigger.Bit > 63 || a.Trigger.DelayMs < 0 || a.Trigger.DelayMs > 3600000 {
			return fmt.Errorf("alarm %d: bit 0…63, delay 0…3600000 ms", a.ID)
		}
		if len(a.Text) > 1000 || len(a.Tag) > 300 || len(a.Group) > 100 {
			return fmt.Errorf("alarm %d: text, tag or group too long", a.ID)
		}
	}
	return nil
}

// alarmRows are the alarms as sheet rows (export), with a heading.
func alarmRows(s AlarmSet) [][]string {
	rows := [][]string{{"ID", "Class", "Group", "Tag", "Condition", "Delay ms", "Ack", "Text"}}
	for _, a := range s.Alarms {
		ack := "no"
		if a.Ack {
			ack = "yes"
		}
		rows = append(rows, []string{strconv.Itoa(a.ID), a.Class, a.Group, a.Tag, a.Trigger.String(), strconv.Itoa(a.Trigger.DelayMs), ack, a.Text})
	}
	return rows
}

// ExportAlarms writes the set as csv, xlsx or plcalarm.
func ExportAlarms(s AlarmSet, format string) (name string, data []byte, err error) {
	if err := s.check(); err != nil {
		return "", nil, err
	}
	base := fileSafe(s.Name, "alarms")
	switch format {
	case "csv":
		var b bytes.Buffer
		b.WriteString("\xEF\xBB\xBF")
		w := csv.NewWriter(&b)
		w.UseCRLF = true
		w.WriteAll(alarmRows(s))
		return base + ".csv", b.Bytes(), w.Error()
	case "xlsx":
		r := alarmRows(s)
		rows := make([][]any, len(r)-1)
		for i, x := range r[1:] {
			id, _ := strconv.Atoi(x[0])
			delay, _ := strconv.Atoi(x[5])
			rows[i] = []any{id, x[1], x[2], x[3], x[4], delay, x[6], x[7]}
		}
		data, err := xlsxWorkbook("Alarms", r[0], rows, []float64{7, 9, 12, 28, 16, 9, 6, 60})
		return base + ".xlsx", data, err
	case "plcalarm", "":
		data, _ := json.MarshalIndent(s, "", "  ")
		return base + ".plcalarm", data, nil
	}
	return "", nil, fmt.Errorf("format must be csv, xlsx or plcalarm")
}

// ImportAlarms reads a .plcalarm (JSON) or an alarm list as a sheet (.csv,
// .tsv, .xlsx, .xls) with the columns ID, Class, Tag, Condition (or Bit /
// Limit), Text, … as Export writes them.
func ImportAlarms(name string, data []byte) (AlarmSet, error) {
	var s AlarmSet
	switch strings.ToLower(filepath.Ext(name)) {
	case ".plcalarm", ".json":
		if err := json.Unmarshal(data, &s); err != nil {
			return s, fmt.Errorf("not an alarm messages file: %w", err)
		}
		return s, s.check()
	}
	rows, err := sheetRows(name, data)
	if err != nil {
		return s, err
	}
	if rows == nil {
		return s, errors.New("open a .plcalarm file or an alarm list (.csv, .xlsx, .xls)")
	}
	col := map[string]int{}
	head := -1
	for i := 0; i < len(rows) && i < 20 && head < 0; i++ {
		c := map[string]int{}
		for j, h := range rows[i] {
			k := strings.NewReplacer(" ", "", "_", "", "-", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(h)))
			key := map[string]string{"id": "id", "no": "id", "number": "id", "nr": "id", "alarmno": "id", "alarmnumber": "id",
				"class": "class", "type": "class", "category": "class", "severity": "class", "priority": "class",
				"group": "group", "area": "group",
				"tag": "tag", "triggertag": "tag", "variable": "tag", "address": "tag",
				"condition": "cond", "trigger": "cond", "when": "cond",
				"bit": "bit", "triggerbit": "bit", "limit": "limit", "value": "limit", "setpoint": "limit",
				"min": "min", "low": "min", "max": "max", "high": "max", "delay": "delay", "delayms": "delay",
				"ack": "ack", "acknowledge": "ack", "text": "text", "message": "text", "alarmtext": "text", "description": "text"}[k]
			if _, dup := c[key]; key != "" && !dup {
				c[key] = j
			}
		}
		_, t := c["tag"]
		_, x := c["text"]
		if t && x {
			col, head = c, i
		}
	}
	if head < 0 {
		return s, errors.New("an alarm list needs the columns Tag and Text (and Condition, Class, ID …)")
	}
	cell := func(r []string, k string) string {
		if j, ok := col[k]; ok && j < len(r) {
			return strings.TrimSpace(r[j])
		}
		return ""
	}
	s.Name = strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	for i, r := range rows[head+1:] {
		tag, text := cell(r, "tag"), cell(r, "text")
		if tag == "" && text == "" {
			continue
		}
		a := AlarmDef{Tag: tag, Text: text, Group: cell(r, "group"), Ack: true}
		a.ID, _ = strconv.Atoi(cell(r, "id"))
		a.Class = strings.ToLower(cell(r, "class"))
		switch a.Class {
		case "error", "critical", "1":
			a.Class = "fault"
		case "warn", "2":
			a.Class = "warning"
		case "information", "message", "3":
			a.Class = "info"
		case "", "alarm", "fault", "warning", "info":
		default:
			a.Class = "alarm"
		}
		switch strings.ToLower(cell(r, "ack")) {
		case "no", "false", "0", "n":
			a.Ack = false
		}
		cond := cell(r, "cond")
		if b := cell(r, "bit"); cond == "" && b != "" {
			cond = "bit " + b
		}
		if l := cell(r, "limit"); cond == "" && l != "" {
			cond = "> " + l
		} else if l != "" && condOpRe.FindStringSubmatch(cond) == nil && regexp.MustCompile(`^(>=|<=|<>|!=|>|<|=)$`).MatchString(cond) {
			cond += " " + l // Condition ">" and Limit "80"
		}
		if mn, mx := cell(r, "min"), cell(r, "max"); cond == "" && mn != "" && mx != "" {
			cond = "outside " + mn + ".." + mx
		}
		t, err := parseAlarmCondition(cond)
		if err != nil {
			return s, fmt.Errorf("row %d: %w", head+2+i, err)
		}
		t.DelayMs, _ = strconv.Atoi(cell(r, "delay"))
		a.Trigger = t
		s.Alarms = append(s.Alarms, a)
	}
	sort.SliceStable(s.Alarms, func(i, j int) bool {
		return s.Alarms[i].ID > 0 && (s.Alarms[j].ID == 0 || s.Alarms[i].ID < s.Alarms[j].ID)
	})
	return s, s.check()
}

func alarmAutosavePath() string {
	dir := os.Getenv("LOCALAPPDATA")
	if dir == "" {
		dir, _ = os.UserConfigDir()
	}
	return filepath.Join(dir, "PLCMonitor", "alarms-autosave.plcalarm")
}

// alarmRoutes: the Alarm messages window's API (this computer only).
//
//	POST /api/alarmtool/open                  → {path, set} (Open dialog; 501 without one)
//	POST /api/alarmtool/save {set, path, as}  → {path}
//	POST /api/alarmtool/import {name, data}   → {set}
//	POST /api/alarmtool/export {set, format}  → {name, data} (csv, xlsx, plcalarm)
//	GET / PUT /api/alarmtool/autosave         → the work in progress
func alarmRoutes(mux *http.ServeMux, readOnly bool) {
	paths := map[string]bool{}
	local := func(w http.ResponseWriter, r *http.Request) bool {
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"only on the computer running PLC Monitor"})
			return false
		}
		return true
	}
	type body struct {
		Set    AlarmSet `json:"set"`
		Path   string   `json:"path"`
		As     bool     `json:"as"`
		Name   string   `json:"name"`
		Data   []byte   `json:"data"`
		Format string   `json:"format"`
	}
	read := func(w http.ResponseWriter, r *http.Request) (body, bool) {
		var b body
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&b); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return b, false
		}
		return b, true
	}
	mux.HandleFunc("POST /api/alarmtool/open", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		if nativeOpenDialog == nil {
			writeJSON(w, http.StatusNotImplemented, apiError{"no Open dialog here"})
			return
		}
		p, ok := nativeOpenDialog("Alarm messages", []string{".plcalarm", ".csv", ".xlsx", ".xls"})
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
			return
		}
		data, err := os.ReadFile(p)
		if err != nil {
			respond(w, err)
			return
		}
		s, err := ImportAlarms(p, data)
		if err != nil {
			respond(w, err)
			return
		}
		stateMu.Lock()
		if strings.EqualFold(filepath.Ext(p), ".plcalarm") {
			paths[p] = true
		} else {
			p = ""
		}
		stateMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"path": p, "set": s})
	})
	mux.HandleFunc("POST /api/alarmtool/save", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		if readOnly {
			writeJSON(w, http.StatusForbidden, apiError{"the dashboard is read-only"})
			return
		}
		b, ok := read(w, r)
		if !ok {
			return
		}
		if err := b.Set.check(); err != nil {
			respond(w, err)
			return
		}
		p := b.Path
		stateMu.Lock()
		known := paths[p]
		stateMu.Unlock()
		if b.As || p == "" || !known {
			if nativeSaveDialog == nil {
				writeJSON(w, http.StatusNotImplemented, apiError{"no Save dialog here"})
				return
			}
			var ok bool
			if p, ok = nativeSaveDialog(fileSafe(b.Set.Name, "alarms")+".plcalarm", "Alarm messages", ".plcalarm"); !ok {
				writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
				return
			}
		}
		data, _ := json.MarshalIndent(b.Set, "", "  ")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			respond(w, err)
			return
		}
		stateMu.Lock()
		paths[p] = true
		stateMu.Unlock()
		log.Printf("alarm messages saved: %s", p)
		writeJSON(w, http.StatusOK, map[string]any{"path": p})
	})
	mux.HandleFunc("POST /api/alarmtool/import", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		b, ok := read(w, r)
		if !ok {
			return
		}
		s, err := ImportAlarms(b.Name, b.Data)
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"set": s})
	})
	mux.HandleFunc("POST /api/alarmtool/export", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		b, ok := read(w, r)
		if !ok {
			return
		}
		name, data, err := ExportAlarms(b.Set, b.Format)
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "data": data})
	})
	mux.HandleFunc("GET /api/alarmtool/autosave", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		data, err := os.ReadFile(alarmAutosavePath())
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"set": nil})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"set":`))
		w.Write(data)
		w.Write([]byte(`}`))
	})
	mux.HandleFunc("PUT /api/alarmtool/autosave", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) || readOnly {
			return
		}
		b, ok := read(w, r)
		if !ok {
			return
		}
		if err := b.Set.check(); err != nil {
			respond(w, err)
			return
		}
		data, _ := json.Marshal(b.Set)
		p := alarmAutosavePath()
		os.MkdirAll(filepath.Dir(p), 0o755)
		respond(w, os.WriteFile(p, data, 0o644))
	})
}
