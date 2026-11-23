package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// openFolder shows a folder in the system file manager (Explorer on Windows).
func openFolder(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer.exe", abs).Start()
	case "darwin":
		return exec.Command("open", abs).Start()
	case "linux":
		return exec.Command("xdg-open", abs).Start()
	}
	return errors.New("opening folders is not supported on this system")
}

// openNetworkConnections opens the Windows "Network Connections" window
// (ncpa.cpl), where the PC's network adapters get their IP addresses.
func openNetworkConnections() error {
	if runtime.GOOS != "windows" {
		return errors.New("the Network Connections window is part of Windows")
	}
	sys := filepath.Join(os.Getenv("SystemRoot"), "System32", "control.exe")
	if _, err := os.Stat(sys); err != nil {
		sys = "control.exe"
	}
	return exec.Command(sys, "ncpa.cpl").Start()
}

// openURL opens a web page in the default browser. Only called with fixed
// addresses (e.g. the Npcap download page), never with user input.
func openURL(u string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	case "darwin":
		return exec.Command("open", u).Start()
	case "linux":
		return exec.Command("xdg-open", u).Start()
	}
	return errors.New("opening web pages is not supported on this system")
}
