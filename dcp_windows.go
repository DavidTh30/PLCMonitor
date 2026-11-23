//go:build windows

package main

// Raw Ethernet access for PROFINET DCP through Npcap (https://npcap.com),
// loaded at run time from System32\Npcap\wpcap.dll, so the program still
// starts and works without it. No cgo: the DLL is called directly.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type wpcap struct {
	findAllDevs, freeAllDevs, openLive, close, sendPacket, nextEx, compile, setFilter, freeCode, getErr uintptr
}

var (
	pcapMu  sync.Mutex
	pcapLib *wpcap
)

const npcapMissing = "Npcap is not installed. PROFINET device discovery and configuration send raw Ethernet frames, " +
	"which on Windows needs the free Npcap driver from npcap.com (install it with “WinPcap API-compatible mode” ticked). " +
	"Then press Check again. Or use a Linux agent, which needs nothing on this PC."

// loadPcap loads wpcap.dll. A failure is not remembered, so installing Npcap
// while PLC Monitor runs and pressing "Check again" works.
func loadPcap() (*wpcap, error) {
	pcapMu.Lock()
	defer pcapMu.Unlock()
	if pcapLib != nil {
		return pcapLib, nil
	}
	sys := filepath.Join(os.Getenv("SystemRoot"), "System32")
	var h windows.Handle
	var err error = os.ErrNotExist
	for _, p := range []string{filepath.Join(sys, "Npcap", "wpcap.dll"), filepath.Join(sys, "wpcap.dll")} {
		if _, statErr := os.Stat(p); statErr != nil {
			continue
		}
		// Packet.dll sits next to wpcap.dll; let Windows find it there.
		if h, err = windows.LoadLibraryEx(p, 0, windows.LOAD_WITH_ALTERED_SEARCH_PATH); err == nil {
			break
		}
	}
	if err != nil {
		return nil, errors.New(npcapMissing)
	}
	l := &wpcap{}
	for name, dst := range map[string]*uintptr{
		"pcap_findalldevs": &l.findAllDevs, "pcap_freealldevs": &l.freeAllDevs, "pcap_open_live": &l.openLive,
		"pcap_close": &l.close, "pcap_sendpacket": &l.sendPacket, "pcap_next_ex": &l.nextEx,
		"pcap_compile": &l.compile, "pcap_setfilter": &l.setFilter, "pcap_freecode": &l.freeCode, "pcap_geterr": &l.getErr,
	} {
		p, err := windows.GetProcAddress(h, name)
		if err != nil {
			return nil, fmt.Errorf("wpcap.dll has no %s: %v (update Npcap)", name, err)
		}
		*dst = p
	}
	pcapLib = l
	return l, nil
}

// pcapIf mirrors struct pcap_if.
type pcapIf struct {
	next  *pcapIf
	name  *byte
	desc  *byte
	addrs uintptr
	flags uint32
}

// pcapPkthdr mirrors struct pcap_pkthdr on Windows (timeval with 32-bit longs).
type pcapPkthdr struct {
	sec, usec    int32
	caplen, plen uint32
}

// bpfProgram mirrors struct bpf_program.
type bpfProgram struct {
	len   uint32
	insns uintptr
}

func cstr(p *byte) string {
	if p == nil {
		return ""
	}
	return windows.BytePtrToString(p)
}

func init() {
	dcpUnavailable = npcapMissing
	// Without Npcap dcpOpen stays nil: the Topology window explains how to
	// get it, and "Check again" calls dcpRecheck after installing.
	dcpRecheck = func() error {
		if _, err := loadPcap(); err != nil {
			return err
		}
		dcpMu.Lock()
		dcpListAdapter, dcpOpen = npcapAdapters, npcapOpen
		dcpMu.Unlock()
		return nil
	}
	dcpRecheck()
}

// windowsAdapters maps adapter GUIDs to their name, description, MAC and IPv4 addresses.
func windowsAdapters() (map[string]DCPAdapter, error) {
	size := uint32(32 << 10)
	var buf []byte
	for i := 0; i < 3; i++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_INCLUDE_PREFIX, 0,
			(*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW || i == 2 {
			return nil, err
		}
	}
	out := map[string]DCPAdapter{}
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		if a.PhysicalAddressLength != 6 || a.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK {
			continue
		}
		ad := DCPAdapter{Name: windows.UTF16PtrToString(a.FriendlyName), Desc: windows.UTF16PtrToString(a.Description),
			MAC: net.HardwareAddr(a.PhysicalAddress[:6]).String(), IPs: []string{}}
		for u := a.FirstUnicastAddress; u != nil; u = u.Next {
			if ip := u.Address.IP(); ip != nil && ip.To4() != nil {
				ad.IPs = append(ad.IPs, ip.String())
			}
		}
		out[strings.ToUpper(cstr(a.AdapterName))] = ad
	}
	return out, nil
}

func npcapAdapters() ([]DCPAdapter, error) {
	l, err := loadPcap()
	if err != nil {
		return nil, err
	}
	win, err := windowsAdapters()
	if err != nil {
		return nil, err
	}
	var head *pcapIf
	errbuf := make([]byte, 512)
	if r, _, _ := syscall.SyscallN(l.findAllDevs, uintptr(unsafe.Pointer(&head)), uintptr(unsafe.Pointer(&errbuf[0]))); int32(r) != 0 {
		return nil, fmt.Errorf("Npcap: %s", cstr(&errbuf[0]))
	}
	defer syscall.SyscallN(l.freeAllDevs, uintptr(unsafe.Pointer(head)))
	list := []DCPAdapter{}
	for d := head; d != nil; d = d.next {
		name := cstr(d.name)
		i := strings.Index(name, "{")
		if i < 0 {
			continue
		}
		ad, ok := win[strings.ToUpper(name[i:])]
		if !ok {
			continue
		}
		ad.ID = name
		list = append(list, ad)
	}
	return list, nil
}

type npcapLink struct {
	l   *wpcap
	h   uintptr
	mac net.HardwareAddr
}

func npcapOpen(adapterID string) (dcpLink, error) {
	l, err := loadPcap()
	if err != nil {
		return nil, err
	}
	adapters, err := npcapAdapters()
	if err != nil {
		return nil, err
	}
	var ad *DCPAdapter
	for i := range adapters {
		if adapters[i].ID == adapterID {
			ad = &adapters[i]
		}
	}
	if ad == nil {
		return nil, errors.New("choose the network adapter the PROFINET devices are connected to")
	}
	mac, _ := net.ParseMAC(ad.MAC)
	dev, _ := windows.BytePtrFromString(adapterID)
	errbuf := make([]byte, 512)
	// snaplen 1518, not promiscuous, 100 ms read timeout.
	h, _, _ := syscall.SyscallN(l.openLive, uintptr(unsafe.Pointer(dev)), 1518, 0, 100, uintptr(unsafe.Pointer(&errbuf[0])))
	if h == 0 {
		return nil, fmt.Errorf("open %s: %s", ad.Name, cstr(&errbuf[0]))
	}
	link := &npcapLink{l: l, h: h, mac: mac}
	// Only DCP frames (also VLAN-tagged ones) reach us.
	filter, _ := windows.BytePtrFromString("ether proto 0x8892 or (vlan and ether proto 0x8892)")
	var prog bpfProgram
	if r, _, _ := syscall.SyscallN(l.compile, h, uintptr(unsafe.Pointer(&prog)), uintptr(unsafe.Pointer(filter)), 1, 0xffffffff); int32(r) == 0 {
		syscall.SyscallN(l.setFilter, h, uintptr(unsafe.Pointer(&prog)))
		syscall.SyscallN(l.freeCode, uintptr(unsafe.Pointer(&prog)))
	}
	return link, nil
}

func (n *npcapLink) MAC() net.HardwareAddr { return n.mac }

func (n *npcapLink) lastErr() string {
	r, _, _ := syscall.SyscallN(n.l.getErr, n.h)
	// r is a char* owned by Npcap; read it without converting uintptr directly.
	return cstr(*(**byte)(unsafe.Pointer(&r)))
}

func (n *npcapLink) Send(frame []byte) error {
	if r, _, _ := syscall.SyscallN(n.l.sendPacket, n.h, uintptr(unsafe.Pointer(&frame[0])), uintptr(len(frame))); int32(r) != 0 {
		return fmt.Errorf("send: %s", n.lastErr())
	}
	return nil
}

func (n *npcapLink) Recv(deadline time.Time) ([]byte, error) {
	for time.Now().Before(deadline) {
		var hdr *pcapPkthdr
		var data *byte
		r, _, _ := syscall.SyscallN(n.l.nextEx, n.h, uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&data)))
		switch int32(r) {
		case 1:
			if hdr == nil || data == nil || hdr.caplen == 0 {
				continue
			}
			return append([]byte(nil), unsafe.Slice(data, hdr.caplen)...), nil
		case 0: // read timeout, keep waiting
		default:
			return nil, fmt.Errorf("receive: %s", n.lastErr())
		}
	}
	return nil, nil
}

func (n *npcapLink) Close() error {
	syscall.SyscallN(n.l.close, n.h)
	return nil
}
