// Command plcmonitor polls tags from a Siemens S7 PLC (S7-300/400/1200/1500)
// over ISO-on-TCP, shows them on a dashboard, logs changes and exports
// them to CSV, MQTT and/or InfluxDB.
//
// Built normally it is a console program with a web dashboard. Built with
// -tags gui on Windows it is a standalone desktop app with its own window
// (see gui_windows.go).
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// sampleConfig is written as config.yaml on the first run. It is its own file,
// so that config.yaml in this folder can be a working config without changing
// what new installations start with.
//
//go:embed config.sample.yaml
var sampleConfig []byte

// version is set by build.ps1 (-ldflags -X main.version=…).
var version = "dev"

func main() {
	// Linux: "plcmonitor install-agent" / "uninstall-agent" set up the
	// PROFINET DCP agent service.
	if len(os.Args) > 1 && (os.Args[1] == "install-agent" || os.Args[1] == "uninstall-agent") {
		f := installAgent
		if os.Args[1] == "uninstall-agent" {
			f = uninstallAgent
		}
		if err := f(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// "plcmonitor pnio-test -ip …": PROFINET IO test from the console.
	if len(os.Args) > 1 && os.Args[1] == "pnio-test" {
		if err := runPNIOTest(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	cfgPath := flag.String("config", "", "path to the YAML config file (default: config.yaml; next to the .exe for the desktop app)")
	simulate := flag.Bool("simulate", false, "use the built-in PLC simulator (overrides config)")
	listen := flag.String("listen", "", "web dashboard address, e.g. :8080 (overrides config)")
	agentAddr := flag.String("dcp-agent", "", "run only the PROFINET DCP agent on this address, e.g. :8095 (for PLC Monitor on another PC)")
	agentToken := flag.String("token", "", "DCP agent: the token clients must send")
	agentTokenFile := flag.String("token-file", "", "DCP agent: read the token from this file")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if *agentAddr != "" {
		if err := runAgent(*agentAddr, *agentToken, *agentTokenFile); err != nil {
			log.Fatal(err)
		}
		return
	}
	path := *cfgPath
	if path == "" {
		path = defaultConfigPath()
	}
	if guiBuild {
		// No console: log to a file, and resolve relative paths in the config
		// (such as the CSV file) next to it.
		dir := filepath.Dir(path)
		os.MkdirAll(dir, 0o755)
		os.Chdir(dir)
		path = filepath.Base(path)
		logToFile("plcmonitor.log")
	}
	log.Printf("PLC Monitor %s starting", version)
	if err := ensureConfig(path); err != nil {
		fatalf("could not create %s: %v", path, err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		fatalf("config: %v", err)
	}
	if *simulate {
		cfg.Simulate = true
	}
	if *listen != "" {
		cfg.Web.Listen = *listen
	} else if guiBuild {
		cfg.Web.Listen = "" // the window has its own private server
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	exitApp = func() {
		log.Printf("exit requested from the File menu")
		stop()
	}

	// The simulator always exists so it can be switched on from the
	// Network settings dialog while the program runs.
	uaCertDir = filepath.Dir(path)
	sim := NewSimulator(cfg.Tags, cfg.Simulator)
	go sim.Run(ctx)
	dialFor := func(c *Config) Dialer {
		switch {
		case c.Simulate:
			return sim.Dialer()
		case c.Protocol == ProtoModbus:
			return ModbusDialer(c.Modbus)
		case c.Protocol == ProtoOPCUA:
			return OPCUADialer(c.OPCUA)
		}
		return S7Dialer(c.PLC)
	}
	if cfg.Simulate {
		log.Printf("running in SIMULATOR mode, no PLC traffic")
	}

	disp, err := NewExporter(cfg.Export)
	if err != nil {
		// Keep running so it can be fixed in the Export settings window.
		log.Printf("export: %v (no data is exported until this is fixed in Export settings)", err)
		disp = failedExporter(err)
	}

	mon := NewMonitor(cfg, dialFor, disp, path)
	mon.OnApply = func(c *Config) { sim.SetTags(c.Tags) }

	// Trend history files. They get their own context so they are flushed
	// after the monitor has stopped writing to them.
	trendCtx, stopTrend := context.WithCancel(context.Background())
	defer stopTrend()
	if !cfg.History.Disabled {
		if trend, err := NewTrendStore(cfg.History); err != nil {
			log.Printf("trend history off: %v", err)
		} else {
			mon.Trend = trend
			go trend.Run(trendCtx)
			abs, _ := filepath.Abs(cfg.History.Path)
			log.Printf("trend history in %s (kept %d days)", abs, cfg.History.KeepDays)
		}
	}
	if len(cfg.Tags) == 0 {
		log.Printf("no tags configured; add them on the dashboard")
	}
	done := make(chan struct{})
	go func() {
		mon.Run(ctx)
		close(done)
	}()

	handler := NewWebHandler(mon, cfg.Web.ReadOnly)
	var servers []*http.Server
	serve := func(ln net.Listener) {
		srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
		servers = append(servers, srv)
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("dashboard: %v", err)
			}
		}()
	}
	if cfg.Web.Listen != "" {
		ln, err := net.Listen("tcp", cfg.Web.Listen)
		if err != nil {
			fatalf("dashboard: %v", err)
		}
		log.Printf("dashboard on http://%s/", displayAddr(cfg.Web.Listen))
		serve(ln)
	}

	if guiBuild {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fatalf("window server: %v", err)
		}
		serve(ln)
		startAlwaysOnTop = cfg.Web.AlwaysOnTop
		// runWindow blocks on the main thread until the window is closed.
		if err := runWindow("http://"+ln.Addr().String()+"/", func() string { return windowTitle(mon) }); err != nil {
			fatalf("%v", err)
		}
		stop()
	}

	<-done
	log.Printf("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	for _, srv := range servers {
		srv.Shutdown(sctx)
	}
	cancel()
	disp.Close()
	if mon.Trend != nil {
		stopTrend()
		mon.Trend.Wait()
	}
}

func windowTitle(m *Monitor) string {
	st, _ := m.Snapshot()
	state := "disconnected"
	if st.Connected {
		state = "connected"
	} else if st.Paused {
		state = "offline"
	}
	return fmt.Sprintf("PLC Monitor - %s (%s)", st.Target, state)
}

// defaultConfigPath is config.yaml in the working directory, or next to the
// executable for the desktop app (which may be started from a shortcut).
func defaultConfigPath() string {
	if guiBuild {
		if exe, err := os.Executable(); err == nil {
			return filepath.Join(filepath.Dir(exe), "config.yaml")
		}
	}
	return "config.yaml"
}

// ensureConfig writes the sample config on first run. The simulator is off
// and no PLC address is set: the program waits, disconnected, until the
// address is entered in Network settings (or the simulator is switched on).
func ensureConfig(path string) error {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return nil
	}
	if err := os.WriteFile(path, sampleConfig, 0o644); err != nil {
		return err
	}
	log.Printf("created sample config %s (enter the PLC address in Network settings, or turn on the simulator there)", path)
	return nil
}

// logToFile sends the log to path, starting a new file when it gets large.
func logToFile(path string) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > 10<<20 {
		os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
}

// fatalf logs and exits; the desktop app also shows the message in a dialog.
func fatalf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	showError(msg)
	os.Exit(1)
}

func displayAddr(listen string) string {
	if len(listen) > 0 && listen[0] == ':' {
		return "localhost" + listen
	}
	return listen
}
