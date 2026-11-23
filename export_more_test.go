package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func sampleChanges(t *testing.T) []Change {
	tags := mustTags(t, []Tag{{Name: "Tank Level", Area: "DB", DB: 1, Offset: 4, Type: "REAL"}, {Name: "Run", Area: "M", Type: "BOOL"}})
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	return []Change{
		{Time: now, Tag: &tags[0], New: 42.5, Good: true, Address: tags[0].Address()},
		{Time: now, Tag: &tags[1], New: true, Good: true, Address: tags[1].Address()},
		{Time: now, Tag: &tags[1], New: nil, Good: false, Address: tags[1].Address()},
	}
}

// recorder is a fake HTTP server that keeps what it was sent.
type recorder struct {
	mu   sync.Mutex
	reqs []*http.Request
	body []string
	srv  *httptest.Server
	code int
}

func newRecorder(t *testing.T) *recorder {
	r := &recorder{code: 204}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		b, _ := io.ReadAll(q.Body)
		r.mu.Lock()
		r.reqs, r.body = append(r.reqs, q), append(r.body, string(b))
		code := r.code
		r.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) last() (*http.Request, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reqs) == 0 {
		return nil, ""
	}
	return r.reqs[len(r.reqs)-1], r.body[len(r.body)-1]
}

func TestLineDBSinks(t *testing.T) {
	for _, name := range []string{"victoriametrics", "questdb"} {
		rec := newRecorder(t)
		s := newLineDBSink(name, LineDBConfig{URL: rec.srv.URL, Measurement: "plc", Token: "tok", FlushSeconds: 1})
		s.Write(sampleChanges(t))
		s.Close() // flushes
		req, body := rec.last()
		if req == nil || req.URL.Path != "/write" || req.URL.Query().Get("precision") != "ms" || req.Header.Get("Authorization") != "Bearer tok" {
			t.Fatalf("%s: request %+v", name, req)
		}
		want := "plc,tag=Tank\\ Level,address=DB1.DBD4,type=REAL value=42.5 1795251600000\nplc,tag=Run,address=M0.0,type=BOOL value=1 1795251600000\n"
		if body != want {
			t.Errorf("%s body:\n%q\nwant\n%q", name, body, want)
		}
		if st := s.Status(); st.Sent != 2 || st.State != "ok" {
			t.Errorf("%s status %+v", name, st)
		}
	}
	// A failing server: the values are kept and sent later.
	rec := newRecorder(t)
	rec.code = 503
	s := newLineDBSink("questdb", LineDBConfig{URL: rec.srv.URL, Measurement: "t", FlushSeconds: 1})
	s.Write(sampleChanges(t))
	waitFor(t, "error", func() bool { return s.Status().State == "error" })
	if st := s.Status(); st.Pending != 2 {
		t.Errorf("kept: %+v", st)
	}
	rec.mu.Lock()
	rec.code = 204
	rec.mu.Unlock()
	waitFor(t, "sent after recovery", func() bool { st := s.Status(); return st.Sent == 2 && st.Pending == 0 })
	s.Close()

	if r := lineDBTest("victoriametrics", LineDBConfig{URL: newRecorder(t).srv.URL, Measurement: "plc"}); !r.OK {
		t.Errorf("test: %+v", r)
	}
}

func TestClickHouseSink(t *testing.T) {
	rec := newRecorder(t)
	rec.code = 200
	s := newClickHouseSink(ClickHouseConfig{URL: rec.srv.URL, Database: "plant", Table: "vals", Username: "u", Password: "p", CreateTable: true, FlushSeconds: 1})
	s.Write(sampleChanges(t))
	s.Close()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.reqs) != 2 {
		t.Fatalf("%d requests", len(rec.reqs))
	}
	if q := rec.reqs[0].URL.Query().Get("query"); !strings.HasPrefix(q, "CREATE TABLE IF NOT EXISTS plant.vals") {
		t.Errorf("create: %s", q)
	}
	if q := rec.reqs[1].URL.Query().Get("query"); q != "INSERT INTO plant.vals FORMAT JSONEachRow" || rec.reqs[1].Header.Get("X-ClickHouse-User") != "u" {
		t.Errorf("insert: %s", q)
	}
	want := `{"address":"DB1.DBD4","tag":"Tank Level","time":"2026-11-21 09:00:00.000","type":"REAL","value":42.5}` + "\n" +
		`{"address":"M0.0","tag":"Run","time":"2026-11-21 09:00:00.000","type":"BOOL","value":1}` + "\n"
	if rec.body[1] != want {
		t.Errorf("rows:\n%s", rec.body[1])
	}
}

func TestTimescaleConfig(t *testing.T) {
	e := ExportConfig{TimescaleDB: TimescaleConfig{Enabled: true, DSN: "postgres://u:p@localhost:1/plc?sslmode=disable"}}
	e.applyMoreDefaults()
	if err := e.validateMore(); err != nil || e.TimescaleDB.Table != "plc_values" {
		t.Fatalf("valid config refused: %v", err)
	}
	e.TimescaleDB.Table = "bad;table"
	if err := e.validateMore(); err == nil {
		t.Error("bad table name accepted")
	}
	if q := timescaleCreateSQL("plc_values"); !strings.Contains(q[0], "time TIMESTAMPTZ NOT NULL") {
		t.Errorf("create: %v", q)
	}
	// Nothing listening on port 1: the test says so, without hanging.
	if r := timescaleTest(TimescaleConfig{DSN: "postgres://u:p@127.0.0.1:1/plc?sslmode=disable&connect_timeout=3", Table: "t"}); r.OK {
		t.Error("connect to nothing passed")
	}
	if r := amqpTest(AMQPConfig{URL: "amqp://guest:guest@127.0.0.1:1/", Exchange: "amq.topic"}); r.OK || !strings.Contains(r.Steps[0].Detail, "refused") {
		t.Errorf("amqp to nothing: %+v", r)
	}
	if k := amqpRoutingKey("plant1.", "Tank"); k != "plant1.Tank" {
		t.Errorf("routing key %q", k)
	}
}

func TestCoAPSink(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	got := make(chan coapMsg, 10)
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			m, err := coapDecode(buf[:n])
			if err != nil {
				continue
			}
			got <- m
			if m.Type == coapCON {
				pc.WriteTo(coapEncode(coapACK, 2<<5|4, m.MID, m.Token, nil, nil), addr) // 2.04 Changed
			}
		}
	}()
	s, err := newCoAPSink(CoAPConfig{URL: "coap://" + pc.LocalAddr().String() + "/plant/s7", Method: "put", Confirmable: true, Payload: "value"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Write(sampleChanges(t)[:2])
	m := <-got
	if m.Code != coapPUT || strings.Join(m.Path, "/") != "plant/s7/Tank Level" || string(m.Payload) != "42.5" || m.Format != 0 {
		t.Errorf("message: %+v %q", m, m.Payload)
	}
	m = <-got
	if string(m.Payload) != "true" {
		t.Errorf("bool payload %q", m.Payload)
	}
	if st := s.Status(); st.Sent != 2 || st.State != "ok" {
		t.Errorf("status %+v", st)
	}
	if r := coapTest(CoAPConfig{URL: "coap://" + pc.LocalAddr().String() + "/x", Method: "post"}); !r.OK {
		t.Errorf("test: %+v", r)
	}
	// Long option values use the extended length.
	long := strings.Repeat("a", 300)
	d, _ := coapDecode(coapEncode(coapNON, coapPOST, 1, nil, []coapOption{{coapOptURIPath, []byte(long)}}, []byte("x")))
	if len(d.Path) != 1 || d.Path[0] != long || string(d.Payload) != "x" {
		t.Errorf("long option: %+v", d)
	}
}

func TestPrometheusSink(t *testing.T) {
	s, err := newPrometheusSink(PrometheusConfig{Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Write(sampleChanges(t))
	resp, err := http.Get("http://" + s.ln.Addr().String() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	text := string(b)
	for _, want := range []string{
		`plcmonitor_value{tag="Tank Level",address="DB1.DBD4",type="REAL"} 42.5`,
		`plcmonitor_value{tag="Run",address="M0.0",type="BOOL"} 1`,
		`plcmonitor_good{tag="Run",address="M0.0",type="BOOL"} 0`,
		"# TYPE plcmonitor_value gauge",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if st := s.Status(); st.Sent != 1 {
		t.Errorf("scrapes: %+v", st)
	}
}

func TestGRPCSink(t *testing.T) {
	s, err := newGRPCSink(GRPCConfig{Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ch := sampleChanges(t)
	s.Write(ch[:2])
	conn, err := grpc.NewClient(s.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp := dynamicpb.NewMessage(grpcTypes.listResp)
	if err := conn.Invoke(ctx, "/plcmonitor.v1.Monitor/List", dynamicpb.NewMessage(grpcTypes.listReq), resp); err != nil {
		t.Fatal(err)
	}
	list := resp.Get(grpcTypes.listResp.Fields().ByName("tags")).List()
	if list.Len() != 2 {
		t.Fatalf("list: %d tags", list.Len())
	}
	v := list.Get(0).Message()
	if v.Get(grpcTypes.value.Fields().ByName("name")).String() != "Tank Level" || v.Get(grpcTypes.value.Fields().ByName("value")).Float() != 42.5 {
		t.Errorf("first: %v", v)
	}

	// Subscribe to Run: its current value first, then the change.
	req := dynamicpb.NewMessage(grpcTypes.subReq)
	tl := req.Mutable(grpcTypes.subReq.Fields().ByName("tags")).List()
	tl.Append(tl.NewElement())
	tl.Set(0, protoreflect.ValueOfString("Run"))
	st, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, "/plcmonitor.v1.Monitor/Subscribe")
	if err != nil {
		t.Fatal(err)
	}
	st.SendMsg(req)
	st.CloseSend()
	first := dynamicpb.NewMessage(grpcTypes.value)
	if err := st.RecvMsg(first); err != nil {
		t.Fatal(err)
	}
	if first.Get(grpcTypes.value.Fields().ByName("text")).String() != "TRUE" {
		t.Errorf("current: %v", first)
	}
	waitFor(t, "subscribed", func() bool { return s.streams.Load() == 1 })
	s.Write(ch[2:])
	next := dynamicpb.NewMessage(grpcTypes.value)
	if err := st.RecvMsg(next); err != nil {
		t.Fatal(err)
	}
	if next.Get(grpcTypes.value.Fields().ByName("good")).Bool() {
		t.Errorf("change: %v", next)
	}
}

func TestOPCUAServerSink(t *testing.T) {
	s, err := newOPCUAServerSink(OPCUAServerConfig{Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Write(sampleChanges(t)[:2])
	b := NewUABrowser()
	defer b.Close()
	ep := "opc.tcp://127.0.0.1:" + strconv.Itoa(s.port)
	if err := b.Start(OPCUAConfig{Endpoint: ep}); err != nil {
		t.Fatal(err)
	}
	var st UABrowseState
	waitFor(t, "browse", func() bool { st = b.State(); return !st.Running })
	if st.Error != "" {
		t.Fatal(st.Error)
	}
	var node string
	for _, v := range st.Vars {
		if v.Name == "PLCMonitor.Tank Level" {
			node = v.Node
			if v.Type != "REAL" || !strings.Contains(v.Comment, "DB1.DBD4") {
				t.Errorf("variable: %+v", v)
			}
		}
	}
	if node == "" {
		t.Fatalf("Tank Level not found in %+v", st.Vars)
	}
	vals, err := b.Read([]string{node})
	if err != nil || vals[0].Value != float32(42.5) {
		t.Errorf("read: %+v %v", vals, err)
	}
}
