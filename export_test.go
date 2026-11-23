package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- a tiny MQTT 3.1.1 broker, enough for paho: QoS 0/1/2, retain, + and # ----

type fakeBroker struct {
	ln       net.Listener
	user     string
	pass     string
	mu       sync.Mutex
	subs     map[net.Conn][]string
	retained map[string][]byte
	wmu      map[net.Conn]*sync.Mutex
}

func startBroker(t *testing.T, user, pass string) *fakeBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBroker{ln: ln, user: user, pass: pass, subs: map[net.Conn][]string{}, retained: map[string][]byte{}, wmu: map[net.Conn]*sync.Mutex{}}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go b.serve(c)
		}
	}()
	return b
}

func (b *fakeBroker) url() string { return "tcp://" + b.ln.Addr().String() }

func topicMatch(filter, topic string) bool {
	f, tp := strings.Split(filter, "/"), strings.Split(topic, "/")
	for i, part := range f {
		if part == "#" {
			return true
		}
		if i >= len(tp) || (part != "+" && part != tp[i]) {
			return false
		}
	}
	return len(f) == len(tp)
}

func mqttStr(s string) []byte {
	out := binary.BigEndian.AppendUint16(nil, uint16(len(s)))
	return append(out, s...)
}

func (b *fakeBroker) send(c net.Conn, typ byte, body []byte) {
	pkt := []byte{typ}
	n := len(body)
	for {
		d := byte(n % 128)
		n /= 128
		if n > 0 {
			d |= 0x80
		}
		pkt = append(pkt, d)
		if n == 0 {
			break
		}
	}
	b.mu.Lock()
	m := b.wmu[c]
	b.mu.Unlock()
	if m != nil {
		m.Lock()
		defer m.Unlock()
	}
	c.Write(append(pkt, body...))
}

func (b *fakeBroker) deliver(c net.Conn, topic string, payload []byte, retain bool) {
	flags := byte(0x30)
	if retain {
		flags |= 1
	}
	b.send(c, flags, append(mqttStr(topic), payload...))
}

func (b *fakeBroker) serve(c net.Conn) {
	defer func() {
		b.mu.Lock()
		delete(b.subs, c)
		delete(b.wmu, c)
		b.mu.Unlock()
		c.Close()
	}()
	b.mu.Lock()
	b.wmu[c] = &sync.Mutex{}
	b.mu.Unlock()
	r := bufio.NewReader(c)
	for {
		h, err := r.ReadByte()
		if err != nil {
			return
		}
		n, mul := 0, 1
		for {
			d, err := r.ReadByte()
			if err != nil {
				return
			}
			n += int(d&0x7f) * mul
			mul *= 128
			if d&0x80 == 0 {
				break
			}
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		str := func(p []byte) (string, []byte) {
			l := int(binary.BigEndian.Uint16(p))
			return string(p[2 : 2+l]), p[2+l:]
		}
		switch h >> 4 {
		case 1: // CONNECT
			_, p := str(body) // protocol name
			flags := p[1]
			p = p[4:]     // level, flags, keepalive
			_, p = str(p) // client ID
			if flags&0x04 != 0 {
				_, p = str(p)
				_, p = str(p)
			}
			var u, pw string
			if flags&0x80 != 0 {
				u, p = str(p)
			}
			if flags&0x40 != 0 {
				pw, _ = str(p)
			}
			code := byte(0)
			if b.user != "" && (u != b.user || pw != b.pass) {
				code = 5 // not authorized
			}
			b.send(c, 0x20, []byte{0, code})
			if code != 0 {
				return
			}
		case 3: // PUBLISH
			qos := (h >> 1) & 3
			topic, p := str(body)
			var id []byte
			if qos > 0 {
				id, p = p[:2], p[2:]
			}
			payload := append([]byte(nil), p...)
			switch qos {
			case 1:
				b.send(c, 0x40, id)
			case 2:
				b.send(c, 0x50, id)
			}
			b.mu.Lock()
			if h&1 != 0 {
				b.retained[topic] = payload
			}
			var to []net.Conn
			for sc, filters := range b.subs {
				for _, f := range filters {
					if topicMatch(f, topic) {
						to = append(to, sc)
						break
					}
				}
			}
			b.mu.Unlock()
			for _, sc := range to {
				b.deliver(sc, topic, payload, false)
			}
		case 6: // PUBREL
			b.send(c, 0x70, body[:2])
		case 8: // SUBSCRIBE
			id, p := body[:2], body[2:]
			var filters []string
			ack := append([]byte(nil), id...)
			for len(p) > 0 {
				var f string
				f, p = str(p)
				p = p[1:]
				filters = append(filters, f)
				ack = append(ack, 0)
			}
			b.mu.Lock()
			b.subs[c] = append(b.subs[c], filters...)
			ret := map[string][]byte{}
			for tp, v := range b.retained {
				for _, f := range filters {
					if topicMatch(f, tp) {
						ret[tp] = v
					}
				}
			}
			b.mu.Unlock()
			b.send(c, 0x90, ack)
			for tp, v := range ret {
				b.deliver(c, tp, v, true)
			}
		case 12: // PINGREQ
			b.send(c, 0xD0, nil)
		case 14: // DISCONNECT
			return
		}
	}
}

// ---- tests ----

func TestMQTTTestAndSink(t *testing.T) {
	b := startBroker(t, "s7user", "secret")
	cfg := MQTTConfig{Broker: b.url(), ClientID: "plcmonitor", Username: "s7user", Password: "wrong", TopicPrefix: "plant1", QoS: 1, Payload: "value", Retain: true}

	r := mqttTest(cfg)
	if r.OK || len(r.Steps) != 1 || !strings.Contains(r.Steps[0].Detail, "username/password") {
		t.Errorf("wrong password: %+v", r)
	}

	cfg.Password = "secret"
	s := NewMQTTSink(cfg)
	waitFor(t, "MQTT sink connected", func() bool { return s.Status().State == "ok" })
	tags := mustTags(t, []Tag{{Name: "Tank_Level", Area: "DB", DB: 1, Offset: 4, Type: "REAL"}, {Name: "Run", Area: "M", Type: "BOOL"}})
	s.Write([]Change{
		{Time: time.Now(), Tag: &tags[0], New: 42.5, Good: true, Address: tags[0].Address()},
		{Time: time.Now(), Tag: &tags[1], New: true, Good: true, Address: tags[1].Address()},
	})
	waitFor(t, "retained values", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return string(b.retained["plant1/Tank_Level"]) == "42.5" && string(b.retained["plant1/Run"]) == "true"
	})
	if st := s.Status(); st.Sent != 2 || st.Target != b.url() {
		t.Errorf("sink status: %+v", st)
	}

	r = mqttTest(cfg)
	if !r.OK {
		t.Fatalf("test failed: %+v", r)
	}
	got := map[string]string{}
	var sawTest bool
	for _, m := range r.Messages {
		got[m.Topic] = m.Payload
		sawTest = sawTest || m.Test
	}
	if got["plant1/Tank_Level"] != "42.5" || got["plant1/status"] != "online" || !sawTest {
		t.Errorf("messages seen by the test: %+v", r.Messages)
	}
	s.Close()
	waitFor(t, "status offline", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return string(b.retained["plant1/status"]) == "offline"
	})

	// JSON payload.
	topic, p := mqttMessage(MQTTConfig{TopicPrefix: "x", Payload: "json"}, Change{Tag: &tags[0], New: 1.5, Good: true, Address: "DB1.DBD4", Time: time.Unix(0, 0)})
	var j mqttPayload
	if topic != "x/Tank_Level" || json.Unmarshal(p, &j) != nil || j.Value != 1.5 || j.Quality != "good" {
		t.Errorf("json payload %s %s", topic, p)
	}
	if _, p := mqttMessage(MQTTConfig{TopicPrefix: "x", Payload: "value"}, Change{Tag: &tags[0], Good: false}); string(p) != "null" {
		t.Errorf("bad value payload %q", p)
	}

	// Nothing listening.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	r = mqttTest(MQTTConfig{Broker: "tcp://" + addr, TopicPrefix: "p"})
	if r.OK || !strings.Contains(r.Steps[0].Detail, "nothing is listening") {
		t.Errorf("refused: %+v", r)
	}
	// The running exporter explains why it keeps "connecting".
	down := NewMQTTSink(MQTTConfig{Broker: "tcp://" + addr, ClientID: "x", TopicPrefix: "p"})
	defer down.Close()
	waitForLong(t, "reason in status", 10*time.Second, func() bool {
		st := down.Status()
		return st.State == "connecting" && strings.Contains(st.LastError, "nothing is listening")
	})
	// Reachable broker, wrong password.
	wrong := NewMQTTSink(MQTTConfig{Broker: b.url(), ClientID: "y", TopicPrefix: "p", Username: "s7user", Password: "bad"})
	defer wrong.Close()
	waitForLong(t, "login reason in status", 10*time.Second, func() bool {
		return strings.Contains(wrong.Status().LastError, "username/password")
	})
}

func TestBrokerURLCheck(t *testing.T) {
	for _, ok := range []string{"tcp://10.0.0.1:1883", "ssl://broker:8883", "ws://h:9001/mqtt", "wss://h/mqtt", "mqtt://h:1883"} {
		if err := checkBrokerURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"10.0.0.1", "tcp://10.0.0.1", "http://h:80", "tcp://"} {
		if err := checkBrokerURL(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

// fakeInflux answers like InfluxDB 1.x, 2.x or 3.x.
func fakeInflux(t *testing.T, version int, created *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		switch {
		case r.URL.Path == "/ping" && version == 3:
			w.Write([]byte(`{"version":"3.1.0","revision":"x"}`))
		case r.URL.Path == "/ping":
			w.Header().Set("X-Influxdb-Version", map[int]string{1: "1.8.10", 2: "v2.7.11"}[version])
			w.WriteHeader(http.StatusNoContent)
		case version == 1 && r.URL.Path == "/query" && r.Method == http.MethodGet:
			if u, p, _ := r.BasicAuth(); u != "admin" || p != "pw" {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"authorization failed"}`))
				return
			}
			w.Write([]byte(`{"results":[{"statement_id":0,"series":[{"name":"databases","columns":["name"],"values":[["_internal"],["plc"]]}]}]}`))
		case version == 1 && r.URL.Path == "/query" && r.Method == http.MethodPost:
			r.ParseForm()
			*created = append(*created, r.PostForm.Get("q"))
			w.Write([]byte(`{"results":[{"statement_id":0}]}`))
		case version == 2 && auth != "Token good":
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"code":"unauthorized","message":"unauthorized access"}`))
		case version == 2 && r.URL.Path == "/api/v2/buckets" && r.Method == http.MethodGet:
			if r.URL.Query().Get("org") != "acme" {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"code":"not found","message":"organization name \"` + r.URL.Query().Get("org") + `\" not found"}`))
				return
			}
			w.Write([]byte(`{"buckets":[{"name":"_monitoring"},{"name":"plc"},{"name":"energy"}]}`))
		case version == 2 && r.URL.Path == "/api/v2/orgs":
			w.Write([]byte(`{"orgs":[{"id":"0123abcd","name":"acme"}]}`))
		case version == 2 && r.URL.Path == "/api/v2/buckets" && r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			*created = append(*created, string(b))
			w.WriteHeader(http.StatusCreated)
		case version == 3 && auth != "Bearer t3":
			w.WriteHeader(http.StatusUnauthorized)
		case version == 3 && r.URL.Path == "/api/v3/configure/database" && r.Method == http.MethodGet:
			w.Write([]byte(`[{"iox::database":"_internal"},{"iox::database":"plant"}]`))
		case version == 3 && r.URL.Path == "/api/v3/configure/database" && r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			*created = append(*created, string(b))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func stepNames(r TestResult) string {
	var s []string
	for _, st := range r.Steps {
		mark := "ok"
		if !st.OK {
			mark = "FAIL"
		} else if st.Warn {
			mark = "warn"
		}
		s = append(s, st.Name+":"+mark)
	}
	return strings.Join(s, " ")
}

func TestInfluxTestHelper(t *testing.T) {
	var created []string

	v2 := fakeInflux(t, 2, &created)
	defer v2.Close()
	good := InfluxDBConfig{Version: 2, URL: v2.URL + "/", Org: "acme", Bucket: "plc", Token: "good"}
	if r := influxTest(good); !r.OK || r.ServerVersion != "v2.7.11" || strings.Join(r.Databases, ",") != "energy,plc" {
		t.Errorf("v2 good: %s %+v", stepNames(r), r)
	}
	bad := good
	bad.Token = "nope"
	if r := influxTest(bad); r.OK || !strings.Contains(stepNames(r), "Log in:FAIL") {
		t.Errorf("v2 bad token: %s", stepNames(r))
	}
	bad = good
	bad.Org = "other"
	if r := influxTest(bad); r.OK || !strings.Contains(stepNames(r), "Organization:FAIL") {
		t.Errorf("v2 bad org: %s", stepNames(r))
	}
	bad = good
	bad.Bucket = "line2"
	if r := influxTest(bad); r.OK || !r.Missing {
		t.Errorf("v2 missing bucket: %s %+v", stepNames(r), r)
	}
	if err := influxCreate(bad); err != nil || len(created) != 1 || !strings.Contains(created[0], `"orgID":"0123abcd"`) || !strings.Contains(created[0], `"name":"line2"`) {
		t.Errorf("create bucket: %v %q", err, created)
	}
	bad = good
	bad.Version = 1 // wrong version selected
	if r := influxTest(bad); r.OK || r.Version != 2 || !strings.Contains(stepNames(r), "Version:FAIL") {
		t.Errorf("version mismatch: %s %+v", stepNames(r), r)
	}

	v1 := fakeInflux(t, 1, &created)
	defer v1.Close()
	c1 := InfluxDBConfig{Version: 1, URL: v1.URL, Database: "plc", Username: "admin", Password: "pw"}
	if r := influxTest(c1); !r.OK || r.Version != 1 {
		t.Errorf("v1: %s", stepNames(r))
	}
	c1.Database = "new"
	if r := influxTest(c1); r.OK || !r.Missing {
		t.Errorf("v1 missing: %s", stepNames(r))
	}
	if err := influxCreate(c1); err != nil || created[len(created)-1] != `CREATE DATABASE "new"` {
		t.Errorf("v1 create: %v %q", err, created)
	}
	c1.Database = `bad"name`
	if err := influxCreate(c1); err == nil {
		t.Error("quote in database name accepted")
	}

	v3 := fakeInflux(t, 3, &created)
	defer v3.Close()
	c3 := InfluxDBConfig{Version: 3, URL: v3.URL, Database: "plant", Token: "t3"}
	if r := influxTest(c3); !r.OK || r.Version != 3 || strings.Join(r.Databases, ",") != "plant" {
		t.Errorf("v3: %s %+v", stepNames(r), r)
	}
	c3.Database = "fresh" // v3 creates it on write: only a warning
	if r := influxTest(c3); !r.OK || !r.Missing {
		t.Errorf("v3 missing: %s", stepNames(r))
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	if r := influxTest(InfluxDBConfig{Version: 2, URL: "http://" + addr}); r.OK || !strings.Contains(r.Steps[0].Detail, "nothing is listening") {
		t.Errorf("refused: %+v", r)
	}
	if r := influxTest(InfluxDBConfig{Version: 2, URL: "localhost:8086"}); r.OK {
		t.Error("URL without http:// accepted")
	}
}

func TestInfluxStatusAndPermanentErrors(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"message":"field type conflict"}`))
		}
	}))
	defer srv.Close()
	tag := mustTags(t, []Tag{{Name: "T", Area: "M", Type: "INT"}})[0]
	s := NewInfluxSink(InfluxDBConfig{Version: 2, URL: srv.URL, Bucket: "b", Measurement: "s7", FlushInterval: time.Hour})
	s.Write([]Change{{Time: time.Now(), Tag: &tag, New: int64(1), Good: true}})
	if st := s.Status(); st.Pending != 1 {
		t.Errorf("pending: %+v", st)
	}
	s.flush()
	st := s.Status()
	if st.State != "error" || !strings.Contains(st.LastError, "field type conflict") || st.Pending != 0 {
		t.Errorf("after rejected write: %+v", st) // 400 is not retried
	}
	s.flush()
	s.Close()
	if posts != 1 {
		t.Errorf("rejected batch was sent %d times", posts)
	}
}

func TestDispatcherReconfigure(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.csv"), filepath.Join(dir, "sub", "b.csv")
	d, err := NewExporter(ExportConfig{CSV: CSVConfig{Enabled: true, Path: a}})
	if err != nil {
		t.Fatal(err)
	}
	tag := mustTags(t, []Tag{{Name: "T", Area: "M", Type: "INT"}})[0]
	ch := []Change{{Time: time.Now(), Tag: &tag, New: int64(1), Good: true, Address: "MW0"}}
	d.Send(ch)
	waitFor(t, "row in a", func() bool { st := d.Status(); return len(st) == 1 && st[0].Sent == 1 })
	if err := d.Reconfigure(ExportConfig{CSV: CSVConfig{Enabled: true, Path: b}}); err != nil {
		t.Fatal(err)
	}
	d.Send(ch)
	d.Send(ch)
	waitFor(t, "rows in b", func() bool { st := d.Status(); return len(st) == 1 && st[0].Sent == 2 })
	if err := d.Reconfigure(ExportConfig{CSV: CSVConfig{Enabled: true, Path: dir}}); err == nil {
		t.Error("a folder was accepted as the CSV file")
	}
	d.Close()
	for path, rows := range map[string]int{a: 1, b: 2} {
		data, _ := os.ReadFile(path)
		if n := strings.Count(string(data), "\n") - 1; n != rows {
			t.Errorf("%s: %d rows, want %d", path, n, rows)
		}
	}
	if st := failedExporter(os.ErrPermission).Status(); len(st) != 1 || st[0].State != "error" {
		t.Errorf("failed exporter status: %+v", st)
	}
}

func TestCSVCheck(t *testing.T) {
	dir := t.TempDir()
	if r := csvTest(filepath.Join(dir, "new.csv")); !r.OK || !strings.Contains(r.Steps[0].Detail, "will be created") {
		t.Errorf("new file: %+v", r)
	}
	f := filepath.Join(dir, "old.csv")
	os.WriteFile(f, []byte("a,b\n"), 0o644)
	if r := csvTest(f); !r.OK || !strings.Contains(r.Steps[0].Detail, "4 bytes") {
		t.Errorf("existing file: %+v", r)
	}
	if r := csvTest(dir); r.OK {
		t.Error("folder accepted")
	}
	if r := csvTest(" "); r.OK {
		t.Error("empty name accepted")
	}
}

func TestExportAPI(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Simulate: true}
	cfg.applyDefaults()
	path := filepath.Join(dir, "config.yaml")
	disp, _ := NewExporter(cfg.Export)
	mon := NewMonitor(cfg, nil, disp, path)
	defer disp.Close()
	h := NewWebHandler(mon, false)
	do := func(method, target, body, remote string) (int, string) {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	const local, remote = "127.0.0.1:5000", "192.168.1.50:5000"
	if c, _ := do("GET", "/api/export", "", remote); c != http.StatusForbidden {
		t.Errorf("remote read of export settings: %d", c)
	}
	c, body := do("GET", "/api/export", "", local)
	var got struct {
		Export exportJSON `json:"export"`
	}
	if c != 200 || json.Unmarshal([]byte(body), &got) != nil || got.Export.InfluxDB.FlushInterval != "5s" || got.Export.MQTT.Payload != "json" {
		t.Fatalf("get: %d %s", c, body)
	}

	e := got.Export
	e.CSV = CSVConfig{Enabled: true, Path: filepath.Join(dir, "log.csv")}
	e.MQTT.Broker = "tcp://127.0.0.1:1" // not enabled: not contacted, but must be valid when enabled
	e.InfluxDB.FlushInterval = "2s"
	b, _ := json.Marshal(e)
	if c, body := do("PUT", "/api/export", string(b), remote); c != http.StatusForbidden {
		t.Errorf("remote change: %d %s", c, body)
	}
	if c, body := do("PUT", "/api/export", string(b), local); c != 200 {
		t.Fatalf("put: %d %s", c, body)
	}
	if mc := mon.Config(); !mc.Export.CSV.Enabled || mc.Export.InfluxDB.FlushInterval != 2*time.Second {
		t.Errorf("config: %+v", mc.Export)
	}
	if st := mon.ExportStatus(); len(st) != 1 || st[0].Name != "csv" {
		t.Errorf("status after apply: %+v", st)
	}
	if saved, _ := os.ReadFile(path); !strings.Contains(string(saved), "log.csv") {
		t.Error("export settings not saved to the config file")
	}

	e.MQTT.Enabled, e.MQTT.Broker = true, "192.168.1.10" // no scheme/port
	b, _ = json.Marshal(e)
	if c, body := do("PUT", "/api/export", string(b), local); c != 400 || !strings.Contains(body, "scheme://host:port") {
		t.Errorf("bad broker: %d %s", c, body)
	}
	e.MQTT.Enabled = false
	e.InfluxDB.FlushInterval = "1ms"
	b, _ = json.Marshal(e)
	if c, _ := do("PUT", "/api/export", string(b), local); c != 400 {
		t.Errorf("bad flush interval: %d", c)
	}
	if c, body := do("POST", "/api/export/test/csv", string(b), local); c != 200 || !strings.Contains(body, `"ok":true`) {
		t.Errorf("csv test: %d %s", c, body)
	}
	for _, page := range []string{"/export", "/help"} {
		if c, body := do("GET", page, "", remote); c != 200 || !strings.Contains(body, "<html") {
			t.Errorf("%s: %d", page, c)
		}
	}
	if c, _ := do("GET", "/api/export/status", "", remote); c != 200 {
		t.Errorf("status: %d", c)
	}

	// Theme: saved in config.yaml from this computer and served in themes.js.
	if c, _ := do("PUT", "/api/ui/theme", `{"theme":"nord"}`, remote); c != http.StatusForbidden {
		t.Errorf("remote theme change: %d", c)
	}
	if c, _ := do("PUT", "/api/ui/theme", `{"theme":"</script>"}`, local); c != 400 {
		t.Errorf("bad theme id: %d", c)
	}
	if c, body := do("PUT", "/api/ui/theme", `{"theme":"nord"}`, local); c != 200 {
		t.Fatalf("theme: %d %s", c, body)
	}
	if c, body := do("GET", "/themes.js", "", remote); c != 200 || !strings.HasPrefix(body, `window.S7_SERVER_THEME = "nord";`) || !strings.Contains(body, "openPicker") {
		t.Errorf("themes.js: %d %.80s", c, body)
	}
	if saved, _ := os.ReadFile(path); !strings.Contains(string(saved), "theme: nord") {
		t.Error("theme not saved")
	}
	do("PUT", "/api/ui/theme", `{"theme":"system"}`, local)
	if mon.Config().Web.Theme != "" {
		t.Error("system theme should clear web.theme")
	}

	// No tags (e.g. after File → Tag configuration → New): an empty list,
	// not null, or the page stops updating.
	if c, body := do("GET", "/api/state", "", local); c != 200 || !strings.Contains(body, `"tags":[]`) {
		t.Errorf("state without tags: %s", body)
	}

	// Window layout: saved from this computer, shared by all windows.
	if c, body := do("GET", "/api/ui/layout", "", remote); c != 200 || !strings.Contains(body, `"charts":[]`) || !strings.Contains(body, `"can_save":false`) {
		t.Errorf("layout: %d %s", c, body)
	}
	lay := `{"chart_place":"window","charts":[{"id":"c1","title":"Tank","tags":["a","b"],"window":true,"scale":"separate","range_ms":3600000}],` +
		`"tag_windows":[{"id":"w1","title":"Line 1","tags":["a"]}]}`
	if c, _ := do("PUT", "/api/ui/layout", lay, remote); c != http.StatusForbidden {
		t.Errorf("remote layout save: %d", c)
	}
	if c, body := do("PUT", "/api/ui/layout", lay, local); c != 200 || !strings.Contains(body, `"rev":1`) {
		t.Fatalf("layout save: %d %s", c, body)
	}
	if l, rev := mon.Layout(); rev != 1 || len(l.Charts) != 1 || l.Charts[0].Scale != "separate" || l.TagWindows[0].Title != "Line 1" {
		t.Errorf("layout: %d %+v", rev, l)
	}
	if saved, _ := os.ReadFile(path); !strings.Contains(string(saved), "title: Line 1") {
		t.Error("layout not saved in config.yaml")
	}
	for _, bad := range []string{
		`{"charts":[{"id":"c1","tags":[]}]}`,                             // chart without tags
		`{"charts":[{"id":"c1","tags":["a"]},{"id":"c1","tags":["b"]}]}`, // duplicate id
		`{"charts":[{"id":"x y","tags":["a"]}]}`,                         // bad id
		`{"chart_place":"elsewhere"}`,                                    // bad place
		`{"tag_windows":[{"id":"w1","title":"","tags":[]}]}`,             // nameless window
	} {
		if c, _ := do("PUT", "/api/ui/layout", bad, local); c != 400 {
			t.Errorf("accepted %s: %d", bad, c)
		}
	}
	// A config change (here the theme) keeps the layout.
	do("PUT", "/api/ui/theme", `{"theme":"dark"}`, local)
	if l, _ := mon.Layout(); len(l.Charts) != 1 {
		t.Error("layout lost by another config change")
	}

	// Always on top: only in the desktop app, and remembered.
	if c, _ := do("PUT", "/api/ui/topmost", `{"on":true}`, local); c != http.StatusNotImplemented {
		t.Errorf("topmost without a window: %d", c)
	}
	var got2 []bool
	setAlwaysOnTop = func(on bool) { got2 = append(got2, on) }
	defer func() { setAlwaysOnTop = nil }()
	if c, _ := do("PUT", "/api/ui/topmost", `{"on":true}`, remote); c != http.StatusForbidden {
		t.Errorf("remote topmost: %d", c)
	}
	if c, body := do("PUT", "/api/ui/topmost", `{"on":true}`, local); c != 200 || len(got2) != 1 || !got2[0] || !mon.Config().Web.AlwaysOnTop {
		t.Errorf("topmost: %d %s %v", c, body, got2)
	}
	if _, body := do("GET", "/api/config", "", local); !strings.Contains(body, `"can_pin":true`) || !strings.Contains(body, `"always_on_top":true`) {
		t.Errorf("config: %s", body)
	}
}
