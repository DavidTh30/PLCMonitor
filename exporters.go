package main

// More export targets, next to CSV, MQTT and InfluxDB:
//
//	databases   VictoriaMetrics, QuestDB (InfluxDB line protocol over HTTP),
//	            ClickHouse (HTTP, JSONEachRow), TimescaleDB (PostgreSQL)
//	messaging   AMQP (RabbitMQ …), CoAP                     (export_msg.go)
//	servers     Prometheus /metrics, gRPC, OPC UA server     (export_servers.go)
//
// The databases get every good value as a row/point, collected and written in
// one request every few seconds by batchSink. The servers keep the latest
// value of every tag and hand it out to whoever asks.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// LineDBConfig is a database that takes InfluxDB line protocol over HTTP
// (VictoriaMetrics, QuestDB).
type LineDBConfig struct {
	Enabled      bool   `yaml:"enabled" json:"enabled"`
	URL          string `yaml:"url" json:"url"`                 // VictoriaMetrics http://host:8428, QuestDB http://host:9000
	Measurement  string `yaml:"measurement" json:"measurement"` // QuestDB: the table
	Username     string `yaml:"username,omitempty" json:"username"`
	Password     string `yaml:"password,omitempty" json:"password"`
	Token        string `yaml:"token,omitempty" json:"token"` // Bearer token, instead of username/password
	FlushSeconds int    `yaml:"flush_seconds,omitempty" json:"flush_seconds"`
}

type TimescaleConfig struct {
	Enabled      bool   `yaml:"enabled" json:"enabled"`
	DSN          string `yaml:"dsn" json:"dsn"` // postgres://user:password@host:5432/db?sslmode=disable
	Table        string `yaml:"table" json:"table"`
	CreateTable  bool   `yaml:"create_table" json:"create_table"` // create the table (and hypertable) if missing
	FlushSeconds int    `yaml:"flush_seconds,omitempty" json:"flush_seconds"`
}

type ClickHouseConfig struct {
	Enabled      bool   `yaml:"enabled" json:"enabled"`
	URL          string `yaml:"url" json:"url"` // HTTP interface, http://host:8123
	Database     string `yaml:"database" json:"database"`
	Table        string `yaml:"table" json:"table"`
	Username     string `yaml:"username,omitempty" json:"username"`
	Password     string `yaml:"password,omitempty" json:"password"`
	CreateTable  bool   `yaml:"create_table" json:"create_table"`
	FlushSeconds int    `yaml:"flush_seconds,omitempty" json:"flush_seconds"`
}

type AMQPConfig struct {
	Enabled       bool   `yaml:"enabled" json:"enabled"`
	URL           string `yaml:"url" json:"url"`                       // amqp://user:password@host:5672/vhost
	Exchange      string `yaml:"exchange" json:"exchange"`             // e.g. amq.topic
	RoutingPrefix string `yaml:"routing_prefix" json:"routing_prefix"` // routing key <prefix>.<tag>
	Payload       string `yaml:"payload" json:"payload"`               // json or value
	Persistent    bool   `yaml:"persistent" json:"persistent"`
}

type CoAPConfig struct {
	Enabled     bool   `yaml:"enabled" json:"enabled"`
	URL         string `yaml:"url" json:"url"`       // coap://host:5683/plcmonitor → coap://host:5683/plcmonitor/<tag>
	Method      string `yaml:"method" json:"method"` // post or put
	Confirmable bool   `yaml:"confirmable" json:"confirmable"`
	Payload     string `yaml:"payload" json:"payload"` // json or value
}

type PrometheusConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Listen  string `yaml:"listen" json:"listen"` // :9273 → http://<this PC>:9273/metrics
}

type GRPCConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Listen  string `yaml:"listen" json:"listen"` // :50051
}

type OPCUAServerConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Listen  string `yaml:"listen" json:"listen"` // :4840 → opc.tcp://<this PC>:4840
}

// applyMoreDefaults fills in the defaults of the newer targets.
func (e *ExportConfig) applyMoreDefaults() {
	def := func(s *string, v string) {
		*s = strings.TrimSpace(*s)
		if *s == "" {
			*s = v
		}
	}
	defN := func(n *int) {
		if *n <= 0 {
			*n = 5
		}
	}
	def(&e.VictoriaMetrics.Measurement, "plc")
	defN(&e.VictoriaMetrics.FlushSeconds)
	def(&e.QuestDB.Measurement, "plc_values")
	defN(&e.QuestDB.FlushSeconds)
	def(&e.TimescaleDB.Table, "plc_values")
	defN(&e.TimescaleDB.FlushSeconds)
	def(&e.ClickHouse.Database, "default")
	def(&e.ClickHouse.Table, "plc_values")
	defN(&e.ClickHouse.FlushSeconds)
	def(&e.AMQP.Exchange, "amq.topic")
	def(&e.AMQP.RoutingPrefix, "plcmonitor")
	def(&e.AMQP.Payload, "json")
	def(&e.CoAP.Method, "post")
	def(&e.CoAP.Payload, "json")
	def(&e.Prometheus.Listen, ":9273")
	def(&e.GRPC.Listen, ":50051")
	def(&e.OPCUAServer.Listen, ":4840")
	for _, u := range []*string{&e.VictoriaMetrics.URL, &e.QuestDB.URL, &e.ClickHouse.URL} {
		*u = strings.TrimRight(strings.TrimSpace(*u), "/")
	}
}

var sqlName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// validateMore checks the enabled newer targets.
func (e *ExportConfig) validateMore() error {
	httpURL := func(what, s string) error {
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%s: the URL must look like http://host:port", what)
		}
		return nil
	}
	listen := func(what, s string) error {
		if _, port, err := net.SplitHostPort(s); err != nil || port == "" {
			return fmt.Errorf("%s: listen address %q must be [host]:port, e.g. :9273", what, s)
		}
		return nil
	}
	if v := e.VictoriaMetrics; v.Enabled {
		if err := httpURL("VictoriaMetrics", v.URL); err != nil {
			return err
		}
	}
	if q := e.QuestDB; q.Enabled {
		if err := httpURL("QuestDB", q.URL); err != nil {
			return err
		}
		if !sqlName.MatchString(q.Measurement) {
			return fmt.Errorf("QuestDB: table %q: use letters, digits and _", q.Measurement)
		}
	}
	if c := e.ClickHouse; c.Enabled {
		if err := httpURL("ClickHouse", c.URL); err != nil {
			return err
		}
		if !sqlName.MatchString(c.Database) || !sqlName.MatchString(c.Table) {
			return fmt.Errorf("ClickHouse: database and table names: use letters, digits and _")
		}
	}
	if t := e.TimescaleDB; t.Enabled {
		if _, err := pgx.ParseConfig(t.DSN); err != nil {
			return fmt.Errorf("TimescaleDB: connection string: %v (e.g. postgres://user:password@host:5432/plc?sslmode=disable)", err)
		}
		if !sqlName.MatchString(t.Table) {
			return fmt.Errorf("TimescaleDB: table %q: use letters, digits and _", t.Table)
		}
	}
	if a := e.AMQP; a.Enabled {
		u, err := url.Parse(a.URL)
		if err != nil || (u.Scheme != "amqp" && u.Scheme != "amqps") || u.Host == "" {
			return fmt.Errorf("AMQP: the URL must look like amqp://user:password@host:5672/")
		}
	}
	if c := e.CoAP; c.Enabled {
		u, err := url.Parse(c.URL)
		if err != nil || u.Scheme != "coap" || u.Host == "" {
			return fmt.Errorf("CoAP: the URL must look like coap://host:5683/path")
		}
		if c.Method != "post" && c.Method != "put" {
			return fmt.Errorf("CoAP: method must be post or put")
		}
	}
	for _, p := range []string{e.AMQP.Payload, e.CoAP.Payload} {
		if p != "json" && p != "value" {
			return fmt.Errorf("payload must be json or value")
		}
	}
	if e.Prometheus.Enabled {
		if err := listen("Prometheus", e.Prometheus.Listen); err != nil {
			return err
		}
	}
	if e.GRPC.Enabled {
		if err := listen("gRPC", e.GRPC.Listen); err != nil {
			return err
		}
	}
	if e.OPCUAServer.Enabled {
		if err := listen("OPC UA server", e.OPCUAServer.Listen); err != nil {
			return err
		}
	}
	return nil
}

// buildMoreSinks adds the newer targets that are enabled.
func buildMoreSinks(c ExportConfig) ([]Sink, error) {
	var out []Sink
	if c.VictoriaMetrics.Enabled {
		out = append(out, newLineDBSink("victoriametrics", c.VictoriaMetrics))
	}
	if c.QuestDB.Enabled {
		out = append(out, newLineDBSink("questdb", c.QuestDB))
	}
	if c.ClickHouse.Enabled {
		out = append(out, newClickHouseSink(c.ClickHouse))
	}
	if c.TimescaleDB.Enabled {
		out = append(out, newTimescaleSink(c.TimescaleDB))
	}
	if c.AMQP.Enabled {
		out = append(out, newAMQPSink(c.AMQP))
	}
	if c.CoAP.Enabled {
		s, err := newCoAPSink(c.CoAP)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if c.Prometheus.Enabled {
		s, err := newPrometheusSink(c.Prometheus)
		if err != nil {
			closeAll(out)
			return nil, err
		}
		out = append(out, s)
	}
	if c.GRPC.Enabled {
		s, err := newGRPCSink(c.GRPC)
		if err != nil {
			closeAll(out)
			return nil, err
		}
		out = append(out, s)
	}
	if c.OPCUAServer.Enabled {
		s, err := newOPCUAServerSink(c.OPCUAServer)
		if err != nil {
			closeAll(out)
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func closeAll(s []Sink) {
	for _, x := range s {
		x.Close()
	}
}

// ---- batching for the databases ----

// point is one good value to store.
type point struct {
	Time    time.Time
	Tag     string
	Address string
	Type    string
	Value   float64
}

func pointsOf(changes []Change) []point {
	var out []point
	for _, c := range changes {
		if !c.Good {
			continue
		}
		if _, ok := influxValue(c.New); !ok {
			continue
		}
		out = append(out, point{c.Time, c.Tag.Name, c.Address, c.Tag.Type, toFloat(c.New)})
	}
	return out
}

// batchSink collects points and hands them to flush every few seconds. On
// failure the points are kept (up to maxKept) and tried again.
type batchSink struct {
	name, target string
	every        time.Duration
	flush        func([]point) error
	closeFn      func()
	mu           sync.Mutex
	buf          []point
	stats        sinkStats
	stop, done   chan struct{}
}

const maxKept = 200000

func newBatchSink(name, target string, seconds int, flush func([]point) error) *batchSink {
	if seconds <= 0 {
		seconds = 5
	}
	b := &batchSink{name: name, target: target, every: time.Duration(seconds) * time.Second, flush: flush,
		stop: make(chan struct{}), done: make(chan struct{})}
	go b.loop()
	return b
}

func (b *batchSink) Name() string { return b.name }

func (b *batchSink) Write(changes []Change) error {
	p := pointsOf(changes)
	b.mu.Lock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - maxKept; over > 0 {
		b.buf = b.buf[over:] // drop the oldest rather than grow without bound
	}
	b.mu.Unlock()
	return nil
}

func (b *batchSink) loop() {
	defer close(b.done)
	t := time.NewTicker(b.every)
	defer t.Stop()
	for {
		select {
		case <-b.stop:
			b.flushNow()
			return
		case <-t.C:
			b.flushNow()
		}
	}
}

func (b *batchSink) flushNow() {
	b.mu.Lock()
	pts := b.buf
	b.buf = nil
	b.mu.Unlock()
	if len(pts) == 0 {
		return
	}
	if err := b.flush(pts); err != nil {
		if b.stats.fail(err) {
			log.Printf("export %s: %v (%d values kept for later)", b.name, err, len(pts))
		}
		if isPermanent(err) { // the server refuses this data: trying again won't help
			return
		}
		b.mu.Lock()
		b.buf = append(pts, b.buf...)
		if over := len(b.buf) - maxKept; over > 0 {
			b.buf = b.buf[over:]
		}
		b.mu.Unlock()
		return
	}
	b.stats.ok(len(pts))
}

func (b *batchSink) Status() SinkStatus {
	st := SinkStatus{Name: b.name, Target: b.target}
	b.mu.Lock()
	st.Pending = len(b.buf)
	b.mu.Unlock()
	b.stats.fill(&st)
	return st
}

func (b *batchSink) Close() error {
	close(b.stop)
	select {
	case <-b.done:
	case <-time.After(10 * time.Second):
	}
	if b.closeFn != nil {
		b.closeFn()
	}
	return nil
}

// httpPost sends body and turns a non-2xx answer into an error.
func httpPost(cl *http.Client, u string, body []byte, ctype string, auth func(*http.Request)) error {
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if auth != nil {
		auth(req)
	}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		return &httpError{code: resp.StatusCode, msg: serverMessage(b.Bytes())}
	}
	return nil
}

func basicOrBearer(user, pass, token string) func(*http.Request) {
	return func(r *http.Request) {
		switch {
		case token != "":
			r.Header.Set("Authorization", "Bearer "+token)
		case user != "":
			r.SetBasicAuth(user, pass)
		}
	}
}

// ---- VictoriaMetrics and QuestDB: line protocol ----

func lineWriteURL(name string, c LineDBConfig) string {
	// VictoriaMetrics: /write (also /influx/write); QuestDB: /write (ILP over HTTP).
	return c.URL + "/write?precision=ms"
}

func linesOf(measurement string, pts []point) []byte {
	var b bytes.Buffer
	for _, p := range pts {
		b.WriteString(influxLine(measurement, p.Tag, p.Address, p.Type, strconv.FormatFloat(p.Value, 'g', -1, 64), p.Time))
	}
	return b.Bytes()
}

func newLineDBSink(name string, c LineDBConfig) *batchSink {
	cl := &http.Client{Timeout: 15 * time.Second}
	u := lineWriteURL(name, c)
	auth := basicOrBearer(c.Username, c.Password, c.Token)
	return newBatchSink(name, c.URL, c.FlushSeconds, func(pts []point) error {
		return httpPost(cl, u, linesOf(c.Measurement, pts), "text/plain; charset=utf-8", auth)
	})
}

// ---- ClickHouse: HTTP interface ----

func clickhouseCreateSQL(c ClickHouseConfig) string {
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (time DateTime64(3, 'UTC'), tag LowCardinality(String), "+
		"address String, type LowCardinality(String), value Float64) ENGINE = MergeTree ORDER BY (tag, time)", c.Database, c.Table)
}

func clickhouseRows(pts []point) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, p := range pts {
		enc.Encode(map[string]any{
			"time": p.Time.UTC().Format("2006-01-02 15:04:05.000"), "tag": p.Tag,
			"address": p.Address, "type": p.Type, "value": p.Value,
		})
	}
	return b.Bytes()
}

func clickhouseQuery(cl *http.Client, c ClickHouseConfig, query string, body []byte) error {
	u := c.URL + "/?" + url.Values{"query": {query}}.Encode()
	return httpPost(cl, u, body, "text/plain; charset=utf-8", func(r *http.Request) {
		if c.Username != "" {
			r.Header.Set("X-ClickHouse-User", c.Username)
			r.Header.Set("X-ClickHouse-Key", c.Password)
		}
	})
}

func newClickHouseSink(c ClickHouseConfig) *batchSink {
	cl := &http.Client{Timeout: 15 * time.Second}
	created := !c.CreateTable
	insert := fmt.Sprintf("INSERT INTO %s.%s FORMAT JSONEachRow", c.Database, c.Table)
	return newBatchSink("clickhouse", c.URL, c.FlushSeconds, func(pts []point) error {
		if !created {
			if err := clickhouseQuery(cl, c, clickhouseCreateSQL(c), nil); err != nil {
				return fmt.Errorf("create table: %w", err)
			}
			created = true
		}
		return clickhouseQuery(cl, c, insert, clickhouseRows(pts))
	})
}

// ---- TimescaleDB: PostgreSQL ----

func timescaleCreateSQL(table string) []string {
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (time TIMESTAMPTZ NOT NULL, tag TEXT NOT NULL, address TEXT, type TEXT, value DOUBLE PRECISION)`, table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_tag_time ON %s (tag, time DESC)`, table, table),
	}
}

func newTimescaleSink(c TimescaleConfig) *batchSink {
	var conn *pgx.Conn
	created := !c.CreateTable
	target := c.DSN
	if cfg, err := pgx.ParseConfig(c.DSN); err == nil {
		target = fmt.Sprintf("postgres://%s:%d/%s", cfg.Host, cfg.Port, cfg.Database) // without the password
	}
	b := newBatchSink("timescaledb", target, c.FlushSeconds, func(pts []point) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if conn == nil || conn.IsClosed() {
			var err error
			if conn, err = pgx.Connect(ctx, c.DSN); err != nil {
				conn = nil
				return err
			}
		}
		if !created {
			for _, q := range timescaleCreateSQL(c.Table) {
				if _, err := conn.Exec(ctx, q); err != nil {
					return fmt.Errorf("create table: %w", err)
				}
			}
			// A hypertable when the TimescaleDB extension is there; a plain
			// PostgreSQL table works too.
			if _, err := conn.Exec(ctx, `SELECT create_hypertable($1, 'time', if_not_exists => TRUE, migrate_data => TRUE)`, c.Table); err != nil {
				log.Printf("export timescaledb: %s stays a plain table: %v", c.Table, err)
			}
			created = true
		}
		rows := make([][]any, len(pts))
		for i, p := range pts {
			rows[i] = []any{p.Time, p.Tag, p.Address, p.Type, p.Value}
		}
		_, err := conn.CopyFrom(ctx, pgx.Identifier{c.Table}, []string{"time", "tag", "address", "type", "value"}, pgx.CopyFromRows(rows))
		if err != nil {
			conn.Close(context.Background())
			conn = nil
		}
		return err
	})
	b.closeFn = func() {
		if conn != nil {
			conn.Close(context.Background())
		}
	}
	return b
}

// ---- Test connection for the newer targets ----

func lineDBTest(name string, c LineDBConfig) TestResult {
	var r TestResult
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	if c.URL == "" {
		r.add("Server address", false, "enter the URL, e.g. %s", map[string]string{"victoriametrics": "http://192.168.1.20:8428", "questdb": "http://192.168.1.20:9000"}[name])
		return r.done()
	}
	cl := &http.Client{Timeout: 5 * time.Second}
	ports := map[string]string{"victoriametrics": "8428", "questdb": "9000"}[name]
	health := map[string]string{"victoriametrics": "/health", "questdb": "/ping"}[name]
	req, _ := http.NewRequest(http.MethodGet, c.URL+health, nil)
	basicOrBearer(c.Username, c.Password, c.Token)(req)
	resp, err := cl.Do(req)
	if err != nil {
		r.add("Reach the server", false, "%s", withHint(err, ports))
		return r.done()
	}
	resp.Body.Close()
	r.add("Reach the server", resp.StatusCode/100 == 2 || resp.StatusCode == 204, "%s%s answers HTTP %d", c.URL, health, resp.StatusCode)
	line := influxLine(c.Measurement, "_plcmonitor_test", "-", "REAL", "1", time.Now())
	if err := httpPost(cl, lineWriteURL(name, c), []byte(line), "text/plain; charset=utf-8", basicOrBearer(c.Username, c.Password, c.Token)); err != nil {
		r.add("Write a test point", false, "%v", err)
	} else {
		r.add("Write a test point", true, "wrote %s,tag=_plcmonitor_test value=1", c.Measurement)
	}
	return r.done()
}

func clickhouseTest(c ClickHouseConfig) TestResult {
	var r TestResult
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	if c.URL == "" {
		r.add("Server address", false, "enter the URL of the HTTP interface, e.g. http://192.168.1.20:8123")
		return r.done()
	}
	cl := &http.Client{Timeout: 5 * time.Second}
	if err := clickhouseQuery(cl, c, "SELECT version()", nil); err != nil {
		r.add("Log in", false, "%s", withHint(err, "8123 (HTTP), 8443 (HTTPS)"))
		return r.done()
	}
	r.add("Log in", true, "ClickHouse answers at %s", c.URL)
	if c.CreateTable {
		if err := clickhouseQuery(cl, c, clickhouseCreateSQL(c), nil); err != nil {
			r.add("Table", false, "create %s.%s: %v", c.Database, c.Table, err)
			return r.done()
		}
		r.add("Table", true, "%s.%s exists (created if it was missing)", c.Database, c.Table)
	} else if err := clickhouseQuery(cl, c, fmt.Sprintf("SELECT count() FROM %s.%s", c.Database, c.Table), nil); err != nil {
		r.add("Table", false, "%s.%s: %v – tick Create the table, or create it yourself", c.Database, c.Table, err)
	} else {
		r.add("Table", true, "%s.%s exists", c.Database, c.Table)
	}
	return r.done()
}

func timescaleTest(c TimescaleConfig) TestResult {
	var r TestResult
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, c.DSN)
	if err != nil {
		r.add("Connect", false, "%s", withHint(err, "5432"))
		return r.done()
	}
	defer conn.Close(context.Background())
	var ver string
	conn.QueryRow(ctx, "SHOW server_version").Scan(&ver)
	r.add("Connect", true, "PostgreSQL %s", ver)
	var ts string
	if err := conn.QueryRow(ctx, "SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'").Scan(&ts); err != nil {
		r.warn("TimescaleDB", "the timescaledb extension is not installed in this database (CREATE EXTENSION timescaledb;) – values are still stored, in a plain table")
	} else {
		r.add("TimescaleDB", true, "extension version %s", ts)
	}
	var exists bool
	conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", c.Table).Scan(&exists)
	switch {
	case exists:
		r.add("Table", true, "%s exists", c.Table)
	case c.CreateTable:
		r.warn("Table", "%s does not exist yet; it is created with the first values", c.Table)
	default:
		r.add("Table", false, "%s does not exist – tick Create the table, or create it yourself", c.Table)
	}
	return r.done()
}

// moreTest runs Test connection for the newer targets.
func moreTest(kind string, c ExportConfig, running []SinkStatus) TestResult {
	switch kind {
	case "victoriametrics":
		return lineDBTest(kind, c.VictoriaMetrics)
	case "questdb":
		return lineDBTest(kind, c.QuestDB)
	case "clickhouse":
		return clickhouseTest(c.ClickHouse)
	case "timescaledb":
		return timescaleTest(c.TimescaleDB)
	case "amqp":
		return amqpTest(c.AMQP)
	case "coap":
		return coapTest(c.CoAP)
	case "prometheus":
		return serverTest("Prometheus", c.Prometheus.Listen, running, "prometheus")
	case "grpc":
		return serverTest("gRPC", c.GRPC.Listen, running, "grpc")
	case "opcua_server":
		return serverTest("the OPC UA server", c.OPCUAServer.Listen, running, "opcua-server")
	}
	var r TestResult
	r.add("Test", false, "unknown exporter %q", kind)
	return r.done()
}
