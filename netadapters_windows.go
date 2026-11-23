//go:build windows

package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func init() {
	listNetAdapters = winNetAdapters
	runElevated = winRunElevated
	hideCmd = func(c *exec.Cmd) {
		c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /*CREATE_NO_WINDOW*/}
	}
}

// powershellExe is Windows PowerShell 5.1 (has the NetAdapter/NetTCPIP modules).
func powershellExe() string {
	p := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return "powershell.exe"
}

// psEncoded is a script for -EncodedCommand (base64 of UTF-16LE).
func psEncoded(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

const psListAdapters = `[Console]::OutputEncoding = [Text.Encoding]::UTF8
$a = @(Get-NetAdapter -ErrorAction SilentlyContinue | ForEach-Object { @{n=$_.Name; d=$_.InterfaceDescription; s="$($_.Status)"; m=$_.MacAddress; t="$($_.PhysicalMediaType)"; sp="$($_.LinkSpeed)"; v=[bool]$_.Virtual} })
$i = @(Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | ForEach-Object { @{n=$_.InterfaceAlias; ip=$_.IPAddress; p=[int]$_.PrefixLength; o="$($_.PrefixOrigin)"} })
$f = @(Get-NetIPInterface -AddressFamily IPv4 -ErrorAction SilentlyContinue | ForEach-Object { @{n=$_.InterfaceAlias; h="$($_.Dhcp)"} })
$r = @(Get-NetRoute -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | ForEach-Object { @{n=$_.InterfaceAlias; g=$_.NextHop} })
$s = @(Get-DnsClientServerAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | ForEach-Object { @{n=$_.InterfaceAlias; x=@($_.ServerAddresses)} })
@{a=$a; i=$i; f=$f; r=$r; s=$s} | ConvertTo-Json -Depth 4 -Compress`

// winNetAdapters lists the adapters with PowerShell: unlike net.Interfaces it
// also shows disabled adapters, and where each address comes from.
func winNetAdapters() ([]NetAdapter, error) {
	cmd := exec.Command(powershellExe(), "-NoProfile", "-NonInteractive", "-EncodedCommand", psEncoded(psListAdapters))
	hideCmd(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing network adapters: %v", err)
	}
	return parseAdapterList(out)
}

// parseAdapterList reads what psListAdapters prints.
func parseAdapterList(out []byte) ([]NetAdapter, error) {
	var raw struct {
		A []struct {
			N, D, S, M, T, Sp string
			V                 bool
		} `json:"a"`
		I []struct {
			N, IP, O string
			P        int
		} `json:"i"`
		F []struct{ N, H string } `json:"f"`
		R []struct{ N, G string } `json:"r"`
		S []struct {
			N string
			X []string
		} `json:"s"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("listing network adapters: %v", err)
	}
	list := make([]NetAdapter, 0, len(raw.A))
	for _, a := range raw.A {
		ad := NetAdapter{Name: a.N, Desc: a.D, Status: a.S, MAC: a.M, Media: a.T, Speed: a.Sp, Virtual: a.V,
			IPs: []AdapterIP{}, Gateway: []string{}, DNS: []string{}}
		for _, f := range raw.F {
			if f.N == a.N {
				ad.DHCP = strings.EqualFold(f.H, "Enabled")
			}
		}
		for _, r := range raw.R {
			if r.N == a.N && r.G != "" && r.G != "0.0.0.0" && !slices.Contains(ad.Gateway, r.G) {
				ad.Gateway = append(ad.Gateway, r.G)
			}
		}
		for _, s := range raw.S {
			if s.N == a.N {
				ad.DNS = append(ad.DNS, s.X...)
			}
		}
		for _, x := range raw.I {
			if x.N == a.N {
				ad.IPs = append(ad.IPs, AdapterIP{IP: x.IP, Prefix: x.P, Mask: net.IP(net.CIDRMask(x.P, 32)).String(), Origin: x.O})
			}
		}
		list = append(list, ad)
	}
	return list, nil
}

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     windows.Handle
}

var procShellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// winRunElevated runs a PowerShell script as administrator: Windows shows
// its UAC prompt. The script's error message comes back through a file,
// because the output of an elevated process can't be read directly.
func winRunElevated(script string) error {
	f, err := os.CreateTemp("", "plcmonitor-netcfg-*.txt")
	if err != nil {
		return err
	}
	outPath := f.Name()
	f.Close()
	defer os.Remove(outPath)
	q := psQuote(outPath)
	full := "$ErrorActionPreference = 'Stop'\ntry {\n" + script + "\n'OK' | Set-Content -LiteralPath " + q + " -Encoding UTF8\n} catch {\n" +
		"$_.Exception.Message | Set-Content -LiteralPath " + q + " -Encoding UTF8\nexit 1\n}"
	params := "-NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand " + psEncoded(full)

	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(powershellExe())
	args, _ := windows.UTF16PtrFromString(params)
	info := shellExecuteInfo{
		fMask:        0x40 | 0x100, // SEE_MASK_NOCLOSEPROCESS | SEE_MASK_NOASYNC
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: args,
		nShow:        0, // SW_HIDE
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, e := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if errors.Is(e, windows.ERROR_CANCELLED) {
			return errors.New("cancelled: Windows did not get administrator permission")
		}
		return fmt.Errorf("running as administrator: %v", e)
	}
	if info.hProcess == 0 {
		return errors.New("running as administrator: no process")
	}
	defer windows.CloseHandle(info.hProcess)
	ev, err := windows.WaitForSingleObject(info.hProcess, uint32((90 * time.Second).Milliseconds()))
	if err != nil {
		return err
	}
	if ev != windows.WAIT_OBJECT_0 {
		return errors.New("the change did not finish within 90 s")
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return err
	}
	msg, _ := os.ReadFile(outPath)
	text := strings.TrimSpace(strings.TrimPrefix(string(msg), "\xEF\xBB\xBF"))
	if code == 0 && text == "OK" {
		return nil
	}
	if text == "" || text == "OK" {
		text = fmt.Sprintf("PowerShell ended with code %d", code)
	}
	return errors.New(text)
}
