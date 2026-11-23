//go:build linux

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	agentBinary = "/usr/local/bin/plcmonitor"
	agentUnit   = "/etc/systemd/system/plcmonitor-dcp.service"

	// Up to 1.2.1 the program was called s7monitor; install-agent takes over
	// that agent (same token) and removes it.
	oldAgentBinary    = "/usr/local/bin/s7monitor"
	oldAgentUnit      = "/etc/systemd/system/s7monitor-dcp.service"
	oldAgentService   = "s7monitor-dcp"
	oldAgentTokenFile = "/etc/s7monitor/agent.token"
)

// The service runs with only CAP_NET_RAW: raw Ethernet, none of root's other powers.
const agentUnitText = `[Unit]
Description=PLC Monitor PROFINET DCP agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s -dcp-agent %s -token-file %s
Restart=on-failure
RestartSec=3
CapabilityBoundingSet=CAP_NET_RAW
AmbientCapabilities=CAP_NET_RAW
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
`

func interactive() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// sudoReexec starts this program again through sudo (it asks for the
// password), with the same arguments. It only returns on failure.
func sudoReexec(why string) error {
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return fmt.Errorf("%s: run it as root", why)
	}
	self, _ := os.Executable()
	fmt.Fprintf(os.Stderr, "%s – asking sudo…\n", why)
	return syscall.Exec(sudo, append([]string{"sudo", "--", self}, os.Args[1:]...), os.Environ())
}

// ensureRawAccess: started by hand without permission for raw Ethernet,
// the agent restarts itself through sudo.
func ensureRawAccess() {
	if rawAllowed() == nil || os.Geteuid() == 0 || !interactive() {
		return
	}
	if err := sudoReexec("raw Ethernet access needs root or CAP_NET_RAW"); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	out.Close()
	return os.Rename(tmp, dst) // also works while the old binary is running
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// installAgent sets the agent up as a systemd service that starts at boot
// with CAP_NET_RAW. Run once: sudo ./plcmonitor install-agent
func installAgent(args []string) error {
	fs := flag.NewFlagSet("install-agent", flag.ExitOnError)
	listen := fs.String("listen", ":8095", "address the agent listens on")
	fs.Parse(args)
	if os.Geteuid() != 0 {
		return sudoReexec("install-agent needs root once, to install the service")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if s, err := filepath.EvalSymlinks(self); err == nil {
		self = s
	}
	if self != agentBinary {
		if err := copyFile(self, agentBinary, 0o755); err != nil {
			return fmt.Errorf("copy to %s: %w", agentBinary, err)
		}
	}
	// Also lets a hand-started agent work without sudo.
	run("setcap", "cap_net_raw+ep", agentBinary)

	if err := os.MkdirAll(filepath.Dir(agentTokenFile), 0o755); err != nil {
		return err
	}
	token := ""
	if b, err := os.ReadFile(agentTokenFile); err == nil {
		token = strings.TrimSpace(string(b))
	}
	// The agent of the earlier name: keep its token (Windows PCs already
	// have it), stop and remove its service and files.
	if token == "" {
		if b, err := os.ReadFile(oldAgentTokenFile); err == nil {
			token = strings.TrimSpace(string(b))
			if token != "" {
				os.WriteFile(agentTokenFile, []byte(token+"\n"), 0o600)
			}
		}
	}
	if _, err := os.Stat(oldAgentUnit); err == nil {
		run("systemctl", "disable", "--now", oldAgentService)
		os.Remove(oldAgentUnit)
		run("systemctl", "daemon-reload")
	}
	os.Remove(oldAgentBinary)
	os.Remove(oldAgentTokenFile)
	os.Remove(filepath.Dir(oldAgentTokenFile)) // only if empty
	if token == "" {
		token = randomToken()
		if err := os.WriteFile(agentTokenFile, []byte(token+"\n"), 0o600); err != nil {
			return err
		}
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		fmt.Printf("No systemd here. Start the agent yourself (it has CAP_NET_RAW now):\n  %s -dcp-agent %s -token-file %s\n", agentBinary, *listen, agentTokenFile)
		return nil
	}
	if err := os.WriteFile(agentUnit, []byte(fmt.Sprintf(agentUnitText, agentBinary, *listen, agentTokenFile)), 0o644); err != nil {
		return err
	}
	for _, c := range [][]string{{"daemon-reload"}, {"enable", "plcmonitor-dcp"}, {"restart", "plcmonitor-dcp"}} {
		if err := run("systemctl", c...); err != nil {
			return err
		}
	}
	_, port, _ := strings.Cut(*listen, ":")
	fmt.Println("PLC Monitor DCP agent installed and running (service plcmonitor-dcp, starts at boot).")
	fmt.Println()
	fmt.Println("In PLC Monitor on Windows: Topology → PROFINET devices → Through: Linux agent")
	for _, n := range LocalNetworks() {
		fmt.Printf("  Address: http://%s:%s   (%s)\n", n.IP, port, n.Interface)
	}
	fmt.Printf("  Token:   %s\n", token)
	fmt.Println()
	fmt.Printf("If a firewall is on, allow the port, e.g.:  sudo ufw allow %s/tcp\n", port)
	fmt.Println("Status: systemctl status plcmonitor-dcp   Remove: sudo plcmonitor uninstall-agent")
	return nil
}

func uninstallAgent(args []string) error {
	if os.Geteuid() != 0 {
		return sudoReexec("uninstall-agent needs root")
	}
	run("systemctl", "disable", "--now", "plcmonitor-dcp")
	if err := os.Remove(agentUnit); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	run("systemctl", "daemon-reload")
	os.Remove(agentTokenFile)
	fmt.Printf("Agent service removed. %s itself is still there; delete it if you no longer need it.\n", agentBinary)
	return nil
}
