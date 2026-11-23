package main

// Network adapters of this PC, for reaching a PLC in another network: list
// them, add or remove an IPv4 address (Windows keeps the others), set the
// IPv4 settings by hand (fixed address, gateway, DNS) or back to DHCP,
// enable or disable an adapter, and ping addresses. Changes need
// administrator rights: Windows asks (UAC) for each one; PLC Monitor itself
// keeps running without them.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NetAdapter is one network adapter of this PC.
type NetAdapter struct {
	Name    string      `json:"name"` // e.g. "Ethernet 2"
	Desc    string      `json:"desc"`
	Status  string      `json:"status"` // Up, Disconnected, Disabled, …
	MAC     string      `json:"mac"`
	Media   string      `json:"media,omitempty"` // 802.3, Native 802.11, BlueTooth, …
	Speed   string      `json:"speed,omitempty"` // e.g. "1 Gbps"
	Virtual bool        `json:"virtual,omitempty"`
	DHCP    bool        `json:"dhcp"` // gets its IPv4 address from DHCP
	IPs     []AdapterIP `json:"ips"`
	Gateway []string    `json:"gateway"`
	DNS     []string    `json:"dns"`
}

// AdapterIP is one IPv4 address of an adapter.
type AdapterIP struct {
	IP     string `json:"ip"`
	Prefix int    `json:"prefix"`
	Mask   string `json:"mask"`
	Origin string `json:"origin"` // Manual, Dhcp, WellKnown (169.254.x), …
}

// These are set by the Windows build; elsewhere they stay nil.
var (
	listNetAdapters func() ([]NetAdapter, error)
	// runElevated runs one PowerShell command with administrator rights
	// (Windows asks the user) and returns its error message, if any.
	runElevated func(script string) error
	// hideCmd keeps a console window from flashing up (Windows).
	hideCmd = func(*exec.Cmd) {}
)

var adapterMu sync.Mutex // one change at a time

// adapterChange is a request from the page.
type adapterChange struct {
	Adapter string   `json:"adapter"`
	Action  string   `json:"action"` // add, remove, static, dhcp, enable, disable
	IP      string   `json:"ip,omitempty"`
	Mask    string   `json:"mask,omitempty"`    // 255.255.255.0 or a prefix length
	Gateway string   `json:"gateway,omitempty"` // static: optional
	DNS     []string `json:"dns,omitempty"`     // static: up to 2, optional
}

// psQuote is a PowerShell single-quoted string.
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// prefixOf reads a mask (255.255.255.0) or a prefix length (24).
func prefixOf(mask string) (int, error) {
	mask = strings.TrimPrefix(strings.TrimSpace(mask), "/")
	if !strings.Contains(mask, ".") {
		n, err := strconv.Atoi(mask)
		if err != nil || n < 8 || n > 32 {
			return 0, fmt.Errorf("%q is not a subnet mask like 255.255.255.0 or a prefix length 8..32", mask)
		}
		return n, nil
	}
	ip := net.ParseIP(mask).To4()
	if ip == nil {
		return 0, fmt.Errorf("%q is not a subnet mask like 255.255.255.0", mask)
	}
	ones, bits := net.IPMask(ip).Size()
	if bits == 0 || ones < 8 {
		return 0, fmt.Errorf("%q is not a subnet mask like 255.255.255.0", mask)
	}
	return ones, nil
}

// hostIPv4 reads an address a device can have (not loopback, multicast, …).
func hostIPv4(s, what string) (net.IP, error) {
	ip := net.ParseIP(strings.TrimSpace(s)).To4()
	if ip == nil || ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.Equal(net.IPv4bcast) {
		return nil, fmt.Errorf("%s %q is not a usable IPv4 address", what, s)
	}
	return ip, nil
}

// subnetHost checks that ip is not the network or broadcast address of ip/prefix.
func subnetHost(ip net.IP, prefix int) (*net.IPNet, error) {
	m := net.CIDRMask(prefix, 32)
	n := &net.IPNet{IP: ip.Mask(m), Mask: m}
	if prefix < 31 {
		bc := make(net.IP, 4)
		for i := range bc {
			bc[i] = n.IP[i] | ^m[i]
		}
		if ip.Equal(n.IP) || ip.Equal(bc) {
			return nil, fmt.Errorf("%s is the network or broadcast address of %s", ip, n)
		}
	}
	return n, nil
}

// adapterScript checks a change against the adapters that exist and makes
// the PowerShell command for it. Only checked values go into the command.
func adapterScript(q adapterChange, list []NetAdapter) (script, what string, err error) {
	var ad *NetAdapter
	for i := range list {
		if list[i].Name == q.Adapter {
			ad = &list[i]
		}
	}
	if ad == nil {
		return "", "", fmt.Errorf("no adapter named %q on this PC", q.Adapter)
	}
	name := psQuote(ad.Name)
	// Every address and default gateway of the adapter goes (both the
	// running and the saved settings), as Windows' IPv4 dialog does.
	const clear = "Remove-NetIPAddress -InterfaceAlias %[1]s -AddressFamily IPv4 -Confirm:$false -ErrorAction SilentlyContinue\n" +
		"Remove-NetRoute -InterfaceAlias %[1]s -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -Confirm:$false -ErrorAction SilentlyContinue\n"
	switch q.Action {
	case "enable":
		return "Enable-NetAdapter -Name " + name + " -Confirm:$false -ErrorAction Stop", "enable " + ad.Name, nil
	case "disable":
		return "Disable-NetAdapter -Name " + name + " -Confirm:$false -ErrorAction Stop", "disable " + ad.Name, nil
	case "dhcp":
		return fmt.Sprintf(clear, name) +
				fmt.Sprintf("Set-NetIPInterface -InterfaceAlias %s -AddressFamily IPv4 -Dhcp Enabled -ErrorAction Stop\n", name) +
				fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias %s -ResetServerAddresses -ErrorAction Stop", name),
			"IPv4 of " + ad.Name + " automatic (DHCP)", nil
	case "add", "remove", "static":
	default:
		return "", "", fmt.Errorf("unknown action %q", q.Action)
	}
	ip, err := hostIPv4(q.IP, "IP address")
	if err != nil {
		return "", "", err
	}
	if q.Action == "remove" {
		for _, a := range ad.IPs {
			if a.IP == ip.String() {
				if strings.EqualFold(a.Origin, "Dhcp") {
					return "", "", fmt.Errorf("%s comes from DHCP: it can't be removed here", a.IP)
				}
				return fmt.Sprintf("Remove-NetIPAddress -InterfaceAlias %s -IPAddress %s -Confirm:$false -ErrorAction Stop", name, psQuote(a.IP)),
					fmt.Sprintf("remove %s from %s", a.IP, ad.Name), nil
			}
		}
		return "", "", fmt.Errorf("%s has no address %s", ad.Name, ip)
	}
	prefix, err := prefixOf(q.Mask)
	if err != nil {
		return "", "", err
	}
	subnet, err := subnetHost(ip, prefix)
	if err != nil {
		return "", "", err
	}
	for _, a := range list {
		if q.Action == "static" && a.Name == ad.Name {
			continue // its addresses are replaced
		}
		for _, x := range a.IPs {
			if x.IP == ip.String() {
				return "", "", fmt.Errorf("%s is already an address of %s", ip, a.Name)
			}
		}
	}
	if q.Action == "add" {
		if ad.DHCP {
			return "", "", fmt.Errorf("%s gets its address from DHCP; Windows can't give it a second, fixed address. "+
				"Set its IP settings to manual first", ad.Name)
		}
		return fmt.Sprintf("New-NetIPAddress -InterfaceAlias %s -IPAddress %s -PrefixLength %d -ErrorAction Stop | Out-Null", name, psQuote(ip.String()), prefix),
			fmt.Sprintf("add %s/%d to %s", ip, prefix, ad.Name), nil
	}

	// static: this address only, optional gateway and DNS servers.
	gw := ""
	if strings.TrimSpace(q.Gateway) != "" {
		g, err := hostIPv4(q.Gateway, "gateway")
		if err != nil {
			return "", "", err
		}
		if !subnet.Contains(g) || g.Equal(ip) {
			return "", "", fmt.Errorf("the gateway %s must be another address in %s", g, subnet)
		}
		gw = " -DefaultGateway " + psQuote(g.String())
	}
	var dns []string
	for _, d := range q.DNS {
		if strings.TrimSpace(d) == "" {
			continue
		}
		x, err := hostIPv4(d, "DNS server")
		if err != nil {
			return "", "", err
		}
		dns = append(dns, psQuote(x.String()))
	}
	if len(dns) > 2 {
		return "", "", errors.New("at most 2 DNS servers")
	}
	dnsCmd := fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias %s -ResetServerAddresses -ErrorAction Stop", name)
	if len(dns) > 0 {
		dnsCmd = fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias %s -ServerAddresses @(%s) -ErrorAction Stop", name, strings.Join(dns, ","))
	}
	script = fmt.Sprintf("Set-NetIPInterface -InterfaceAlias %s -AddressFamily IPv4 -Dhcp Disabled -ErrorAction Stop\n", name) +
		fmt.Sprintf(clear, name) +
		fmt.Sprintf("New-NetIPAddress -InterfaceAlias %s -IPAddress %s -PrefixLength %d%s -ErrorAction Stop | Out-Null\n", name, psQuote(ip.String()), prefix, gw) +
		dnsCmd
	what = fmt.Sprintf("IPv4 of %s manual: %s/%d", ad.Name, ip, prefix)
	if gw != "" {
		what += ", gateway " + strings.Trim(gw[len(" -DefaultGateway "):], "'")
	}
	return script, what, nil
}

// PingResult is the answer of one address to a ping.
type PingResult struct {
	IP    string  `json:"ip"`
	Alive bool    `json:"alive"`
	MS    float64 `json:"ms,omitempty"`
	TTL   int     `json:"ttl,omitempty"`
	Self  bool    `json:"self,omitempty"` // an address of this PC
	Error string  `json:"error,omitempty"`
}

var (
	pingTTL  = regexp.MustCompile(`(?i)ttl[=:]\s*(\d+)`)
	pingTime = regexp.MustCompile(`(?i)[=<]\s*([\d.]+)\s*ms`)
)

// pingOnce asks ip once with the system's ping (no administrator rights needed).
func pingOnce(ctx context.Context, ip string, timeout time.Duration) PingResult {
	r := PingResult{IP: ip}
	ms := int(timeout / time.Millisecond)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "ping", "-n", "1", "-w", strconv.Itoa(ms), ip)
	} else {
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-W", strconv.Itoa(max(1, (ms+999)/1000)), ip)
	}
	hideCmd(cmd)
	out, _ := cmd.Output()
	if m := pingTTL.FindSubmatch(out); m != nil { // "Destination host unreachable" has no TTL
		r.Alive = true
		r.TTL, _ = strconv.Atoi(string(m[1]))
		if t := pingTime.FindSubmatch(out); t != nil {
			r.MS, _ = strconv.ParseFloat(string(t[1]), 64)
		}
	}
	return r
}

// pingAll pings up to 64 addresses at once.
func pingAll(ctx context.Context, ips []string, timeout time.Duration) ([]PingResult, error) {
	if len(ips) == 0 || len(ips) > 64 {
		return nil, errors.New("give 1..64 addresses")
	}
	own := map[string]bool{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				own[n.IP.String()] = true
			}
		}
	}
	res := make([]PingResult, len(ips))
	var wg sync.WaitGroup
	for i, s := range ips {
		ip := net.ParseIP(strings.TrimSpace(s)).To4()
		if ip == nil {
			res[i] = PingResult{IP: s, Error: "not an IPv4 address"}
			continue
		}
		wg.Add(1)
		go func(i int, ip string) {
			defer wg.Done()
			res[i] = pingOnce(ctx, ip, timeout)
			res[i].Self = own[ip]
		}(i, ip.String())
	}
	wg.Wait()
	return res, nil
}

// netAdapterRoutes: GET /api/os/adapters, POST /api/os/adapters (a change),
// POST /api/os/ping. This computer only; changes not on a read-only dashboard.
func netAdapterRoutes(mux *http.ServeMux, readOnly bool) {
	mux.HandleFunc("GET /api/os/adapters", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"only on the computer running PLC Monitor"})
			return
		}
		if listNetAdapters == nil {
			writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": "managing network adapters is only possible on Windows", "adapters": []NetAdapter{}})
			return
		}
		list, err := listNetAdapters()
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"available": true, "can_change": !readOnly && runElevated != nil, "adapters": list})
	})
	mux.HandleFunc("POST /api/os/adapters", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) || r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusForbidden, apiError{"only on the computer running PLC Monitor"})
			return
		}
		if readOnly {
			writeJSON(w, http.StatusForbidden, apiError{"the dashboard is read-only"})
			return
		}
		if listNetAdapters == nil || runElevated == nil {
			respond(w, errors.New("managing network adapters is only possible on Windows"))
			return
		}
		var q adapterChange
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&q); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		adapterMu.Lock()
		defer adapterMu.Unlock()
		list, err := listNetAdapters()
		if err != nil {
			respond(w, err)
			return
		}
		script, what, err := adapterScript(q, list)
		if err == nil {
			err = runElevated(script)
		}
		result := "done"
		if err != nil {
			result = "FAILED: " + err.Error()
		}
		log.Printf("NETWORK ADAPTER %s (from %s) – %s", what, r.RemoteAddr, result)
		respond(w, err)
	})
	mux.HandleFunc("POST /api/os/ping", func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) || r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusForbidden, apiError{"only on the computer running PLC Monitor"})
			return
		}
		var q struct {
			IPs       []string `json:"ips"`
			TimeoutMS int      `json:"timeout_ms"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&q); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		t := time.Duration(min(max(q.TimeoutMS, 200), 5000)) * time.Millisecond
		ctx, cancel := context.WithTimeout(r.Context(), t+5*time.Second)
		defer cancel()
		res, err := pingAll(ctx, q.IPs, t)
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": res})
	})
}
