package main

// LLDP (IEEE 802.1AB, EtherType 0x88CC): every PROFINET device and managed
// switch tells its link partner, every few seconds, who it is and on which
// port – the name of station, the port ("port-001"), its management IP
// address. PRONETA draws its port topology from this. Here we listen on an
// adapter and report what the device or switch on the other end of this
// PC's cable says. (Bridges do not forward LLDP, so only the direct
// neighbour is heard; a managed switch reports its own neighbours over SNMP.)
//
// Also here: DCP Reset to factory settings, the other PRONETA function that
// works through DCP.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

const lldpEtherType = 0x88CC

// The "nearest bridge" multicast address LLDP frames go to.
var lldpMulticast = net.HardwareAddr{0x01, 0x80, 0xc2, 0x00, 0x00, 0x0e}

// LLDPNeighbor is what one link partner announced.
type LLDPNeighbor struct {
	SourceMAC  string   `json:"source_mac"`            // the sending port's MAC
	ChassisID  string   `json:"chassis_id"`            // PROFINET: the name of station
	ChassisSub string   `json:"chassis_kind"`          // mac, ip, name, …
	PortID     string   `json:"port_id"`               // PROFINET: "port-001" or "port-001.name"
	PortDesc   string   `json:"port_desc,omitempty"`   // e.g. "Siemens, SIMATIC S7, CPU-1200, …, Ethernet Port, X1 P1"
	SysName    string   `json:"system_name,omitempty"` // host name
	SysDesc    string   `json:"system_desc,omitempty"` // device description, firmware
	Caps       []string `json:"capabilities,omitempty"`
	MgmtIP     string   `json:"mgmt_ip,omitempty"`
	TTL        int      `json:"ttl"`
	PNAlias    string   `json:"pn_alias,omitempty"`       // PROFINET alias name (port.station)
	PNChassis  string   `json:"pn_chassis_mac,omitempty"` // PROFINET chassis MAC
	MAU        string   `json:"mau,omitempty"`            // link: 100BASE-TX full duplex …
	Autoneg    string   `json:"autoneg,omitempty"`
	Profinet   bool     `json:"profinet"` // has PROFINET extensions
	Adapter    string   `json:"adapter,omitempty"`
	Seen       string   `json:"seen"` // time received
}

var lldpCaps = []string{"other", "repeater", "bridge / switch", "WLAN access point", "router", "telephone", "DOCSIS cable device", "station"}

var mauTypes = map[uint16]string{10: "10BASE-T half duplex", 11: "10BASE-T full duplex", 15: "100BASE-TX half duplex", 16: "100BASE-TX full duplex",
	17: "100BASE-FX half duplex", 18: "100BASE-FX full duplex", 29: "1000BASE-T half duplex", 30: "1000BASE-T full duplex"}

func lldpID(sub byte, v []byte, isPort bool) (string, string) {
	switch {
	case !isPort && sub == 4, isPort && sub == 3: // MAC address
		if len(v) == 6 {
			return net.HardwareAddr(v).String(), "mac"
		}
	case !isPort && sub == 5, isPort && sub == 4: // network address: family byte + address
		if len(v) == 5 && v[0] == 1 {
			return net.IP(v[1:5]).String(), "ip"
		}
	}
	return strings.TrimRight(string(v), "\x00 "), map[bool]map[byte]string{
		false: {1: "chassis component", 2: "interface alias", 3: "port component", 6: "interface name", 7: "name"},
		true:  {1: "interface alias", 2: "port component", 5: "interface name", 6: "agent circuit", 7: "name"},
	}[isPort][sub]
}

// parseLLDP decodes an LLDP frame.
func parseLLDP(pkt []byte) (LLDPNeighbor, bool) {
	src, p, ok := etherPayload(pkt, lldpEtherType)
	if !ok {
		return LLDPNeighbor{}, false
	}
	n := LLDPNeighbor{SourceMAC: src.String()}
	for len(p) >= 2 {
		h := binary.BigEndian.Uint16(p[:2])
		typ, l := int(h>>9), int(h&0x1ff)
		if typ == 0 || 2+l > len(p) {
			break
		}
		v := p[2 : 2+l]
		p = p[2+l:]
		switch typ {
		case 1:
			if l > 1 {
				n.ChassisID, n.ChassisSub = lldpID(v[0], v[1:], false)
			}
		case 2:
			if l > 1 {
				n.PortID, _ = lldpID(v[0], v[1:], true)
			}
		case 3:
			if l >= 2 {
				n.TTL = int(binary.BigEndian.Uint16(v))
			}
		case 4:
			n.PortDesc = strings.TrimRight(string(v), "\x00 ")
		case 5:
			n.SysName = strings.TrimRight(string(v), "\x00 ")
		case 6:
			n.SysDesc = strings.TrimRight(string(v), "\x00 ")
		case 7:
			if l >= 4 {
				en := binary.BigEndian.Uint16(v[2:4])
				for b, c := range lldpCaps {
					if en&(1<<b) != 0 {
						n.Caps = append(n.Caps, c)
					}
				}
			}
		case 8:
			if l >= 2 && int(v[0]) >= 5 && len(v) >= 1+int(v[0]) && v[1] == 1 && n.MgmtIP == "" {
				n.MgmtIP = net.IP(v[2:6]).String()
			}
		case 127:
			if l < 4 {
				continue
			}
			oui, sub, d := [3]byte(v[0:3]), v[3], v[4:]
			switch {
			case oui == [3]byte{0x00, 0x0e, 0xcf}: // PROFINET
				n.Profinet = true
				switch {
				case sub == 3:
					n.PNAlias = strings.TrimRight(string(d), "\x00 ")
				case sub == 5 && len(d) >= 6:
					n.PNChassis = net.HardwareAddr(d[:6]).String()
				}
			case oui == [3]byte{0x00, 0x12, 0x0f} && sub == 1 && len(d) >= 5: // IEEE 802.3 MAC/PHY
				switch {
				case d[0]&2 != 0:
					n.Autoneg = "on"
				case d[0]&1 != 0:
					n.Autoneg = "supported, off"
				default:
					n.Autoneg = "not supported"
				}
				mau := binary.BigEndian.Uint16(d[3:5])
				if s, ok := mauTypes[mau]; ok {
					n.MAU = s
				} else if mau != 0 {
					n.MAU = fmt.Sprintf("MAU type %d", mau)
				}
			}
		}
	}
	return n, n.ChassisID != "" || n.PortID != ""
}

// lldpListen collects the LLDP frames on an adapter for wait (PROFINET
// devices send every 5 s, switches every 30 s).
func lldpListen(adapter string, wait time.Duration) ([]LLDPNeighbor, error) {
	if rawOpen == nil {
		return nil, errors.New(dcpUnavailable)
	}
	link, err := rawOpen(adapter, lldpEtherType, true)
	if err != nil {
		return nil, err
	}
	defer link.Close()
	own := link.MAC().String()
	out := []LLDPNeighbor{}
	seen := map[string]int{}
	deadline := time.Now().Add(wait)
	for {
		pkt, err := link.Recv(deadline)
		if err != nil {
			return out, err
		}
		if pkt == nil {
			return out, nil
		}
		nb, ok := parseLLDP(pkt)
		if !ok || nb.SourceMAC == own {
			continue
		}
		nb.Adapter, nb.Seen = adapter, time.Now().Format(time.RFC3339)
		key := nb.SourceMAC + "|" + nb.PortID
		if i, dup := seen[key]; dup {
			out[i] = nb // the newest
		} else {
			seen[key] = len(out)
			out = append(out, nb)
		}
	}
}

// ---- DCP Reset to factory settings ----

// Reset modes (block qualifier of DCP Control / ResetToFactory).
var dcpResetModes = map[string]uint16{
	"communication": 0x0004, // IP address and name of station (what PRONETA and TIA do)
	"application":   0x0002,
	"engineering":   0x0006,
	"all":           0x0008,
	"device":        0x000A,
}

func resetBlock(mode uint16) dcpBlock { return dcpBlock{Opt: 0x05, Sub: 0x06, Qualifier: mode} }

// factoryResetBlock is the older "Factory reset" (devices before PROFINET V2.3).
func factoryResetBlock() dcpBlock { return dcpBlock{Opt: 0x05, Sub: 0x05} }

// dcpReset resets a device to factory settings; devices that do not know
// ResetToFactory get the older Factory reset.
func dcpReset(adapter string, mac net.HardwareAddr, mode string) error {
	q, ok := dcpResetModes[mode]
	if !ok {
		return fmt.Errorf("reset mode must be one of communication, application, engineering, all, device")
	}
	err := dcpSet(adapter, mac, []dcpBlock{resetBlock(q)})
	if err != nil && mode == "communication" && strings.Contains(err.Error(), "reset to factory: suboption not supported") {
		return dcpSet(adapter, mac, []dcpBlock{factoryResetBlock()})
	}
	return err
}
