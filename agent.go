package main

// "plcmonitor -dcp-agent :8095": runs only the PROFINET DCP agent, for S7
// Monitor on another computer (normally Windows without Npcap). On Linux it
// sends the Ethernet frames itself; "plcmonitor install-agent" sets it up as
// a service that starts at boot with the raw-network permission it needs.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const agentTokenFile = "/etc/plcmonitor/agent.token"

func randomToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func runAgent(addr, token, tokenFile string) error {
	ensureRawAccess() // Linux: asks for sudo by itself when started by hand without permission
	if tokenFile != "" {
		b, err := os.ReadFile(tokenFile)
		if err != nil {
			return fmt.Errorf("token file: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		token = randomToken()
		log.Printf("no -token given; this run uses the token %s", token)
	}
	if _, err := (localDCP{}).Adapters(); err != nil {
		log.Printf("WARNING: %v", err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	log.Printf("PLC Monitor PROFINET DCP agent listening on %s", ln.Addr())
	log.Printf("In PLC Monitor: Topology → PROFINET devices → Through: Linux agent, address one of:")
	for _, n := range LocalNetworks() {
		log.Printf("    http://%s:%s   (%s)", n.IP, port, n.Interface)
	}
	srv := &http.Server{Handler: NewAgentHandler(token), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
