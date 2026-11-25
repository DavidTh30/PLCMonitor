package main

// Where PROFINET DCP runs, and its HTTP API.
//
// Two backends do the actual work:
//   - local: this computer sends the Ethernet frames itself (Linux: raw
//     socket, no extra software; Windows: needs Npcap).
//   - agent: a Linux PC on the plant network runs "plcmonitor -dcp-agent" and
//     does it; this program passes the requests on (with a token). That way
//     a Windows PC needs no driver at all.
//
// The same handlers serve the dashboard (Topology window) and the agent.

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

// DCPConfig says where PROFINET DCP runs (Topology → PROFINET devices).
type DCPConfig struct {
	Source     string `yaml:"source,omitempty" json:"source"`       // agent or local
	AgentURL   string `yaml:"agent_url,omitempty" json:"agent_url"` // e.g. http://192.168.0.50:8095
	AgentToken string `yaml:"agent_token,omitempty" json:"-"`
}

// defaultDCPSource: Linux can do DCP itself; elsewhere a Linux agent is the
// way without a driver.
func defaultDCPSource() string {
	if runtime.GOOS == "linux" {
		return "local"
	}
	return "agent"
}

func (d *DCPConfig) validate() error {
	if d.Source != "agent" && d.Source != "local" {
		return fmt.Errorf("dcp.source must be agent or local")
	}
	if d.AgentURL != "" {
		u, err := url.Parse(d.AgentURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("agent address %q: use http://host:port, e.g. http://192.168.0.50:8095", d.AgentURL)
		}
	}
	return nil
}

// dcpSetReq is a Set request: IP parameters and/or name of station.
type dcpSetReq struct {
	Adapter   string `json:"adapter"`
	MAC       string `json:"mac"`
	SetIP     bool   `json:"set_ip"`
	IP        string `json:"ip"`
	Mask      string `json:"mask"`
	Gateway   string `json:"gateway"`
	SetName   bool   `json:"set_name"`
	Name      string `json:"name"`
	Permanent bool   `json:"permanent"`
}

// build checks the request and makes its DCP blocks.
func (q dcpSetReq) build() (net.HardwareAddr, []dcpBlock, string, error) {
	mac, err := net.ParseMAC(q.MAC)
	if err != nil || len(mac) != 6 {
		return nil, nil, "", errors.New("bad MAC address")
	}
	var blocks []dcpBlock
	var what []string
	if q.SetIP {
		ip, mask, gw, err := checkIPSettings(q.IP, q.Mask, q.Gateway)
		if err != nil {
			return nil, nil, "", err
		}
		blocks = append(blocks, ipBlock(ip, mask, gw, q.Permanent))
		what = append(what, fmt.Sprintf("IP %s mask %s gateway %s", ip, mask, gw))
	}
	if q.SetName {
		if err := checkStationName(q.Name); err != nil {
			return nil, nil, "", err
		}
		blocks = append(blocks, nameBlock(q.Name, q.Permanent))
		what = append(what, fmt.Sprintf("name %q", q.Name))
	}
	if len(blocks) == 0 {
		return nil, nil, "", errors.New("nothing to change")
	}
	return mac, blocks, strings.Join(what, ", "), nil
}

type dcpBackend interface {
	Adapters() ([]DCPAdapter, error)
	Identify(adapter string, wait time.Duration) ([]DCPDevice, error)
	Set(q dcpSetReq) error
	Flash(adapter, mac string) error
	LLDP(adapter string, wait time.Duration) ([]LLDPNeighbor, error)
	Reset(adapter, mac, mode string) error
}

// localDCP sends the frames from this computer.
type localDCP struct{}

func (localDCP) Adapters() ([]DCPAdapter, error) {
	if dcpListAdapter == nil {
		return nil, errors.New(dcpUnavailable)
	}
	return dcpListAdapter()
}

func (localDCP) Identify(adapter string, wait time.Duration) ([]DCPDevice, error) {
	return dcpIdentify(adapter, wait)
}

func (localDCP) Set(q dcpSetReq) error {
	mac, blocks, _, err := q.build()
	if err != nil {
		return err
	}
	return dcpSet(q.Adapter, mac, blocks)
}

func (localDCP) Flash(adapter, mac string) error {
	m, err := net.ParseMAC(mac)
	if err != nil || len(m) != 6 {
		return errors.New("bad MAC address")
	}
	return dcpSet(adapter, m, []dcpBlock{signalBlock()})
}

func (localDCP) LLDP(adapter string, wait time.Duration) ([]LLDPNeighbor, error) {
	return lldpListen(adapter, wait)
}

func (localDCP) Reset(adapter, mac, mode string) error {
	m, err := net.ParseMAC(mac)
	if err != nil || len(m) != 6 {
		return errors.New("bad MAC address")
	}
	return dcpReset(adapter, m, mode)
}

// agentDCP passes the requests to a Linux agent (plcmonitor -dcp-agent).
type agentDCP struct {
	url, token string
	client     *http.Client
}

func newAgentDCP(c DCPConfig) (*agentDCP, error) {
	if c.AgentURL == "" {
		return nil, errors.New("no Linux agent set up yet: enter its address and token (see “How to set up the Linux agent”), or choose “This PC (Npcap)”")
	}
	return &agentDCP{url: strings.TrimRight(c.AgentURL, "/"), token: c.AgentToken, client: &http.Client{Timeout: 20 * time.Second}}, nil
}

func (a *agentDCP) call(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.url+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("Linux agent %s: %s", a.url, withHint(err, "8095 (the agent's default)"))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("the Linux agent refused the token – copy it again from the agent (sudo cat /etc/plcmonitor/agent.token)")
	}
	if resp.StatusCode/100 != 2 {
		var e apiError
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("Linux agent: %s", e.Error)
		}
		return fmt.Errorf("Linux agent: HTTP %d – is %s really an PLC Monitor agent?", resp.StatusCode, a.url)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("Linux agent: unexpected answer: %v", err)
		}
	}
	return nil
}

func (a *agentDCP) Adapters() ([]DCPAdapter, error) {
	var r struct {
		Available bool         `json:"available"`
		Error     string       `json:"error"`
		Adapters  []DCPAdapter `json:"adapters"`
	}
	if err := a.call(http.MethodGet, "/api/dcp/adapters", nil, &r); err != nil {
		return nil, err
	}
	if !r.Available {
		return nil, fmt.Errorf("Linux agent: %s", r.Error)
	}
	return r.Adapters, nil
}

func (a *agentDCP) Identify(adapter string, wait time.Duration) ([]DCPDevice, error) {
	var r struct {
		Devices []DCPDevice `json:"devices"`
	}
	err := a.call(http.MethodPost, "/api/dcp/identify", map[string]any{"adapter": adapter, "wait_ms": wait.Milliseconds()}, &r)
	return r.Devices, err
}

func (a *agentDCP) Set(q dcpSetReq) error {
	if _, _, _, err := q.build(); err != nil { // check here too, for a clear message
		return err
	}
	return a.call(http.MethodPost, "/api/dcp/set", q, nil)
}

func (a *agentDCP) Flash(adapter, mac string) error {
	return a.call(http.MethodPost, "/api/dcp/flash", map[string]string{"adapter": adapter, "mac": mac}, nil)
}

func (a *agentDCP) LLDP(adapter string, wait time.Duration) ([]LLDPNeighbor, error) {
	var r struct {
		Neighbors []LLDPNeighbor `json:"neighbors"`
	}
	err := a.call(http.MethodPost, "/api/dcp/lldp", map[string]any{"adapter": adapter, "wait_ms": wait.Milliseconds()}, &r)
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		err = errors.New("the Linux agent is older than this PLC Monitor: copy the new plcmonitor-linux file to it and run install-agent again")
	}
	return r.Neighbors, err
}

func (a *agentDCP) Reset(adapter, mac, mode string) error {
	return a.call(http.MethodPost, "/api/dcp/reset", map[string]string{"adapter": adapter, "mac": mac, "mode": mode}, nil)
}

// dcpAccess decides who may do what; backend picks where DCP runs.
type dcpAccess struct {
	backend func() (dcpBackend, error)
	query   func(http.ResponseWriter, *http.Request) bool // list, identify, flash
	change  func(http.ResponseWriter, *http.Request) bool // set IP / name
	info    func(r *http.Request, res map[string]any)     // extra fields for GET adapters
	where   string                                        // for the log
}

func dcpAPI(mux *http.ServeMux, a dcpAccess) {
	mux.HandleFunc("GET /api/dcp/adapters", func(w http.ResponseWriter, r *http.Request) {
		if !a.query(w, r) {
			return
		}
		res := map[string]any{"available": false, "adapters": []DCPAdapter{}}
		if a.info != nil {
			a.info(r, res)
		}
		b, err := a.backend()
		if err == nil {
			var list []DCPAdapter
			if list, err = b.Adapters(); err == nil {
				res["available"], res["adapters"] = true, list
			}
		}
		if err != nil {
			res["error"] = err.Error()
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("POST /api/dcp/identify", func(w http.ResponseWriter, r *http.Request) {
		if !a.query(w, r) {
			return
		}
		var body struct {
			Adapter string `json:"adapter"`
			WaitMs  int    `json:"wait_ms"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		wait := time.Duration(body.WaitMs) * time.Millisecond
		if wait < time.Second || wait > 10*time.Second {
			wait = 3 * time.Second
		}
		b, err := a.backend()
		var devs []DCPDevice
		if err == nil {
			devs, err = b.Identify(body.Adapter, wait)
		}
		if err != nil {
			respond(w, err)
			return
		}
		if devs == nil {
			devs = []DCPDevice{}
		}
		writeJSON(w, http.StatusOK, struct {
			Devices []DCPDevice `json:"devices"`
		}{devs})
	})
	mux.HandleFunc("POST /api/dcp/set", func(w http.ResponseWriter, r *http.Request) {
		if !a.change(w, r) {
			return
		}
		var q dcpSetReq
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		mac, _, what, err := q.build()
		if err != nil {
			respond(w, err)
			return
		}
		b, err := a.backend()
		if err == nil {
			err = b.Set(q)
		}
		result := "done"
		if err != nil {
			result = "FAILED: " + err.Error()
		}
		log.Printf("DCP SET %s via %s: %s (permanent=%v, from %s) – %s", mac, a.where, what, q.Permanent, r.RemoteAddr, result)
		respond(w, err)
	})
	mux.HandleFunc("POST /api/dcp/flash", func(w http.ResponseWriter, r *http.Request) {
		if !a.query(w, r) {
			return
		}
		var body struct {
			Adapter string `json:"adapter"`
			MAC     string `json:"mac"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		b, err := a.backend()
		if err == nil {
			err = b.Flash(body.Adapter, body.MAC)
		}
		respond(w, err)
	})
	// LLDP: what the device or switch at the other end of the cable announces.
	mux.HandleFunc("POST /api/dcp/lldp", func(w http.ResponseWriter, r *http.Request) {
		if !a.query(w, r) {
			return
		}
		var body struct {
			Adapter string `json:"adapter"`
			WaitMs  int    `json:"wait_ms"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		wait := time.Duration(body.WaitMs) * time.Millisecond
		if wait < time.Second || wait > 35*time.Second {
			wait = 6 * time.Second
		}
		b, err := a.backend()
		var list []LLDPNeighbor
		if err == nil {
			list, err = b.LLDP(body.Adapter, wait)
		}
		if err != nil {
			respond(w, err)
			return
		}
		if list == nil {
			list = []LLDPNeighbor{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"neighbors": list})
	})
	// Reset to factory settings: a change, like Set.
	mux.HandleFunc("POST /api/dcp/reset", func(w http.ResponseWriter, r *http.Request) {
		if !a.change(w, r) {
			return
		}
		var body struct {
			Adapter string `json:"adapter"`
			MAC     string `json:"mac"`
			Mode    string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		if body.Mode == "" {
			body.Mode = "communication"
		}
		b, err := a.backend()
		if err == nil {
			err = b.Reset(body.Adapter, body.MAC, body.Mode)
		}
		result := "done"
		if err != nil {
			result = "FAILED: " + err.Error()
		}
		log.Printf("DCP RESET %s via %s: mode %s (from %s) – %s", body.MAC, a.where, body.Mode, r.RemoteAddr, result)
		respond(w, err)
	})
}

// dcpRoutes adds the PROFINET API to the dashboard. It sends frames onto the
// plant network (directly or through the agent), so only from this computer,
// and changes not on a read-only dashboard.
func dcpRoutes(mux *http.ServeMux, m *Monitor, local func(http.ResponseWriter, *http.Request) bool, readOnly bool, onlyHere func(http.ResponseWriter, *http.Request) bool) {
	backend := func() (dcpBackend, error) {
		c := m.Config().DCP
		if c.Source == "agent" {
			return newAgentDCP(c)
		}
		return localDCP{}, nil
	}
	query := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodGet && !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"PROFINET device functions only work on the computer running PLC Monitor"})
			return false
		}
		return r.Method == http.MethodGet || onlyHere(w, r)
	}
	dcpAPI(mux, dcpAccess{backend: backend, query: query, change: local, where: "dashboard",
		info: func(r *http.Request, res map[string]any) {
			c := m.Config().DCP
			res["source"], res["agent_url"], res["has_token"] = c.Source, c.AgentURL, c.AgentToken != ""
			res["can_set"] = !readOnly && isLoopback(r)
			res["local_available"] = dcpListAdapter != nil
			res["local_error"] = map[bool]string{true: "", false: dcpUnavailable}[dcpListAdapter != nil]
			res["npcap_check"] = dcpRecheck != nil
		}})
	// Settings: where DCP runs, the agent's address and token.
	mux.HandleFunc("PUT /api/dcp/settings", func(w http.ResponseWriter, r *http.Request) {
		if !local(w, r) {
			return
		}
		var body struct {
			Source     string  `json:"source"`
			AgentURL   *string `json:"agent_url"`
			AgentToken *string `json:"agent_token"` // nil keeps the saved token
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		respond(w, m.Update(func(c *Config) error {
			if body.Source != "" {
				c.DCP.Source = body.Source
			}
			if body.AgentURL != nil {
				u := strings.TrimRight(strings.TrimSpace(*body.AgentURL), "/")
				if u != "" && !strings.Contains(u, "://") {
					u = "http://" + u
				}
				if pu, err := url.Parse(u); err == nil && u != "" && pu.Port() == "" {
					u += ":8095"
				}
				c.DCP.AgentURL = u
			}
			if body.AgentToken != nil {
				c.DCP.AgentToken = strings.TrimSpace(*body.AgentToken)
			}
			return c.DCP.validate()
		}))
	})
	// Npcap was installed while PLC Monitor runs: try again.
	mux.HandleFunc("POST /api/dcp/npcap/check", func(w http.ResponseWriter, r *http.Request) {
		if !onlyHere(w, r) {
			return
		}
		if dcpRecheck == nil {
			respond(w, errors.New("not needed on this system"))
			return
		}
		respond(w, dcpRecheck())
	})
	// Open the Npcap download page in the default browser.
	mux.HandleFunc("POST /api/dcp/npcap/download", func(w http.ResponseWriter, r *http.Request) {
		if onlyHere(w, r) {
			respond(w, openURL("https://npcap.com/#download"))
		}
	})
}

// dcpRecheck tries to load Npcap again (Windows); nil elsewhere.
var dcpRecheck func() error

// ---- agent ----

// NewAgentHandler serves the DCP API for other computers (plcmonitor
// -dcp-agent). Every request needs the token, also from the agent's own
// computer: on a shared Linux PC other users must not reconfigure devices.
func NewAgentHandler(token string) http.Handler {
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, apiError{"wrong or missing token"})
			return false
		}
		if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, apiError{"Content-Type must be application/json"})
			return false
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		return true
	}
	dcpAPI(mux, dcpAccess{backend: func() (dcpBackend, error) { return localDCP{}, nil }, query: auth, change: auth, where: "agent",
		info: func(r *http.Request, res map[string]any) { res["agent"] = true }})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		status := "ready: raw Ethernet access works"
		if _, err := (localDCP{}).Adapters(); err != nil {
			status = "NOT ready: " + err.Error()
		}
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>PLC Monitor DCP agent</title>
<body style="font:15px system-ui;max-width:640px;margin:40px auto;line-height:1.5">
<h2>PLC Monitor – PROFINET DCP agent</h2><p>Status: <b>%s</b></p>
<p>Use it from PLC Monitor on Windows: <b>Topology → PROFINET devices → Through: Linux agent</b>,
with this address and the token from <code>/etc/plcmonitor/agent.token</code>.</p></body>`, htmlEscape(status))
	})
	return mux
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
