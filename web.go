package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed web/index.html
var indexHTML []byte

// The Export settings window and the Help window.
//
//go:embed web/export.html
var exportHTML []byte

//go:embed web/help.html
var helpHTML []byte

// The Scan PLC window (data blocks → tags).
//
//go:embed web/plcscan.html
var plcScanHTML []byte

// The PLC tags window (OPC UA browser).
//
//go:embed web/plctags.html
var plcTagsHTML []byte

// The Data view window (what MQTT and InfluxDB get).
//
//go:embed web/dataview.html
var dataViewHTML []byte

// The Network adapters window (this PC's IP addresses, ping, helpers).
//
//go:embed web/netadapters.html
var netAdaptersHTML []byte

// themesJS holds the colour themes and the theme picker, used by every page.
//
//go:embed web/themes.js
var themesJS []byte

var themeID = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// bootID tells the windows that the program was restarted.
var bootID = strconv.FormatInt(time.Now().UnixNano(), 36)

// iconPNG is the app icon: the browser tab icon here, and the window and
// taskbar icon in the desktop build.
//
//go:embed winres/icon.png
var iconPNG []byte

// NetworkSettings is the editable connection part of the config. The flat
// address/rack/slot fields are the S7 settings; Modbus has its own object.
type NetworkSettings struct {
	Protocol       string          `json:"protocol"` // s7, modbus or opcua; "" keeps the current one
	Simulate       bool            `json:"simulate"`
	Address        string          `json:"address"`
	Rack           int             `json:"rack"`
	Slot           int             `json:"slot"`
	ConnectionType string          `json:"connection_type"`
	Timeout        string          `json:"timeout"`
	Modbus         *ModbusSettings `json:"modbus,omitempty"`
	OPCUA          *OPCUASettings  `json:"opcua,omitempty"`
	PollInterval   string          `json:"poll_interval"`
	ReconnectDelay string          `json:"reconnect_delay"`
	ReconnectMax   string          `json:"reconnect_max"`
}

// OPCUASettings: the password is never sent back; an empty one keeps the
// saved password unless ClearPassword is set.
type OPCUASettings struct {
	Endpoint      string `json:"endpoint"`
	Security      string `json:"security"`
	Policy        string `json:"policy"`
	Username      string `json:"username"`
	Password      string `json:"password,omitempty"`
	PasswordSet   bool   `json:"password_set"`
	ClearPassword bool   `json:"clear_password,omitempty"`
	Timeout       string `json:"timeout"`
}

type ModbusSettings struct {
	Mode       string `json:"mode"`
	Address    string `json:"address"`
	SerialPort string `json:"serial_port"`
	Baud       int    `json:"baud"`
	DataBits   int    `json:"data_bits"`
	Parity     string `json:"parity"`
	StopBits   int    `json:"stop_bits"`
	UnitID     int    `json:"unit_id"`
	WordOrder  string `json:"word_order"`
	MaxGap     int    `json:"max_gap"`
	Timeout    string `json:"timeout"`
}

func networkOf(c *Config) NetworkSettings {
	mb := c.Modbus
	return NetworkSettings{
		Protocol: c.Protocol, Simulate: c.Simulate,
		Address: c.PLC.Address, Rack: c.PLC.Rack, Slot: c.PLC.Slot,
		ConnectionType: c.PLC.ConnectionType, Timeout: c.PLC.Timeout.String(),
		Modbus: &ModbusSettings{
			Mode: mb.Mode, Address: mb.Address, SerialPort: mb.SerialPort, Baud: mb.Baud,
			DataBits: mb.DataBits, Parity: mb.Parity, StopBits: mb.StopBits, UnitID: mb.UnitID,
			WordOrder: mb.WordOrder, MaxGap: mb.MaxGap, Timeout: mb.Timeout.String(),
		},
		OPCUA: &OPCUASettings{Endpoint: c.OPCUA.Endpoint, Security: c.OPCUA.Security, Policy: c.OPCUA.Policy,
			Username: c.OPCUA.Username, PasswordSet: c.OPCUA.Password != "", Timeout: c.OPCUA.Timeout.String()},
		PollInterval: c.PollInterval.String(), ReconnectDelay: c.ReconnectDelay.String(),
		ReconnectMax: c.ReconnectMax.String(),
	}
}

func (n NetworkSettings) applyTo(c *Config) error {
	if n.Protocol != "" {
		c.Protocol = n.Protocol
	}
	if mb := n.Modbus; mb != nil {
		t, err := time.ParseDuration(mb.Timeout)
		if err != nil || t < 50*time.Millisecond {
			return fmt.Errorf("Modbus timeout: %q is not a duration of at least 50ms", mb.Timeout)
		}
		c.Modbus = ModbusConfig{
			Mode: mb.Mode, Address: mb.Address, SerialPort: mb.SerialPort, Baud: mb.Baud,
			DataBits: mb.DataBits, Parity: mb.Parity, StopBits: mb.StopBits, UnitID: mb.UnitID,
			WordOrder: mb.WordOrder, MaxGap: mb.MaxGap, Timeout: t,
		}
	}
	if o := n.OPCUA; o != nil {
		t, err := time.ParseDuration(o.Timeout)
		if err != nil || t < 100*time.Millisecond {
			return fmt.Errorf("OPC UA timeout: %q is not a duration of at least 100ms", o.Timeout)
		}
		pw := c.OPCUA.Password
		if o.Password != "" || o.ClearPassword {
			pw = o.Password
		}
		c.OPCUA = OPCUAConfig{Endpoint: strings.TrimSpace(o.Endpoint), Security: o.Security, Policy: o.Policy,
			Username: strings.TrimSpace(o.Username), Password: pw, Timeout: t}
		c.OPCUA.applyDefaults()
		if err := c.OPCUA.validate(); err != nil {
			return err
		}
	}
	durs := []struct {
		name string
		in   string
		out  *time.Duration
		min  time.Duration
	}{
		{"timeout", n.Timeout, &c.PLC.Timeout, 100 * time.Millisecond},
		{"poll interval", n.PollInterval, &c.PollInterval, 10 * time.Millisecond},
		{"reconnect delay", n.ReconnectDelay, &c.ReconnectDelay, 100 * time.Millisecond},
		{"reconnect max", n.ReconnectMax, &c.ReconnectMax, 100 * time.Millisecond},
	}
	for _, d := range durs {
		v, err := time.ParseDuration(d.in)
		if err != nil {
			return fmt.Errorf("%s: %q is not a duration (use e.g. 500ms, 1s, 2m)", d.name, d.in)
		}
		if v < d.min {
			return fmt.Errorf("%s must be at least %s", d.name, d.min)
		}
		*d.out = v
	}
	if n.Rack < 0 || n.Rack > 7 || n.Slot < 0 || n.Slot > 31 {
		return errors.New("rack must be 0..7 and slot 0..31")
	}
	c.PLC.Address, c.PLC.Rack, c.PLC.Slot, c.PLC.ConnectionType = n.Address, n.Rack, n.Slot, n.ConnectionType
	// A simulator choice made here is the user's, so it is saved to the file
	// (unlike the -simulate command-line override).
	c.Simulate, c.fileSimulate = n.Simulate, n.Simulate
	return nil
}

// NewWebHandler serves the dashboard at / and the JSON API under /api/.
func NewWebHandler(m *Monitor, readOnly bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache") // a new version of the program shows its new pages
		w.Write(indexHTML)
	})
	for path, page := range map[string][]byte{"/export": exportHTML, "/help": helpHTML, "/plcscan": plcScanHTML, "/plctags": plcTagsHTML, "/dataview": dataViewHTML, "/netadapters": netAdaptersHTML} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(page)
		})
	}
	// themes.js starts with the theme saved in config.yaml, so every window
	// opens in it without a flash, even when localStorage is empty.
	mux.HandleFunc("GET /themes.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		theme, _ := json.Marshal(m.Config().Web.Theme)
		fmt.Fprintf(w, "window.S7_SERVER_THEME = %s;\n", theme)
		w.Write(themesJS)
	})
	mux.HandleFunc("PUT /api/ui/theme", func(w http.ResponseWriter, r *http.Request) {
		// Appearance only, so also allowed on a read-only dashboard, but only
		// from this computer since it is saved in config.yaml.
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"the saved theme can only be changed on the computer running PLC Monitor"})
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, apiError{"Content-Type must be application/json"})
			return
		}
		var body struct {
			Theme string `json:"theme"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || !themeID.MatchString(body.Theme) {
			respond(w, errors.New(`body must be {"theme": "<theme id>"}`))
			return
		}
		if body.Theme == "system" {
			body.Theme = ""
		}
		if m.Config().Web.Theme == body.Theme {
			respond(w, nil)
			return
		}
		respond(w, m.Update(func(c *Config) error { c.Web.Theme = body.Theme; return nil }))
	})
	// Window layout: charts (one or more tags each, in the main window or
	// their own) and tag windows. Everyone can read it; it is saved in
	// config.yaml only from this computer (other browsers keep their own).
	mux.HandleFunc("GET /api/ui/layout", func(w http.ResponseWriter, r *http.Request) {
		l, rev := m.Layout()
		if l.Charts == nil {
			l.Charts = []ChartLayout{}
		}
		if l.TagWindows == nil {
			l.TagWindows = []TagWindowLayout{}
		}
		writeJSON(w, http.StatusOK, struct {
			Rev     int          `json:"rev"`
			Boot    string       `json:"boot"` // changes when the program restarts (rev starts again)
			Layout  LayoutConfig `json:"layout"`
			CanSave bool         `json:"can_save"`
		}{rev, bootID, l, isLoopback(r)})
	})
	mux.HandleFunc("PUT /api/ui/layout", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"the layout is only saved on the computer running PLC Monitor"})
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, apiError{"Content-Type must be application/json"})
			return
		}
		var l LayoutConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&l); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		rev, err := m.SetLayout(l)
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Rev int `json:"rev"`
		}{rev})
	})
	// View → Always on top (desktop app only), remembered in config.yaml.
	mux.HandleFunc("PUT /api/ui/topmost", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) || r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusForbidden, apiError{"only in the desktop app"})
			return
		}
		if setAlwaysOnTop == nil {
			writeJSON(w, http.StatusNotImplemented, apiError{"Always on top is only available in the desktop app (PLCMonitorApp.exe)"})
			return
		}
		var body struct {
			On bool `json:"on"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			respond(w, errors.New(`body must be {"on": true|false}`))
			return
		}
		setAlwaysOnTop(body.On)
		if m.Config().Web.AlwaysOnTop != body.On {
			if err := m.Update(func(c *Config) error { c.Web.AlwaysOnTop = body.On; return nil }); err != nil {
				log.Printf("always on top: not saved: %v", err)
			}
		}
		respond(w, nil)
	})
	mux.HandleFunc("GET /favicon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=86400")
		w.Write(iconPNG)
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		st, tags := m.Snapshot()
		writeJSON(w, http.StatusOK, struct {
			Status Status     `json:"status"`
			Tags   []TagState `json:"tags"`
		}{st, tags})
	})
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		c := m.Config()
		tags := c.Tags
		if tags == nil {
			tags = []Tag{}
		}
		writeJSON(w, http.StatusOK, struct {
			Network       NetworkSettings `json:"network"`
			Tags          []Tag           `json:"tags"`
			Simulated     bool            `json:"simulated"`
			ReadOnly      bool            `json:"read_only"`
			NativeDialogs bool            `json:"native_dialogs"`
			FilesAllowed  bool            `json:"files_allowed"`
			Local         bool            `json:"local"` // request comes from this computer (Exit allowed)
			CanWrite      bool            `json:"can_write"`
			HistoryFiles  bool            `json:"history_files"`
			CanPin        bool            `json:"can_pin"` // desktop app: Always on top available
			AlwaysOnTop   bool            `json:"always_on_top"`
		}{networkOf(c), tags, c.Simulate, readOnly, nativeFileDialog != nil, isLoopback(r) && !readOnly, isLoopback(r),
			!readOnly && (isLoopback(r) || c.Web.AllowRemoteWrite), m.Trend != nil,
			setAlwaysOnTop != nil && isLoopback(r), c.Web.AlwaysOnTop})
	})
	mux.HandleFunc("GET /api/serialports", func(w http.ResponseWriter, r *http.Request) {
		ports := serialPorts()
		if ports == nil {
			ports = []string{}
		}
		writeJSON(w, http.StatusOK, ports)
	})
	// Trend history from the files: GET /api/trend/{name}?from=ms&to=ms&max=n
	// returns at most about 2n points (min/max per time bucket).
	trendRange := func(r *http.Request) (time.Time, time.Time, error) {
		q := r.URL.Query()
		from, err1 := strconv.ParseInt(q.Get("from"), 10, 64)
		to, err2 := strconv.ParseInt(q.Get("to"), 10, 64)
		if err1 != nil || err2 != nil || to <= from {
			return time.Time{}, time.Time{}, errors.New("from and to must be Unix milliseconds with from < to")
		}
		return time.UnixMilli(from), time.UnixMilli(to), nil
	}
	mux.HandleFunc("GET /api/trend/{name}", func(w http.ResponseWriter, r *http.Request) {
		from, to, err := trendRange(r)
		if err != nil {
			respond(w, err)
			return
		}
		name := r.PathValue("name")
		if _, ok := m.Tag(name); !ok {
			respond(w, notFound("tag not found"))
			return
		}
		var samples []Sample
		if m.Trend != nil {
			if samples, err = m.Trend.Read(name, from, to); err != nil {
				writeJSON(w, http.StatusInternalServerError, apiError{err.Error()})
				return
			}
		} else { // history files off: what is in memory
			_, all, _ := m.History(name, from.UnixMilli()-1)
			for _, s := range all {
				if s.T <= to.UnixMilli() {
					samples = append(samples, s)
				}
			}
		}
		max, _ := strconv.Atoi(r.URL.Query().Get("max"))
		if max <= 0 || max > 4000 {
			max = 1000
		}
		samples = Downsample(samples, from.UnixMilli(), to.UnixMilli(), max)
		points := make([][2]any, len(samples))
		for i, s := range samples {
			points[i] = [2]any{s.T, jsonSafe(s.V)}
		}
		writeJSON(w, http.StatusOK, struct {
			Points [][2]any `json:"points"`
			Files  bool     `json:"files"` // false: only the in-memory history was available
		}{points, m.Trend != nil})
	})
	mux.HandleFunc("GET /api/trend/{name}/csv", func(w http.ResponseWriter, r *http.Request) {
		from, to, err := trendRange(r)
		if err == nil && m.Trend == nil {
			err = errors.New("trend history files are turned off (history.disabled in config)")
		}
		if err != nil {
			respond(w, err)
			return
		}
		name := r.PathValue("name")
		file := fmt.Sprintf("%s %s - %s.csv", strings.TrimSuffix(trendFileName(name), ".csv"),
			from.Format("2006-01-02 1504"), to.Format("2006-01-02 1504"))
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file))
		if err := m.Trend.ExportCSV(w, name, from, to); err != nil {
			log.Printf("history export: %v", err)
		}
	})
	// Several tags side by side (a combined chart):
	// GET /api/trends/csv?tag=a&tag=b&from=ms&to=ms
	mux.HandleFunc("GET /api/trends/csv", func(w http.ResponseWriter, r *http.Request) {
		from, to, err := trendRange(r)
		names := r.URL.Query()["tag"]
		switch {
		case err != nil:
		case m.Trend == nil:
			err = errors.New("trend history files are turned off (history.disabled in config)")
		case len(names) == 0 || len(names) > 16:
			err = errors.New("give 1 to 16 tags as tag=…")
		}
		if err != nil {
			respond(w, err)
			return
		}
		for _, n := range names {
			if _, ok := m.Tag(n); !ok {
				respond(w, notFound(fmt.Sprintf("tag %q not found", n)))
				return
			}
		}
		base := strings.TrimSuffix(trendFileName(names[0]), ".csv")
		if len(names) > 1 {
			base = fmt.Sprintf("%s +%d", base, len(names)-1)
		}
		file := fmt.Sprintf("%s %s - %s.csv", base, from.Format("2006-01-02 1504"), to.Format("2006-01-02 1504"))
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file))
		if err := m.Trend.ExportCSVMulti(w, names, from, to); err != nil {
			log.Printf("history export: %v", err)
		}
	})
	// Topology: network scan and Modbus slave-ID scan. Only from this
	// computer, since a scan sends traffic onto the plant network.
	scanner := NewScanner(m)
	localJSON := func(w http.ResponseWriter, r *http.Request) bool {
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"scanning only works on the computer running PLC Monitor"})
			return false
		}
		if r.Method != http.MethodDelete && r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, apiError{"Content-Type must be application/json"})
			return false
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		return true
	}
	// This PC's IPv4 networks; "down": an address but no link (cable?).
	mux.HandleFunc("GET /api/netifs", func(w http.ResponseWriter, r *http.Request) {
		nets := LocalNetworksAll()
		if nets == nil {
			nets = []LocalNetwork{}
		}
		writeJSON(w, http.StatusOK, nets)
	})
	mux.HandleFunc("GET /api/scan", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, scanner.State())
	})
	mux.HandleFunc("POST /api/scan", func(w http.ResponseWriter, r *http.Request) {
		if !localJSON(w, r) {
			return
		}
		var opt ScanOptions
		if err := json.NewDecoder(r.Body).Decode(&opt); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		respond(w, scanner.Start(opt))
	})
	mux.HandleFunc("POST /api/scan/bus", func(w http.ResponseWriter, r *http.Request) {
		if !localJSON(w, r) {
			return
		}
		var body struct{ From, To int }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		respond(w, scanner.StartBus(body.From, body.To))
	})
	mux.HandleFunc("DELETE /api/scan", func(w http.ResponseWriter, r *http.Request) {
		if localJSON(w, r) {
			scanner.Stop()
			respond(w, nil)
		}
	})

	// Open the history folder in Explorer (desktop use, this computer only).
	mux.HandleFunc("POST /api/history/folder", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) || r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusForbidden, apiError{"only on the computer running PLC Monitor"})
			return
		}
		if m.Trend == nil {
			respond(w, errors.New("trend history files are turned off"))
			return
		}
		respond(w, openFolder(m.Trend.cfg.Path))
	})
	// The Windows Network Connections window (the PC's adapters and IP addresses).
	mux.HandleFunc("POST /api/os/network-connections", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) || r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusForbidden, apiError{"only on the computer running PLC Monitor"})
			return
		}
		respond(w, openNetworkConnections())
	})
	// The folder of the configuration (config.yaml, plc-symbols.json, …) in Explorer.
	mux.HandleFunc("POST /api/config/folder", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) || r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusForbidden, apiError{"only on the computer running PLC Monitor"})
			return
		}
		if m.path == "" {
			respond(w, errors.New("the configuration is kept in memory only (no config file)"))
			return
		}
		dir, err := filepath.Abs(filepath.Dir(m.path))
		if err == nil {
			err = openFolder(dir)
		}
		respond(w, err)
	})
	mux.HandleFunc("GET /api/history/{name}", func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		st, samples, ok := m.History(r.PathValue("name"), since)
		if !ok {
			writeJSON(w, http.StatusNotFound, apiError{"tag not found"})
			return
		}
		points := make([][2]any, len(samples))
		for i, s := range samples {
			points[i] = [2]any{s.T, jsonSafe(s.V)}
		}
		writeJSON(w, http.StatusOK, struct {
			Name    string   `json:"name"`
			Address string   `json:"address"`
			Type    string   `json:"type"`
			Display string   `json:"display"`
			Good    bool     `json:"good"`
			Points  [][2]any `json:"points"` // [unix ms, value or null]
		}{st.Name, st.Address, st.Type, st.Display, st.Good, points})
	})

	// guard rejects edits when read-only, and requires a JSON content type,
	// which makes browsers send a CORS preflight so that other web sites
	// cannot post edits to this API.
	// Scan PLC: find data blocks and read their bytes (see plcscan.go).
	plcScan := NewPLCScanner(m)

	guardN := func(w http.ResponseWriter, r *http.Request, limit int64) bool {
		if readOnly {
			writeJSON(w, http.StatusForbidden, apiError{"dashboard is read-only (web.read_only in config)"})
			return false
		}
		if r.Method != http.MethodDelete && r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, apiError{"Content-Type must be application/json"})
			return false
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		return true
	}
	guard := func(w http.ResponseWriter, r *http.Request) bool { return guardN(w, r, 1<<20) }

	// Variable names for Scan PLC, from files exported from TIA Portal
	// (see symbols.go).
	symbols := NewSymbolStore(symbolPath(m.path))
	mux.HandleFunc("GET /api/plcscan/symbols", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, symbols.State())
	})
	mux.HandleFunc("POST /api/plcscan/symbols", func(w http.ResponseWriter, r *http.Request) {
		if !guardN(w, r, 48<<20) {
			return
		}
		var body struct {
			Name string `json:"name"`
			Data []byte `json:"data"` // base64
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		st, err := symbols.Import(body.Name, body.Data, plcScan.State().Found)
		if err != nil {
			respond(w, err)
			return
		}
		log.Printf("imported variable names from %s", body.Name)
		writeJSON(w, http.StatusOK, st)
	})
	// A PLC tag table from TIA Portal (.xlsx/.xml/.csv) as tags; the page
	// adds them to the list (with undo).
	mux.HandleFunc("POST /api/tags/tia", func(w http.ResponseWriter, r *http.Request) {
		if !guardN(w, r, 48<<20) {
			return
		}
		var body struct {
			Name string `json:"name"`
			Data []byte `json:"data"` // base64
			Path string `json:"path"` // or a file on this computer (File → Open dialog)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		if body.Path != "" {
			if !isLoopback(r) { // reading files by path: this computer only, like the File menu
				writeJSON(w, http.StatusForbidden, apiError{"the File menu only works on the computer running PLC Monitor"})
				return
			}
			if !isTagTable(body.Path) {
				respond(w, errors.New("not a PLC tag table (.xlsx, .xml or .csv)"))
				return
			}
			st, err := os.Stat(body.Path)
			if err == nil && st.Size() > 32<<20 {
				err = errors.New("the file is larger than 32 MB")
			}
			if err == nil {
				body.Data, err = os.ReadFile(body.Path)
			}
			if err != nil {
				respond(w, err)
				return
			}
			body.Name = filepath.Base(body.Path)
		}
		tags, skipped, err := TIATags(body.Name, body.Data)
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tags": tags, "skipped": skipped})
	})
	// A block source (.scl/.db/.udt) as data blocks with their variables as
	// tags; DB numbers from the imported names (Scan PLC) or, by size, from
	// the last scan. The page lets the user pick and adds them.
	mux.HandleFunc("POST /api/tags/source", func(w http.ResponseWriter, r *http.Request) {
		if !guardN(w, r, 48<<20) {
			return
		}
		var body struct {
			Name string `json:"name"`
			Data []byte `json:"data"`
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		if body.Path != "" {
			if !isLoopback(r) {
				writeJSON(w, http.StatusForbidden, apiError{"the File menu only works on the computer running PLC Monitor"})
				return
			}
			if !isBlockSource(body.Path) {
				respond(w, errors.New("not a block source (.scl, .db, .udt)"))
				return
			}
			data, err := os.ReadFile(body.Path)
			if err != nil {
				respond(w, err)
				return
			}
			body.Data, body.Name = data, filepath.Base(body.Path)
		}
		known := map[string]int{}
		for _, b := range symbols.State().Blocks {
			if b.Kind == "db" && b.DB != 0 {
				known[b.Name] = b.DB
			}
		}
		found := plcScan.State().Found
		blocks, err := SourceTags(body.Name, body.Data, known, found)
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"blocks": blocks, "found": found})
	})
	// The DB tags as SCL data blocks (standard access) for "Generate blocks
	// from source"; block names from imported sources where known.
	mux.HandleFunc("GET /api/tags/scl", func(w http.ResponseWriter, r *http.Request) {
		names := map[int]string{}
		for _, b := range symbols.State().Blocks {
			if b.Kind == "db" && b.DB != 0 {
				names[b.DB] = b.Name
			}
		}
		data, n, skipped, err := TagsToSCL(m.Config().Tags, names)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "skipped": skipped})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data, "count": n, "skipped": skipped})
	})
	// The tag list as a TIA Portal PLC tag table: ?format=xlsx|xml. The file
	// comes back as base64 with how many tags it holds and which were left out.
	mux.HandleFunc("GET /api/tags/tia", func(w http.ResponseWriter, r *http.Request) {
		data, n, skipped, err := TIAExport(m.Config().Tags, r.URL.Query().Get("format"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "skipped": skipped})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data, "count": n, "skipped": skipped})
	})
	mux.HandleFunc("PUT /api/plcscan/symbols/db", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		var body struct {
			Block string `json:"block"`
			DB    int    `json:"db"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		respond(w, symbols.SetDB(body.Block, body.DB))
	})
	// PLC tags over OPC UA (see opcua.go).
	uaBrowse := NewUABrowser()
	mux.HandleFunc("GET /api/opcua/browse", func(w http.ResponseWriter, r *http.Request) {
		st := uaBrowse.State()
		addS7(st.Vars, symbols.State())
		c := m.Config()
		writeJSON(w, http.StatusOK, struct {
			UABrowseState
			DefaultEndpoint string `json:"default_endpoint"`
		}{st, uaEndpointFor(c)})
	})
	mux.HandleFunc("POST /api/opcua/browse", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		var o OPCUAConfig
		if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		c := m.Config()
		if o.Password == "" && o.Username == c.OPCUA.Username {
			o.Password = c.OPCUA.Password // the saved one (never sent to the page)
		}
		o.Timeout = c.OPCUA.Timeout
		log.Printf("OPC UA browse %s", o.Endpoint)
		respond(w, uaBrowse.Start(o))
	})
	mux.HandleFunc("DELETE /api/opcua/browse", func(w http.ResponseWriter, r *http.Request) {
		if guard(w, r) {
			uaBrowse.Stop()
			respond(w, nil)
		}
	})
	mux.HandleFunc("POST /api/opcua/read", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		var body struct {
			Nodes []string `json:"nodes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		vals, err := uaBrowse.Read(body.Nodes)
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, vals)
	})
	// A tag file (YAML, as File → Save as writes it) from a list of tags.
	mux.HandleFunc("POST /api/opcua/tagfile", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		var tags []Tag
		if err := json.NewDecoder(r.Body).Decode(&tags); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		seen := map[string]bool{}
		for i := range tags {
			a, err := parseArea(tags[i].Area)
			if err == nil {
				err = tags[i].normalize(a.Protocol())
			}
			if err != nil {
				respond(w, fmt.Errorf("tag %q: %w", tags[i].Name, err))
				return
			}
			if seen[tags[i].Name] {
				respond(w, fmt.Errorf("duplicate tag name %q", tags[i].Name))
				return
			}
			seen[tags[i].Name] = true
		}
		writeJSON(w, http.StatusOK, struct {
			YAML string `json:"yaml"`
		}{string(marshalTags(&Config{Tags: tags}))})
	})

	mux.HandleFunc("DELETE /api/plcscan/symbols", func(w http.ResponseWriter, r *http.Request) {
		if guard(w, r) {
			respond(w, symbols.Remove(r.URL.Query().Get("file")))
		}
	})

	mux.HandleFunc("GET /api/plcscan", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, plcScan.State())
	})
	mux.HandleFunc("POST /api/plcscan", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		var opt PLCScanOptions
		if err := json.NewDecoder(r.Body).Decode(&opt); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		respond(w, plcScan.Start(opt))
	})
	mux.HandleFunc("DELETE /api/plcscan", func(w http.ResponseWriter, r *http.Request) {
		if guard(w, r) {
			plcScan.Stop()
			respond(w, nil)
		}
	})
	mux.HandleFunc("GET /api/plcscan/read", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		num := func(k string) int { n, _ := strconv.Atoi(q.Get(k)); return n }
		data, err := plcScan.ReadArea(q.Get("area"), num("db"), num("start"), num("size"))
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Data []byte `json:"data"` // base64
		}{data})
	})

	// Editing endpoints. Each change is validated, kept in the workspace
	// config and applied to the running monitor at once.
	edit := func(h func(r *http.Request, c *Config) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if guard(w, r) {
				respond(w, m.Update(func(c *Config) error { return h(r, c) }))
			}
		}
	}

	// File menu: POST /api/file/{network|tags}/{new|open|save|saveas} with
	// {"path": "..."} for open and saveas.
	// Opening and saving files by path is only allowed from this computer,
	// so a dashboard shared on the network cannot read or overwrite files.
	local := func(w http.ResponseWriter, r *http.Request) bool {
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"the File menu only works on the computer running PLC Monitor"})
			return false
		}
		return guard(w, r)
	}
	// Save an exported picture/PDF with the Windows Save as dialog (desktop
	// app). 501 elsewhere: the page then downloads the file instead.
	mux.HandleFunc("POST /api/save-file", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) || r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusForbidden, apiError{"only on the computer running PLC Monitor"})
			return
		}
		if nativeSaveDialog == nil {
			writeJSON(w, http.StatusNotImplemented, apiError{"no Save dialog in this version"})
			return
		}
		var body struct {
			Name string `json:"name"`
			Data []byte `json:"data"` // base64 in JSON
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&body); err != nil {
			respond(w, fmt.Errorf("bad request: %w", err))
			return
		}
		ext := strings.ToLower(filepath.Ext(body.Name))
		label := map[string]string{".png": "PNG picture", ".jpg": "JPG picture", ".pdf": "PDF document", ".csv": "CSV file", ".yaml": "Tag configuration",
			".xlsx": "PLC tag table (Excel)", ".xml": "PLC tag table (XML)", ".sdf": "PLC tag table (SDF)", ".scl": "SCL source"}[ext]
		if label == "" || strings.ContainsAny(body.Name, `\/:*?"<>|`) {
			respond(w, errors.New("only .png, .jpg, .pdf, .csv, .yaml, .xlsx, .xml, .sdf and .scl files with a plain file name"))
			return
		}
		path, ok := nativeSaveDialog(body.Name, label, ext)
		if !ok {
			writeJSON(w, http.StatusOK, struct {
				Cancelled bool `json:"cancelled"`
			}{true})
			return
		}
		if err := os.WriteFile(path, body.Data, 0o644); err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Path string `json:"path"`
		}{path})
	})
	// PROFINET DCP: find devices, set IP / name of station, flash LED.
	dcpRoutes(mux, m, local, readOnly, localJSON)
	// This PC's network adapters: list, add/remove an address, enable/disable.
	netAdapterRoutes(mux, readOnly)
	exportViewRoutes(mux, m) // Data view: what MQTT and InfluxDB get
	exportRoutes(mux, m, func(w http.ResponseWriter, r *http.Request) bool {
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"export settings can only be changed on the computer running PLC Monitor"})
			return false
		}
		return guard(w, r)
	})
	mux.HandleFunc("POST /api/file/{kind}/{action}", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		k, err := parseFileKind(r.PathValue("kind"))
		if err != nil {
			respond(w, err)
			return
		}
		var body struct {
			Path string `json:"path"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		path := strings.TrimSpace(body.Path)
		switch r.PathValue("action") {
		case "new":
			err = m.FileNew(k)
		case "open":
			if path == "" {
				err = errors.New("no file chosen")
			} else {
				err = m.FileOpen(k, path)
			}
		case "save":
			err = m.FileSave(k, "")
		case "saveas":
			if path == "" {
				err = errors.New("no file name given")
			} else {
				if filepath.Ext(path) == "" {
					path += k.DefaultExt()
				}
				err = m.FileSave(k, path)
			}
		default:
			err = notFound("unknown file action " + r.PathValue("action"))
		}
		respond(w, err)
	})
	// Writing values and signal generators. Allowed when not read-only, and
	// from other computers only with web.allow_remote_write.
	canWrite := func(r *http.Request) bool {
		return !readOnly && (isLoopback(r) || m.Config().Web.AllowRemoteWrite)
	}
	writeGuard := func(w http.ResponseWriter, r *http.Request) bool {
		if !canWrite(r) {
			msg := "writing is only allowed on the computer running PLC Monitor (web.allow_remote_write)"
			if readOnly {
				msg = "dashboard is read-only (web.read_only in config)"
			}
			writeJSON(w, http.StatusForbidden, apiError{msg})
			return false
		}
		if r.Method != http.MethodDelete && r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, apiError{"Content-Type must be application/json"})
			return false
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		return true
	}
	mux.HandleFunc("POST /api/write/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !writeGuard(w, r) {
			return
		}
		var body struct {
			Value json.RawMessage `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Value) == 0 {
			respond(w, errors.New(`body must be {"value": ...}`))
			return
		}
		t, ok := m.Tag(r.PathValue("name"))
		if !ok {
			respond(w, notFound("tag not found"))
			return
		}
		raw := strings.Trim(string(body.Value), `"`) // accepts 12, "12", true, "TRUE"
		v, err := t.ParseValue(raw)
		if err == nil {
			err = m.Write(t.Name, v)
		}
		if err == nil {
			log.Printf("WRITE  %-20s %-14s = %s (from %s)", t.Name, t.Address(), FormatValue(v), r.RemoteAddr)
		}
		respond(w, err)
	})
	mux.HandleFunc("POST /api/gen/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !writeGuard(w, r) {
			return
		}
		var spec GenSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		respond(w, m.StartGenerator(r.PathValue("name"), spec))
	})
	mux.HandleFunc("DELETE /api/gen/{name}", func(w http.ResponseWriter, r *http.Request) {
		if writeGuard(w, r) {
			m.StopGenerator(r.PathValue("name"))
			respond(w, nil)
		}
	})
	mux.HandleFunc("DELETE /api/gen", func(w http.ResponseWriter, r *http.Request) {
		// Stopping generators is always allowed from this computer, even
		// read-only (it only makes the program write less).
		if !canWrite(r) && !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"not allowed from this computer"})
			return
		}
		m.StopGenerator("")
		respond(w, nil)
	})

	// File → Exit: stops the program (closes the desktop window). Allowed
	// from this computer only, also in read-only mode.
	mux.HandleFunc("POST /api/exit", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"Exit only works on the computer running PLC Monitor"})
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, apiError{"Content-Type must be application/json"})
			return
		}
		if exitApp == nil {
			writeJSON(w, http.StatusNotImplemented, apiError{"exit is not available"})
			return
		}
		writeJSON(w, http.StatusOK, struct{ OK bool }{true})
		go func() {
			time.Sleep(200 * time.Millisecond) // let the reply reach the page first
			exitApp()
		}()
	})
	// Native Open/Save dialog of the desktop app; 501 in the browser version,
	// where the page asks for a path instead.
	mux.HandleFunc("POST /api/file/dialog", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		if nativeFileDialog == nil {
			writeJSON(w, http.StatusNotImplemented, apiError{"no native file dialog in this version"})
			return
		}
		var body struct {
			Kind string `json:"kind"`
			Save bool   `json:"save"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		k, err := parseFileKind(body.Kind)
		if err != nil {
			respond(w, err)
			return
		}
		path, ok := nativeFileDialog(k, body.Save, *k.path(m.Config()))
		writeJSON(w, http.StatusOK, struct {
			Path      string `json:"path"`
			Cancelled bool   `json:"cancelled"`
		}{path, !ok})
	})
	// Connect / Disconnect button: {"connect": false} closes the connection
	// and stops all traffic until {"connect": true}.
	mux.HandleFunc("POST /api/connection", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		var body struct {
			Connect bool `json:"connect"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		m.SetPaused(!body.Connect)
		log.Printf("%s requested by %s", map[bool]string{true: "connect", false: "disconnect"}[body.Connect], r.RemoteAddr)
		respond(w, nil)
	})
	mux.HandleFunc("PUT /api/network", edit(func(r *http.Request, c *Config) error {
		var n NetworkSettings
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			return fmt.Errorf("bad JSON: %w", err)
		}
		return n.applyTo(c)
	}))
	mux.HandleFunc("POST /api/tags", edit(func(r *http.Request, c *Config) error {
		t, err := decodeTag(r, c)
		if err != nil {
			return err
		}
		c.Tags = append(c.Tags, t)
		return nil
	}))
	// The whole tag list in one step: paste, delete several, insert at a
	// position, undo and redo in the tag table. All or nothing.
	mux.HandleFunc("PUT /api/tags", edit(func(r *http.Request, c *Config) error {
		var tags []Tag
		if err := json.NewDecoder(r.Body).Decode(&tags); err != nil {
			return fmt.Errorf("bad JSON: %w", err)
		}
		if len(tags) > 5000 {
			return errors.New("at most 5000 tags")
		}
		// A tag of the other protocol is refused when it is new; ones that
		// were already in the list (e.g. brought back by undo) stay, unpolled.
		had := map[string]Tag{}
		for _, t := range c.Tags {
			had[t.Name] = t
		}
		for _, t := range tags {
			if a, err := parseArea(t.Area); err == nil && a.Protocol() != c.Protocol {
				if old, ok := had[t.Name]; !ok || old.Area != t.Area {
					return fmt.Errorf("tag %q: area %s belongs to %s, but the network protocol is %s", t.Name, a, protoName(a.Protocol()), protoName(c.Protocol))
				}
			}
		}
		c.Tags = tags
		return nil
	}))
	mux.HandleFunc("PUT /api/tags/{name}", edit(func(r *http.Request, c *Config) error {
		i, err := findTag(c, r.PathValue("name"))
		if err != nil {
			return err
		}
		t, err := decodeTag(r, c)
		if err != nil {
			return err
		}
		c.Tags[i] = t
		return nil
	}))
	mux.HandleFunc("DELETE /api/tags/{name}", edit(func(r *http.Request, c *Config) error {
		i, err := findTag(c, r.PathValue("name"))
		if err != nil {
			return err
		}
		c.Tags = append(c.Tags[:i], c.Tags[i+1:]...)
		return nil
	}))
	return mux
}

type apiError struct {
	Error string `json:"error"`
}

// respond answers an API call: {"OK":true}, or the error with 404 for
// notFound and 400 otherwise.
func respond(w http.ResponseWriter, err error) {
	var nf notFound
	switch {
	case errors.As(err, &nf):
		writeJSON(w, http.StatusNotFound, apiError{err.Error()})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, apiError{err.Error()})
	default:
		writeJSON(w, http.StatusOK, struct{ OK bool }{true})
	}
}

type notFound string

func (n notFound) Error() string { return string(n) }

func findTag(c *Config, name string) (int, error) {
	for i := range c.Tags {
		if c.Tags[i].Name == name {
			return i, nil
		}
	}
	return 0, notFound(fmt.Sprintf("tag %q not found", name))
}

// decodeTag reads a tag from the request and checks that its area belongs to
// the current protocol (tag files may hold the other protocol's tags, but
// adding or editing one by hand is a mistake).
func decodeTag(r *http.Request, c *Config) (Tag, error) {
	var t Tag
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		return t, fmt.Errorf("bad JSON: %w", err)
	}
	if a, err := parseArea(t.Area); err == nil && a.Protocol() != c.Protocol {
		return t, fmt.Errorf("area %s belongs to %s, but the network protocol is %s", a, protoName(a.Protocol()), protoName(c.Protocol))
	}
	return t, nil
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// exitApp stops the program (File → Exit). Set by main, and replaced by the
// desktop build to close the window.
var exitApp func()

// setAlwaysOnTop keeps the desktop window (and its popups) above other
// windows. Set by the desktop build; nil elsewhere. startAlwaysOnTop is the
// saved state, applied when the window opens.
var (
	setAlwaysOnTop   func(on bool)
	startAlwaysOnTop bool
)

// nativeSaveDialog asks where to save an exported file (desktop build only).
var nativeSaveDialog func(name, label, ext string) (string, bool)

// nativeFileDialog shows an Open (save=false) or Save As dialog and returns
// the chosen path. It is set by the desktop build; nil elsewhere.
var nativeFileDialog func(k FileKind, save bool, current string) (string, bool)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
