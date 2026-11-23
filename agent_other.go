//go:build !linux

package main

import "errors"

// The agent service and its installer are for Linux; on other systems
// -dcp-agent still runs (with Npcap on Windows), without the installer.

func ensureRawAccess() {}

func installAgent([]string) error {
	return errors.New("install-agent is for the Linux version (plcmonitor-linux-amd64 / -arm64)")
}

func uninstallAgent([]string) error {
	return errors.New("uninstall-agent is for the Linux version")
}
