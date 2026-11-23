package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAdapterScript(t *testing.T) {
	list := []NetAdapter{
		{Name: "Ethernet 2", Status: "Up", IPs: []AdapterIP{{IP: "192.168.5.200", Prefix: 24, Origin: "Manual"}}},
		{Name: "Wi-Fi", Status: "Up", DHCP: true, IPs: []AdapterIP{{IP: "10.0.0.7", Prefix: 24, Origin: "Dhcp"}}},
		{Name: "Bob's LAN", Status: "Disabled", IPs: []AdapterIP{}},
	}
	ok := []struct {
		q    adapterChange
		want string
	}{
		{adapterChange{Adapter: "Ethernet 2", Action: "add", IP: "192.168.1.200", Mask: "255.255.255.0"},
			"New-NetIPAddress -InterfaceAlias 'Ethernet 2' -IPAddress '192.168.1.200' -PrefixLength 24"},
		{adapterChange{Adapter: "Ethernet 2", Action: "add", IP: "10.14.95.200", Mask: "/16"}, "-PrefixLength 16"},
		{adapterChange{Adapter: "Ethernet 2", Action: "remove", IP: "192.168.5.200"},
			"Remove-NetIPAddress -InterfaceAlias 'Ethernet 2' -IPAddress '192.168.5.200' -Confirm:$false"},
		{adapterChange{Adapter: "Bob's LAN", Action: "enable"}, "Enable-NetAdapter -Name 'Bob''s LAN'"},
		{adapterChange{Adapter: "Wi-Fi", Action: "disable"}, "Disable-NetAdapter -Name 'Wi-Fi'"},
		{adapterChange{Adapter: "Wi-Fi", Action: "dhcp"}, "-Dhcp Enabled"},
		{adapterChange{Adapter: "Wi-Fi", Action: "static", IP: "192.168.1.200", Mask: "24", Gateway: "192.168.1.1", DNS: []string{"8.8.8.8", ""}},
			"-IPAddress '192.168.1.200' -PrefixLength 24 -DefaultGateway '192.168.1.1'"},
		{adapterChange{Adapter: "Wi-Fi", Action: "static", IP: "192.168.1.200", Mask: "24", DNS: []string{"8.8.8.8"}}, "-ServerAddresses @('8.8.8.8')"},
		{adapterChange{Adapter: "Ethernet 2", Action: "static", IP: "192.168.5.200", Mask: "24"}, "-ResetServerAddresses"}, // its own address again
	}
	for _, c := range ok {
		s, _, err := adapterScript(c.q, list)
		if err != nil || !strings.Contains(s, c.want) {
			t.Errorf("%+v: %q, %v; want %q", c.q, s, err, c.want)
		}
	}
	bad := []adapterChange{
		{Adapter: "Ethernet 9", Action: "enable"},
		{Adapter: "Ethernet 2", Action: "rename"},
		{Adapter: "Ethernet 2", Action: "add", IP: "192.168.1.1'; Remove-Item C:\\", Mask: "24"},
		{Adapter: "Ethernet 2", Action: "add", IP: "192.168.1.0", Mask: "24"},   // network
		{Adapter: "Ethernet 2", Action: "add", IP: "192.168.1.255", Mask: "24"}, // broadcast
		{Adapter: "Ethernet 2", Action: "add", IP: "127.0.0.2", Mask: "8"},
		{Adapter: "Ethernet 2", Action: "add", IP: "169.254.1.1", Mask: "16"},
		{Adapter: "Ethernet 2", Action: "add", IP: "192.168.1.200", Mask: "255.0.255.0"},
		{Adapter: "Ethernet 2", Action: "add", IP: "192.168.1.200", Mask: "4"},
		{Adapter: "Ethernet 2", Action: "add", IP: "10.0.0.7", Mask: "24"},                                   // already on Wi-Fi
		{Adapter: "Wi-Fi", Action: "add", IP: "192.168.1.200", Mask: "24"},                                   // DHCP
		{Adapter: "Wi-Fi", Action: "remove", IP: "10.0.0.7"},                                                 // from DHCP
		{Adapter: "Ethernet 2", Action: "remove", IP: "192.168.1.9"},                                         // not there
		{Adapter: "Ethernet 2", Action: "static", IP: "192.168.1.200", Mask: "24", Gateway: "192.168.2.1"},   // gateway outside
		{Adapter: "Ethernet 2", Action: "static", IP: "192.168.1.200", Mask: "24", Gateway: "192.168.1.200"}, // gateway = itself
		{Adapter: "Ethernet 2", Action: "static", IP: "192.168.1.200", Mask: "24", DNS: []string{"x'"}},
		{Adapter: "Ethernet 2", Action: "static", IP: "192.168.1.200", Mask: "24", DNS: []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"}},
		{Adapter: "Ethernet 2", Action: "static", IP: "10.0.0.7", Mask: "24"}, // on Wi-Fi
	}
	for _, q := range bad {
		if s, _, err := adapterScript(q, list); err == nil {
			t.Errorf("%+v: accepted: %q", q, s)
		}
	}
}

func TestPingLocal(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	res, err := pingAll(context.Background(), []string{"127.0.0.1", "nope"}, time.Second)
	if err != nil || !res[0].Alive || res[1].Error == "" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestPrefixOf(t *testing.T) {
	for in, want := range map[string]int{"24": 24, "/16": 16, "255.255.255.0": 24, "255.255.0.0": 16, "255.255.255.252": 30, "32": 32} {
		if got, err := prefixOf(in); err != nil || got != want {
			t.Errorf("prefixOf(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
}
