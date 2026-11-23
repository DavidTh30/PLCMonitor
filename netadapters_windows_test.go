//go:build windows

package main

import "testing"

func TestParseAdapterList(t *testing.T) {
	out := `{"a":[{"n":"Ethernet 2","d":"Realtek","s":"Up","m":"00-11-22-33-44-55"},{"n":"Wi-Fi","d":"Intel","s":"Disabled","m":"AA-BB"}],` +
		`"i":[{"n":"Ethernet 2","ip":"192.168.5.200","p":24,"o":"Manual"},{"n":"Ethernet 2","ip":"192.168.1.200","p":24,"o":"Manual"},{"n":"Loopback Pseudo-Interface 1","ip":"127.0.0.1","p":8,"o":"WellKnown"}],` +
		`"f":[{"n":"Ethernet 2","h":"Disabled"},{"n":"Wi-Fi","h":"Enabled"}]}`
	list, err := parseAdapterList([]byte(out))
	if err != nil || len(list) != 2 {
		t.Fatal(list, err)
	}
	e, w := list[0], list[1]
	if e.DHCP || len(e.IPs) != 2 || e.IPs[1].Mask != "255.255.255.0" || e.IPs[0].Origin != "Manual" {
		t.Errorf("Ethernet 2: %+v", e)
	}
	if !w.DHCP || w.Status != "Disabled" || len(w.IPs) != 0 {
		t.Errorf("Wi-Fi: %+v", w)
	}
}

// The real adapters of this PC (read only).
func TestWinNetAdaptersLive(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	list, err := winNetAdapters()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		t.Logf("%-28s %-12s %-14s %-9s dhcp=%-5v %v gw=%v dns=%v", a.Name, a.Status, a.Media, a.Speed, a.DHCP, a.IPs, a.Gateway, a.DNS)
	}
}
