package main

// PROFINET DCP (Discovery and Configuration Protocol): finds PROFINET
// devices on the local Ethernet segment, even those without an IP address,
// and sets their IP parameters and name of station, like "Accessible
// devices" / "Assign IP address" in TIA Portal. DCP runs directly on
// Ethernet (EtherType 0x8892), so it needs raw packet access: on Windows the
// Npcap driver (see dcp_windows.go). Everything here is portable: frames,
// decoding, checks and the HTTP API.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

const dcpEtherType = 0x8892

// The multicast address DCP Identify requests go to.
var dcpMulticast = net.HardwareAddr{0x01, 0x0e, 0xcf, 0x00, 0x00, 0x00}

// DCPAdapter is a network adapter DCP can use.
type DCPAdapter struct {
	ID   string   `json:"id"`   // pcap device name
	Name string   `json:"name"` // Windows name, e.g. "Ethernet 2"
	Desc string   `json:"desc"` // e.g. "Realtek PCIe GbE Family Controller"
	MAC  string   `json:"mac"`
	IPs  []string `json:"ips"`
}

// DCPDevice is a PROFINET device that answered an Identify request.
type DCPDevice struct {
	MAC           string `json:"mac"`
	Name          string `json:"name"` // name of station
	TypeOfStation string `json:"type"` // vendor's device type, e.g. "S7-1200"
	VendorID      uint16 `json:"vendor_id"`
	DeviceID      uint16 `json:"device_id"`
	Vendor        string `json:"vendor,omitempty"`
	Role          string `json:"role,omitempty"`
	IP            string `json:"ip"`
	Mask          string `json:"mask"`
	Gateway       string `json:"gateway"`
	IPSet         bool   `json:"ip_set"` // false: the device has no IP address yet
	IPConflict    bool   `json:"ip_conflict,omitempty"`
	Alias         string `json:"alias,omitempty"`
}

// dcpLink sends and receives raw Ethernet frames on one adapter.
type dcpLink interface {
	MAC() net.HardwareAddr
	Send(frame []byte) error
	// Recv returns the next DCP frame, or nil when the deadline passes.
	Recv(deadline time.Time) ([]byte, error)
	Close() error
}

// Set by dcp_windows.go when Npcap can be loaded; nil means DCP is not
// available (dcpUnavailable says why).
var (
	dcpOpen        func(adapterID string) (dcpLink, error)
	dcpListAdapter func() ([]DCPAdapter, error)
	dcpUnavailable = "PROFINET device functions need Windows (with Npcap) or Linux"
)

// ---- frames ----

func ethHeader(dst, src net.HardwareAddr) []byte {
	b := append(append(make([]byte, 0, 64), dst...), src...)
	return binary.BigEndian.AppendUint16(b, dcpEtherType)
}

// pad60 pads a frame to the Ethernet minimum (without FCS).
func pad60(b []byte) []byte {
	for len(b) < 60 {
		b = append(b, 0)
	}
	return b
}

// buildIdentify is an "Identify All" request.
func buildIdentify(src net.HardwareAddr, xid uint32) []byte {
	b := ethHeader(dcpMulticast, src)
	b = binary.BigEndian.AppendUint16(b, 0xFEFE) // FrameID: Identify multicast request
	b = append(b, 0x05, 0x00)                    // service Identify, request
	b = binary.BigEndian.AppendUint32(b, xid)
	b = binary.BigEndian.AppendUint16(b, 0x0080) // response delay factor: answers spread over ~1.3 s
	b = binary.BigEndian.AppendUint16(b, 4)      // data length
	b = append(b, 0xFF, 0xFF, 0x00, 0x00)        // option All / suboption All, no data
	return pad60(b)
}

// dcpBlock is one Set block. Qualifier: 0 = temporary, 1 = permanent.
type dcpBlock struct {
	Opt, Sub  byte
	Qualifier uint16
	Data      []byte
}

func buildSet(dst, src net.HardwareAddr, xid uint32, blocks []dcpBlock) []byte {
	var data []byte
	for _, bl := range blocks {
		data = append(data, bl.Opt, bl.Sub)
		data = binary.BigEndian.AppendUint16(data, uint16(2+len(bl.Data)))
		data = binary.BigEndian.AppendUint16(data, bl.Qualifier)
		data = append(data, bl.Data...)
		if len(bl.Data)%2 == 1 {
			data = append(data, 0) // blocks start on an even offset
		}
	}
	b := ethHeader(dst, src)
	b = binary.BigEndian.AppendUint16(b, 0xFEFD) // FrameID: Get/Set
	b = append(b, 0x04, 0x00)                    // service Set, request
	b = binary.BigEndian.AppendUint32(b, xid)
	b = binary.BigEndian.AppendUint16(b, 0) // reserved
	b = binary.BigEndian.AppendUint16(b, uint16(len(data)))
	return pad60(append(b, data...))
}

func ipBlock(ip, mask, gw net.IP, permanent bool) dcpBlock {
	d := append(append(append([]byte{}, ip.To4()...), mask.To4()...), gw.To4()...)
	return dcpBlock{Opt: 0x01, Sub: 0x02, Qualifier: qualifier(permanent), Data: d}
}

func nameBlock(name string, permanent bool) dcpBlock {
	return dcpBlock{Opt: 0x02, Sub: 0x02, Qualifier: qualifier(permanent), Data: []byte(name)}
}

// signalBlock makes the device flash its LED (about 3 s), to find it in the cabinet.
func signalBlock() dcpBlock { return dcpBlock{Opt: 0x05, Sub: 0x03, Data: []byte{0x01, 0x00}} }

func qualifier(permanent bool) uint16 {
	if permanent {
		return 1
	}
	return 0
}

// dcpPayload returns the sender and the DCP part of an Ethernet frame
// (with or without a VLAN tag).
func dcpPayload(pkt []byte) (net.HardwareAddr, []byte, bool) {
	if len(pkt) < 14 {
		return nil, nil, false
	}
	et, off := binary.BigEndian.Uint16(pkt[12:14]), 14
	if et == 0x8100 {
		if len(pkt) < 18 {
			return nil, nil, false
		}
		et, off = binary.BigEndian.Uint16(pkt[16:18]), 18
	}
	if et != dcpEtherType {
		return nil, nil, false
	}
	return net.HardwareAddr(pkt[6:12]), pkt[off:], true
}

// dcpBlocks walks the blocks of a DCP response: fn(option, suboption, data
// after the 2-byte block info, block info).
func dcpBlocks(p []byte, fn func(opt, sub byte, data []byte, info uint16)) {
	for len(p) >= 4 {
		l := int(binary.BigEndian.Uint16(p[2:4]))
		if 4+l > len(p) {
			return
		}
		blk := p[4 : 4+l]
		var info uint16
		if len(blk) >= 2 {
			info = binary.BigEndian.Uint16(blk[:2])
			fn(p[0], p[1], blk[2:], info)
		} else {
			fn(p[0], p[1], nil, 0)
		}
		next := 4 + l + l%2
		if next > len(p) {
			return
		}
		p = p[next:]
	}
}

var dcpVendors = map[uint16]string{0x002A: "Vendor 002A", 0x00B0: "Phoenix Contact", 0x011E: "Beckhoff", 0x0019: "ABB",
	0x0109: "WAGO", 0x0174: "Murrelektronik", 0x00DA: "Balluff", 0x0310: "Festo", 0x0137: "Turck", 0x0035: "Pepperl+Fuchs"}

// parseIdentify decodes an Identify response with transaction id xid.
func parseIdentify(pkt []byte, xid uint32) (DCPDevice, bool) {
	src, p, ok := dcpPayload(pkt)
	if !ok || len(p) < 12 || binary.BigEndian.Uint16(p[0:2]) != 0xFEFF || p[2] != 0x05 || p[3] != 0x01 ||
		binary.BigEndian.Uint32(p[4:8]) != xid {
		return DCPDevice{}, false
	}
	n := int(binary.BigEndian.Uint16(p[10:12]))
	if 12+n > len(p) {
		n = len(p) - 12
	}
	d := DCPDevice{MAC: src.String(), IP: "0.0.0.0", Mask: "0.0.0.0", Gateway: "0.0.0.0"}
	dcpBlocks(p[12:12+n], func(opt, sub byte, data []byte, info uint16) {
		switch {
		case opt == 0x01 && sub == 0x02 && len(data) >= 12:
			d.IP, d.Mask, d.Gateway = net.IP(data[0:4]).String(), net.IP(data[4:8]).String(), net.IP(data[8:12]).String()
			d.IPSet = info&0x0003 != 0 || d.IP != "0.0.0.0"
			d.IPConflict = info&0x0080 != 0
		case opt == 0x02 && sub == 0x01:
			d.TypeOfStation = strings.TrimRight(string(data), "\x00 ")
		case opt == 0x02 && sub == 0x02:
			d.Name = strings.TrimRight(string(data), "\x00")
		case opt == 0x02 && sub == 0x03 && len(data) >= 4:
			d.VendorID, d.DeviceID = binary.BigEndian.Uint16(data[0:2]), binary.BigEndian.Uint16(data[2:4])
			d.Vendor = dcpVendors[d.VendorID]
		case opt == 0x02 && sub == 0x04 && len(data) >= 1:
			var roles []string
			for bit, r := range []string{"IO device", "IO controller", "IO multidevice", "PN supervisor"} {
				if data[0]&(1<<bit) != 0 {
					roles = append(roles, r)
				}
			}
			d.Role = strings.Join(roles, ", ")
		case opt == 0x02 && sub == 0x06:
			d.Alias = strings.TrimRight(string(data), "\x00")
		}
	})
	return d, true
}

var dcpErrors = map[byte]string{
	1: "option not supported",
	2: "suboption not supported or no data set",
	3: "suboption not set",
	4: "resource error",
	5: "set not possible for local reasons (for example the device takes its IP address or name from the PLC project, or the CPU is in RUN)",
	6: "device is in operation; set not possible",
}

// parseSetResponse decodes a Set response from mac with transaction id xid.
// It returns an error describing every block the device refused.
func parseSetResponse(pkt []byte, mac net.HardwareAddr, xid uint32) (bool, error) {
	src, p, ok := dcpPayload(pkt)
	if !ok || src.String() != mac.String() || len(p) < 12 || binary.BigEndian.Uint16(p[0:2]) != 0xFEFD ||
		p[2] != 0x04 || binary.BigEndian.Uint32(p[4:8]) != xid {
		return false, nil
	}
	if p[3] != 0x01 {
		return true, fmt.Errorf("the device rejected the request (service type %d)", p[3])
	}
	n := int(binary.BigEndian.Uint16(p[10:12]))
	if 12+n > len(p) {
		n = len(p) - 12
	}
	var errs []string
	dcpBlocks(p[12:12+n], func(opt, sub byte, data []byte, info uint16) {
		// Response block (5/4): the block info is option + suboption, data[0] the error.
		if opt == 0x05 && sub == 0x04 && len(data) >= 1 && data[0] != 0 {
			what := map[uint16]string{0x0102: "IP address", 0x0202: "name of station", 0x0503: "flash LED"}[info]
			if what == "" {
				what = fmt.Sprintf("option %d/%d", info>>8, info&0xff)
			}
			msg := dcpErrors[data[0]]
			if msg == "" {
				msg = fmt.Sprintf("error %d", data[0])
			}
			errs = append(errs, what+": "+msg)
		}
	})
	if len(errs) > 0 {
		return true, errors.New(strings.Join(errs, "; "))
	}
	return true, nil
}

// ---- checks ----

var stationLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// checkStationName applies the PROFINET rules for a name of station.
func checkStationName(n string) error {
	switch {
	case n == "":
		return errors.New("the name of station is empty")
	case len(n) > 240:
		return errors.New("the name of station is longer than 240 characters")
	case n != strings.ToLower(n):
		return errors.New("the name of station must be lower case (e.g. plc-line1)")
	case net.ParseIP(n) != nil:
		return errors.New("the name of station must not look like an IP address")
	case strings.HasPrefix(n, "port-"):
		return errors.New(`the name of station must not start with "port-"`)
	}
	for _, label := range strings.Split(n, ".") {
		if !stationLabel.MatchString(label) {
			return fmt.Errorf("%q: use letters a–z, digits and '-', separated by '.', each part 1–63 characters, not starting or ending with '-'", n)
		}
	}
	return nil
}

// checkIPSettings validates an IP address, subnet mask and gateway.
func checkIPSettings(ipS, maskS, gwS string) (net.IP, net.IP, net.IP, error) {
	ip, mask, gw := net.ParseIP(strings.TrimSpace(ipS)).To4(), net.ParseIP(strings.TrimSpace(maskS)).To4(), net.ParseIP(strings.TrimSpace(gwS)).To4()
	if strings.TrimSpace(gwS) == "" {
		gw = net.IPv4zero.To4()
	}
	switch {
	case ip == nil:
		return nil, nil, nil, fmt.Errorf("%q is not an IPv4 address", ipS)
	case mask == nil:
		return nil, nil, nil, fmt.Errorf("%q is not a subnet mask", maskS)
	case gw == nil:
		return nil, nil, nil, fmt.Errorf("%q is not an IPv4 gateway (leave it empty or 0.0.0.0 for none)", gwS)
	}
	ones, bits := net.IPMask(mask).Size()
	if bits == 0 || ones < 8 || ones > 30 {
		return nil, nil, nil, fmt.Errorf("%s is not a usable subnet mask (e.g. 255.255.255.0)", mask)
	}
	m := net.IPMask(mask)
	network := ip.Mask(m)
	bcast := make(net.IP, 4)
	for i := range bcast {
		bcast[i] = network[i] | ^m[i]
	}
	switch {
	case ip.Equal(network) || ip.Equal(bcast):
		return nil, nil, nil, fmt.Errorf("%s is the network or broadcast address of that subnet", ip)
	case ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified():
		return nil, nil, nil, fmt.Errorf("%s cannot be a device address", ip)
	case !gw.IsUnspecified() && !gw.Mask(m).Equal(network):
		return nil, nil, nil, fmt.Errorf("the gateway %s is not in the device's subnet %s/%d", gw, network, ones)
	}
	return ip, mask, gw, nil
}

// ---- operations ----

var dcpMu sync.Mutex // one DCP exchange at a time

func dcpIdentify(adapter string, wait time.Duration) ([]DCPDevice, error) {
	if dcpOpen == nil {
		return nil, errors.New(dcpUnavailable)
	}
	dcpMu.Lock()
	defer dcpMu.Unlock()
	link, err := dcpOpen(adapter)
	if err != nil {
		return nil, err
	}
	defer link.Close()
	xid := rand.Uint32()
	if err := link.Send(buildIdentify(link.MAC(), xid)); err != nil {
		return nil, err
	}
	devices := []DCPDevice{}
	seen := map[string]bool{}
	deadline := time.Now().Add(wait)
	for {
		pkt, err := link.Recv(deadline)
		if err != nil {
			return devices, err
		}
		if pkt == nil {
			return devices, nil
		}
		if d, ok := parseIdentify(pkt, xid); ok && !seen[d.MAC] {
			seen[d.MAC] = true
			devices = append(devices, d)
		}
	}
}

func dcpSet(adapter string, mac net.HardwareAddr, blocks []dcpBlock) error {
	if dcpOpen == nil {
		return errors.New(dcpUnavailable)
	}
	dcpMu.Lock()
	defer dcpMu.Unlock()
	link, err := dcpOpen(adapter)
	if err != nil {
		return err
	}
	defer link.Close()
	xid := rand.Uint32()
	if err := link.Send(buildSet(mac, link.MAC(), xid, blocks)); err != nil {
		return err
	}
	deadline := time.Now().Add(4 * time.Second)
	for {
		pkt, err := link.Recv(deadline)
		if err != nil {
			return err
		}
		if pkt == nil {
			return errors.New("no answer from the device within 4 s (wrong adapter, or the device does not support DCP)")
		}
		if done, err := parseSetResponse(pkt, mac, xid); done {
			return err
		}
	}
}
