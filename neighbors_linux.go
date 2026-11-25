//go:build linux

package main

// The neighbour table on Linux: /proc/net/arp.

import (
	"net"
	"os"
	"strings"
)

func init() {
	readNeighborTable = linuxNeighbors
}

func linuxNeighbors() ([]Neighbor, error) {
	data, err := os.ReadFile("/proc/net/arp")
	if err != nil {
		return nil, err
	}
	return parseProcARP(string(data)), nil
}

// parseProcARP reads "IP address  HW type  Flags  HW address  Mask  Device".
func parseProcARP(s string) []Neighbor {
	local := map[string]string{} // device → its IPv4 address
	if ifs, err := net.Interfaces(); err == nil {
		for _, i := range ifs {
			if addrs, err := i.Addrs(); err == nil {
				for _, a := range addrs {
					if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
						local[i.Name] = n.IP.String()
						break
					}
				}
			}
		}
	}
	out := []Neighbor{}
	for i, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 6 {
			continue
		}
		ip := net.ParseIP(f[0])
		if ip == nil || skipNeighbor(ip, f[3]) || f[2] == "0x0" {
			continue
		}
		st := "reachable"
		if f[2] == "0x6" {
			st = "permanent"
		}
		out = append(out, Neighbor{IP: ip.String(), MAC: strings.ToLower(f[3]), State: st, Interface: f[5], LocalIP: local[f[5]]})
	}
	return out
}
