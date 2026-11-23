package main

// Messaging targets: AMQP (RabbitMQ and other AMQP 0-9-1 brokers) and CoAP
// (RFC 7252, the web protocol of small devices, over UDP).

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// payloadOf is the message body for a change, as for MQTT: JSON (value,
// quality, address, type, ts) or the value alone.
func payloadOf(format string, c Change) []byte {
	_, p := mqttMessage(MQTTConfig{TopicPrefix: "x", Payload: format}, c)
	return p
}

// ---- AMQP ----

type amqpSink struct {
	cfg    AMQPConfig
	mu     sync.Mutex
	conn   *amqp.Connection
	ch     *amqp.Channel
	stats  sinkStats
	quit   chan struct{}
	target string
}

func amqpTarget(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		u.User = nil // no password in the status
		return u.String()
	}
	return raw
}

func newAMQPSink(c AMQPConfig) *amqpSink {
	s := &amqpSink{cfg: c, quit: make(chan struct{}), target: amqpTarget(c.URL)}
	go s.keepConnected()
	return s
}

// keepConnected dials, and dials again after the connection is lost.
func (s *amqpSink) keepConnected() {
	for {
		conn, err := amqp.DialConfig(s.cfg.URL, amqp.Config{Dial: amqp.DefaultDial(8 * time.Second), Properties: amqp.Table{"connection_name": "PLC Monitor"}})
		var ch *amqp.Channel
		if err == nil {
			ch, err = conn.Channel()
			if err != nil {
				conn.Close()
			}
		}
		if err != nil {
			if s.stats.fail(fmt.Errorf("connect: %w", err)) {
				log.Printf("export amqp: %v", err)
			}
			select {
			case <-s.quit:
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}
		log.Printf("export amqp: connected to %s", s.target)
		s.mu.Lock()
		s.conn, s.ch = conn, ch
		s.mu.Unlock()
		closed := conn.NotifyClose(make(chan *amqp.Error, 1))
		select {
		case <-s.quit:
			return
		case e := <-closed:
			s.mu.Lock()
			s.conn, s.ch = nil, nil
			s.mu.Unlock()
			if e != nil {
				s.stats.fail(fmt.Errorf("connection lost: %v", e))
			}
		}
	}
}

func (s *amqpSink) Name() string { return "amqp" }

func amqpRoutingKey(prefix, tag string) string {
	return strings.Trim(prefix, ".") + "." + tag
}

func (s *amqpSink) Write(changes []Change) error {
	s.mu.Lock()
	ch := s.ch
	s.mu.Unlock()
	if ch == nil {
		s.stats.fail(errors.New("not connected – values are not sent until the broker is reachable"))
		return nil
	}
	mode := amqp.Transient
	if s.cfg.Persistent {
		mode = amqp.Persistent
	}
	ctype := "application/json"
	if s.cfg.Payload == "value" {
		ctype = "text/plain"
	}
	for _, c := range changes {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := ch.PublishWithContext(ctx, s.cfg.Exchange, amqpRoutingKey(s.cfg.RoutingPrefix, c.Tag.Name), false, false, amqp.Publishing{
			ContentType: ctype, DeliveryMode: mode, Timestamp: c.Time, AppId: "plcmonitor",
			Body: payloadOf(s.cfg.Payload, c),
		})
		cancel()
		if err != nil {
			s.stats.fail(err)
			return err
		}
	}
	s.stats.ok(len(changes))
	return nil
}

func (s *amqpSink) Status() SinkStatus {
	st := SinkStatus{Name: "amqp", Target: s.target}
	s.mu.Lock()
	if s.ch == nil {
		st.State = "connecting"
	}
	s.mu.Unlock()
	s.stats.fill(&st)
	return st
}

func (s *amqpSink) Close() error {
	close(s.quit)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Close()
	}
	return nil
}

func amqpTest(c AMQPConfig) TestResult {
	var r TestResult
	conn, err := amqp.DialConfig(c.URL, amqp.Config{Dial: amqp.DefaultDial(6 * time.Second)})
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "403") || strings.Contains(strings.ToLower(msg), "access_refused") {
			msg += " → the broker refused the username/password (RabbitMQ: guest only works from the broker's own PC)"
		} else if h := netHint(err, "5672 (amqp://), 5671 (amqps://)"); h != "" {
			msg += " → " + h
		}
		r.add("Connect", false, "%s", msg)
		return r.done()
	}
	defer conn.Close()
	r.add("Connect", true, "connected to %s", amqpTarget(c.URL))
	ch, err := conn.Channel()
	if err != nil {
		r.add("Channel", false, "%v", err)
		return r.done()
	}
	if err := ch.ExchangeDeclarePassive(c.Exchange, "topic", true, false, false, false, nil); err != nil {
		r.add("Exchange", false, "exchange %q: %v – create it, or use amq.topic", c.Exchange, err)
		return r.done()
	}
	r.add("Exchange", true, "exchange %q exists", c.Exchange)
	key := amqpRoutingKey(c.RoutingPrefix, "_test")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ch.PublishWithContext(ctx, c.Exchange, key, false, false, amqp.Publishing{ContentType: "text/plain", Body: []byte("PLC Monitor test")}); err != nil {
		r.add("Publish", false, "%v", err)
	} else {
		r.add("Publish", true, "published to %s with routing key %s – bind a queue to see it (e.g. routing key %s.#)", c.Exchange, key, strings.Trim(c.RoutingPrefix, "."))
	}
	return r.done()
}

// ---- CoAP ----

const (
	coapCON, coapNON, coapACK, coapRST = 0, 1, 2, 3
	coapPOST, coapPUT                  = 2, 3
	coapOptURIPath, coapOptFormat      = 11, 12
	coapFormatText, coapFormatJSON     = 0, 50
)

type coapOption struct {
	num   int
	value []byte
}

// coapEncode builds one CoAP message (RFC 7252 §3).
func coapEncode(typ, code byte, mid uint16, token []byte, opts []coapOption, payload []byte) []byte {
	b := []byte{1<<6 | typ<<4 | byte(len(token)), code, byte(mid >> 8), byte(mid)}
	b = append(b, token...)
	sort.SliceStable(opts, func(i, j int) bool { return opts[i].num < opts[j].num })
	ext := func(n int) (nib byte, more []byte) {
		switch {
		case n < 13:
			return byte(n), nil
		case n < 269:
			return 13, []byte{byte(n - 13)}
		default:
			return 14, []byte{byte((n - 269) >> 8), byte(n - 269)}
		}
	}
	last := 0
	for _, o := range opts {
		dn, dx := ext(o.num - last)
		ln, lx := ext(len(o.value))
		b = append(b, dn<<4|ln)
		b = append(b, dx...)
		b = append(b, lx...)
		b = append(b, o.value...)
		last = o.num
	}
	if len(payload) > 0 {
		b = append(b, 0xFF)
		b = append(b, payload...)
	}
	return b
}

type coapMsg struct {
	Type    byte
	Code    byte
	MID     uint16
	Token   []byte
	Path    []string
	Format  int
	Payload []byte
}

// coapDecode reads a CoAP message (for acknowledgements, and tests).
func coapDecode(b []byte) (coapMsg, error) {
	var m coapMsg
	if len(b) < 4 || b[0]>>6 != 1 {
		return m, errors.New("not a CoAP message")
	}
	m.Type, m.Code, m.MID = b[0]>>4&3, b[1], binary.BigEndian.Uint16(b[2:])
	tkl := int(b[0] & 15)
	if len(b) < 4+tkl {
		return m, errors.New("short CoAP message")
	}
	m.Token = b[4 : 4+tkl]
	i, num := 4+tkl, 0
	m.Format = -1
	for i < len(b) {
		if b[i] == 0xFF {
			m.Payload = b[i+1:]
			break
		}
		d, l := int(b[i]>>4), int(b[i]&15)
		i++
		read := func(n int) (int, bool) {
			switch n {
			case 13:
				if i >= len(b) {
					return 0, false
				}
				i++
				return int(b[i-1]) + 13, true
			case 14:
				if i+1 >= len(b) {
					return 0, false
				}
				i += 2
				return int(binary.BigEndian.Uint16(b[i-2:])) + 269, true
			case 15:
				return 0, false
			}
			return n, true
		}
		var ok bool
		if d, ok = read(d); !ok {
			return m, errors.New("bad CoAP option")
		}
		if l, ok = read(l); !ok || i+l > len(b) {
			return m, errors.New("bad CoAP option")
		}
		num += d
		v := b[i : i+l]
		i += l
		switch num {
		case coapOptURIPath:
			m.Path = append(m.Path, string(v))
		case coapOptFormat:
			f := 0
			for _, x := range v {
				f = f<<8 | int(x)
			}
			m.Format = f
		}
	}
	return m, nil
}

type coapSink struct {
	cfg   CoAPConfig
	addr  string
	base  []string
	conn  net.Conn
	mid   uint16
	stats sinkStats
}

func newCoAPSink(c CoAPConfig) (*coapSink, error) {
	u, err := url.Parse(c.URL)
	if err != nil {
		return nil, err
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "5683")
	}
	s := &coapSink{cfg: c, addr: host}
	for _, p := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if p != "" {
			s.base = append(s.base, p)
		}
	}
	var m [2]byte
	rand.Read(m[:])
	s.mid = binary.BigEndian.Uint16(m[:])
	return s, nil
}

func (s *coapSink) Name() string { return "coap" }

// send delivers one message; confirmable ones wait for the ACK (with two
// retransmissions, RFC 7252 §4.2).
func (s *coapSink) send(path []string, format int, payload []byte) error {
	if s.conn == nil {
		c, err := net.DialTimeout("udp", s.addr, 5*time.Second)
		if err != nil {
			return err
		}
		s.conn = c
	}
	s.mid++
	token := make([]byte, 4)
	rand.Read(token)
	typ := byte(coapNON)
	if s.cfg.Confirmable {
		typ = coapCON
	}
	code := byte(coapPOST)
	if s.cfg.Method == "put" {
		code = coapPUT
	}
	opts := []coapOption{}
	for _, p := range path {
		opts = append(opts, coapOption{coapOptURIPath, []byte(p)})
	}
	if format > 0 {
		opts = append(opts, coapOption{coapOptFormat, []byte{byte(format)}})
	} else {
		opts = append(opts, coapOption{coapOptFormat, nil}) // 0 = text/plain
	}
	msg := coapEncode(typ, code, s.mid, token, opts, payload)
	if !s.cfg.Confirmable {
		_, err := s.conn.Write(msg)
		return err
	}
	buf := make([]byte, 1500)
	wait := 2 * time.Second
	for try := 0; try < 3; try++ {
		if _, err := s.conn.Write(msg); err != nil {
			return err
		}
		deadline := time.Now().Add(wait)
		for {
			s.conn.SetReadDeadline(deadline)
			n, err := s.conn.Read(buf)
			if err != nil {
				break // timeout: send again
			}
			r, err := coapDecode(buf[:n])
			if err != nil || r.MID != s.mid {
				continue
			}
			if r.Type == coapRST {
				return errors.New("the server reset the message (RST)")
			}
			if r.Type == coapACK {
				if cls := r.Code >> 5; cls != 0 && cls != 2 {
					return fmt.Errorf("CoAP %d.%02d from the server for /%s", cls, r.Code&31, strings.Join(path, "/"))
				}
				return nil
			}
		}
		wait *= 2
	}
	return errors.New("no acknowledgement from the CoAP server")
}

func (s *coapSink) Write(changes []Change) error {
	format := coapFormatJSON
	if s.cfg.Payload == "value" {
		format = coapFormatText
	}
	for _, c := range changes {
		path := append(append([]string{}, s.base...), c.Tag.Name)
		if err := s.send(path, format, payloadOf(s.cfg.Payload, c)); err != nil {
			if s.stats.fail(err) {
				log.Printf("export coap: %v", err)
			}
			return nil
		}
	}
	s.stats.ok(len(changes))
	return nil
}

func (s *coapSink) Status() SinkStatus {
	st := SinkStatus{Name: "coap", Target: s.cfg.URL}
	s.stats.fill(&st)
	return st
}

func (s *coapSink) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func coapTest(c CoAPConfig) TestResult {
	var r TestResult
	s, err := newCoAPSink(c)
	if err != nil {
		r.add("Address", false, "%v", err)
		return r.done()
	}
	defer s.Close()
	s.cfg.Confirmable = true // to see an answer
	path := append(append([]string{}, s.base...), "_test")
	if err := s.send(path, coapFormatText, []byte("PLC Monitor test")); err != nil {
		r.add("Send", false, "%v (is a CoAP server listening on %s, UDP?)", err, s.addr)
	} else {
		r.add("Send", true, "%s coap://%s/%s was acknowledged", strings.ToUpper(c.Method), s.addr, strings.Join(path, "/"))
	}
	return r.done()
}
