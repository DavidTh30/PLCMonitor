package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExportPreview(t *testing.T) {
	mon, sim := newTestMonitor(t, []Tag{{Name: "Tank Level", Area: "DB", DB: 1, Offset: 0, Type: "REAL"}, {Name: "Run", Area: "M", Type: "BOOL"}})
	mon.Update(func(c *Config) error {
		c.Export.MQTT = MQTTConfig{Broker: "tcp://x:1883", TopicPrefix: "plant1", Payload: "json"}
		c.Export.InfluxDB = InfluxDBConfig{Version: 2, Bucket: "b", Measurement: "s7"}
		return nil
	})
	go sim.Run(t.Context())
	go mon.Run(t.Context())
	waitFor(t, "values", func() bool { _, s := mon.Snapshot(); return s[0].Good && s[1].Good })
	p := mon.ExportPreview()
	if len(p.Rows) != 2 || p.MQTT.Prefix != "plant1" || p.Influx.Target != "b" {
		t.Fatalf("preview: %+v", p)
	}
	r := p.Rows[0]
	if r.Topic != "plant1/Tank Level" || !strings.Contains(r.Payload, `"quality":"good"`) || !strings.Contains(r.Payload, `"address":"DB1.DBD0"`) {
		t.Errorf("mqtt: %+v", r)
	}
	if !strings.HasPrefix(r.Line, `s7,tag=Tank\ Level,address=DB1.DBD0,type=REAL value=`) || strings.HasSuffix(r.Line, "\n") {
		t.Errorf("line: %q", r.Line)
	}
	if !strings.Contains(p.Rows[1].Line, "type=BOOL value=") {
		t.Errorf("bool line: %q", p.Rows[1].Line)
	}
}

func TestInfluxLatest(t *testing.T) {
	var gotPath, gotBody, gotQuery string
	answer := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody, gotQuery = r.URL.Path, string(b), r.URL.RawQuery
		io.WriteString(w, answer)
	}))
	defer srv.Close()

	// 1.x: InfluxQL
	answer = `{"results":[{"statement_id":0,"series":[{"name":"s7","tags":{"address":"DB1.DBD4","tag":"Tank_Level"},"columns":["time","value"],"values":[[1700000000000,42.5]]}]}]}`
	v := InfluxLatest(InfluxDBConfig{Version: 1, URL: srv.URL, Database: "plant", Measurement: "s7"})
	if v.Error != "" || v.Language != "InfluxQL" || gotPath != "/query" || !strings.Contains(gotQuery, "db=plant") ||
		len(v.Rows) != 1 || v.Rows[0].Tag != "Tank_Level" || v.Rows[0].Value != 42.5 || v.Rows[0].Address != "DB1.DBD4" {
		t.Errorf("v1: %+v (%s?%s)", v, gotPath, gotQuery)
	}

	// 2.x: Flux, CSV with one header per table
	answer = ",result,table,_time,_value,address,tag\r\n,_result,0,2026-11-21T09:00:00Z,1,M0.0,Run\r\n\r\n,result,table,_time,_value,address,tag\r\n,_result,1,2026-11-21T09:00:01Z,42.5,DB1.DBD4,Tank_Level\r\n"
	v = InfluxLatest(InfluxDBConfig{Version: 2, URL: srv.URL, Org: "acme", Bucket: "plant", Measurement: "s7"})
	if v.Error != "" || v.Language != "Flux" || gotPath != "/api/v2/query" || !strings.Contains(gotBody, `from(bucket: "plant")`) ||
		len(v.Rows) != 2 || v.Rows[0].Tag != "Run" || v.Rows[1].Value != 42.5 {
		t.Errorf("v2: %+v (%s)", v, gotBody)
	}

	// 3: SQL
	answer = `[{"tag":"Run","address":"M0.0","value":1.0,"time":"2026-11-21T09:00:00"}]`
	v = InfluxLatest(InfluxDBConfig{Version: 3, URL: srv.URL, Database: "plant", Measurement: "s7"})
	if v.Error != "" || v.Language != "SQL" || gotPath != "/api/v3/query_sql" || !strings.Contains(gotBody, `"db":"plant"`) ||
		len(v.Rows) != 1 || v.Rows[0].Value != 1.0 {
		t.Errorf("v3: %+v (%s)", v, gotBody)
	}

	// Errors are passed on.
	answer = `{"results":[{"statement_id":0,"error":"database not found: plant"}]}`
	if v := InfluxLatest(InfluxDBConfig{Version: 1, URL: srv.URL, Database: "plant", Measurement: "s7"}); !strings.Contains(v.Error, "database not found") {
		t.Errorf("error: %+v", v)
	}
	if v := InfluxLatest(InfluxDBConfig{Version: 2}); v.Error == "" || v.Query == "" {
		t.Errorf("no URL: %+v", v)
	}
}

func TestMQTTWatcher(t *testing.T) {
	b := startBroker(t, "", "")
	cfg := MQTTConfig{Broker: b.url(), ClientID: "plcmonitor", TopicPrefix: "plant1", Payload: "value", Retain: true}
	s := NewMQTTSink(cfg)
	defer s.Close()
	waitFor(t, "sink connected", func() bool { return s.Status().State == "ok" })
	tags := mustTags(t, []Tag{{Name: "Level", Area: "M", Offset: 0, Type: "INT"}})
	s.Write([]Change{{Time: time.Now(), Tag: &tags[0], New: int64(7), Good: true, Address: "MW0"}})

	w := NewMQTTWatcher()
	defer w.Stop()
	if v := w.View(MQTTConfig{}); v.Error == "" {
		t.Error("no broker accepted")
	}
	waitFor(t, "watched messages", func() bool {
		v := w.View(cfg)
		for _, m := range v.Messages {
			if m.Topic == "plant1/Level" && m.Payload == "7" && m.Retained {
				return v.Connected && v.Topic == "plant1/#"
			}
		}
		return false
	})
	s.Write([]Change{{Time: time.Now(), Tag: &tags[0], New: int64(8), Good: true, Address: "MW0"}})
	waitFor(t, "live message", func() bool {
		for _, m := range w.View(cfg).Messages {
			if m.Topic == "plant1/Level" && m.Payload == "8" && m.Count >= 2 {
				return true
			}
		}
		return false
	})
}

// TestExportServe runs the test MQTT broker and a small InfluxDB 2.x stand-in
// (it keeps the last point of every tag and answers the Data view query),
// for trying the Data view without real servers (developers):
//
//	$env:PLC_EXPORT_SERVE = "1"; go test -run TestExportServe -timeout 0 -v
func TestExportServe(t *testing.T) {
	if os.Getenv("PLC_EXPORT_SERVE") == "" {
		t.Skip("set PLC_EXPORT_SERVE to run the test MQTT broker and InfluxDB stand-in")
	}
	b := startBroker(t, "", "")
	var mu sync.Mutex
	last := map[string][3]string{} // tag -> address, value, time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/write":
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			for _, ln := range strings.Split(strings.TrimSpace(string(body)), "\n") {
				f := strings.Fields(strings.ReplaceAll(ln, `\ `, "\x00"))
				if len(f) < 3 {
					continue
				}
				kv := map[string]string{}
				for _, p := range strings.Split(f[0], ",")[1:] {
					k, v, _ := strings.Cut(p, "=")
					kv[k] = strings.ReplaceAll(v, "\x00", " ")
				}
				ms, _ := strconv.ParseInt(f[2], 10, 64)
				last[kv["tag"]] = [3]string{kv["address"], strings.TrimPrefix(f[1], "value="), time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)}
			}
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/query":
			mu.Lock()
			out := ",result,table,_time,_value,address,tag\r\n"
			i := 0
			for tag, v := range last {
				out += fmt.Sprintf(",_result,%d,%s,%s,%s,%s\r\n", i, v[2], v[1], v[0], tag)
				i++
			}
			mu.Unlock()
			io.WriteString(w, out)
		default:
			w.Header().Set("X-Influxdb-Version", "v2.7.0")
			io.WriteString(w, `{"status":"pass","version":"v2.7.0"}`)
		}
	}))
	defer srv.Close()
	info := fmt.Sprintf("mqtt=%s\ninflux=%s\n", b.url(), srv.URL)
	os.WriteFile(os.Getenv("PLC_EXPORT_SERVE"), []byte(info), 0o644)
	t.Log(info)
	select {}
}
