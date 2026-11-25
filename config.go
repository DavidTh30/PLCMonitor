package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole plcmonitor configuration file.
type Config struct {
	// NetworkFile and TagsFile are the network/tag files last opened or saved
	// from the File menu. The settings themselves are also kept below, so the
	// workspace (including unsaved edits) survives a restart.
	NetworkFile string `yaml:"network_file,omitempty"`
	TagsFile    string `yaml:"tags_file,omitempty"`

	Protocol       string        `yaml:"protocol"` // s7, modbus or opcua
	PLC            PLCConfig     `yaml:"plc"`
	Modbus         ModbusConfig  `yaml:"modbus"`
	OPCUA          OPCUAConfig   `yaml:"opcua,omitempty"`
	PollInterval   time.Duration `yaml:"poll_interval"`
	ReconnectDelay time.Duration `yaml:"reconnect_delay"`
	ReconnectMax   time.Duration `yaml:"reconnect_max"`
	Simulate       bool          `yaml:"simulate"`
	Simulator      SimConfig     `yaml:"simulator"`
	Web            WebConfig     `yaml:"web"`
	History        HistoryConfig `yaml:"history"`
	Export         ExportConfig  `yaml:"export"`
	Layout         LayoutConfig  `yaml:"layout,omitempty"`
	DCP            DCPConfig     `yaml:"dcp,omitempty"` // PROFINET device functions (Topology)
	Tags           []Tag         `yaml:"tags"`

	// Values as written in the file, before command-line overrides, so that
	// saving edits from the dashboard does not persist -simulate or -listen.
	fileSimulate bool
	fileListen   string
}

type PLCConfig struct {
	Address        string        `yaml:"address"` // host or host:port, port defaults to 102
	Rack           int           `yaml:"rack"`
	Slot           int           `yaml:"slot"`
	ConnectionType string        `yaml:"connection_type"` // pg, op or basic
	Timeout        time.Duration `yaml:"timeout"`
}

// ModbusConfig is used when protocol is modbus.
type ModbusConfig struct {
	Mode       string        `yaml:"mode"`        // tcp (TCP/IP), rtu (RS485/RS232 COM port) or rtuovertcp (RS485 via an Ethernet gateway)
	Address    string        `yaml:"address"`     // tcp, rtuovertcp: host or host:port (port 502 if omitted)
	SerialPort string        `yaml:"serial_port"` // rtu: e.g. COM3
	Baud       int           `yaml:"baud"`
	DataBits   int           `yaml:"data_bits"`
	Parity     string        `yaml:"parity"` // N, E or O
	StopBits   int           `yaml:"stop_bits"`
	UnitID     int           `yaml:"unit_id"`    // slave address, 1..247 (TCP gateways: often 1 or 255)
	WordOrder  string        `yaml:"word_order"` // big (high word first, default) or little
	MaxGap     int           `yaml:"max_gap"`    // unused registers/coils that may be read to merge requests
	Timeout    time.Duration `yaml:"timeout"`
}

type SimConfig struct {
	// DropEvery makes the simulated link fail periodically so the reconnect
	// logic can be exercised without hardware. Zero disables it.
	DropEvery time.Duration `yaml:"drop_every"`
	// DownFor is how long the simulated PLC stays unreachable after a drop.
	DownFor time.Duration `yaml:"down_for"`
}

type WebConfig struct {
	Listen   string `yaml:"listen"`    // empty disables the dashboard
	ReadOnly bool   `yaml:"read_only"` // true hides network/tag editing and writing values
	// AllowRemoteWrite lets dashboards on other computers write values and
	// run signal generators. Off by default: only this computer can write.
	AllowRemoteWrite bool `yaml:"allow_remote_write"`
	// Theme is the colour theme chosen in View → Theme (see web/themes.js);
	// empty follows the Windows light/dark setting.
	Theme string `yaml:"theme,omitempty"`
	// AlwaysOnTop keeps the desktop window above other windows (View menu).
	AlwaysOnTop bool `yaml:"always_on_top,omitempty"`
	// NoAutoConnect starts disconnected until Connect (Tools → Connect
	// automatically at start, unticked). Default: connect at start.
	NoAutoConnect bool `yaml:"no_auto_connect,omitempty"`
}

// HistoryConfig controls the trend history files used by the charts.
type HistoryConfig struct {
	Disabled  bool          `yaml:"disabled"`  // history is on unless this is true
	Path      string        `yaml:"path"`      // folder; relative to the working folder
	Interval  time.Duration `yaml:"interval"`  // at most one sample per tag per interval
	Heartbeat time.Duration `yaml:"heartbeat"` // write an unchanged value at least this often
	KeepDays  int           `yaml:"keep_days"` // delete days older than this; default 30, -1 keeps everything
}

// LayoutConfig is the window layout: the charts (each with one or more tags,
// shown in the main window or in a window of its own) and the extra tag
// windows. It is kept here so the desktop app restores it after a restart.
type LayoutConfig struct {
	ChartPlace string            `yaml:"chart_place,omitempty" json:"chart_place"` // where new charts open: main (default) or window
	Charts     []ChartLayout     `yaml:"charts,omitempty" json:"charts"`
	TagWindows []TagWindowLayout `yaml:"tag_windows,omitempty" json:"tag_windows"`
	// StatusItems are the items ticked in the Status menu (empty: the defaults).
	StatusItems []string `yaml:"status_items,omitempty" json:"status_items"`
	// Columns are the tag table's shown columns (empty: all).
	Columns []string `yaml:"columns,omitempty" json:"columns"`
	// HeaderFolded: the button row under the menu bar is folded away.
	HeaderFolded bool `yaml:"header_folded,omitempty" json:"header_folded"`
	// TagTree: the tag list is shown as a tree; TreeClosed are its folded groups.
	TagTree    bool     `yaml:"tag_tree,omitempty" json:"tag_tree"`
	TreeClosed []string `yaml:"tree_closed,omitempty" json:"tree_closed"`
	// RowSpacing of the tag list: compact, comfortable, or "" (normal).
	RowSpacing string `yaml:"row_spacing,omitempty" json:"row_spacing"`
}

type ChartLayout struct {
	ID      string   `yaml:"id" json:"id"`
	Title   string   `yaml:"title,omitempty" json:"title"` // empty: made from the tag names
	Tags    []string `yaml:"tags" json:"tags"`
	Window  bool     `yaml:"window,omitempty" json:"window"`     // shown in its own window
	Scale   string   `yaml:"scale,omitempty" json:"scale"`       // shared (default) or separate
	RangeMs int64    `yaml:"range_ms,omitempty" json:"range_ms"` // shown period
}

type TagWindowLayout struct {
	ID    string   `yaml:"id" json:"id"`
	Title string   `yaml:"title" json:"title"`
	Tags  []string `yaml:"tags" json:"tags"`
}

var layoutID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

// validate checks a layout sent by the dashboard.
func (l *LayoutConfig) validate() error {
	if l.ChartPlace != "" && l.ChartPlace != "main" && l.ChartPlace != "window" {
		return fmt.Errorf("chart_place must be main or window")
	}
	if len(l.Charts) > 60 || len(l.TagWindows) > 30 || len(l.StatusItems) > 40 {
		return fmt.Errorf("too many charts, tag windows or status items")
	}
	if len(l.Columns) > 20 {
		return fmt.Errorf("too many columns")
	}
	switch l.RowSpacing {
	case "", "normal", "compact", "comfortable":
	default:
		return fmt.Errorf("row_spacing must be compact, normal or comfortable")
	}
	if len(l.TreeClosed) > 2000 {
		return fmt.Errorf("too many folded groups")
	}
	for _, g := range l.TreeClosed {
		if len(g) > 300 {
			return fmt.Errorf("folded group name too long")
		}
	}
	for _, s := range append(append([]string{}, l.StatusItems...), l.Columns...) {
		if !layoutID.MatchString(s) {
			return fmt.Errorf("bad status item or column %q", s)
		}
	}
	seen := map[string]bool{}
	for _, c := range l.Charts {
		if !layoutID.MatchString(c.ID) || seen[c.ID] {
			return fmt.Errorf("bad or duplicate chart id %q", c.ID)
		}
		seen[c.ID] = true
		if len(c.Tags) == 0 || len(c.Tags) > 16 {
			return fmt.Errorf("a chart needs 1 to 16 tags")
		}
		if c.Scale != "" && c.Scale != "shared" && c.Scale != "separate" {
			return fmt.Errorf("chart scale must be shared or separate")
		}
		if len(c.Title) > 100 || c.RangeMs < 0 || c.RangeMs > 400*24*3600e3 {
			return fmt.Errorf("bad chart title or range")
		}
	}
	for _, w := range l.TagWindows {
		if !layoutID.MatchString(w.ID) || seen[w.ID] {
			return fmt.Errorf("bad or duplicate tag window id %q", w.ID)
		}
		seen[w.ID] = true
		if strings.TrimSpace(w.Title) == "" || len(w.Title) > 100 || len(w.Tags) > 2000 {
			return fmt.Errorf("a tag window needs a name (at most 100 characters)")
		}
	}
	return nil
}

// ExportConfig is also the body of the Export settings API, hence the JSON tags.
type ExportConfig struct {
	CSV      CSVConfig      `yaml:"csv" json:"csv"`
	MQTT     MQTTConfig     `yaml:"mqtt" json:"mqtt"`
	InfluxDB InfluxDBConfig `yaml:"influxdb" json:"influxdb"`

	// More targets (exporters.go, export_msg.go, export_servers.go).
	VictoriaMetrics LineDBConfig      `yaml:"victoriametrics,omitempty" json:"victoriametrics"`
	QuestDB         LineDBConfig      `yaml:"questdb,omitempty" json:"questdb"`
	TimescaleDB     TimescaleConfig   `yaml:"timescaledb,omitempty" json:"timescaledb"`
	ClickHouse      ClickHouseConfig  `yaml:"clickhouse,omitempty" json:"clickhouse"`
	AMQP            AMQPConfig        `yaml:"amqp,omitempty" json:"amqp"`
	CoAP            CoAPConfig        `yaml:"coap,omitempty" json:"coap"`
	Prometheus      PrometheusConfig  `yaml:"prometheus,omitempty" json:"prometheus"`
	GRPC            GRPCConfig        `yaml:"grpc,omitempty" json:"grpc"`
	OPCUAServer     OPCUAServerConfig `yaml:"opcua_server,omitempty" json:"opcua_server"`
}

type CSVConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Path    string `yaml:"path" json:"path"`
}

type MQTTConfig struct {
	Enabled     bool   `yaml:"enabled" json:"enabled"`
	Broker      string `yaml:"broker" json:"broker"` // e.g. tcp://localhost:1883, ssl://host:8883, ws://host:9001
	ClientID    string `yaml:"client_id" json:"client_id"`
	Username    string `yaml:"username" json:"username"`
	Password    string `yaml:"password" json:"password"`
	TopicPrefix string `yaml:"topic_prefix" json:"topic_prefix"`
	QoS         byte   `yaml:"qos" json:"qos"`
	Retain      bool   `yaml:"retain" json:"retain"`
	// Payload is json (value, quality, address, type, ts) or value (just the
	// value as text, e.g. 42.5 or true), which suits simple HMIs and lamps.
	Payload string `yaml:"payload" json:"payload"`
	// TLSInsecure accepts any server certificate on ssl:// and wss://
	// (self-signed brokers). Only for testing.
	TLSInsecure bool `yaml:"tls_insecure,omitempty" json:"tls_insecure"`
}

type InfluxDBConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Version int    `yaml:"version" json:"version"` // 1, 2 (default) or 3
	URL     string `yaml:"url" json:"url"`         // e.g. http://localhost:8086 (v3 Core: http://localhost:8181)
	// v1: database (+ retention_policy, username, password)
	// v2: org, bucket, token
	// v3: database, token
	Database        string `yaml:"database" json:"database"`
	RetentionPolicy string `yaml:"retention_policy" json:"retention_policy"`
	Username        string `yaml:"username" json:"username"`
	Password        string `yaml:"password" json:"password"`
	Org             string `yaml:"org" json:"org"`
	Bucket          string `yaml:"bucket" json:"bucket"`
	Token           string `yaml:"token" json:"token"`
	Measurement     string `yaml:"measurement" json:"measurement"`
	// FlushInterval goes over JSON as text ("5s"), see influxJSON in export.go.
	FlushInterval time.Duration `yaml:"flush_interval" json:"-"`
}

// LoadConfig reads, defaults and validates a YAML config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.fileSimulate, cfg.fileListen = cfg.Simulate, cfg.Web.Listen
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.ReconnectDelay <= 0 {
		c.ReconnectDelay = 2 * time.Second
	}
	if c.ReconnectMax < c.ReconnectDelay {
		c.ReconnectMax = 30 * time.Second
	}
	if c.PLC.Timeout <= 0 {
		c.PLC.Timeout = 5 * time.Second
	}
	if c.PLC.ConnectionType == "" {
		c.PLC.ConnectionType = "pg"
	}
	c.Protocol = strings.ToLower(strings.TrimSpace(c.Protocol))
	if c.Protocol == "" {
		c.Protocol = ProtoS7
	}
	mb := &c.Modbus
	mb.Mode = strings.ToLower(strings.TrimSpace(mb.Mode))
	if mb.Mode == "" {
		mb.Mode = "tcp"
	}
	if mb.Baud <= 0 {
		mb.Baud = 9600
	}
	if mb.DataBits <= 0 {
		mb.DataBits = 8
	}
	mb.Parity = strings.ToUpper(strings.TrimSpace(mb.Parity))
	if mb.Parity == "" {
		mb.Parity = "N"
	}
	if mb.StopBits <= 0 {
		mb.StopBits = 1
	}
	if mb.UnitID <= 0 {
		mb.UnitID = 1
	}
	mb.WordOrder = strings.ToLower(strings.TrimSpace(mb.WordOrder))
	if mb.WordOrder == "" {
		mb.WordOrder = "big"
	}
	if mb.Timeout <= 0 {
		mb.Timeout = 2 * time.Second
	}
	c.OPCUA.applyDefaults()
	if c.Simulator.DownFor <= 0 {
		c.Simulator.DownFor = 5 * time.Second
	}
	if c.History.Path == "" {
		c.History.Path = "history"
	}
	if c.History.Interval <= 0 {
		c.History.Interval = time.Second
	}
	if c.History.Heartbeat <= 0 {
		c.History.Heartbeat = time.Minute
	}
	if c.History.KeepDays == 0 {
		c.History.KeepDays = 30
	}
	if c.Export.CSV.Path == "" {
		c.Export.CSV.Path = "changes.csv"
	}
	if c.Export.MQTT.ClientID == "" {
		c.Export.MQTT.ClientID = "plcmonitor"
	}
	if c.Export.MQTT.TopicPrefix == "" {
		c.Export.MQTT.TopicPrefix = "plcmonitor"
	}
	c.Export.MQTT.Broker = strings.TrimSpace(c.Export.MQTT.Broker)
	c.Export.MQTT.TopicPrefix = strings.Trim(strings.TrimSpace(c.Export.MQTT.TopicPrefix), "/")
	c.Export.MQTT.Payload = strings.ToLower(strings.TrimSpace(c.Export.MQTT.Payload))
	if c.Export.MQTT.Payload == "" {
		c.Export.MQTT.Payload = "json"
	}
	c.Export.InfluxDB.URL = strings.TrimRight(strings.TrimSpace(c.Export.InfluxDB.URL), "/")
	if c.DCP.Source == "" {
		c.DCP.Source = defaultDCPSource()
	}
	if c.Export.InfluxDB.Measurement == "" {
		c.Export.InfluxDB.Measurement = "plc"
	}
	if c.Export.InfluxDB.Version == 0 {
		c.Export.InfluxDB.Version = 2 // what older configs (url/org/bucket/token) meant
	}
	if c.Export.InfluxDB.Version == 3 && c.Export.InfluxDB.Database == "" {
		c.Export.InfluxDB.Database = c.Export.InfluxDB.Bucket // v3 calls it a database
	}
	c.Export.applyMoreDefaults()
	if c.Export.InfluxDB.FlushInterval <= 0 {
		c.Export.InfluxDB.FlushInterval = 5 * time.Second
	}
}

func (c *Config) validate() error {
	if err := c.validateNetwork(); err != nil {
		return err
	}
	seen := map[string]bool{}
	uaOff := 0
	for i := range c.Tags {
		t := &c.Tags[i]
		if err := t.normalize(c.Protocol); err != nil {
			return fmt.Errorf("tag #%d (%q): %w", i+1, t.Name, err)
		}
		if seen[t.Name] {
			return fmt.Errorf("duplicate tag name %q", t.Name)
		}
		seen[t.Name] = true
		t.swapWords = c.Protocol == ProtoModbus && c.Modbus.WordOrder == "little"
		if t.area == AreaUA { // each OPC UA tag gets an 8-byte slot in the UA image
			t.uaOff = uaOff
			uaOff += 8
		}
	}
	if err := c.DCP.validate(); err != nil {
		return err
	}
	return c.Export.validate()
}

// validate checks the export settings; the enabled exporters must be usable.
func (e *ExportConfig) validate() error {
	if e.CSV.Enabled && strings.TrimSpace(e.CSV.Path) == "" {
		return fmt.Errorf("export.csv.path is required when CSV is enabled")
	}
	mq := e.MQTT
	if mq.Enabled {
		if mq.Broker == "" {
			return fmt.Errorf("export.mqtt.broker is required when MQTT is enabled")
		}
		if err := checkBrokerURL(mq.Broker); err != nil {
			return err
		}
		if strings.ContainsAny(mq.TopicPrefix, "#+") {
			return fmt.Errorf("export.mqtt.topic_prefix must not contain # or +")
		}
	}
	if mq.QoS > 2 {
		return fmt.Errorf("export.mqtt.qos must be 0, 1 or 2")
	}
	if mq.Payload != "json" && mq.Payload != "value" {
		return fmt.Errorf("export.mqtt.payload must be json or value")
	}
	if err := e.validateMore(); err != nil {
		return err
	}
	if ix := e.InfluxDB; ix.Enabled {
		switch {
		case ix.URL == "":
			return fmt.Errorf("export.influxdb.url is required when InfluxDB is enabled")
		case !strings.HasPrefix(ix.URL, "http://") && !strings.HasPrefix(ix.URL, "https://"):
			return fmt.Errorf("export.influxdb.url must start with http:// or https:// (e.g. http://localhost:8086)")
		case ix.Version == 1 && ix.Database == "":
			return fmt.Errorf("export.influxdb.database is required for InfluxDB 1.x")
		case ix.Version == 2 && ix.Bucket == "":
			return fmt.Errorf("export.influxdb.bucket is required for InfluxDB 2.x")
		case ix.Version == 3 && ix.Database == "":
			return fmt.Errorf("export.influxdb.database is required for InfluxDB 3")
		case ix.Version < 1 || ix.Version > 3:
			return fmt.Errorf("export.influxdb.version must be 1, 2 or 3")
		}
	}
	return nil
}

func (c *Config) validateNetwork() error {
	// An empty address is allowed (e.g. simulator switched off before the
	// PLC's IP is known): the program then stays disconnected and says why,
	// see missingTarget.
	switch c.Protocol {
	case ProtoS7:
		if _, err := connectionTypeCode(c.PLC.ConnectionType); err != nil {
			return err
		}
	case ProtoModbus:
		mb := c.Modbus
		switch mb.Mode {
		case "tcp", "rtu", "rtuovertcp":
		default:
			return fmt.Errorf("modbus.mode %q: want tcp, rtu or rtuovertcp", mb.Mode)
		}
		if mb.UnitID < 0 || mb.UnitID > 255 {
			return fmt.Errorf("Modbus unit ID must be 0..255")
		}
		if mb.Parity != "N" && mb.Parity != "E" && mb.Parity != "O" {
			return fmt.Errorf("Modbus parity must be N, E or O")
		}
		if mb.DataBits < 5 || mb.DataBits > 8 || mb.StopBits < 1 || mb.StopBits > 2 {
			return fmt.Errorf("Modbus data bits must be 5..8 and stop bits 1 or 2")
		}
		if mb.WordOrder != "big" && mb.WordOrder != "little" {
			return fmt.Errorf("Modbus word order must be big or little")
		}
		if mb.MaxGap < 0 || mb.MaxGap > 100 {
			return fmt.Errorf("Modbus max gap must be 0..100")
		}
	case ProtoOPCUA:
		return c.OPCUA.validate()
	default:
		return fmt.Errorf("protocol %q: want s7, modbus or opcua", c.Protocol)
	}
	return nil
}

// missingTarget says what is missing to connect to a real device, or "".
func missingTarget(c *Config) string {
	if c.Simulate {
		return ""
	}
	if c.Protocol == ProtoModbus {
		switch {
		case c.Modbus.Mode == "rtu" && strings.TrimSpace(c.Modbus.SerialPort) == "":
			return "no serial port set – choose the COM port in Network settings, or turn the simulator on"
		case c.Modbus.Mode == "rtuovertcp" && strings.TrimSpace(c.Modbus.Address) == "":
			return "no gateway address set – enter the RS485 gateway's IP address in Network settings, or turn the simulator on"
		case c.Modbus.Mode == "tcp" && strings.TrimSpace(c.Modbus.Address) == "":
			return "no device address set – enter the Modbus device's IP address in Network settings, or turn the simulator on"
		}
		return ""
	}
	if c.Protocol == ProtoOPCUA {
		if c.OPCUA.Endpoint == "" {
			return "no OPC UA endpoint set – enter it in Network settings (e.g. opc.tcp://192.168.0.1:4840), or turn the simulator on"
		}
		return ""
	}
	if strings.TrimSpace(c.PLC.Address) == "" {
		return "no PLC address set – enter the PLC's IP address in Network settings, or turn the simulator on"
	}
	return ""
}

// clone returns a copy that can be edited without touching c.
func (c *Config) clone() *Config {
	n := *c
	n.Tags = append([]Tag(nil), c.Tags...)
	return &n
}

// SaveConfig writes cfg back to path as YAML. The first save keeps the
// original file as path.bak, because comments in it are not preserved.
func SaveConfig(path string, cfg *Config) error {
	out := cfg.clone()
	out.Simulate, out.Web.Listen = cfg.fileSimulate, cfg.fileListen
	data, err := yaml.Marshal(out)
	if err != nil {
		return err
	}
	data = append([]byte("# plcmonitor configuration (last saved from the dashboard).\n"+
		"# The original hand-written file, with comments, is in "+filepath.Base(path)+".bak.\n\n"), data...)
	if _, err := os.Stat(path + ".bak"); os.IsNotExist(err) {
		if orig, err := os.ReadFile(path); err == nil {
			if err := os.WriteFile(path+".bak", orig, 0o644); err != nil {
				return err
			}
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func connectionTypeCode(s string) (int, error) {
	switch strings.ToLower(s) {
	case "pg":
		return 1, nil
	case "op":
		return 2, nil
	case "basic":
		return 3, nil
	}
	return 0, fmt.Errorf("plc.connection_type %q: want pg, op or basic", s)
}
