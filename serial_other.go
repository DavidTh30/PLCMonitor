//go:build !windows

package main

// serialPorts is only implemented on Windows; elsewhere the port name
// (e.g. /dev/ttyUSB0) is typed in.
func serialPorts() []string { return nil }
