package main

// State graphics: images that show the state of a tag (a lamp off / on /
// fault, a motor stopped / running …), to try out before they go into the
// HMI. The window (web/stategfx.html) edits a set of graphics; each graphic
// is bound to a tag and has states – a condition on the value and an image
// (PNG or SVG, kept in the file as a data URL).
//
// Files: .plcstate (the set, JSON) and a ZIP for the HMI (the set, every
// image as its own file, and states.csv: graphic, tag, state, condition, file).

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// StateSet is a .plcstate file.
type StateSet struct {
	Format   string         `json:"format"` // plcmonitor-stategraphics
	Version  int            `json:"version"`
	Name     string         `json:"name"`
	Graphics []StateGraphic `json:"graphics"`
	// ModeColors: the colours of the operating modes (manual, auto, disable,
	// enable, alarm, warning, fault, interlock, lock) the library uses;
	// missing ones are the defaults. ModeNoBadge: no letter badge.
	ModeColors  map[string]string `json:"mode_colors,omitempty"`
	ModeNoBadge bool              `json:"mode_no_badge,omitempty"`
}

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// StateGraphic is one graphic: a tag and its states.
type StateGraphic struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Tag    string       `json:"tag,omitempty"`
	Width  int          `json:"width"`
	Height int          `json:"height"`
	States []StateImage `json:"states"`
}

// StateImage is a state: when the value matches, this image shows.
type StateImage struct {
	Label string    `json:"label"`
	When  StateWhen `json:"when"`
	Image string    `json:"image,omitempty"` // data:image/svg+xml;base64,… or data:image/png;base64,…
	File  string    `json:"file,omitempty"`  // its file name when imported
	Blink bool      `json:"blink,omitempty"`
}

// StateWhen is a state's condition: eq (value), range (min ≤ value ≤ max),
// bit (bit n of the value set) or else (any other value).
type StateWhen struct {
	Op    string  `json:"op"`
	Value float64 `json:"value,omitempty"`
	Min   float64 `json:"min,omitempty"`
	Max   float64 `json:"max,omitempty"`
	Bit   int     `json:"bit,omitempty"`
}

const stateFormat = "plcmonitor-stategraphics"

func (w StateWhen) String() string {
	switch w.Op {
	case "eq":
		return "= " + strconv.FormatFloat(w.Value, 'g', -1, 64)
	case "range":
		return strconv.FormatFloat(w.Min, 'g', -1, 64) + " … " + strconv.FormatFloat(w.Max, 'g', -1, 64)
	case "bit":
		return "bit " + strconv.Itoa(w.Bit) + " set"
	}
	return "else"
}

var dataURLRe = regexp.MustCompile(`^data:image/(png|svg\+xml|jpeg|gif|webp);base64,([A-Za-z0-9+/=]*)$`)

// check validates a set (sizes, conditions, images).
func (s *StateSet) check() error {
	if s.Format != "" && s.Format != stateFormat {
		return fmt.Errorf("not a state graphics file (format %q)", s.Format)
	}
	s.Format, s.Version = stateFormat, 1
	if len(s.ModeColors) > 32 {
		return errors.New("too many mode colours")
	}
	for k, c := range s.ModeColors {
		if len(k) > 20 || !hexColorRe.MatchString(c) {
			return fmt.Errorf("mode colour %q: %q is not #rrggbb", k, c)
		}
	}
	if len(s.Graphics) > 500 {
		return errors.New("at most 500 graphics")
	}
	for gi := range s.Graphics {
		g := &s.Graphics[gi]
		if len(g.States) > 128 {
			return fmt.Errorf("%s: at most 128 states", g.Name)
		}
		if g.Width <= 0 || g.Width > 4000 {
			g.Width = 96
		}
		if g.Height <= 0 || g.Height > 4000 {
			g.Height = 96
		}
		for si, st := range g.States {
			switch st.When.Op {
			case "eq", "range", "bit", "else":
			default:
				return fmt.Errorf("%s, state %d: condition %q (eq, range, bit or else)", g.Name, si+1, st.When.Op)
			}
			if st.When.Op == "bit" && (st.When.Bit < 0 || st.When.Bit > 63) {
				return fmt.Errorf("%s, state %d: bit 0…63", g.Name, si+1)
			}
			if st.Image != "" && !dataURLRe.MatchString(st.Image) {
				return fmt.Errorf("%s, state %d: the image is not a PNG / SVG data URL", g.Name, si+1)
			}
		}
	}
	return nil
}

// imageBytes decodes a state's image: its bytes and file extension.
func imageBytes(dataURL string) ([]byte, string, error) {
	m := dataURLRe.FindStringSubmatch(dataURL)
	if m == nil {
		return nil, "", errors.New("no image")
	}
	b, err := base64.StdEncoding.DecodeString(m[2])
	ext := map[string]string{"png": ".png", "svg+xml": ".svg", "jpeg": ".jpg", "gif": ".gif", "webp": ".webp"}[m[1]]
	return b, ext, err
}

var fileBad = regexp.MustCompile(`[^\p{L}\p{N}_\-]+`)

func fileSafe(s, def string) string {
	s = strings.Trim(fileBad.ReplaceAllString(s, "_"), "_")
	if s == "" {
		return def
	}
	return s
}

// ExportStateZIP writes the set for the HMI: the .plcstate, each image as a
// file (images/<graphic>/<nn>_<state>.svg|png) and states.csv.
func ExportStateZIP(s StateSet) ([]byte, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	set, _ := json.MarshalIndent(s, "", "  ")
	name := fileSafe(s.Name, "states")
	f, _ := zw.Create(name + ".plcstate")
	f.Write(set)
	var rows [][]string
	rows = append(rows, []string{"Graphic", "Tag", "State", "Condition", "Blink", "File", "Width", "Height"})
	for gi, g := range s.Graphics {
		dir := fmt.Sprintf("images/%02d_%s", gi+1, fileSafe(g.Name, "graphic"))
		for si, st := range g.States {
			file := ""
			if b, ext, err := imageBytes(st.Image); err == nil {
				file = fmt.Sprintf("%s/%02d_%s%s", dir, si+1, fileSafe(st.Label, "state"), ext)
				w, _ := zw.Create(file)
				w.Write(b)
			}
			rows = append(rows, []string{g.Name, g.Tag, st.Label, st.When.String(), strconv.FormatBool(st.Blink), file, strconv.Itoa(g.Width), strconv.Itoa(g.Height)})
		}
	}
	var c bytes.Buffer
	c.WriteString("\xEF\xBB\xBF")
	cw := csv.NewWriter(&c)
	cw.UseCRLF = true
	cw.WriteAll(rows)
	w, _ := zw.Create("states.csv")
	w.Write(c.Bytes())
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ImportStateFile reads a .plcstate (or .json) file, or a ZIP: one made by
// Export (its .plcstate), or a folder of images – one graphic per folder,
// one state per image (motor_0.png, motor_1.png → value 0, 1).
func ImportStateFile(name string, data []byte) (StateSet, error) {
	var s StateSet
	switch strings.ToLower(filepath.Ext(name)) {
	case ".plcstate", ".json":
		if err := json.Unmarshal(data, &s); err != nil {
			return s, fmt.Errorf("not a state graphics file: %w", err)
		}
		return s, s.check()
	case ".zip":
	default:
		return s, errors.New("open a .plcstate file or a ZIP")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return s, fmt.Errorf("not a ZIP: %w", err)
	}
	read := func(f *zip.File) ([]byte, error) {
		if f.UncompressedSize64 > 16<<20 {
			return nil, fmt.Errorf("%s is larger than 16 MB", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 16<<20))
	}
	for _, f := range zr.File {
		if strings.EqualFold(path.Ext(f.Name), ".plcstate") {
			b, err := read(f)
			if err != nil {
				return s, err
			}
			return ImportStateFile(f.Name, b)
		}
	}
	// A ZIP of images: a graphic per folder.
	mime := map[string]string{".png": "png", ".svg": "svg+xml", ".jpg": "jpeg", ".jpeg": "jpeg", ".gif": "gif", ".webp": "webp"}
	groups := map[string][]*zip.File{}
	var order []string
	for _, f := range zr.File {
		if mime[strings.ToLower(path.Ext(f.Name))] == "" || f.FileInfo().IsDir() {
			continue
		}
		d := path.Dir(f.Name)
		if _, ok := groups[d]; !ok {
			order = append(order, d)
		}
		groups[d] = append(groups[d], f)
	}
	if len(order) == 0 {
		return s, errors.New("the ZIP has no .plcstate and no images")
	}
	s.Name = strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	numRe := regexp.MustCompile(`(-?\d+)$`)
	for i, d := range order {
		fs := groups[d]
		sort.Slice(fs, func(a, b int) bool { return fs[a].Name < fs[b].Name })
		gname := path.Base(d)
		if d == "." {
			gname = s.Name
		}
		g := StateGraphic{ID: fmt.Sprintf("g%d", i+1), Name: gname, Width: 96, Height: 96}
		for k, f := range fs {
			b, err := read(f)
			if err != nil {
				return s, err
			}
			base := strings.TrimSuffix(path.Base(f.Name), path.Ext(f.Name))
			st := StateImage{Label: base, File: path.Base(f.Name),
				Image: "data:image/" + mime[strings.ToLower(path.Ext(f.Name))] + ";base64," + base64.StdEncoding.EncodeToString(b),
				When:  StateWhen{Op: "eq", Value: float64(k)}}
			if m := numRe.FindStringSubmatch(base); m != nil { // motor_2.png: value 2
				v, _ := strconv.Atoi(m[1])
				st.When.Value = float64(v)
			}
			g.States = append(g.States, st)
		}
		s.Graphics = append(s.Graphics, g)
	}
	return s, s.check()
}

// The paths chosen in this run's Save / Open dialogs: Save writes only there.
var (
	stateMu    sync.Mutex
	statePaths = map[string]bool{}
)

func stateAutosavePath() string {
	dir := os.Getenv("LOCALAPPDATA")
	if dir == "" {
		dir, _ = os.UserConfigDir()
	}
	return filepath.Join(dir, "PLCMonitor", "stategraphics-autosave.plcstate")
}

// stateRoutes: the State graphics window's API (this computer only).
//
//	POST /api/stategfx/open             → {path, set} (Open dialog; 501 without one)
//	POST /api/stategfx/save {set, path, as} → {path} (Save / Save as)
//	POST /api/stategfx/import {name, data}  → {set}
//	POST /api/stategfx/export {set}     → {name, data} (ZIP)
//	GET / PUT /api/stategfx/autosave    → the work in progress
func stateRoutes(mux *http.ServeMux, readOnly bool) {
	local := func(w http.ResponseWriter, r *http.Request) bool {
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"only on the computer running PLC Monitor"})
			return false
		}
		return true
	}
	type body struct {
		Set  StateSet `json:"set"`
		Path string   `json:"path"`
		As   bool     `json:"as"`
		Name string   `json:"name"`
		Data []byte   `json:"data"`
	}
	read := func(w http.ResponseWriter, r *http.Request) (body, bool) {
		var b body
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&b); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return b, false
		}
		return b, true
	}
	mux.HandleFunc("POST /api/stategfx/open", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		if nativeOpenDialog == nil {
			writeJSON(w, http.StatusNotImplemented, apiError{"no Open dialog here"})
			return
		}
		p, ok := nativeOpenDialog("State graphics", []string{".plcstate", ".zip"})
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
			return
		}
		data, err := os.ReadFile(p)
		if err != nil {
			respond(w, err)
			return
		}
		s, err := ImportStateFile(p, data)
		if err != nil {
			respond(w, err)
			return
		}
		if strings.EqualFold(filepath.Ext(p), ".plcstate") {
			stateMu.Lock()
			statePaths[p] = true
			stateMu.Unlock()
		} else {
			p = "" // a ZIP: Save asks where the .plcstate goes
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": p, "set": s})
	})
	mux.HandleFunc("POST /api/stategfx/save", func(w http.ResponseWriter, r *http.Request) {
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
		known := statePaths[p]
		stateMu.Unlock()
		if b.As || p == "" || !known {
			if nativeSaveDialog == nil {
				writeJSON(w, http.StatusNotImplemented, apiError{"no Save dialog here"})
				return
			}
			var ok bool
			if p, ok = nativeSaveDialog(fileSafe(b.Set.Name, "states")+".plcstate", "State graphics", ".plcstate"); !ok {
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
		statePaths[p] = true
		stateMu.Unlock()
		log.Printf("state graphics saved: %s", p)
		writeJSON(w, http.StatusOK, map[string]any{"path": p})
	})
	mux.HandleFunc("POST /api/stategfx/import", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		b, ok := read(w, r)
		if !ok {
			return
		}
		s, err := ImportStateFile(b.Name, b.Data)
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"set": s})
	})
	mux.HandleFunc("POST /api/stategfx/export", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		b, ok := read(w, r)
		if !ok {
			return
		}
		data, err := ExportStateZIP(b.Set)
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": fileSafe(b.Set.Name, "states") + ".zip", "data": data})
	})
	mux.HandleFunc("GET /api/stategfx/autosave", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		data, err := os.ReadFile(stateAutosavePath())
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"set": nil})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"set":`))
		w.Write(data)
		w.Write([]byte(`}`))
	})
	mux.HandleFunc("PUT /api/stategfx/autosave", func(w http.ResponseWriter, r *http.Request) {
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
		p := stateAutosavePath()
		os.MkdirAll(filepath.Dir(p), 0o755)
		respond(w, os.WriteFile(p, data, 0o644))
	})
}
