package main

// PROFINET IO cyclic data (RT_CLASS_1 frames) and alarms (RTA) for the IO
// test session in pnio_ar.go.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"runtime"
	"time"
)

const (
	rtaFrameHigh = 0xFC01
	rtaFrameLow  = 0xFE01

	rtaData = 1
	rtaNack = 2
	rtaAck  = 3
	rtaErr  = 4
)

// rtOpen opens a raw link that only receives PROFINET frames from peer, with
// as little buffering delay as possible. Windows sets it (Npcap immediate
// mode); elsewhere pnioOpenLink falls back to rawOpen.
var rtOpen func(adapterID string, peer net.HardwareAddr) (dcpLink, error)

// peerLink drops frames that do not come from peer.
type peerLink struct {
	dcpLink
	peer net.HardwareAddr
}

func (p peerLink) Recv(deadline time.Time) ([]byte, error) {
	for {
		pkt, err := p.dcpLink.Recv(deadline)
		if err != nil || pkt == nil {
			return pkt, err
		}
		if src, _, ok := etherPayload(pkt, dcpEtherType); ok && bytes.Equal(src, p.peer) {
			return pkt, nil
		}
	}
}

// pnioOpenLink opens the raw link for one IO test session.
func pnioOpenLink(adapterID string, peer net.HardwareAddr) (dcpLink, error) {
	open := rtOpen
	if open == nil {
		if rawOpen == nil {
			return nil, fmt.Errorf("%s", dcpUnavailable)
		}
		open = func(id string, _ net.HardwareAddr) (dcpLink, error) { return rawOpen(id, dcpEtherType, false) }
	}
	l, err := open(adapterID, peer)
	if err != nil {
		return nil, err
	}
	return peerLink{l, peer}, nil
}

// ---- cyclic output ----

// sendLoop sends the output frame once per cycle at absolute deadlines.
func (s *pnioSession) sendLoop() {
	defer s.wg.Done()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cyc := s.cfg.cycle()
	step := s.cfg.SendClockFactor * s.cfg.ReductionRatio
	frame := append(append([]byte{}, s.cfg.DeviceMAC...), s.link.MAC()...)
	frame = be16(frame, dcpEtherType)
	frame = be16(frame, s.ar.OutFrameID)
	data := len(frame)
	frame = append(frame, make([]byte, s.out.Length+4)...)
	status := data + s.out.Length
	var counter uint16
	next := time.Now()
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		lateBy := time.Since(next)
		s.mu.Lock()
		copy(frame[data:], s.outBuf)
		s.mu.Unlock()
		counter += step
		binary.BigEndian.PutUint16(frame[status:], counter)
		frame[status+2] = 0x35 // primary, data valid, RUN, no station problem
		frame[status+3] = 0
		err := s.link.Send(frame)
		s.mu.Lock()
		s.sent++
		if lateBy > cyc/2 {
			s.late++
		}
		s.maxLate = max(s.maxLate, lateBy)
		s.mu.Unlock()
		if err != nil {
			s.event("send: %v", err)
		}
		next = next.Add(cyc)
		if d := time.Until(next); d > 0 {
			select {
			case <-s.stop:
				return
			case <-time.After(d):
			}
		} else if -d > 2*cyc {
			next = time.Now() // far behind (PC was busy): do not send a burst
		}
	}
}

// ---- receive: inputs and alarms ----

func (s *pnioSession) recvLoop() {
	defer s.wg.Done()
	watchdog := time.Duration(s.cfg.WatchdogFactor) * s.cfg.cycle()
	stale := false
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		pkt, err := s.link.Recv(time.Now().Add(50 * time.Millisecond))
		if err != nil {
			s.event("receive: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if pkt != nil {
			src, p, ok := etherPayload(pkt, dcpEtherType)
			if ok && len(p) >= 2 && bytes.Equal(src, s.cfg.DeviceMAC) {
				fid := binary.BigEndian.Uint16(p)
				switch {
				case fid == s.ar.InFrameID:
					s.onInput(p[2:])
				case fid == rtaFrameHigh || fid == rtaFrameLow:
					s.onRTA(fid, p[2:])
				}
			}
		}
		s.rtaRetry()
		s.mu.Lock()
		last := s.lastIn
		s.mu.Unlock()
		if !last.IsZero() {
			if gone := time.Since(last) > watchdog; gone != stale {
				stale = gone
				if gone {
					s.event("inputs stopped: no frame from the device for %v", watchdog)
				} else {
					s.event("inputs are back")
				}
			}
		}
	}
}

func (s *pnioSession) onInput(p []byte) {
	n := s.in.Length
	if len(p) < n+4 {
		return
	}
	st := p[n+2]
	s.mu.Lock()
	if s.inBuf == nil {
		s.inBuf = make([]byte, n)
	}
	copy(s.inBuf, p[:n])
	old := s.inStatus
	first := s.inCount == 0
	s.inStatus = st
	s.inCount++
	s.lastIn = time.Now()
	s.mu.Unlock()
	if first || old != st {
		s.event("device data status: %s", dataStatusText(st))
	}
}

// ---- alarms (RTA, acyclic real-time over layer 2) ----

// rtaState is one alarm channel (high or low priority), window size 1.
type rtaState struct {
	lastSent, lastRecv uint16
	queue              [][]byte // RTA-SDUs (AlarmAck blocks) still to send
	waiting            []byte   // sent DATA frame not yet acknowledged
	sentAt             time.Time
	tries              int
}

func (r *rtaState) init() { r.lastSent, r.lastRecv = 0xFFFF, 0xFFFE }

func (s *pnioSession) rtaFrame(fid, pduType uint16, flags byte, send, ack uint16, sdu []byte) []byte {
	f := append(append([]byte{}, s.cfg.DeviceMAC...), s.link.MAC()...)
	f = be16(f, dcpEtherType)
	f = be16(f, fid)
	f = be16(f, s.ar.DevAlarm)      // AlarmDstEndpoint
	f = be16(f, s.ar.LocalAlarmRef) // AlarmSrcEndpoint
	f = append(f, byte(0x10|pduType), flags)
	f = be16(f, send)
	f = be16(f, ack)
	f = be16(f, uint16(len(sdu)))
	return pad60(append(f, sdu...))
}

var alarmTypes = map[uint16]string{
	1: "diagnosis", 2: "process", 3: "pull", 4: "plug", 5: "status", 6: "update", 7: "redundancy",
	8: "controlled by supervisor", 9: "released", 0x0A: "plug wrong submodule", 0x0B: "return of submodule",
	0x0C: "diagnosis disappears", 0x0D: "multicast communication mismatch", 0x0E: "port data change",
	0x0F: "sync data changed", 0x10: "isochronous mode problem", 0x11: "network component problem",
	0x12: "time data changed", 0x13: "dynamic frame packing problem", 0x14: "MRPD problem",
	0x1E: "upload and retrieval", 0x1F: "pull module",
}

func (s *pnioSession) onRTA(fid uint16, p []byte) {
	if len(p) < 12 {
		return
	}
	dst := binary.BigEndian.Uint16(p[0:])
	pdu := p[4] & 0x0F
	send := binary.BigEndian.Uint16(p[6:])
	ack := binary.BigEndian.Uint16(p[8:])
	n := int(binary.BigEndian.Uint16(p[10:]))
	if dst != s.ar.LocalAlarmRef || 12+n > len(p) {
		return
	}
	sdu := p[12 : 12+n]
	pri := 0
	if fid == rtaFrameLow {
		pri = 1
	}
	s.mu.Lock()
	st := &s.rta[pri]
	if st.waiting != nil && ack == st.lastSent && (pdu == rtaAck || pdu == rtaData) {
		st.waiting = nil // our DATA arrived
	}
	var out [][]byte
	switch pdu {
	case rtaData:
		dup := send == st.lastRecv
		st.lastRecv = send
		out = append(out, s.rtaFrame(fid, rtaAck, 0x01, st.lastSent, send, nil))
		if !dup {
			if ackSDU, text := alarmAck(sdu); ackSDU != nil {
				st.queue = append(st.queue, ackSDU)
				defer s.event("alarm: %s", text)
			}
		}
	case rtaNack:
		if st.waiting != nil {
			out = append(out, st.waiting)
		}
	case rtaErr:
		s.aborted = fmt.Sprintf("device aborted the AR (RTA ERR % X)", sdu)
		s.state = "aborted"
		defer s.event("%s", s.aborted)
	}
	out = append(out, s.rtaNextLocked(fid, pri)...)
	s.mu.Unlock()
	for _, f := range out {
		if err := s.link.Send(f); err != nil {
			s.event("alarm send: %v", err)
		}
	}
}

// rtaNextLocked starts sending the next queued DATA if none is outstanding.
func (s *pnioSession) rtaNextLocked(fid uint16, pri int) [][]byte {
	st := &s.rta[pri]
	if st.waiting != nil || len(st.queue) == 0 {
		return nil
	}
	sdu := st.queue[0]
	st.queue = st.queue[1:]
	st.lastSent = (st.lastSent + 1) & 0x7FFF
	st.waiting = s.rtaFrame(fid, rtaData, 0x11, st.lastSent, st.lastRecv, sdu)
	st.sentAt, st.tries = time.Now(), 1
	return [][]byte{st.waiting}
}

// rtaRetry resends an unacknowledged alarm DATA (RTATimeoutFactor 1 = 100 ms,
// 3 retries).
func (s *pnioSession) rtaRetry() {
	var out [][]byte
	s.mu.Lock()
	for pri := range s.rta {
		st := &s.rta[pri]
		if st.waiting == nil || time.Since(st.sentAt) < 100*time.Millisecond {
			continue
		}
		if st.tries > 3 {
			st.waiting = nil
			s.event("alarm acknowledge was not confirmed by the device")
			fid := uint16(rtaFrameHigh)
			if pri == 1 {
				fid = rtaFrameLow
			}
			out = append(out, s.rtaNextLocked(fid, pri)...)
			continue
		}
		st.tries++
		st.sentAt = time.Now()
		out = append(out, st.waiting)
	}
	s.mu.Unlock()
	for _, f := range out {
		s.link.Send(f)
	}
}

// alarmAck builds the AlarmAck block for an AlarmNotification and describes it.
func alarmAck(sdu []byte) ([]byte, string) {
	list, err := splitBlocks(sdu)
	if err != nil || len(list) == 0 || (list[0].Type != 0x0001 && list[0].Type != 0x0002) {
		return nil, ""
	}
	r := &beReader{b: list[0].Body}
	typ, api, slot, sub := r.u16(), r.u32(), r.u16(), r.u16()
	mod, smod, spec := r.u32(), r.u32(), r.u16()
	if r.bad {
		return nil, ""
	}
	b := be16(nil, typ)
	b = be32(b, api)
	b = be16(b, slot)
	b = be16(b, sub)
	b = be16(b, spec)
	b = append(b, 0, 0, 0, 0) // PNIOStatus OK
	ackType := uint16(0x8001)
	if list[0].Type == 0x0002 {
		ackType = 0x8002
	}
	name := alarmTypes[typ]
	if name == "" {
		name = fmt.Sprintf("type 0x%04X", typ)
	}
	return appendBlock(nil, ackType, b), fmt.Sprintf("%s alarm, slot %d subslot 0x%04X (module 0x%X, submodule 0x%X, specifier 0x%04X)",
		name, slot, sub, mod, smod, spec)
}
