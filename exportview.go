package main

// Data view: how the tags look on the other side of the exporters.
//
//   - Sent: for every tag, the MQTT topic and payload and the InfluxDB line
//     PLC Monitor produces for its current value (worked out locally, with the
//     saved export settings).
//   - On the broker: a watcher subscribes to <prefix>/# with its own client
//     ID and keeps the last message of every topic, while the window is open.
//   - In the database: the last value of every tag, read back with the
//     query language of the InfluxDB version (InfluxQL, Flux or SQL). The
//     query is shown too, as a starting point for Grafana.

import (
	"bytes"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gopcua/opcua/id"
	"google.golang.org/protobuf/encoding/protojson"
)

// PreviewRow is one tag as it is sent.
type PreviewRow struct {
	Tag     string `json:"tag"`
	Address string `json:"address"`
	Type    string `json:"type"`
	Value   string `json:"value"`
	Good    bool   `json:"good"`
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
	Line    string `json:"line"` // InfluxDB line protocol; "" when no point is written
	// More: what each of the newer targets gets, by target key.
	More map[string]string `json:"more"`
}

// TargetInfo describes one of the newer targets for the Data view.
type TargetInfo struct {
	Key     string      `json:"key"`
	Label   string      `json:"label"`
	Group   string      `json:"group"`
	Enabled bool        `json:"enabled"`
	Status  string      `json:"status"` // name in /api/export/status
	What    string      `json:"what"`   // column heading: what each tag becomes
	Info    [][2]string `json:"info"`
	Note    string      `json:"note,omitempty"`
}

type Preview struct {
	MQTT struct {
		Enabled bool   `json:"enabled"`
		Broker  string `json:"broker"`
		Prefix  string `json:"prefix"`
		Payload string `json:"payload"`
		QoS     byte   `json:"qos"`
		Retain  bool   `json:"retain"`
	} `json:"mqtt"`
	Influx struct {
		Enabled     bool   `json:"enabled"`
		Version     int    `json:"version"`
		URL         string `json:"url"`
		Target      string `json:"target"` // database or bucket
		Measurement string `json:"measurement"`
	} `json:"influx"`
	Rows    []PreviewRow `json:"rows"`
	Targets []TargetInfo `json:"targets"`
}

// ExportPreview works out what each tag looks like for MQTT and InfluxDB.
func (m *Monitor) ExportPreview() Preview {
	c := m.Config()
	e := c.Export
	var p Preview
	p.MQTT.Enabled, p.MQTT.Broker, p.MQTT.Prefix = e.MQTT.Enabled, e.MQTT.Broker, e.MQTT.TopicPrefix
	p.MQTT.Payload, p.MQTT.QoS, p.MQTT.Retain = e.MQTT.Payload, e.MQTT.QoS, e.MQTT.Retain
	ix := e.InfluxDB
	p.Influx.Enabled, p.Influx.Version, p.Influx.URL, p.Influx.Measurement = ix.Enabled, ix.Version, ix.URL, ix.Measurement
	p.Influx.Target = influxTarget(ix)
	_, states := m.Snapshot()
	now := time.Now()
	p.Rows = []PreviewRow{}
	for _, s := range states {
		t := &Tag{Name: s.Name, Type: s.Type}
		ts := s.Updated
		if ts.IsZero() {
			ts = now
		}
		ch := Change{Time: ts, Tag: t, New: s.Value, Good: s.Good, Address: s.Address}
		topic, payload := mqttMessage(e.MQTT, ch)
		row := PreviewRow{Tag: s.Name, Address: s.Address, Type: s.Type, Value: s.Display, Good: s.Good, Topic: topic, Payload: string(payload)}
		if f, ok := influxValue(s.Value); ok && s.Good {
			row.Line = strings.TrimSuffix(influxLine(ix.Measurement, s.Name, s.Address, s.Type, f, ts), "\n")
		}
		row.More = moreOf(e, liveVal{Name: s.Name, Address: s.Address, Type: s.Type, Value: s.Value, Good: s.Good, Time: ts})
		p.Rows = append(p.Rows, row)
	}
	p.Targets = targetsOf(e)
	return p
}

func influxTarget(ix InfluxDBConfig) string {
	switch ix.Version {
	case 1:
		if ix.RetentionPolicy != "" {
			return ix.Database + "." + ix.RetentionPolicy
		}
		return ix.Database
	case 3:
		return ix.Database
	}
	return ix.Bucket
}

// ---- MQTT watcher ----

// BrokerMsg is the last message seen on one topic.
type BrokerMsg struct {
	Topic    string    `json:"topic"`
	Payload  string    `json:"payload"`
	Retained bool      `json:"retained"`
	Time     time.Time `json:"time"`
	Count    int       `json:"count"`
}

type BrokerView struct {
	Watching  bool        `json:"watching"`
	Connected bool        `json:"connected"`
	Broker    string      `json:"broker"`
	Topic     string      `json:"topic"` // what is subscribed
	Error     string      `json:"error,omitempty"`
	Messages  []BrokerMsg `json:"messages"`
}

// MQTTWatcher subscribes to the broker while someone looks at it.
type MQTTWatcher struct {
	mu       sync.Mutex
	client   mqtt.Client
	cfg      MQTTConfig
	topics   map[string]*BrokerMsg
	err      string
	lastPoll time.Time
}

func NewMQTTWatcher() *MQTTWatcher {
	w := &MQTTWatcher{topics: map[string]*BrokerMsg{}}
	go func() { // stop watching when the window is gone
		for range time.Tick(10 * time.Second) {
			w.mu.Lock()
			if w.client != nil && time.Since(w.lastPoll) > 45*time.Second {
				w.stopLocked()
			}
			w.mu.Unlock()
		}
	}()
	return w
}

func (w *MQTTWatcher) stopLocked() {
	if w.client != nil {
		c := w.client
		w.client = nil
		go c.Disconnect(100)
	}
}

func (w *MQTTWatcher) Stop() {
	w.mu.Lock()
	w.stopLocked()
	w.mu.Unlock()
}

// View returns what the broker sent so far, and starts (or restarts, when
// the settings changed) the subscription.
func (w *MQTTWatcher) View(c MQTTConfig) BrokerView {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastPoll = time.Now()
	prefix := strings.Trim(strings.TrimSpace(c.TopicPrefix), "/")
	v := BrokerView{Broker: c.Broker, Topic: prefix + "/#", Messages: []BrokerMsg{}}
	if strings.TrimSpace(c.Broker) == "" {
		v.Error = "no broker set – enter it in Export settings → MQTT"
		return v
	}
	same := w.cfg.Broker == c.Broker && w.cfg.TopicPrefix == c.TopicPrefix && w.cfg.Username == c.Username &&
		w.cfg.Password == c.Password && w.cfg.TLSInsecure == c.TLSInsecure
	if w.client == nil || !same {
		w.stopLocked()
		w.cfg, w.topics, w.err = c, map[string]*BrokerMsg{}, ""
		w.start(c, prefix)
	}
	v.Watching = true
	v.Connected = w.client != nil && w.client.IsConnectionOpen()
	v.Error = w.err
	for _, m := range w.topics {
		v.Messages = append(v.Messages, *m)
	}
	sort.Slice(v.Messages, func(i, j int) bool { return v.Messages[i].Topic < v.Messages[j].Topic })
	return v
}

func (w *MQTTWatcher) start(c MQTTConfig, prefix string) {
	suffix := make([]byte, 3)
	for i := range suffix {
		suffix[i] = byte(rand.IntN(256))
	}
	if c.ClientID == "" {
		c.ClientID = "plcmonitor"
	}
	c.ClientID += "-view-" + hex.EncodeToString(suffix) // never kick the exporter off
	opts := mqttOptions(c).SetConnectRetry(true).SetConnectRetryInterval(5 * time.Second).SetAutoReconnect(true)
	topic := prefix + "/#"
	opts.SetOnConnectHandler(func(cl mqtt.Client) {
		t := cl.Subscribe(topic, 0, func(_ mqtt.Client, m mqtt.Message) {
			p := string(m.Payload())
			if len(p) > 2000 {
				p = p[:2000] + "…"
			}
			w.mu.Lock()
			b := w.topics[m.Topic()]
			if b == nil {
				if len(w.topics) >= 5000 {
					w.mu.Unlock()
					return
				}
				b = &BrokerMsg{Topic: m.Topic()}
				w.topics[m.Topic()] = b
			}
			b.Payload, b.Retained, b.Time = p, m.Retained(), time.Now()
			b.Count++
			w.mu.Unlock()
		})
		go func() {
			if t.WaitTimeout(5*time.Second) && t.Error() != nil {
				w.mu.Lock()
				w.err = "subscribe " + topic + ": " + t.Error().Error()
				w.mu.Unlock()
			}
		}()
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		w.mu.Lock()
		w.err = "connection lost: " + err.Error()
		w.mu.Unlock()
	})
	cl := mqtt.NewClient(opts)
	w.client = cl
	go func() {
		t := cl.Connect()
		if t.WaitTimeout(8*time.Second) && t.Error() != nil {
			w.mu.Lock()
			if w.client == cl {
				w.err = withHint(t.Error(), "1883 (tcp://), 8883 (ssl://)")
			}
			w.mu.Unlock()
		}
	}()
}

// ---- InfluxDB read back ----

// DBValue is the last point of one tag in the database.
type DBValue struct {
	Tag     string `json:"tag"`
	Address string `json:"address,omitempty"`
	Value   any    `json:"value"`
	Time    string `json:"time"`
}

type DBView struct {
	Language string    `json:"language"` // InfluxQL, Flux or SQL
	Query    string    `json:"query"`
	Rows     []DBValue `json:"rows"`
	Error    string    `json:"error,omitempty"`
}

// influxLatestQuery is the query for the last value of every tag.
func influxLatestQuery(c InfluxDBConfig) (lang, q string) {
	m := c.Measurement
	switch c.Version {
	case 1:
		return "InfluxQL", fmt.Sprintf(`SELECT last("value") AS "value" FROM %s WHERE time > now() - 30d GROUP BY "tag", "address"`,
			strconv.Quote(m))
	case 3:
		return "SQL", fmt.Sprintf(`SELECT tag, address, last_value(value ORDER BY time) AS value, max(time) AS time FROM %s `+
			`WHERE time > now() - INTERVAL '30 days' GROUP BY tag, address ORDER BY tag`, strconv.Quote(m))
	}
	return "Flux", fmt.Sprintf(`from(bucket: %s)
  |> range(start: -30d)
  |> filter(fn: (r) => r._measurement == %s and r._field == "value")
  |> last()
  |> keep(columns: ["_time", "_value", "tag", "address"])`, strconv.Quote(c.Bucket), strconv.Quote(m))
}

// InfluxLatest reads the last value of every tag back from the database.
func InfluxLatest(c InfluxDBConfig) DBView {
	if c.Version < 1 || c.Version > 3 {
		c.Version = 2
	}
	lang, q := influxLatestQuery(c)
	v := DBView{Language: lang, Query: q, Rows: []DBValue{}}
	if strings.TrimSpace(c.URL) == "" {
		v.Error = "no server set – enter it in Export settings → InfluxDB"
		return v
	}
	cl := influxClient{c, &http.Client{Timeout: 10 * time.Second}}
	var (
		resp *http.Response
		body []byte
		err  error
	)
	switch c.Version {
	case 1:
		qv := url.Values{"db": {c.Database}, "q": {q}, "epoch": {"ms"}}
		if c.RetentionPolicy != "" {
			qv.Set("rp", c.RetentionPolicy)
		}
		resp, body, err = cl.do(http.MethodGet, "/query", qv, nil, "")
	case 3:
		b, _ := json.Marshal(map[string]string{"db": c.Database, "q": q, "format": "json"})
		resp, body, err = cl.do(http.MethodPost, "/api/v3/query_sql", nil, bytes.NewReader(b), "application/json")
	default:
		qv := url.Values{}
		if c.Org != "" {
			qv.Set("org", c.Org)
		}
		resp, body, err = cl.do(http.MethodPost, "/api/v2/query", qv, strings.NewReader(q), "application/vnd.flux")
	}
	if err != nil {
		v.Error = withHint(err, "8086, or 8181 for InfluxDB 3")
		return v
	}
	if resp.StatusCode/100 != 2 {
		v.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, serverMessage(body))
		return v
	}
	switch c.Version {
	case 1:
		v.Rows, err = parseInfluxQL(body)
	case 3:
		v.Rows, err = parseInfluxSQL(body)
	default:
		v.Rows, err = parseFluxCSV(body)
	}
	if err != nil {
		v.Error = err.Error()
	}
	sort.SliceStable(v.Rows, func(i, j int) bool { return v.Rows[i].Tag < v.Rows[j].Tag })
	return v
}

func parseInfluxQL(b []byte) ([]DBValue, error) {
	var j struct {
		Results []struct {
			Error  string `json:"error"`
			Series []struct {
				Tags    map[string]string `json:"tags"`
				Columns []string          `json:"columns"`
				Values  [][]any           `json:"values"`
			} `json:"series"`
		} `json:"results"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("unexpected answer: %v", err)
	}
	if j.Error != "" {
		return nil, errors.New(j.Error)
	}
	out := []DBValue{}
	for _, r := range j.Results {
		if r.Error != "" {
			return nil, errors.New(r.Error)
		}
		for _, s := range r.Series {
			for _, row := range s.Values {
				d := DBValue{Tag: s.Tags["tag"], Address: s.Tags["address"]}
				for i, col := range s.Columns {
					if i >= len(row) {
						break
					}
					switch col {
					case "time":
						if f, ok := row[i].(float64); ok {
							d.Time = time.UnixMilli(int64(f)).Format(time.RFC3339Nano)
						} else {
							d.Time = fmt.Sprint(row[i])
						}
					case "value":
						d.Value = row[i]
					}
				}
				out = append(out, d)
			}
		}
	}
	return out, nil
}

// parseFluxCSV reads Flux's CSV answer, where each table starts with its
// own header row.
func parseFluxCSV(b []byte) ([]DBValue, error) {
	r := csv.NewReader(bytes.NewReader(b))
	r.FieldsPerRecord = -1
	r.Comment = '#'
	recs, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("unexpected answer: %v", err)
	}
	out := []DBValue{}
	var col map[string]int
	for _, rec := range recs {
		if len(rec) == 0 || (len(rec) == 1 && rec[0] == "") {
			col = nil
			continue
		}
		if contains(rec, "_value") {
			col = map[string]int{}
			for i, h := range rec {
				col[h] = i
			}
			continue
		}
		if col == nil {
			continue
		}
		get := func(k string) string {
			if i, ok := col[k]; ok && i < len(rec) {
				return rec[i]
			}
			return ""
		}
		d := DBValue{Tag: get("tag"), Address: get("address"), Time: get("_time")}
		if f, err := strconv.ParseFloat(get("_value"), 64); err == nil {
			d.Value = f
		} else {
			d.Value = get("_value")
		}
		out = append(out, d)
	}
	return out, nil
}

func parseInfluxSQL(b []byte) ([]DBValue, error) {
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("unexpected answer: %s", serverMessage(b))
	}
	out := []DBValue{}
	for _, r := range rows {
		d := DBValue{Value: r["value"]}
		d.Tag, _ = r["tag"].(string)
		d.Address, _ = r["address"].(string)
		d.Time = fmt.Sprint(r["time"])
		out = append(out, d)
	}
	return out, nil
}

// exportViewRoutes: this computer only, as they use the saved credentials.
func exportViewRoutes(mux *http.ServeMux, m *Monitor) {
	watcher := NewMQTTWatcher()
	only := func(w http.ResponseWriter, r *http.Request) bool {
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"only on the computer running PLC Monitor"})
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/export/preview", func(w http.ResponseWriter, r *http.Request) {
		if only(w, r) {
			writeJSON(w, http.StatusOK, m.ExportPreview())
		}
	})
	mux.HandleFunc("GET /api/export/broker", func(w http.ResponseWriter, r *http.Request) {
		if only(w, r) {
			writeJSON(w, http.StatusOK, watcher.View(m.Config().Export.MQTT))
		}
	})
	mux.HandleFunc("DELETE /api/export/broker", func(w http.ResponseWriter, r *http.Request) {
		if only(w, r) {
			watcher.Stop()
			respond(w, nil)
		}
	})
	mux.HandleFunc("GET /api/export/grpc.proto", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `inline; filename="monitor.proto"`)
		w.Write([]byte(grpcProto))
	})
	mux.HandleFunc("GET /api/export/database", func(w http.ResponseWriter, r *http.Request) {
		if only(w, r) {
			writeJSON(w, http.StatusOK, InfluxLatest(m.Config().Export.InfluxDB))
		}
	})
}

// moreOf is what each of the newer targets gets for one tag, made with the
// same code that sends it.
func moreOf(e ExportConfig, v liveVal) map[string]string {
	m := map[string]string{}
	tag := &Tag{Name: v.Name, Type: v.Type}
	ch := Change{Time: v.Time, Tag: tag, New: v.Value, Good: v.Good, Address: v.Address}
	pts := pointsOf([]Change{ch})
	noPoint := "(no value written while the read is bad)"
	if len(pts) == 1 {
		p := pts[0]
		m["victoriametrics"] = strings.TrimSuffix(string(linesOf(e.VictoriaMetrics.Measurement, pts)), "\n")
		m["questdb"] = strings.TrimSuffix(string(linesOf(e.QuestDB.Measurement, pts)), "\n")
		m["timescaledb"] = fmt.Sprintf("INSERT INTO %s (time, tag, address, type, value) VALUES ('%s', '%s', '%s', '%s', %s)",
			e.TimescaleDB.Table, p.Time.UTC().Format("2006-01-02 15:04:05.000Z07:00"), strings.ReplaceAll(p.Tag, "'", "''"),
			p.Address, p.Type, strconv.FormatFloat(p.Value, 'g', -1, 64))
		m["clickhouse"] = strings.TrimSuffix(string(clickhouseRows(pts)), "\n")
	} else {
		for _, k := range []string{"victoriametrics", "questdb", "timescaledb", "clickhouse"} {
			m[k] = noPoint
		}
	}
	m["amqp"] = fmt.Sprintf("exchange %s · key %s\n%s", e.AMQP.Exchange, amqpRoutingKey(e.AMQP.RoutingPrefix, v.Name), payloadOf(e.AMQP.Payload, ch))
	base := strings.TrimRight(e.CoAP.URL, "/")
	if base == "" {
		base = "coap://<host>:5683/<path>"
	}
	m["coap"] = fmt.Sprintf("%s %s/%s\n%s", strings.ToUpper(e.CoAP.Method), base, url.PathEscape(v.Name), payloadOf(e.CoAP.Payload, ch))
	var lines []string
	for _, l := range strings.Split(prometheusText([]liveVal{v}), "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	m["prometheus"] = strings.Join(lines, "\n")
	if grpcDescriptors() == nil {
		opts := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}
		if b, err := opts.Marshal(tagValueMsg(v)); err == nil {
			m["grpc"] = string(b)
		}
	}
	dt := map[uint32]string{id.Boolean: "Boolean", id.Byte: "Byte", id.SByte: "SByte", id.Int16: "Int16", id.UInt16: "UInt16",
		id.Int32: "Int32", id.UInt32: "UInt32", id.Float: "Float", id.Double: "Double"}[uaDataTypeFor(v.Type)]
	status := "Good"
	if !v.Good {
		status = "BadCommunicationError"
	}
	m["opcua_server"] = fmt.Sprintf("ns=1;s=%s · %s = %v · %s", v.Name, dt, uaTyped(v.Type, v.Value), status)
	return m
}

// targetsOf describes the newer targets (passwords left out).
func targetsOf(e ExportConfig) []TargetInfo {
	addr := func(listen string) string {
		h, p := listenHost(listen)
		return h + ":" + p
	}
	return []TargetInfo{
		{Key: "victoriametrics", Label: "VictoriaMetrics", Group: "Databases", Enabled: e.VictoriaMetrics.Enabled, Status: "victoriametrics",
			What: "Line written (to /write)", Info: [][2]string{{"Server", e.VictoriaMetrics.URL}, {"Series", e.VictoriaMetrics.Measurement + "_value{tag, address, type}"}}},
		{Key: "questdb", Label: "QuestDB", Group: "Databases", Enabled: e.QuestDB.Enabled, Status: "questdb",
			What: "Line written (ILP over HTTP)", Info: [][2]string{{"Server", e.QuestDB.URL}, {"Table", e.QuestDB.Measurement}}},
		{Key: "timescaledb", Label: "TimescaleDB", Group: "Databases", Enabled: e.TimescaleDB.Enabled, Status: "timescaledb",
			What: "Row stored (sent in batches with COPY)", Info: [][2]string{{"Database", amqpTarget(e.TimescaleDB.DSN)}, {"Table", e.TimescaleDB.Table}}},
		{Key: "clickhouse", Label: "ClickHouse", Group: "Databases", Enabled: e.ClickHouse.Enabled, Status: "clickhouse",
			What: "Row inserted (JSONEachRow)", Info: [][2]string{{"Server", e.ClickHouse.URL}, {"Table", e.ClickHouse.Database + "." + e.ClickHouse.Table}}},
		{Key: "amqp", Label: "AMQP", Group: "Messaging", Enabled: e.AMQP.Enabled, Status: "amqp",
			What: "Message published", Info: [][2]string{{"Broker", amqpTarget(e.AMQP.URL)}, {"Exchange", e.AMQP.Exchange}, {"Routing key", e.AMQP.RoutingPrefix + ".<tag>"}}},
		{Key: "coap", Label: "CoAP", Group: "Messaging", Enabled: e.CoAP.Enabled, Status: "coap",
			What: "Request sent (UDP)", Info: [][2]string{{"Server", e.CoAP.URL}, {"Method", strings.ToUpper(e.CoAP.Method)}}},
		{Key: "prometheus", Label: "Prometheus", Group: "Servers", Enabled: e.Prometheus.Enabled, Status: "prometheus",
			What: "Lines in /metrics", Info: [][2]string{{"Scrape", "http://" + addr(e.Prometheus.Listen) + "/metrics"}}},
		{Key: "grpc", Label: "gRPC", Group: "Servers", Enabled: e.GRPC.Enabled, Status: "grpc",
			What: "TagValue message (List / Subscribe)", Info: [][2]string{{"Server", addr(e.GRPC.Listen)}, {"Service", "plcmonitor.v1.Monitor"}}},
		{Key: "opcua_server", Label: "OPC UA server", Group: "Servers", Enabled: e.OPCUAServer.Enabled, Status: "opcua-server",
			What: "OPC UA variable (node · type = value · status)", Info: [][2]string{{"Endpoint", "opc.tcp://" + addr(e.OPCUAServer.Listen)}, {"Folder", "Objects/PLCMonitor"}}},
	}
}
