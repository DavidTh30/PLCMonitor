package main

// Export settings window: the settings API, and the "Test connection" and
// "Create" helpers for InfluxDB, MQTT and the CSV file.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// exportJSON is the Export settings as sent to and from the window.
type exportJSON struct {
	CSV      CSVConfig  `json:"csv"`
	MQTT     MQTTConfig `json:"mqtt"`
	InfluxDB influxJSON `json:"influxdb"`

	VictoriaMetrics LineDBConfig      `json:"victoriametrics"`
	QuestDB         LineDBConfig      `json:"questdb"`
	TimescaleDB     TimescaleConfig   `json:"timescaledb"`
	ClickHouse      ClickHouseConfig  `json:"clickhouse"`
	AMQP            AMQPConfig        `json:"amqp"`
	CoAP            CoAPConfig        `json:"coap"`
	Prometheus      PrometheusConfig  `json:"prometheus"`
	GRPC            GRPCConfig        `json:"grpc"`
	OPCUAServer     OPCUAServerConfig `json:"opcua_server"`
}

// influxJSON carries the flush interval as text ("5s").
type influxJSON struct {
	InfluxDBConfig
	FlushInterval string `json:"flush_interval"`
}

func exportOf(c ExportConfig) exportJSON {
	return exportJSON{CSV: c.CSV, MQTT: c.MQTT,
		InfluxDB:        influxJSON{c.InfluxDB, c.InfluxDB.FlushInterval.String()},
		VictoriaMetrics: c.VictoriaMetrics, QuestDB: c.QuestDB, TimescaleDB: c.TimescaleDB, ClickHouse: c.ClickHouse,
		AMQP: c.AMQP, CoAP: c.CoAP, Prometheus: c.Prometheus, GRPC: c.GRPC, OPCUAServer: c.OPCUAServer}
}

func (ij influxJSON) config() (InfluxDBConfig, error) {
	c := ij.InfluxDBConfig
	c.FlushInterval = 5 * time.Second
	if s := strings.TrimSpace(ij.FlushInterval); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < time.Second || d > 10*time.Minute {
			return c, fmt.Errorf("InfluxDB flush interval: %q must be a duration from 1s to 10m", s)
		}
		c.FlushInterval = d
	}
	return c, nil
}

func (e exportJSON) config() (ExportConfig, error) {
	ix, err := e.InfluxDB.config()
	c := ExportConfig{CSV: e.CSV, MQTT: e.MQTT, InfluxDB: ix,
		VictoriaMetrics: e.VictoriaMetrics, QuestDB: e.QuestDB, TimescaleDB: e.TimescaleDB, ClickHouse: e.ClickHouse,
		AMQP: e.AMQP, CoAP: e.CoAP, Prometheus: e.Prometheus, GRPC: e.GRPC, OPCUAServer: e.OPCUAServer}
	c.applyMoreDefaults()
	return c, err
}

var brokerSchemes = []string{"tcp", "mqtt", "ssl", "tls", "mqtts", "ws", "wss"}

// checkBrokerURL accepts the broker URL forms the MQTT library understands.
func checkBrokerURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return fmt.Errorf("MQTT broker %q: use scheme://host:port, e.g. tcp://192.168.1.10:1883", s)
	}
	for _, sc := range brokerSchemes {
		if u.Scheme == sc {
			if u.Port() == "" && sc != "ws" && sc != "wss" {
				return fmt.Errorf("MQTT broker %q: add the port, e.g. %s://%s:%s", s, sc, u.Hostname(), map[bool]string{true: "8883", false: "1883"}[sc == "ssl" || sc == "tls" || sc == "mqtts"])
			}
			return nil
		}
	}
	return fmt.Errorf("MQTT broker %q: scheme must be one of %s", s, strings.Join(brokerSchemes, ", "))
}

// ---- Test results ----

type TestStep struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Warn   bool   `json:"warn,omitempty"` // OK, but worth a look
	Detail string `json:"detail"`
}

type MQTTSample struct {
	Topic    string `json:"topic"`
	Payload  string `json:"payload"`
	Retained bool   `json:"retained"`
	Test     bool   `json:"test,omitempty"` // the test message itself
}

type TestResult struct {
	OK            bool         `json:"ok"`
	Steps         []TestStep   `json:"steps"`
	ServerVersion string       `json:"server_version,omitempty"`
	Version       int          `json:"version,omitempty"`   // detected InfluxDB major version
	Databases     []string     `json:"databases,omitempty"` // databases or buckets on the server
	Missing       bool         `json:"missing,omitempty"`   // the configured one does not exist (can be created)
	Messages      []MQTTSample `json:"messages,omitempty"`
}

func (r *TestResult) add(name string, ok bool, format string, args ...any) {
	r.Steps = append(r.Steps, TestStep{Name: name, OK: ok, Detail: fmt.Sprintf(format, args...)})
}

func (r *TestResult) warn(name string, format string, args ...any) {
	r.Steps = append(r.Steps, TestStep{Name: name, OK: true, Warn: true, Detail: fmt.Sprintf(format, args...)})
}

func (r *TestResult) done() TestResult {
	r.OK = len(r.Steps) > 0
	for _, s := range r.Steps {
		r.OK = r.OK && s.OK
	}
	if r.Steps == nil {
		r.Steps = []TestStep{}
	}
	return *r
}

// netHint turns a network error into advice.
func netHint(err error, defaultPorts string) string {
	msg := err.Error()
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr):
		return "the host name is unknown – check the spelling, or use the IP address"
	case strings.Contains(msg, "refused"):
		return "nothing is listening on that port – is the server running? Usual ports: " + defaultPorts
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") || strings.Contains(msg, "timed out"):
		return "no answer – check the IP address, the cable/Wi-Fi, and the firewall on the server"
	case strings.Contains(msg, "certificate") || strings.Contains(msg, "x509") || strings.Contains(msg, "tls"):
		return "TLS problem – check the certificate, or use http:// / tcp:// if the server has no TLS"
	case strings.Contains(msg, "malformed HTTP") || strings.Contains(msg, "server gave HTTP response"):
		return "the port answers but not with plain HTTP – try https:// or another port"
	}
	return ""
}

func withHint(err error, ports string) string {
	if h := netHint(err, ports); h != "" {
		return err.Error() + " → " + h
	}
	return err.Error()
}

// ---- InfluxDB ----

type influxClient struct {
	cfg  InfluxDBConfig
	http *http.Client
}

func (c influxClient) do(method, path string, q url.Values, body io.Reader, ctype string) (*http.Response, []byte, error) {
	u := strings.TrimRight(c.cfg.URL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	(&InfluxSink{cfg: c.cfg}).auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp, b, nil
}

// serverMessage is the error text in an InfluxDB answer.
func serverMessage(b []byte) string {
	var j struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &j) == nil {
		if j.Message != "" {
			return j.Message
		}
		if j.Error != "" {
			return j.Error
		}
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func majorVersion(v string) int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v != "" && v[0] >= '1' && v[0] <= '3' {
		return int(v[0] - '0')
	}
	return 0
}

// influxTest checks the server, the version, the credentials and the
// database/bucket, and lists the databases/buckets found.
func influxTest(c InfluxDBConfig) TestResult {
	var r TestResult
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		r.add("Server address", false, "the URL must start with http:// or https://, e.g. http://192.168.1.20:8086")
		return r.done()
	}
	if c.Version < 1 || c.Version > 3 {
		c.Version = 2
	}
	cl := influxClient{c, &http.Client{Timeout: 5 * time.Second}}
	const ports = "8086 for InfluxDB 1.x/2.x, 8181 for InfluxDB 3 Core/Enterprise"

	// 1. Reachable, and which version?
	resp, body, err := cl.do(http.MethodGet, "/ping", nil, nil, "")
	if err != nil {
		r.add("Reach the server", false, "%s", withHint(err, ports))
		return r.done()
	}
	ver := resp.Header.Get("X-Influxdb-Version")
	if ver == "" {
		var j struct {
			Version string `json:"version"`
		}
		json.Unmarshal(body, &j)
		ver = j.Version
	}
	r.ServerVersion, r.Version = ver, majorVersion(ver)
	switch {
	case resp.StatusCode/100 == 2 && ver != "":
		r.add("Reach the server", true, "InfluxDB %s answers at %s", ver, c.URL)
	case resp.StatusCode/100 == 2:
		r.add("Reach the server", true, "a server answers at %s", c.URL)
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		r.add("Reach the server", true, "a server answers at %s (it wants a token even for /ping)", c.URL)
	default:
		r.add("Reach the server", false, "HTTP %d from %s/ping – is this really InfluxDB? %s", resp.StatusCode, c.URL, serverMessage(body))
		return r.done()
	}
	if r.Version != 0 && r.Version != c.Version {
		r.add("Version", false, "the server is InfluxDB %d but version %d is selected – choose version %d", r.Version, c.Version, r.Version)
		return r.done()
	}

	// 2. Credentials and the database/bucket.
	switch c.Version {
	case 1:
		resp, body, err = cl.do(http.MethodGet, "/query", url.Values{"q": {"SHOW DATABASES"}}, nil, "")
		if err != nil {
			r.add("Log in", false, "%v", err)
			break
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			r.add("Log in", false, "username or password rejected (HTTP %d): %s", resp.StatusCode, serverMessage(body))
			break
		}
		var j struct {
			Results []struct {
				Error  string `json:"error"`
				Series []struct {
					Values [][]any `json:"values"`
				} `json:"series"`
			} `json:"results"`
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &j) != nil || resp.StatusCode/100 != 2 {
			r.add("Log in", false, "HTTP %d: %s", resp.StatusCode, serverMessage(body))
			break
		}
		if j.Error != "" || (len(j.Results) > 0 && j.Results[0].Error != "") {
			msg := j.Error
			if msg == "" {
				msg = j.Results[0].Error
			}
			// A non-admin user may not list databases; writing can still work.
			r.warn("Log in", "logged in, but the database list is not available to this user (%s)", msg)
			break
		}
		r.add("Log in", true, "%s", map[bool]string{true: "logged in as " + c.Username, false: "no login needed (authentication is off)"}[c.Username != ""])
		for _, res := range j.Results {
			for _, s := range res.Series {
				for _, v := range s.Values {
					if len(v) > 0 {
						if name, ok := v[0].(string); ok {
							r.Databases = append(r.Databases, name)
						}
					}
				}
			}
		}
		r.checkTarget("Database", c.Database, true)
	case 2:
		if c.Token == "" {
			r.add("Log in", false, "InfluxDB 2 needs an API token: in the InfluxDB web page open Load Data → API Tokens → Generate API Token")
			break
		}
		q := url.Values{"limit": {"100"}}
		if c.Org != "" {
			q.Set("org", c.Org)
		}
		resp, body, err = cl.do(http.MethodGet, "/api/v2/buckets", q, nil, "")
		if err != nil {
			r.add("Log in", false, "%v", err)
			break
		}
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			r.add("Log in", false, "the token was rejected (HTTP %d): %s", resp.StatusCode, serverMessage(body))
		case resp.StatusCode == 404:
			r.add("Organization", false, "organization %q not found: %s. The organization name is shown in the InfluxDB web page (top left, or About)", c.Org, serverMessage(body))
		case resp.StatusCode/100 != 2:
			r.add("Log in", false, "HTTP %d: %s", resp.StatusCode, serverMessage(body))
		default:
			var j struct {
				Buckets []struct {
					Name string `json:"name"`
				} `json:"buckets"`
			}
			json.Unmarshal(body, &j)
			r.add("Log in", true, "the token is accepted")
			if c.Org == "" {
				r.add("Organization", false, "fill in the organization: InfluxDB 2 needs it to write")
			} else {
				r.add("Organization", true, "%s", c.Org)
			}
			for _, b := range j.Buckets {
				if !strings.HasPrefix(b.Name, "_") { // _monitoring, _tasks
					r.Databases = append(r.Databases, b.Name)
				}
			}
			r.checkTarget("Bucket", c.Bucket, true)
		}
	case 3:
		resp, body, err = cl.do(http.MethodGet, "/api/v3/configure/database", url.Values{"format": {"json"}}, nil, "")
		if err != nil {
			r.add("Log in", false, "%v", err)
			break
		}
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			msg := "the token was rejected"
			if c.Token == "" {
				msg = "a token is needed: create one with  influxdb3 create token --admin"
			}
			r.add("Log in", false, "%s (HTTP %d)", msg, resp.StatusCode)
		case resp.StatusCode/100 != 2:
			r.add("Log in", false, "HTTP %d: %s", resp.StatusCode, serverMessage(body))
		default:
			r.add("Log in", true, "%s", map[bool]string{true: "the token is accepted", false: "no token needed (server runs without authentication)"}[c.Token != ""])
			var rows []map[string]any
			json.Unmarshal(body, &rows)
			for _, row := range rows {
				for k, v := range row {
					if s, ok := v.(string); ok && strings.Contains(k, "database") && !strings.HasPrefix(s, "_") {
						r.Databases = append(r.Databases, s)
					}
				}
			}
			// InfluxDB 3 creates a database on its first write.
			r.checkTarget("Database", c.Database, false)
		}
	}
	sort.Strings(r.Databases)
	return r.done()
}

// checkTarget reports whether the database/bucket exists. must: writing
// fails if it does not.
func (r *TestResult) checkTarget(what, name string, must bool) {
	switch {
	case name == "":
		r.add(what, false, "fill in the %s name", strings.ToLower(what))
	case contains(r.Databases, name):
		r.add(what, true, "%q exists – data will be written there", name)
	case must:
		r.Missing = true
		r.add(what, false, "%q does not exist yet – press “Create %s” or pick one from the list", name, strings.ToLower(what))
	default:
		r.Missing = true
		r.warn(what, "%q does not exist yet; InfluxDB 3 creates it with the first value written", name)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

var safeDBName = func(s string) error {
	if s == "" || strings.ContainsAny(s, "\"\\\n\r\t") || len(s) > 64 {
		return fmt.Errorf("%q is not a usable name (no quotes, backslashes or line breaks, at most 64 characters)", s)
	}
	return nil
}

// influxCreate creates the configured database (v1, v3) or bucket (v2).
func influxCreate(c InfluxDBConfig) error {
	cl := influxClient{c, &http.Client{Timeout: 10 * time.Second}}
	check := func(resp *http.Response, body []byte, err error) error {
		if err != nil {
			return err
		}
		if resp.StatusCode/100 != 2 {
			return &httpError{resp.StatusCode, serverMessage(body)}
		}
		return nil
	}
	switch c.Version {
	case 1:
		if err := safeDBName(c.Database); err != nil {
			return err
		}
		form := url.Values{"q": {`CREATE DATABASE "` + c.Database + `"`}}
		resp, body, err := cl.do(http.MethodPost, "/query", nil, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
		if err := check(resp, body, err); err != nil {
			return err
		}
		if msg := serverMessage(body); strings.Contains(msg, `"error"`) || strings.Contains(strings.ToLower(msg), "not authorized") {
			return errors.New(msg)
		}
		return nil
	case 2:
		if err := safeDBName(c.Bucket); err != nil {
			return err
		}
		if c.Org == "" {
			return errors.New("fill in the organization first")
		}
		resp, body, err := cl.do(http.MethodGet, "/api/v2/orgs", url.Values{"org": {c.Org}}, nil, "")
		if err := check(resp, body, err); err != nil {
			return fmt.Errorf("find organization: %w", err)
		}
		var orgs struct {
			Orgs []struct {
				ID string `json:"id"`
			} `json:"orgs"`
		}
		if json.Unmarshal(body, &orgs) != nil || len(orgs.Orgs) == 0 {
			return fmt.Errorf("organization %q not found", c.Org)
		}
		b, _ := json.Marshal(map[string]any{"orgID": orgs.Orgs[0].ID, "name": c.Bucket, "retentionRules": []any{}})
		resp, body, err = cl.do(http.MethodPost, "/api/v2/buckets", nil, bytes.NewReader(b), "application/json")
		return check(resp, body, err)
	case 3:
		if err := safeDBName(c.Database); err != nil {
			return err
		}
		b, _ := json.Marshal(map[string]string{"db": c.Database})
		resp, body, err := cl.do(http.MethodPost, "/api/v3/configure/database", nil, bytes.NewReader(b), "application/json")
		return check(resp, body, err)
	}
	return errors.New("unknown InfluxDB version")
}

// ---- MQTT ----

// mqttTest connects with a separate client ID, subscribes to <prefix>/#,
// publishes a test message and collects what the broker sends back
// (including the retained tag values, if PLC Monitor already publishes).
func mqttTest(c MQTTConfig) TestResult {
	var r TestResult
	c.Broker = strings.TrimSpace(c.Broker)
	c.TopicPrefix = strings.Trim(strings.TrimSpace(c.TopicPrefix), "/")
	if c.TopicPrefix == "" {
		c.TopicPrefix = "plcmonitor"
	}
	if err := checkBrokerURL(c.Broker); err != nil {
		r.add("Broker address", false, "%v", err)
		return r.done()
	}
	suffix := make([]byte, 3)
	rand.Read(suffix)
	if c.ClientID == "" {
		c.ClientID = "plcmonitor"
	}
	c.ClientID += "-test-" + hex.EncodeToString(suffix) // never kick the running exporter off

	var mu sync.Mutex
	var got []MQTTSample
	testTopic := c.TopicPrefix + "/_test"
	testPayload := "PLC Monitor test " + time.Now().Format(time.RFC3339)
	echo := make(chan struct{}, 1)
	opts := mqttOptions(c).SetAutoReconnect(false).SetConnectRetry(false).SetConnectTimeout(6 * time.Second)
	cl := mqtt.NewClient(opts)
	tok := cl.Connect()
	if !tok.WaitTimeout(8 * time.Second) {
		r.add("Connect", false, "no answer from %s within 8 s → check the address and port, and the firewall", c.Broker)
		return r.done()
	}
	if err := tok.Error(); err != nil {
		msg := err.Error()
		low := strings.ToLower(msg)
		switch {
		case strings.Contains(low, "not authorized") || strings.Contains(low, "bad user name or password"):
			msg += " → the broker refused the username/password (or anonymous login is off: set allow_anonymous / add a user)"
		case strings.Contains(low, "identifier rejected"):
			msg += " → the broker rejected the client ID"
		default:
			if h := netHint(err, "1883 (tcp://), 8883 (ssl://), 9001 or 8083 (ws://)"); h != "" {
				msg += " → " + h
			}
		}
		r.add("Connect", false, "%s", msg)
		return r.done()
	}
	defer cl.Disconnect(100)
	r.add("Connect", true, "connected to %s%s", c.Broker, map[bool]string{true: " as " + c.Username, false: ""}[c.Username != ""])

	st := cl.Subscribe(c.TopicPrefix+"/#", 0, func(_ mqtt.Client, m mqtt.Message) {
		p := string(m.Payload())
		isTest := m.Topic() == testTopic && p == testPayload
		if isTest {
			select {
			case echo <- struct{}{}:
			default:
			}
		}
		if len(p) > 300 {
			p = p[:300] + "…"
		}
		mu.Lock()
		if len(got) < 50 {
			got = append(got, MQTTSample{Topic: m.Topic(), Payload: p, Retained: m.Retained(), Test: isTest})
		}
		mu.Unlock()
	})
	subOK := st.WaitTimeout(5*time.Second) && st.Error() == nil
	if s, ok := st.(*mqtt.SubscribeToken); ok && subOK {
		for _, code := range s.Result() {
			if code >= 0x80 {
				subOK = false
			}
		}
	}
	if !subOK {
		r.add("Subscribe", false, "the broker refused a subscription to %s/# (access rules / ACL for this user)", c.TopicPrefix)
	}

	pt := cl.Publish(testTopic, c.QoS, false, testPayload)
	if !pt.WaitTimeout(5*time.Second) || pt.Error() != nil {
		r.add("Publish", false, "publishing to %s failed: %v", testTopic, pt.Error())
		return r.done()
	}
	if subOK {
		select {
		case <-echo:
			r.add("Publish and receive", true, "published to %s and received it back", testTopic)
		case <-time.After(2 * time.Second):
			r.add("Publish and receive", false, "published to %s but it did not come back – the broker's access rules (ACL) may not let this user publish there", testTopic)
		}
		time.Sleep(300 * time.Millisecond) // retained values arrive right after subscribing
	} else {
		r.add("Publish", true, "published to %s", testTopic)
	}
	mu.Lock()
	r.Messages = append([]MQTTSample(nil), got...)
	mu.Unlock()
	sort.SliceStable(r.Messages, func(i, j int) bool { return r.Messages[i].Topic < r.Messages[j].Topic })
	return r.done()
}

// ---- CSV ----

func csvTest(path string) TestResult {
	var r TestResult
	path = strings.TrimSpace(path)
	if path == "" {
		r.add("File", false, "enter a file name, e.g. changes.csv or D:\\logs\\plc-changes.csv")
		return r.done()
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		r.add("File", false, "%v", err)
		return r.done()
	}
	dir := filepath.Dir(abs)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		r.warn("Folder", "%s does not exist yet; it is created when you apply", dir)
	}
	fi, err := os.Stat(abs)
	switch {
	case err == nil && fi.IsDir():
		r.add("File", false, "%s is a folder; add a file name such as changes.csv", abs)
		return r.done()
	case err == nil:
		f, err := os.OpenFile(abs, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			r.add("File", false, "%s exists but cannot be written: %v (is it open in Excel?)", abs, err)
			return r.done()
		}
		f.Close()
		r.add("File", true, "%s – %s, last change %s; new rows are added at the end", abs, sizeText(fi.Size()), fi.ModTime().Format("2006-01-02 15:04:05"))
	default:
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			f, err := os.CreateTemp(dir, ".plcmonitor-test-*")
			if err != nil {
				r.add("File", false, "cannot write in %s: %v", dir, err)
				return r.done()
			}
			f.Close()
			os.Remove(f.Name())
		}
		r.add("File", true, "%s will be created with a header row", abs)
	}
	return r.done()
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// ---- HTTP routes ----

// exportRoutes adds the Export settings API. local accepts only edits from
// this computer (the settings hold passwords and tokens).
func exportRoutes(mux *http.ServeMux, m *Monitor, local func(http.ResponseWriter, *http.Request) bool) {
	mux.HandleFunc("GET /api/export", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"export settings are only available on the computer running PLC Monitor"})
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Export exportJSON   `json:"export"`
			Status []SinkStatus `json:"status"`
		}{exportOf(m.Config().Export), m.ExportStatus()})
	})
	mux.HandleFunc("GET /api/export/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.ExportStatus())
	})
	mux.HandleFunc("PUT /api/export", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		var e exportJSON
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		ec, err := e.config()
		if err == nil {
			err = m.Update(func(c *Config) error { c.Export = ec; return nil })
		}
		respond(w, err)
	})
	mux.HandleFunc("POST /api/export/test/{kind}", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		var e exportJSON
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		res := make(chan TestResult, 1)
		go func() {
			switch r.PathValue("kind") {
			case "influxdb":
				ix, _ := e.InfluxDB.config()
				res <- influxTest(ix)
			case "mqtt":
				res <- mqttTest(e.MQTT)
			case "csv":
				res <- csvTest(e.CSV.Path)
			default:
				c, _ := e.config()
				res <- moreTest(r.PathValue("kind"), c, m.ExportStatus())
			}
		}()
		select {
		case t := <-res:
			writeJSON(w, http.StatusOK, t)
		case <-ctx.Done():
			respond(w, errors.New("the test took too long"))
		}
	})
	mux.HandleFunc("POST /api/export/create/influxdb", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		var ij influxJSON
		if err := json.NewDecoder(r.Body).Decode(&ij); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		ix, _ := ij.config()
		respond(w, influxCreate(ix))
	})
	mux.HandleFunc("POST /api/export/csv/folder", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		var body struct {
			Path string `json:"path"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		p := strings.TrimSpace(body.Path)
		if p == "" {
			p = m.Config().Export.CSV.Path
		}
		abs, _ := filepath.Abs(p)
		respond(w, openFolder(filepath.Dir(abs)))
	})
}
