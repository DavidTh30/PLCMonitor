package main

// Server targets: other programs connect to PLC Monitor and read the tags.
//
//	Prometheus  GET http://<this PC>:9273/metrics (text exposition format)
//	gRPC        plcmonitor.v1.Monitor: List and Subscribe (stream), with
//	            server reflection, so grpcurl and Postman see the API
//	OPC UA      opc.tcp://<this PC>:4840, Objects/S7Monitor/<tag>, read-only
//
// Each keeps the latest value of every tag, from the changes it is sent.

import (
	"context"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopcua/opcua/id"
	uaserver "github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// liveVal is the latest state of one tag.
type liveVal struct {
	Name, Address, Type string
	Value               any // bool, int64 or float64; nil before the first good read
	Good                bool
	Time                time.Time
}

func (v liveVal) float() (float64, bool) {
	if v.Value == nil {
		return 0, false
	}
	f := toFloat(v.Value)
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}

// latest keeps the newest value of every tag, in the order first seen.
type latest struct {
	mu    sync.RWMutex
	vals  map[string]*liveVal
	order []string
}

func newLatest() *latest { return &latest{vals: map[string]*liveVal{}} }

// apply stores the changes and returns the new states.
func (l *latest) apply(changes []Change) []liveVal {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]liveVal, 0, len(changes))
	for _, c := range changes {
		v := l.vals[c.Tag.Name]
		if v == nil {
			v = &liveVal{Name: c.Tag.Name}
			l.vals[c.Tag.Name] = v
			l.order = append(l.order, c.Tag.Name)
		}
		v.Address, v.Type, v.Good, v.Time = c.Address, c.Tag.Type, c.Good, c.Time
		if c.Good && c.New != nil {
			v.Value = c.New
		}
		out = append(out, *v)
	}
	return out
}

func (l *latest) all() []liveVal {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]liveVal, 0, len(l.order))
	for _, n := range l.order {
		out = append(out, *l.vals[n])
	}
	return out
}

// listenURL turns ":9273" into a reachable-looking address for the status.
func listenHost(listen string) (host, port string) {
	host, port, _ = net.SplitHostPort(listen)
	if host == "" || host == "0.0.0.0" || host == "::" {
		if h, err := os.Hostname(); err == nil {
			host = h
		} else {
			host = "localhost"
		}
	}
	return host, port
}

// ---- Prometheus ----

type prometheusSink struct {
	vals    *latest
	srv     *http.Server
	ln      net.Listener
	scrapes atomic.Int64
	last    atomic.Int64 // unix ms of the last scrape
	target  string
}

func newPrometheusSink(c PrometheusConfig) (*prometheusSink, error) {
	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return nil, fmt.Errorf("Prometheus: listen on %s: %w", c.Listen, err)
	}
	host, _ := listenHost(c.Listen)
	s := &prometheusSink{vals: newLatest(), ln: ln}
	s.target = fmt.Sprintf("http://%s:%d/metrics", host, ln.Addr().(*net.TCPAddr).Port)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		s.scrapes.Add(1)
		s.last.Store(time.Now().UnixMilli())
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Write([]byte(prometheusText(s.vals.all())))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body><h1>PLC Monitor</h1><p><a href="metrics">/metrics</a> – the tags for Prometheus</p></body></html>`)
	})
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go s.srv.Serve(ln)
	log.Printf("export prometheus: serving %s", s.target)
	return s, nil
}

var promEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// prometheusText renders the tags in the text exposition format.
func prometheusText(vals []liveVal) string {
	var b strings.Builder
	label := func(v liveVal) string {
		return fmt.Sprintf(`{tag="%s",address="%s",type="%s"}`, promEscaper.Replace(v.Name), promEscaper.Replace(v.Address), promEscaper.Replace(v.Type))
	}
	b.WriteString("# HELP plcmonitor_value Current value of the tag (BOOL is 0 or 1).\n# TYPE plcmonitor_value gauge\n")
	for _, v := range vals {
		if f, ok := v.float(); ok {
			fmt.Fprintf(&b, "plcmonitor_value%s %s\n", label(v), strconv.FormatFloat(f, 'g', -1, 64))
		}
	}
	b.WriteString("# HELP plcmonitor_good 1 when the last read of the tag was good, 0 when not (PLC unreachable, bad address).\n# TYPE plcmonitor_good gauge\n")
	for _, v := range vals {
		g := 0
		if v.Good {
			g = 1
		}
		fmt.Fprintf(&b, "plcmonitor_good%s %d\n", label(v), g)
	}
	b.WriteString("# HELP plcmonitor_last_change_seconds Unix time of the tag's last change.\n# TYPE plcmonitor_last_change_seconds gauge\n")
	for _, v := range vals {
		fmt.Fprintf(&b, "plcmonitor_last_change_seconds%s %.3f\n", label(v), float64(v.Time.UnixMilli())/1000)
	}
	return b.String()
}

func (s *prometheusSink) Name() string { return "prometheus" }
func (s *prometheusSink) Write(changes []Change) error {
	s.vals.apply(changes)
	return nil
}
func (s *prometheusSink) Status() SinkStatus {
	st := SinkStatus{Name: "prometheus", State: "ok", Target: s.target, Sent: s.scrapes.Load()}
	if ms := s.last.Load(); ms > 0 {
		st.LastOK = time.UnixMilli(ms)
	}
	return st
}
func (s *prometheusSink) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

// ---- gRPC ----

// grpcProto is the API, as a .proto file (also in the README).
const grpcProto = `syntax = "proto3";
package plcmonitor.v1;

service Monitor {
  // The current value of every tag.
  rpc List(ListRequest) returns (ListResponse);
  // The current values, then every change, until the client stops.
  rpc Subscribe(SubscribeRequest) returns (stream TagValue);
}

message ListRequest {}
message ListResponse { repeated TagValue tags = 1; }
message SubscribeRequest {
  repeated string tags = 1;   // empty: all tags
  bool skip_current = 2;      // only changes, not the current values first
}
message TagValue {
  string name = 1;
  string address = 2;         // e.g. DB1.DBD4
  string type = 3;            // e.g. REAL
  double value = 4;           // BOOL is 0 or 1
  string text = 5;            // the value as text, e.g. TRUE or 42.5
  bool good = 6;              // false when the read failed
  int64 time_unix_ms = 7;
}
`

var (
	grpcOnce  sync.Once
	grpcFile  protoreflect.FileDescriptor
	grpcErr   error
	grpcTypes struct {
		listReq, listResp, subReq, value protoreflect.MessageDescriptor
	}
)

// grpcDescriptors builds the descriptors of grpcProto in code (no protoc
// needed) and registers them, which gRPC server reflection uses.
func grpcDescriptors() error {
	grpcOnce.Do(func() {
		opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
		rep := descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
		f := func(name string, n int32, l *descriptorpb.FieldDescriptorProto_Label, t descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.FieldDescriptorProto {
			fd := &descriptorpb.FieldDescriptorProto{Name: proto.String(name), Number: proto.Int32(n), Label: l, Type: t.Enum(), JsonName: proto.String(jsonName(name))}
			if typeName != "" {
				fd.TypeName = proto.String(typeName)
			}
			return fd
		}
		T := descriptorpb.FieldDescriptorProto_TYPE_STRING
		fdp := &descriptorpb.FileDescriptorProto{
			Name: proto.String("plcmonitor/v1/monitor.proto"), Package: proto.String("plcmonitor.v1"), Syntax: proto.String("proto3"),
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: proto.String("ListRequest")},
				{Name: proto.String("ListResponse"), Field: []*descriptorpb.FieldDescriptorProto{
					f("tags", 1, rep, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".plcmonitor.v1.TagValue")}},
				{Name: proto.String("SubscribeRequest"), Field: []*descriptorpb.FieldDescriptorProto{
					f("tags", 1, rep, T, ""), f("skip_current", 2, opt, descriptorpb.FieldDescriptorProto_TYPE_BOOL, "")}},
				{Name: proto.String("TagValue"), Field: []*descriptorpb.FieldDescriptorProto{
					f("name", 1, opt, T, ""), f("address", 2, opt, T, ""), f("type", 3, opt, T, ""),
					f("value", 4, opt, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE, ""), f("text", 5, opt, T, ""),
					f("good", 6, opt, descriptorpb.FieldDescriptorProto_TYPE_BOOL, ""),
					f("time_unix_ms", 7, opt, descriptorpb.FieldDescriptorProto_TYPE_INT64, "")}},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: proto.String("Monitor"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{Name: proto.String("List"), InputType: proto.String(".plcmonitor.v1.ListRequest"), OutputType: proto.String(".plcmonitor.v1.ListResponse")},
					{Name: proto.String("Subscribe"), InputType: proto.String(".plcmonitor.v1.SubscribeRequest"), OutputType: proto.String(".plcmonitor.v1.TagValue"), ServerStreaming: proto.Bool(true)},
				},
			}},
		}
		grpcFile, grpcErr = protodesc.NewFile(fdp, protoregistry.GlobalFiles)
		if grpcErr != nil {
			return
		}
		if _, err := protoregistry.GlobalFiles.FindFileByPath(grpcFile.Path()); err != nil {
			grpcErr = protoregistry.GlobalFiles.RegisterFile(grpcFile)
		}
		m := grpcFile.Messages()
		grpcTypes.listReq, grpcTypes.listResp = m.ByName("ListRequest"), m.ByName("ListResponse")
		grpcTypes.subReq, grpcTypes.value = m.ByName("SubscribeRequest"), m.ByName("TagValue")
	})
	return grpcErr
}

func jsonName(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func tagValueMsg(v liveVal) *dynamicpb.Message {
	m := dynamicpb.NewMessage(grpcTypes.value)
	f := grpcTypes.value.Fields()
	m.Set(f.ByName("name"), protoreflect.ValueOfString(v.Name))
	m.Set(f.ByName("address"), protoreflect.ValueOfString(v.Address))
	m.Set(f.ByName("type"), protoreflect.ValueOfString(v.Type))
	if x, ok := v.float(); ok {
		m.Set(f.ByName("value"), protoreflect.ValueOfFloat64(x))
	}
	m.Set(f.ByName("text"), protoreflect.ValueOfString(FormatValue(v.Value)))
	m.Set(f.ByName("good"), protoreflect.ValueOfBool(v.Good))
	m.Set(f.ByName("time_unix_ms"), protoreflect.ValueOfInt64(v.Time.UnixMilli()))
	return m
}

type grpcSink struct {
	vals    *latest
	srv     *grpc.Server
	target  string
	addr    string // host:port it listens on
	mu      sync.Mutex
	subs    map[chan liveVal]map[string]bool // subscriber → tags (nil: all)
	streams atomic.Int64
	sent    atomic.Int64
}

func newGRPCSink(c GRPCConfig) (*grpcSink, error) {
	if err := grpcDescriptors(); err != nil {
		return nil, fmt.Errorf("gRPC: %w", err)
	}
	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return nil, fmt.Errorf("gRPC: listen on %s: %w", c.Listen, err)
	}
	host, _ := listenHost(c.Listen)
	s := &grpcSink{vals: newLatest(), srv: grpc.NewServer(), subs: map[chan liveVal]map[string]bool{}, addr: ln.Addr().String(),
		target: fmt.Sprintf("%s:%d (plcmonitor.v1.Monitor)", host, ln.Addr().(*net.TCPAddr).Port)}
	s.srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "plcmonitor.v1.Monitor",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{MethodName: "List", Handler: func(_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			req := dynamicpb.NewMessage(grpcTypes.listReq)
			if err := dec(req); err != nil {
				return nil, err
			}
			resp := dynamicpb.NewMessage(grpcTypes.listResp)
			list := resp.Mutable(grpcTypes.listResp.Fields().ByName("tags")).List()
			for _, v := range s.vals.all() {
				list.Append(protoreflect.ValueOfMessage(tagValueMsg(v)))
			}
			return resp, nil
		}}},
		Streams: []grpc.StreamDesc{{StreamName: "Subscribe", ServerStreams: true, Handler: func(_ any, stream grpc.ServerStream) error {
			req := dynamicpb.NewMessage(grpcTypes.subReq)
			if err := stream.RecvMsg(req); err != nil {
				return err
			}
			var want map[string]bool
			tl := req.Get(grpcTypes.subReq.Fields().ByName("tags")).List()
			if tl.Len() > 0 {
				want = map[string]bool{}
				for i := 0; i < tl.Len(); i++ {
					want[tl.Get(i).String()] = true
				}
			}
			ch := make(chan liveVal, 1024)
			s.mu.Lock()
			s.subs[ch] = want
			s.mu.Unlock()
			s.streams.Add(1)
			defer func() {
				s.mu.Lock()
				delete(s.subs, ch)
				s.mu.Unlock()
				s.streams.Add(-1)
			}()
			if !req.Get(grpcTypes.subReq.Fields().ByName("skip_current")).Bool() {
				for _, v := range s.vals.all() {
					if want == nil || want[v.Name] {
						if err := stream.SendMsg(tagValueMsg(v)); err != nil {
							return err
						}
						s.sent.Add(1)
					}
				}
			}
			for {
				select {
				case <-stream.Context().Done():
					return nil
				case v := <-ch:
					if err := stream.SendMsg(tagValueMsg(v)); err != nil {
						return err
					}
					s.sent.Add(1)
				}
			}
		}}},
		Metadata: "plcmonitor/v1/monitor.proto",
	}, struct{}{})
	reflection.Register(s.srv)
	go s.srv.Serve(ln)
	log.Printf("export grpc: serving %s", s.target)
	return s, nil
}

func (s *grpcSink) Name() string { return "grpc" }
func (s *grpcSink) Write(changes []Change) error {
	vals := s.vals.apply(changes)
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch, want := range s.subs {
		for _, v := range vals {
			if want == nil || want[v.Name] {
				select {
				case ch <- v:
				default: // a slow client misses values rather than holding up the others
				}
			}
		}
	}
	return nil
}
func (s *grpcSink) Status() SinkStatus {
	st := SinkStatus{Name: "grpc", State: "ok", Target: fmt.Sprintf("%s · %d stream(s)", s.target, s.streams.Load()), Sent: s.sent.Load()}
	return st
}
func (s *grpcSink) Close() error {
	done := make(chan struct{})
	go func() { s.srv.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.srv.Stop()
	}
	return nil
}

// ---- OPC UA server ----

type opcuaServerSink struct {
	vals   *latest
	srv    *uaserver.Server
	ns     *uaserver.NodeNameSpace
	folder *uaserver.Node
	mu     sync.Mutex
	nodes  map[string]*ua.NodeID
	target string
	port   int
	reads  atomic.Int64
}

// uaDataTypeFor is the OPC UA data type of a tag type.
func uaDataTypeFor(t string) uint32 {
	return map[string]uint32{
		"BOOL": id.Boolean, "BYTE": id.Byte, "USINT": id.Byte, "SINT": id.SByte, "INT": id.Int16,
		"WORD": id.UInt16, "UINT": id.UInt16, "DINT": id.Int32, "DWORD": id.UInt32, "UDINT": id.UInt32,
		"REAL": id.Float, "LREAL": id.Double,
	}[t]
}

// uaTyped converts a tag value to the Go type of its OPC UA data type.
func uaTyped(t string, v any) any {
	if v == nil {
		return nil
	}
	switch t {
	case "BOOL":
		b, _ := v.(bool)
		return b
	case "BYTE", "USINT":
		return uint8(toInt(v))
	case "SINT":
		return int8(toInt(v))
	case "INT":
		return int16(toInt(v))
	case "WORD", "UINT":
		return uint16(toInt(v))
	case "DINT":
		return int32(toInt(v))
	case "DWORD", "UDINT":
		return uint32(toInt(v))
	case "REAL":
		return float32(toFloat(v))
	}
	return toFloat(v)
}

func newOPCUAServerSink(c OPCUAServerConfig) (*opcuaServerSink, error) {
	host, portText, _ := net.SplitHostPort(c.Listen)
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, fmt.Errorf("OPC UA server: port %q", portText)
	}
	if port == 0 { // pick a free port (tests)
		ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			return nil, err
		}
		port = ln.Addr().(*net.TCPAddr).Port
		ln.Close()
	}
	opts := []uaserver.Option{
		uaserver.EnableSecurity("None", ua.MessageSecurityModeNone),
		uaserver.EnableAuthMode(ua.UserTokenTypeAnonymous),
		uaserver.ServerName("PLC Monitor"), uaserver.ManufacturerName("PLC Monitor"), uaserver.ProductName("PLC Monitor"),
	}
	advertised := host
	if host == "" || host == "0.0.0.0" {
		// Listen on every interface; tell clients this PC's name.
		opts = append(opts, uaserver.EndPoint("0.0.0.0", port))
		advertised, _ = listenHost(c.Listen)
		opts = append(opts, uaserver.EndPoint(advertised, port), uaserver.EndPoint("localhost", port))
	} else {
		opts = append(opts, uaserver.EndPoint(host, port))
	}
	srv := uaserver.New(opts...)
	ns := uaserver.NewNodeNameSpace(srv, "urn:plcmonitor")
	root := srv.Node(ua.NewNumericNodeID(0, id.ObjectsFolder))
	folder := ns.AddNode(uaserver.NewFolderNode(ua.NewStringNodeID(ns.ID(), "PLCMonitor"), "PLCMonitor"))
	root.AddRef(folder, uaserver.RefTypeIDOrganizes, true)
	if err := srv.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("OPC UA server on port %d: %w", port, err)
	}
	s := &opcuaServerSink{vals: newLatest(), srv: srv, ns: ns, folder: folder, nodes: map[string]*ua.NodeID{}, port: port,
		target: fmt.Sprintf("opc.tcp://%s:%d · ns=%d;s=<tag>", advertised, port, ns.ID())}
	log.Printf("export opcua-server: serving %s", s.target)
	return s, nil
}

func (s *opcuaServerSink) Name() string { return "opcua-server" }

// node adds the variable for a tag the first time it is seen.
func (s *opcuaServerSink) node(v liveVal) *ua.NodeID {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.nodes[v.Name]; n != nil {
		return n
	}
	nid := ua.NewStringNodeID(s.ns.ID(), v.Name)
	name, typ := v.Name, v.Type
	x := uaserver.NewVariableNode(nid, name, func() *ua.DataValue {
		s.reads.Add(1)
		cur := s.current(name)
		dv := &ua.DataValue{EncodingMask: ua.DataValueValue | ua.DataValueSourceTimestamp | ua.DataValueServerTimestamp,
			SourceTimestamp: cur.Time, ServerTimestamp: time.Now()}
		val := uaTyped(typ, cur.Value)
		if val == nil {
			val = uaTyped(typ, int64(0))
		}
		dv.Value = ua.MustVariant(val)
		if !cur.Good {
			dv.EncodingMask |= ua.DataValueStatusCode
			dv.Status = ua.StatusBadCommunicationError
		}
		return dv
	})
	if dt := uaDataTypeFor(typ); dt != 0 {
		x.SetAttribute(ua.AttributeIDDataType, uaserver.DataValueFromValue(ua.NewNumericNodeID(0, dt)))
	}
	x.SetAttribute(ua.AttributeIDAccessLevel, uaserver.DataValueFromValue(byte(ua.AccessLevelTypeCurrentRead)))
	x.SetAttribute(ua.AttributeIDUserAccessLevel, uaserver.DataValueFromValue(byte(ua.AccessLevelTypeCurrentRead)))
	x.SetAttribute(ua.AttributeIDValueRank, uaserver.DataValueFromValue(int32(-1)))
	x.SetAttribute(ua.AttributeIDDescription, uaserver.DataValueFromValue(&ua.LocalizedText{EncodingMask: ua.LocalizedTextText, Text: v.Address + " · " + v.Type}))
	s.ns.AddNode(x)
	s.folder.AddRef(x, uaserver.RefTypeIDHasComponent, true)
	s.nodes[v.Name] = nid
	return nid
}

func (s *opcuaServerSink) current(name string) liveVal {
	for _, v := range s.vals.all() {
		if v.Name == name {
			return v
		}
	}
	return liveVal{}
}

func (s *opcuaServerSink) Write(changes []Change) error {
	for _, v := range s.vals.apply(changes) {
		nid := s.node(v)
		s.ns.ChangeNotification(nid) // subscriptions get the new value
	}
	return nil
}

func (s *opcuaServerSink) Status() SinkStatus {
	s.mu.Lock()
	n := len(s.nodes)
	s.mu.Unlock()
	return SinkStatus{Name: "opcua-server", State: "ok", Target: fmt.Sprintf("%s · %d variables", s.target, n), Sent: s.reads.Load()}
}

func (s *opcuaServerSink) Close() error { return s.srv.Close() }

// serverTest checks that the listen address is free (or ours already).
func serverTest(what, listen string, running []SinkStatus, name string) TestResult {
	var r TestResult
	for _, st := range running {
		if st.Name == name {
			r.add("Running", true, "serving %s", st.Target)
			return r.done()
		}
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		r.add("Listen", false, "%s cannot listen on %s: %v (another program uses the port?)", what, listen, err)
		return r.done()
	}
	ln.Close()
	r.add("Listen", true, "%s is free; press Apply to start %s", listen, what)
	return r.done()
}

var _ = sort.Strings
