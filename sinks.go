package main

import (
	"bytes"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Sink receives batches of changes. Each sink runs on its own goroutine, so a
// slow broker or database never stalls PLC polling.
type Sink interface {
	Name() string
	Write(changes []Change) error
	Close() error
	Status() SinkStatus
}

// SinkStatus is what the Export settings window shows for each exporter.
type SinkStatus struct {
	Name      string    `json:"name"`
	State     string    `json:"state"`  // ok, connecting, error
	Target    string    `json:"target"` // file, broker or server
	Sent      int64     `json:"sent"`   // rows / messages / points delivered
	Pending   int       `json:"pending,omitempty"`
	Dropped   int64     `json:"dropped,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	ErrorAt   time.Time `json:"error_at,omitzero"`
	LastOK    time.Time `json:"last_ok,omitzero"`
}

// sinkStats is the bookkeeping shared by the sinks.
type sinkStats struct {
	mu        sync.Mutex
	sent      int64
	lastErr   string
	errAt     time.Time
	lastOK    time.Time
	lastLogAt time.Time
}

func (s *sinkStats) ok(n int) {
	s.mu.Lock()
	s.sent += int64(n)
	s.lastOK = time.Now()
	s.lastErr = ""
	s.mu.Unlock()
}

// fail records err; it reports whether to log it (not more than once a
// minute for the same error, so an unreachable server does not flood the log).
func (s *sinkStats) fail(err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := err.Error()
	logIt := msg != s.lastErr || time.Since(s.lastLogAt) > time.Minute
	s.lastErr, s.errAt = msg, time.Now()
	if logIt {
		s.lastLogAt = s.errAt
	}
	return logIt
}

func (s *sinkStats) fill(st *SinkStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st.Sent, st.LastError, st.ErrorAt, st.LastOK = s.sent, s.lastErr, s.errAt, s.lastOK
	if st.State == "" {
		st.State = "ok"
		if s.lastErr != "" {
			st.State = "error"
		}
	}
}

// Dispatcher fans changes out to the exporters. Its set of exporters can be
// replaced while the program runs (Export settings window).
type Dispatcher struct {
	mu      sync.RWMutex
	set     *sinkSet
	cfg     ExportConfig
	closing sync.WaitGroup // replaced sets still flushing
	problem string         // why the configured exporters could not start
}

// failedExporter stands in when the exporters in the config could not be
// created at start-up, so the program still runs and shows why.
func failedExporter(err error) *Dispatcher {
	return &Dispatcher{set: newSinkSet(nil), problem: err.Error()}
}

type sinkSet struct {
	sinks   []Sink
	chans   []chan []Change
	dropped []int64 // changes dropped per sink because its queue was full
	dropMu  sync.Mutex
	wg      sync.WaitGroup
}

func newSinkSet(sinks []Sink) *sinkSet {
	s := &sinkSet{sinks: sinks, dropped: make([]int64, len(sinks))}
	for _, sk := range sinks {
		ch := make(chan []Change, 256)
		s.chans = append(s.chans, ch)
		s.wg.Add(1)
		go func(sk Sink, ch chan []Change) {
			defer s.wg.Done()
			for batch := range ch {
				if err := sk.Write(batch); err != nil {
					log.Printf("export %s: %v", sk.Name(), err)
				}
			}
		}(sk, ch)
	}
	return s
}

func (s *sinkSet) close() {
	for _, ch := range s.chans {
		close(ch)
	}
	s.wg.Wait()
	for _, sk := range s.sinks {
		if err := sk.Close(); err != nil {
			log.Printf("export %s: close: %v", sk.Name(), err)
		}
	}
}

// NewDispatcher sends to a fixed list of sinks (tests).
func NewDispatcher(sinks []Sink) *Dispatcher {
	return &Dispatcher{set: newSinkSet(sinks)}
}

// NewExporter builds the exporters enabled in c.
func NewExporter(c ExportConfig) (*Dispatcher, error) {
	sinks, err := BuildSinks(c)
	if err != nil {
		return nil, err
	}
	for _, s := range sinks {
		log.Printf("export enabled: %s", s.Name())
	}
	return &Dispatcher{set: newSinkSet(sinks), cfg: c}, nil
}

// Reconfigure switches to the exporters of c. Nothing changes if c is the
// current config; otherwise the old exporters are flushed and closed.
func (d *Dispatcher) Reconfigure(c ExportConfig) error {
	d.mu.RLock()
	same := d.cfg == c
	d.mu.RUnlock()
	if same {
		return nil
	}
	sinks, err := BuildSinks(c)
	if err != nil {
		return err
	}
	d.mu.Lock()
	old := d.set
	d.set, d.cfg, d.problem = newSinkSet(sinks), c, ""
	d.mu.Unlock()
	names := []string{}
	for _, s := range sinks {
		names = append(names, s.Name())
	}
	if len(names) == 0 {
		names = []string{"nothing"}
	}
	log.Printf("export settings changed; exporting to: %s", strings.Join(names, ", "))
	// Flushing may wait for a slow server; don't hold up the caller. Close
	// waits for it.
	d.closing.Add(1)
	go func() {
		defer d.closing.Done()
		old.close()
	}()
	return nil
}

func (d *Dispatcher) Send(changes []Change) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	s := d.set
	for i, ch := range s.chans {
		select {
		case ch <- changes:
		default:
			s.dropMu.Lock()
			s.dropped[i] += int64(len(changes))
			s.dropMu.Unlock()
			log.Printf("export %s: queue full, dropped %d change(s)", s.sinks[i].Name(), len(changes))
		}
	}
}

// Status describes every running exporter.
func (d *Dispatcher) Status() []SinkStatus {
	d.mu.RLock()
	s, problem := d.set, d.problem
	d.mu.RUnlock()
	out := []SinkStatus{}
	if problem != "" {
		out = append(out, SinkStatus{Name: "export", State: "error", LastError: problem})
	}
	for i, sk := range s.sinks {
		st := sk.Status()
		s.dropMu.Lock()
		st.Dropped = s.dropped[i]
		s.dropMu.Unlock()
		st.Pending += len(s.chans[i])
		out = append(out, st)
	}
	return out
}

// Close drains queues and closes every sink.
func (d *Dispatcher) Close() {
	d.mu.Lock()
	s := d.set
	d.set = newSinkSet(nil)
	d.mu.Unlock()
	s.close()
	d.closing.Wait()
}

// BuildSinks creates the exporters enabled in the config.
func BuildSinks(c ExportConfig) ([]Sink, error) {
	var sinks []Sink
	if c.CSV.Enabled {
		s, err := NewCSVSink(c.CSV.Path)
		if err != nil {
			return nil, err
		}
		sinks = append(sinks, s)
	}
	if c.MQTT.Enabled {
		sinks = append(sinks, NewMQTTSink(c.MQTT))
	}
	if c.InfluxDB.Enabled {
		sinks = append(sinks, NewInfluxSink(c.InfluxDB))
	}
	more, err := buildMoreSinks(c)
	if err != nil {
		closeAll(sinks)
		return nil, err
	}
	return append(sinks, more...), nil
}

func quality(good bool) string {
	if good {
		return "good"
	}
	return "bad"
}

// ---- CSV ----

type CSVSink struct {
	f     *os.File
	w     *csv.Writer
	path  string
	stats sinkStats
}

func NewCSVSink(path string) (*CSVSink, error) {
	if dir := filepath.Dir(path); dir != "." {
		os.MkdirAll(dir, 0o755)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("csv: %w", err)
	}
	abs, _ := filepath.Abs(path)
	s := &CSVSink{f: f, w: csv.NewWriter(f), path: abs}
	if fi, err := f.Stat(); err == nil && fi.Size() == 0 {
		s.w.Write([]string{"timestamp", "tag", "address", "type", "old", "new", "quality"})
		s.w.Flush()
	}
	return s, nil
}

func (s *CSVSink) Name() string { return "csv" }

func (s *CSVSink) Write(changes []Change) error {
	for _, c := range changes {
		s.w.Write([]string{
			c.Time.Format(time.RFC3339Nano), c.Tag.Name, c.Address, c.Tag.Type,
			FormatValue(c.Old), FormatValue(c.New), quality(c.Good),
		})
	}
	s.w.Flush()
	if err := s.w.Error(); err != nil {
		s.stats.fail(err)
		return err
	}
	s.stats.ok(len(changes))
	return nil
}

func (s *CSVSink) Status() SinkStatus {
	st := SinkStatus{Name: "csv", Target: s.path}
	s.stats.fill(&st)
	return st
}

func (s *CSVSink) Close() error { return s.f.Close() }

// ---- MQTT ----

// MQTTSink publishes each change to <prefix>/<tag> and keeps <prefix>/status
// at "online"/"offline" (the latter via last will).
type MQTTSink struct {
	cfg    MQTTConfig
	client mqtt.Client
	stats  sinkStats
	quit   chan struct{}
}

// mqttOptions are the client options for c, shared by the sink and the
// Test connection button.
func mqttOptions(c MQTTConfig) *mqtt.ClientOptions {
	opts := mqtt.NewClientOptions().
		AddBroker(c.Broker).
		SetClientID(c.ClientID).
		SetUsername(c.Username).
		SetPassword(c.Password).
		SetConnectTimeout(10 * time.Second)
	if c.TLSInsecure {
		opts.SetTLSConfig(&tls.Config{InsecureSkipVerify: true}) // the user asked for it (self-signed broker)
	}
	return opts
}

func NewMQTTSink(c MQTTConfig) *MQTTSink {
	s := &MQTTSink{cfg: c}
	statusTopic := c.TopicPrefix + "/status"
	opts := mqttOptions(c).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5*time.Second).
		SetWill(statusTopic, "offline", 1, true).
		SetOnConnectHandler(func(cl mqtt.Client) {
			log.Printf("MQTT connected to %s", c.Broker)
			s.stats.ok(0)
			cl.Publish(statusTopic, 1, true, "online")
		}).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			log.Printf("MQTT connection lost: %v", err)
			s.stats.fail(fmt.Errorf("connection lost: %w", err))
		})
	s.client = mqtt.NewClient(opts)
	s.quit = make(chan struct{})
	s.client.Connect() // with ConnectRetry this keeps trying in the background
	go s.explain()
	return s
}

// explain finds out why the broker can't be reached while the library
// silently retries, so the Export window can say so.
func (s *MQTTSink) explain() {
	for delay := 3 * time.Second; ; delay = 10 * time.Second {
		select {
		case <-s.quit:
			return
		case <-time.After(delay):
		}
		if s.client.IsConnectionOpen() {
			continue
		}
		var err error
		if addr, ok := brokerDialAddr(s.cfg.Broker); ok {
			conn, derr := net.DialTimeout("tcp", addr, 5*time.Second)
			if derr != nil {
				err = fmt.Errorf("%s", withHint(derr, "1883 (tcp://), 8883 (ssl://)"))
			} else {
				conn.Close()
				err = errors.New("the broker is reachable but did not accept the connection – check username/password, client ID and TLS (press Test connection for details)")
			}
		}
		if err != nil && s.stats.fail(err) {
			log.Printf("MQTT %s: %v", s.cfg.Broker, err)
		}
	}
}

// brokerDialAddr is host:port of a broker URL.
func brokerDialAddr(broker string) (string, bool) {
	u, err := url.Parse(broker)
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"ws": "80", "wss": "443", "ssl": "8883", "tls": "8883", "mqtts": "8883"}[u.Scheme]
		if port == "" {
			port = "1883"
		}
	}
	return net.JoinHostPort(u.Hostname(), port), true
}

func (s *MQTTSink) Name() string { return "mqtt" }

type mqttPayload struct {
	Value   any    `json:"value"`
	Quality string `json:"quality"`
	Address string `json:"address"`
	Type    string `json:"type"`
	TS      string `json:"ts"`
}

// mqttMessage is the topic and payload published for one change.
func mqttMessage(cfg MQTTConfig, c Change) (string, []byte) {
	topic := cfg.TopicPrefix + "/" + c.Tag.Name
	if cfg.Payload == "value" {
		if !c.Good || c.New == nil {
			return topic, []byte("null") // the link or the tag is bad
		}
		return topic, []byte(strings.ToLower(FormatValue(c.New)))
	}
	p, _ := json.Marshal(mqttPayload{
		Value: jsonSafe(c.New), Quality: quality(c.Good), Address: c.Address,
		Type: c.Tag.Type, TS: c.Time.Format(time.RFC3339Nano),
	})
	return topic, p
}

func (s *MQTTSink) Write(changes []Change) error {
	for _, c := range changes {
		topic, p := mqttMessage(s.cfg, c)
		// Publish is queued by paho while offline (ConnectRetry), so no wait here.
		s.client.Publish(topic, s.cfg.QoS, s.cfg.Retain, p)
	}
	if s.client.IsConnectionOpen() {
		s.stats.ok(len(changes))
	}
	return nil
}

func (s *MQTTSink) Status() SinkStatus {
	st := SinkStatus{Name: "mqtt", Target: s.cfg.Broker}
	if s.client.IsConnectionOpen() {
		st.State = "ok"
	} else {
		st.State = "connecting"
	}
	s.stats.fill(&st)
	if st.State == "ok" {
		st.LastError = "" // an old error from before the reconnect
	}
	return st
}

func (s *MQTTSink) Close() error {
	close(s.quit)
	if s.client.IsConnected() {
		s.client.Publish(s.cfg.TopicPrefix+"/status", 1, true, "offline").WaitTimeout(2 * time.Second)
	}
	s.client.Disconnect(500)
	return nil
}

// ---- InfluxDB (v2 HTTP write API, line protocol) ----

// InfluxSink buffers points and writes them every FlushInterval, in line
// protocol, to the write API of the configured InfluxDB version:
//
//	1.x: POST /write?db=&rp=&precision=ms, with basic auth if a username is set
//	2.x: POST /api/v2/write?org=&bucket=&precision=ms, "Authorization: Token ..."
//	3.x: POST /api/v3/write_lp?db=&precision=millisecond, "Authorization: Bearer ..."
type InfluxSink struct {
	cfg   InfluxDBConfig
	http  *http.Client
	mu    sync.Mutex
	buf   bytes.Buffer
	lines int // points in buf
	stop  chan struct{}
	done  chan struct{}
	stats sinkStats
}

func NewInfluxSink(c InfluxDBConfig) *InfluxSink {
	if c.Version == 0 {
		c.Version = 2
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = 5 * time.Second
	}
	s := &InfluxSink{cfg: c, http: &http.Client{Timeout: 10 * time.Second}, stop: make(chan struct{}), done: make(chan struct{})}
	go s.loop()
	go func() { // tell early in the log whether the server is reachable
		if err := s.ping(); err != nil {
			s.stats.fail(fmt.Errorf("server not reachable: %w", err))
			log.Printf("export influxdb v%d: %s not reachable yet: %v", c.Version, c.URL, err)
		} else {
			log.Printf("export influxdb v%d: %s is reachable", c.Version, c.URL)
		}
	}()
	return s
}

func (s *InfluxSink) Status() SinkStatus {
	st := SinkStatus{Name: "influxdb", Target: fmt.Sprintf("%s (v%d)", s.cfg.URL, s.cfg.Version)}
	s.mu.Lock()
	st.Pending = s.lines
	s.mu.Unlock()
	s.stats.fill(&st)
	return st
}

// ping checks the server's health endpoint (/ping on 1.x, /health on 2.x and 3.x).
func (s *InfluxSink) ping() error {
	path := "/health"
	if s.cfg.Version == 1 {
		path = "/ping"
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(s.cfg.URL, "/")+path, nil)
	if err != nil {
		return err
	}
	s.auth(req)
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *InfluxSink) auth(req *http.Request) {
	switch {
	case s.cfg.Version == 1 && s.cfg.Username != "":
		req.SetBasicAuth(s.cfg.Username, s.cfg.Password)
	case s.cfg.Version == 2 && s.cfg.Token != "":
		req.Header.Set("Authorization", "Token "+s.cfg.Token)
	case s.cfg.Version == 3 && s.cfg.Token != "":
		req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	}
}

// writeURL is the write endpoint for the configured version.
func (s *InfluxSink) writeURL() string {
	base := strings.TrimRight(s.cfg.URL, "/")
	switch s.cfg.Version {
	case 1:
		q := url.Values{"db": {s.cfg.Database}, "precision": {"ms"}}
		if s.cfg.RetentionPolicy != "" {
			q.Set("rp", s.cfg.RetentionPolicy)
		}
		return base + "/write?" + q.Encode()
	case 3:
		q := url.Values{"db": {s.cfg.Database}, "precision": {"millisecond"}}
		return base + "/api/v3/write_lp?" + q.Encode()
	}
	q := url.Values{"bucket": {s.cfg.Bucket}, "precision": {"ms"}}
	if s.cfg.Org != "" {
		q.Set("org", s.cfg.Org)
	}
	return base + "/api/v2/write?" + q.Encode()
}

func (s *InfluxSink) Name() string { return "influxdb" }

func (s *InfluxSink) Write(changes []Change) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range changes {
		if !c.Good {
			continue // a bad-quality point carries no new value
		}
		f, ok := influxValue(c.New)
		if !ok {
			continue
		}
		s.buf.WriteString(influxLine(s.cfg.Measurement, c.Tag.Name, c.Address, c.Tag.Type, f, c.Time))
		s.lines++
	}
	const maxBuffered = 8 << 20 // drop oldest data rather than grow without bound
	if s.buf.Len() > maxBuffered {
		log.Printf("export influxdb: buffer over %d bytes, discarding", maxBuffered)
		s.buf.Reset()
		s.lines = 0
	}
	return nil
}

// influxLine is one point in line protocol, e.g.
// s7,tag=Tank_Level,address=DB1.DBD4,type=REAL value=42.5 1700000000000
func influxLine(measurement, tag, address, typ, value string, t time.Time) string {
	return fmt.Sprintf("%s,tag=%s,address=%s,type=%s value=%s %d\n",
		escMeasurement(measurement), escTag(tag), escTag(address), escTag(typ), value, t.UnixMilli())
}

// influxValue writes every tag as a float field so all tags can share one
// measurement without field type conflicts. BOOL becomes 0/1.
func influxValue(v any) (string, bool) {
	f := toFloat(v)
	if v == nil || jsonSafe(f) == nil {
		return "", false
	}
	return strconv.FormatFloat(f, 'g', -1, 64), true
}

var tagEscaper = strings.NewReplacer(",", `\,`, "=", `\=`, " ", `\ `)

func escTag(s string) string { return tagEscaper.Replace(s) }

var measurementEscaper = strings.NewReplacer(",", `\,`, " ", `\ `)

func escMeasurement(s string) string { return measurementEscaper.Replace(s) }

func (s *InfluxSink) loop() {
	defer close(s.done)
	t := time.NewTicker(s.cfg.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			s.flush()
			return
		case <-t.C:
			s.flush()
		}
	}
}

func (s *InfluxSink) flush() {
	s.mu.Lock()
	if s.buf.Len() == 0 {
		s.mu.Unlock()
		return
	}
	body := append([]byte(nil), s.buf.Bytes()...)
	n := s.lines
	s.buf.Reset()
	s.lines = 0
	s.mu.Unlock()

	if err := s.post(body); err != nil {
		if s.stats.fail(err) {
			log.Printf("export influxdb: %v (will retry)", err)
		}
		if isPermanent(err) {
			// The server rejected the data itself (e.g. a field type clash):
			// sending it again would fail forever.
			return
		}
		s.mu.Lock()
		rest := append([]byte(nil), s.buf.Bytes()...)
		s.buf.Reset()
		s.buf.Write(body) // put the failed batch back in front
		s.buf.Write(rest)
		s.lines += n
		s.mu.Unlock()
		return
	}
	s.stats.ok(n)
}

// httpError is a non-2xx answer from InfluxDB.
type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string {
	hint := ""
	switch e.code {
	case 401, 403:
		hint = " (check the token / username and password, and that they may write to this database)"
	case 404:
		hint = " (database, bucket or organization not found, or wrong InfluxDB version selected)"
	}
	return fmt.Sprintf("HTTP %d: %s%s", e.code, e.msg, hint)
}

// isPermanent: 400 (bad data) and 422 (unprocessable) are not retried; auth
// and server errors are, since they get fixed outside PLC Monitor.
func isPermanent(err error) bool {
	var he *httpError
	return errors.As(err, &he) && (he.code == 400 || he.code == 422)
}

func (s *InfluxSink) post(body []byte) error {
	req, err := http.NewRequest(http.MethodPost, s.writeURL(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	s.auth(req)
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &httpError{resp.StatusCode, string(bytes.TrimSpace(msg))}
	}
	return nil
}

func (s *InfluxSink) Close() error {
	close(s.stop)
	<-s.done
	return nil
}
