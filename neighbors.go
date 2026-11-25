package main

// Network neighbours: the addresses this computer has recently exchanged
// packets with on its local networks (the ARP cache, "arp -a"). They show
// every device on the cable – PCs, phones, routers, printers – also ones
// without any industrial port, which the Topology scan does not list. An
// entry can be old (the device may be gone), so the window pings each one.

import (
	"context"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Neighbor is one entry of the neighbour (ARP) table.
type Neighbor struct {
	IP        string `json:"ip"`
	MAC       string `json:"mac"`
	Vendor    string `json:"vendor,omitempty"` // from the MAC address
	Random    bool   `json:"random_mac,omitempty"`
	Name      string `json:"name,omitempty"`  // reverse DNS
	State     string `json:"state,omitempty"` // reachable, stale, permanent …
	Router    bool   `json:"router,omitempty"`
	Interface string `json:"interface"` // adapter name
	LocalIP   string `json:"local_ip"`  // this PC's address on that adapter
	Subnet    string `json:"subnet,omitempty"`
}

// readNeighborTable is set per platform; nil: not available here.
var readNeighborTable func() ([]Neighbor, error)

// macVendors: the first 3 bytes of a MAC address (OUI) of makers often met
// on plant networks.
var macVendors = map[string]string{
	"00:1b:1b": "Siemens", "00:0e:8c": "Siemens", "08:00:06": "Siemens", "28:63:36": "Siemens", "00:1c:06": "Siemens",
	"ac:64:17": "Siemens", "20:87:56": "Siemens", "e0:dc:a0": "Siemens", "8c:f3:19": "Siemens", "00:1f:f8": "Siemens", "68:3e:02": "Siemens",
	"00:a0:45": "Phoenix Contact", "00:01:05": "Beckhoff", "00:30:de": "WAGO", "00:80:f4": "Schneider Electric", "00:00:54": "Schneider Electric",
	"00:00:bc": "Rockwell Automation", "00:1d:9c": "Rockwell Automation", "e4:90:69": "Rockwell Automation", "5c:88:16": "Rockwell Automation",
	"00:00:0a": "Omron", "00:26:92": "Mitsubishi Electric", "00:90:e8": "Moxa", "00:80:63": "Hirschmann", "ec:e5:55": "Hirschmann",
	"00:0b:ab": "Advantech", "00:d0:c9": "Advantech", "00:07:7c": "Westermo", "00:60:65": "B&R", "00:0f:9f": "Bosch Rexroth",
	"00:50:c2": "IEEE registered small vendor", "00:15:5d": "Microsoft Hyper-V", "00:50:56": "VMware", "00:0c:29": "VMware",
	"08:00:27": "VirtualBox", "b8:27:eb": "Raspberry Pi", "dc:a6:32": "Raspberry Pi", "e4:5f:01": "Raspberry Pi", "d8:3a:dd": "Raspberry Pi",
	"2c:cf:67": "Raspberry Pi", "24:0a:c4": "Espressif (ESP32)", "30:ae:a4": "Espressif (ESP32)", "a4:cf:12": "Espressif (ESP32)",
	"24:6f:28": "Espressif (ESP32)", "84:cc:a8": "Espressif (ESP32)", "08:3a:f2": "Espressif (ESP32)", "3c:61:05": "Espressif (ESP32)",
	"7c:9e:bd": "Espressif (ESP32)", "ec:62:60": "Espressif (ESP32)", "c8:f0:9e": "Espressif (ESP32)", "94:b9:7e": "Espressif (ESP32)",
	"98:f4:ab": "Espressif (ESP32)", "e8:68:e7": "Espressif (ESP32)", "40:22:d8": "Espressif (ESP32)", "cc:50:e3": "Espressif (ESP32)",
	"00:e0:4c": "Realtek", "52:54:00": "QEMU / KVM", "00:1a:11": "Google", "f0:9f:c2": "Ubiquiti", "74:83:c2": "Ubiquiti",
	"00:1e:58": "D-Link", "14:cc:20": "TP-Link", "50:c7:bf": "TP-Link", "e8:48:b8": "TP-Link", "98:da:c4": "TP-Link", "ac:84:c6": "TP-Link",
	"00:04:4b": "NVIDIA", "00:1b:21": "Intel", "3c:97:0e": "Intel", "a0:36:9f": "Intel", "00:24:d7": "Intel", "00:0d:b9": "PC Engines",
	"00:17:88": "Philips Hue", "00:11:32": "Synology", "00:08:9b": "QNAP", "00:1e:06": "Wibrain", "00:06:5b": "Dell", "f8:bc:12": "Dell",
	"00:25:64": "Dell", "3c:d9:2b": "HP", "00:1b:78": "HP", "9c:8e:99": "HP", "00:0f:fe": "Gigabyte", "00:24:8c": "ASUS", "04:d4:c4": "ASUS",
	"00:1a:a0": "Dell", "00:00:0c": "Cisco", "00:1b:54": "Cisco", "00:26:0b": "Cisco", "70:ca:9b": "Cisco", "a8:9d:21": "Cisco",
}

// macInfo: the maker of a MAC address, and whether it is a random
// (locally administered) one – phones, tablets, virtual machines, hotspots.
func macInfo(mac string) (string, bool) {
	mac = strings.ToLower(strings.ReplaceAll(mac, "-", ":"))
	if len(mac) < 8 {
		return "", false
	}
	first := mac[:2]
	random := len(first) == 2 && strings.ContainsRune("26ae", rune(first[1]))
	return macVendors[mac[:8]], random
}

// skipNeighbor: multicast, broadcast and unresolved entries are not devices.
func skipNeighbor(ip net.IP, mac string) bool {
	ip4 := ip.To4()
	if ip4 == nil || ip4.IsUnspecified() || ip4.IsLoopback() || ip4.IsMulticast() || ip4.Equal(net.IPv4bcast) || ip4[0] == 169 && ip4[1] == 254 && ip4[3] == 255 {
		return true
	}
	m := strings.ToLower(mac)
	return m == "" || m == "00:00:00:00:00:00" || m == "ff:ff:ff:ff:ff:ff" || strings.HasPrefix(m, "01:00:5e")
}

// finishNeighbors fills in maker, subnet and names, drops broadcast
// addresses of the adapters' subnets and sorts.
func finishNeighbors(list []Neighbor) []Neighbor {
	nets := map[string]*net.IPNet{} // local IP → its network
	if ifs, err := net.Interfaces(); err == nil {
		for _, i := range ifs {
			addrs, _ := i.Addrs()
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
					nets[n.IP.String()] = n
				}
			}
		}
	}
	out := list[:0]
	for _, n := range list {
		if ipn := nets[n.LocalIP]; ipn != nil {
			ip := net.ParseIP(n.IP).To4()
			bc := make(net.IP, 4)
			base := ipn.IP.To4().Mask(ipn.Mask)
			for i := range bc {
				bc[i] = base[i] | ^ipn.Mask[len(ipn.Mask)-4+i]
			}
			if ip.Equal(bc) {
				continue
			}
			ones, _ := ipn.Mask.Size()
			n.Subnet = (&net.IPNet{IP: base, Mask: ipn.Mask}).String()
			_ = ones
		}
		n.Vendor, n.Random = macInfo(n.MAC)
		out = append(out, n)
	}
	// Names by reverse DNS, all at once, briefly.
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	for i := range out {
		wg.Add(1)
		go func(n *Neighbor) {
			defer wg.Done()
			if names, err := net.DefaultResolver.LookupAddr(ctx, n.IP); err == nil && len(names) > 0 {
				n.Name = strings.TrimSuffix(names[0], ".")
			}
		}(&out[i])
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Interface != out[j].Interface {
			return out[i].Interface < out[j].Interface
		}
		return ipLess(out[i].IP, out[j].IP)
	})
	return out
}

func ipLess(a, b string) bool {
	x, y := net.ParseIP(a).To4(), net.ParseIP(b).To4()
	if x == nil || y == nil {
		return a < b
	}
	for i := 0; i < 4; i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

// GET /api/net/neighbors: this computer's neighbour table (this computer only).
func neighborRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/net/neighbors", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"only on the computer running PLC Monitor"})
			return
		}
		if readNeighborTable == nil {
			writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": "the neighbour table can only be read on Windows and Linux", "neighbors": []Neighbor{}})
			return
		}
		list, err := readNeighborTable()
		if err != nil {
			respond(w, err)
			return
		}
		if list == nil {
			list = []Neighbor{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"available": true, "neighbors": finishNeighbors(list)})
	})
}
