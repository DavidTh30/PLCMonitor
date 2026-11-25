//go:build windows

package main

// The neighbour table on Windows: GetIpNetTable2 (iphlpapi), the same table
// "arp -a" prints, with each entry's state.

import (
	"fmt"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi         = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetIpNetTbl2 = iphlpapi.NewProc("GetIpNetTable2")
	procFreeMibTable = iphlpapi.NewProc("FreeMibTable")
)

// mibIPNetRow2 mirrors MIB_IPNET_ROW2 (88 bytes).
type mibIPNetRow2 struct {
	Family    uint16 // SOCKADDR_INET: AF_INET
	Port      uint16
	Addr      [4]byte  // IPv4 address
	_         [20]byte // rest of SOCKADDR_INET (28 bytes in all)
	IfIndex   uint32   // offset 28
	IfLuid    uint64   // offset 32
	Phys      [32]byte // offset 40
	PhysLen   uint32   // offset 72
	State     int32    // offset 76: NL_NEIGHBOR_STATE
	Flags     uint8    // offset 80: IsRouter, IsUnreachable
	_         [3]byte
	Reachable uint32 // offset 84
}

var neighborStates = []string{"unreachable", "incomplete", "probe", "delay", "stale", "reachable", "permanent"}

func init() {
	readNeighborTable = windowsNeighbors
}

func windowsNeighbors() ([]Neighbor, error) {
	if err := procGetIpNetTbl2.Find(); err != nil {
		return nil, err
	}
	var table unsafe.Pointer // MIB_IPNET_TABLE2*: NumEntries, then the rows at offset 8
	if r, _, _ := procGetIpNetTbl2.Call(windows.AF_INET, uintptr(unsafe.Pointer(&table))); r != 0 {
		return nil, fmt.Errorf("GetIpNetTable2: error %d", r)
	}
	defer procFreeMibTable.Call(uintptr(table))
	count := *(*uint32)(table)
	rows := unsafe.Slice((*mibIPNetRow2)(unsafe.Add(table, 8)), count)

	// Adapter name and this PC's IPv4 address per interface index.
	type ifInfo struct{ name, ip string }
	ifs := map[uint32]ifInfo{}
	if list, err := net.Interfaces(); err == nil {
		for _, i := range list {
			inf := ifInfo{name: i.Name}
			if addrs, err := i.Addrs(); err == nil {
				for _, a := range addrs {
					if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
						inf.ip = n.IP.String()
						break
					}
				}
			}
			ifs[uint32(i.Index)] = inf
		}
	}
	out := []Neighbor{}
	for _, r := range rows {
		if r.Family != windows.AF_INET || r.PhysLen != 6 {
			continue
		}
		ip := net.IP(r.Addr[:])
		mac := net.HardwareAddr(r.Phys[:6]).String()
		if skipNeighbor(ip, mac) {
			continue
		}
		st := ""
		if r.State >= 0 && int(r.State) < len(neighborStates) {
			st = neighborStates[r.State]
		}
		if st == "unreachable" || st == "incomplete" {
			continue
		}
		inf := ifs[r.IfIndex]
		out = append(out, Neighbor{IP: ip.String(), MAC: mac, State: st, Router: r.Flags&1 != 0, Interface: inf.name, LocalIP: inf.ip})
	}
	return out, nil
}
