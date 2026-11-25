package main

import (
	"encoding/binary"
	"net"
	"runtime"
	"testing"
)

func tlv(typ int, v []byte) []byte {
	return append(binary.BigEndian.AppendUint16(nil, uint16(typ<<9|len(v))), v...)
}

// A PROFINET device's LLDP frame: name of station as chassis, port-001,
// management IP, PROFINET chassis MAC and the link type.
func TestParseLLDP(t *testing.T) {
	src := net.HardwareAddr{0x28, 0x63, 0x36, 0x11, 0x22, 0x33}
	f := append(append(append([]byte{}, lldpMulticast...), src...), 0x88, 0xcc)
	f = append(f, tlv(1, append([]byte{7}, "plc-line1"...))...)
	f = append(f, tlv(2, append([]byte{7}, "port-001"...))...)
	f = append(f, tlv(3, []byte{0, 20})...)
	f = append(f, tlv(4, []byte("Siemens, SIMATIC S7, CPU-1200, X1 P1"))...)
	f = append(f, tlv(7, []byte{0, 0x84, 0, 0x80})...)                            // station
	f = append(f, tlv(8, []byte{5, 1, 192, 168, 0, 10, 2, 0, 0, 0, 1, 0})...)     // mgmt IPv4
	f = append(f, tlv(127, append([]byte{0x00, 0x0e, 0xcf, 5}, src...))...)       // PROFINET chassis MAC
	f = append(f, tlv(127, []byte{0x00, 0x12, 0x0f, 1, 3, 0x6c, 0x00, 0, 16})...) // autoneg on, 100BASE-TX FD
	f = append(f, tlv(0, nil)...)
	n, ok := parseLLDP(f)
	if !ok || n.ChassisID != "plc-line1" || n.PortID != "port-001" || n.TTL != 20 || n.MgmtIP != "192.168.0.10" ||
		!n.Profinet || n.PNChassis != src.String() || n.MAU != "100BASE-TX full duplex" || n.Autoneg != "on" ||
		len(n.Caps) != 1 || n.Caps[0] != "station" || n.SourceMAC != src.String() {
		t.Fatalf("%v %+v", ok, n)
	}
	// A switch with a MAC chassis ID.
	g := append(append(append([]byte{}, lldpMulticast...), src...), 0x88, 0xcc)
	g = append(g, tlv(1, append([]byte{4}, src...))...)
	g = append(g, tlv(2, append([]byte{5}, "Gi0/3"...))...)
	if n, ok := parseLLDP(g); !ok || n.ChassisID != src.String() || n.ChassisSub != "mac" || n.PortID != "Gi0/3" {
		t.Fatalf("%+v", n)
	}
	if _, ok := parseLLDP(f[:20]); ok {
		t.Error("a broken frame accepted")
	}
}

func TestResetFrame(t *testing.T) {
	src, dst := net.HardwareAddr{2, 0, 0, 0, 0, 1}, net.HardwareAddr{0x28, 0x63, 0x36, 1, 2, 3}
	f := buildSet(dst, src, 7, []dcpBlock{resetBlock(dcpResetModes["communication"])})
	// DCP header 10 bytes after the Ethernet header, then option 5 / suboption 6, length 2, qualifier 0x0004.
	b := f[14+12:]
	if b[0] != 5 || b[1] != 6 || binary.BigEndian.Uint16(b[2:4]) != 2 || binary.BigEndian.Uint16(b[4:6]) != 4 {
		t.Fatalf("% x", f[14:34])
	}
	if err := dcpReset("x", dst, "nonsense"); err == nil {
		t.Error("unknown mode accepted")
	}
}

func TestMACInfo(t *testing.T) {
	if v, r := macInfo("28-63-36-AA-BB-CC"); v != "Siemens" || r {
		t.Error(v, r)
	}
	if _, r := macInfo("ca:58:c0:bd:66:4f"); !r {
		t.Error("random MAC not seen")
	}
	if !skipNeighbor(net.ParseIP("224.0.0.22"), "01:00:5e:00:00:16") || !skipNeighbor(net.ParseIP("192.168.0.255"), "ff:ff:ff:ff:ff:ff") ||
		skipNeighbor(net.ParseIP("192.168.0.10"), "28:63:36:aa:bb:cc") {
		t.Error("skip rules")
	}
}

// The table read through GetIpNetTable2 has the entries "arp -a" shows.
func TestNeighborTable(t *testing.T) {
	if readNeighborTable == nil {
		t.Skip("no neighbour table on " + runtime.GOOS)
	}
	list, err := readNeighborTable()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range list {
		if net.ParseIP(n.IP).To4() == nil || len(n.MAC) != 17 || n.Interface == "" {
			t.Errorf("odd entry %+v", n)
		}
	}
	t.Logf("%d neighbours, e.g. %+v", len(list), first(list))
}

func first(l []Neighbor) any {
	if len(l) == 0 {
		return nil
	}
	return l[0]
}
