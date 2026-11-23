package main

import (
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// serialPorts lists the COM ports Windows currently knows about, including
// USB-RS485 adapters, in natural order (COM2 before COM10).
func serialPorts() []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.QUERY_VALUE)
	if err != nil {
		return nil // no serial ports at all
	}
	defer k.Close()
	names, err := k.ReadValueNames(0)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var ports []string
	for _, n := range names {
		if p, _, err := k.GetStringValue(n); err == nil && p != "" && !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	num := func(p string) int {
		n, _ := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(p), "COM"))
		return n
	}
	sort.Slice(ports, func(i, j int) bool { return num(ports[i]) < num(ports[j]) })
	return ports
}
