package main

// OPC UA: the third kind of connection, and the PLC tags browser.
//
// S7-1200 (firmware 4.4+) and S7-1500 PLCs have an OPC UA server that
// publishes the PLC's variables by their symbolic names: PLC tags (inputs,
// outputs, markers) and data blocks, optimized ones included. S7 PUT/GET
// only carries bytes, so this is how names get from the PLC to PLC Monitor
// without exporting files from TIA Portal.
//
// A tag read over OPC UA has area UA and a node ID such as
//
//	ns=3;s="Line_Data"."Motor1"."Speed"
//
// Inside the monitor every area is a byte image, so each UA tag gets an
// 8-byte slot in a UA image (Tag.uaOff, given out by Config.validate). A read
// fetches the values of a block's nodes in one OPC UA Read request and
// encodes them into the slots; from there the normal decode path is used.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"
)

// OPCUAConfig is used when protocol is opcua, and by the PLC tags browser.
type OPCUAConfig struct {
	Endpoint string        `yaml:"endpoint" json:"endpoint"` // opc.tcp://192.168.0.1:4840
	Security string        `yaml:"security" json:"security"` // none, sign or signandencrypt
	Policy   string        `yaml:"policy" json:"policy"`     // Basic256Sha256 (default with security), Aes128_Sha256_RsaOaep, Aes256_Sha256_RsaPss
	Username string        `yaml:"username,omitempty" json:"username"`
	Password string        `yaml:"password,omitempty" json:"password,omitempty"`
	Timeout  time.Duration `yaml:"timeout" json:"-"`
}

var uaPolicies = map[string]string{
	"basic256sha256": "Basic256Sha256", "aes128_sha256_rsaoaep": "Aes128_Sha256_RsaOaep",
	"aes256_sha256_rsapss": "Aes256_Sha256_RsaPss", "basic256": "Basic256", "basic128rsa15": "Basic128Rsa15",
}

func (o *OPCUAConfig) applyDefaults() {
	o.Endpoint = strings.TrimSpace(o.Endpoint)
	o.Security = strings.ToLower(strings.TrimSpace(o.Security))
	if o.Security == "" {
		o.Security = "none"
	}
	if p, ok := uaPolicies[strings.ToLower(strings.TrimSpace(o.Policy))]; ok {
		o.Policy = p
	}
	if o.Security != "none" && o.Policy == "" {
		o.Policy = "Basic256Sha256"
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Second
	}
}

func (o *OPCUAConfig) validate() error {
	if o.Endpoint != "" {
		u, err := url.Parse(o.Endpoint)
		if err != nil || u.Scheme != "opc.tcp" || u.Host == "" {
			return fmt.Errorf("OPC UA endpoint must look like opc.tcp://192.168.0.1:4840")
		}
	}
	switch o.Security {
	case "none", "sign", "signandencrypt":
	default:
		return fmt.Errorf("OPC UA security must be none, sign or signandencrypt")
	}
	if o.Security != "none" {
		ok := false
		for _, p := range uaPolicies {
			ok = ok || p == o.Policy
		}
		if !ok {
			return fmt.Errorf("OPC UA security policy %q is not supported", o.Policy)
		}
	}
	return nil
}

// uaEndpointFor gives the endpoint to use: the configured one, or port 4840
// on the S7 PLC's address.
func uaEndpointFor(c *Config) string {
	if c.OPCUA.Endpoint != "" {
		return c.OPCUA.Endpoint
	}
	host := strings.TrimSpace(c.PLC.Address)
	if host == "" {
		return ""
	}
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	return "opc.tcp://" + host + ":4840"
}

// uaCertDir is where the client certificate for signed/encrypted
// connections is kept (next to the config; set in main).
var uaCertDir = "."

// uaClientCert loads or creates the self-signed client certificate. The PLC
// must trust it (TIA Portal: CPU properties → OPC UA → Server → Security →
// Trusted clients, or "Automatically accept client certificates during
// runtime").
func uaClientCert() (certDER []byte, key *rsa.PrivateKey, err error) {
	certPath := filepath.Join(uaCertDir, "opcua-client-cert.pem")
	keyPath := filepath.Join(uaCertDir, "opcua-client-key.pem")
	if pair, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		if k, ok := pair.PrivateKey.(*rsa.PrivateKey); ok {
			return pair.Certificate[0], k, nil
		}
	}
	key, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	host, _ := os.Hostname()
	uri, _ := url.Parse(uaAppURI)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "PLC Monitor", Organization: []string{"PLC Monitor"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment | x509.KeyUsageKeyEncipherment |
			x509.KeyUsageDataEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{host},
		URIs:                  []*url.URL{uri},
	}
	certDER, err = x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), 0o644)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600)
	return certDER, key, nil
}

const uaAppURI = "urn:plcmonitor:client"

// uaConnect opens an OPC UA session.
func uaConnect(ctx context.Context, o OPCUAConfig) (*opcua.Client, error) {
	o.applyDefaults()
	if o.Endpoint == "" {
		return nil, errors.New("no OPC UA endpoint set – enter it in Network settings (e.g. opc.tcp://192.168.0.1:4840)")
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout+5*time.Second)
	defer cancel()
	eps, err := opcua.GetEndpoints(ctx, o.Endpoint, opcua.DialTimeout(o.Timeout))
	if err != nil {
		return nil, fmt.Errorf("OPC UA %s: %w (is the OPC UA server switched on in the CPU properties?)", o.Endpoint, err)
	}
	policy, mode := "None", ua.MessageSecurityModeNone
	switch o.Security {
	case "sign":
		policy, mode = o.Policy, ua.MessageSecurityModeSign
	case "signandencrypt":
		policy, mode = o.Policy, ua.MessageSecurityModeSignAndEncrypt
	}
	ep, err := opcua.SelectEndpoint(eps, policy, mode)
	if err != nil || ep == nil {
		var have []string
		for _, e := range eps {
			have = append(have, fmt.Sprintf("%s/%s", strings.TrimPrefix(e.SecurityPolicyURI, "http://opcfoundation.org/UA/SecurityPolicy#"), e.SecurityMode))
		}
		return nil, fmt.Errorf("the server has no endpoint with security %s/%s (it offers: %s)", policy, o.Security, strings.Join(have, ", "))
	}
	// The server may give its own host name in the endpoint URL, which
	// this PC may not resolve: keep the address that was entered.
	ep.EndpointURL = o.Endpoint
	auth := ua.UserTokenTypeAnonymous
	opts := []opcua.Option{
		opcua.ApplicationURI(uaAppURI), opcua.ApplicationName("PLC Monitor"),
		opcua.RequestTimeout(o.Timeout), opcua.DialTimeout(o.Timeout), opcua.AutoReconnect(false),
	}
	if o.Username != "" {
		auth = ua.UserTokenTypeUserName
		opts = append(opts, opcua.AuthUsername(o.Username, o.Password))
	} else {
		opts = append(opts, opcua.AuthAnonymous())
	}
	if mode != ua.MessageSecurityModeNone || o.Username != "" {
		cert, key, err := uaClientCert()
		if err != nil {
			return nil, fmt.Errorf("client certificate: %w", err)
		}
		// The application URI must be the certificate's: a certificate made
		// by an earlier version (which the PLC may already trust) has its own.
		if c, err := x509.ParseCertificate(cert); err == nil && len(c.URIs) > 0 {
			opts = append(opts, opcua.ApplicationURI(c.URIs[0].String()))
		}
		opts = append(opts, opcua.Certificate(cert), opcua.PrivateKey(key))
	}
	opts = append(opts, opcua.SecurityFromEndpoint(ep, auth))
	c, err := opcua.NewClient(o.Endpoint, opts...)
	if err != nil {
		return nil, err
	}
	if err := c.Connect(ctx); err != nil {
		hint := ""
		if mode != ua.MessageSecurityModeNone {
			hint = " (the PLC must trust PLC Monitor's certificate: opcua-client-cert.pem next to the config)"
		}
		return nil, fmt.Errorf("OPC UA connect %s: %w%s", o.Endpoint, err, hint)
	}
	return c, nil
}

// ---- reading and writing tags ----

type uaPLC struct {
	c       *opcua.Client
	timeout time.Duration
	ids     map[*Tag]*ua.NodeID
}

// OPCUADialer returns a Dialer for an OPC UA server.
func OPCUADialer(o OPCUAConfig) Dialer {
	return func() (PLC, error) {
		o.applyDefaults()
		c, err := uaConnect(context.Background(), o)
		if err != nil {
			return nil, err
		}
		return &uaPLC{c: c, timeout: o.Timeout, ids: map[*Tag]*ua.NodeID{}}, nil
	}
}

func (p *uaPLC) nodeID(t *Tag) (*ua.NodeID, error) {
	if n, ok := p.ids[t]; ok {
		return n, nil
	}
	n, err := ua.ParseNodeID(t.Node)
	if err != nil {
		return nil, err
	}
	p.ids[t] = n
	return n, nil
}

func (p *uaPLC) ReadBlock(b *Block, buf []byte) error {
	req := &ua.ReadRequest{TimestampsToReturn: ua.TimestampsToReturnNeither}
	for _, t := range b.Tags {
		n, err := p.nodeID(t)
		if err != nil {
			return &blockError{err: fmt.Errorf("%s: bad node ID %q", t.Name, t.Node)}
		}
		req.NodesToRead = append(req.NodesToRead, &ua.ReadValueID{NodeID: n, AttributeID: ua.AttributeIDValue})
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	resp, err := p.c.Read(ctx, req)
	if err != nil {
		return fmt.Errorf("OPC UA read: %w", err)
	}
	if len(resp.Results) != len(b.Tags) {
		return fmt.Errorf("OPC UA read: %d results for %d nodes", len(resp.Results), len(b.Tags))
	}
	b.TagErrs = make([]error, len(b.Tags))
	for i, t := range b.Tags {
		r := resp.Results[i]
		if r.Status != ua.StatusOK {
			b.TagErrs[i] = fmt.Errorf("%s: %v", t.Node, r.Status)
			continue
		}
		v, err := uaToTagValue(r.Value, t)
		if err != nil {
			b.TagErrs[i] = err
			continue
		}
		start, end := t.imageRange()
		tmp := make([]byte, end)
		t.Encode(tmp, v)
		copy(buf[start-b.Start:end-b.Start], tmp[start:end])
	}
	return nil
}

// uaToTagValue converts a node's value to what the tag holds: bool, int64
// or float64.
func uaToTagValue(v *ua.Variant, t *Tag) (any, error) {
	if v == nil {
		return nil, fmt.Errorf("%s: no value", t.Node)
	}
	var f float64
	isInt := true
	switch x := v.Value().(type) {
	case bool:
		if t.dtype == TypeBool {
			return x, nil
		}
		if x {
			f = 1
		}
	case int8:
		f = float64(x)
	case uint8:
		f = float64(x)
	case int16:
		f = float64(x)
	case uint16:
		f = float64(x)
	case int32:
		f = float64(x)
	case uint32:
		f = float64(x)
	case int64:
		f = float64(x)
	case uint64:
		f = float64(x)
	case float32:
		f, isInt = float64(x), false
	case float64:
		f, isInt = x, false
	default:
		return nil, fmt.Errorf("%s: a %T value can't be shown as %s", t.Node, v.Value(), t.Type)
	}
	switch {
	case t.dtype == TypeBool:
		return f != 0, nil
	case t.dtype.IsFloat():
		return f, nil
	case !isInt:
		return int64(math.Round(f)), nil
	}
	return int64(f), nil
}

// uaVariantFor makes the value to write, with the OPC UA type the PLC
// expects for the tag's type.
func uaVariantFor(t *Tag, v any) (*ua.Variant, error) {
	var x any
	switch t.Type {
	case "BOOL":
		x = v.(bool)
	case "BYTE", "USINT":
		x = uint8(toInt(v))
	case "SINT":
		x = int8(toInt(v))
	case "INT":
		x = int16(toInt(v))
	case "WORD", "UINT":
		x = uint16(toInt(v))
	case "DINT":
		x = int32(toInt(v))
	case "DWORD", "UDINT":
		x = uint32(toInt(v))
	case "REAL":
		x = float32(toFloat(v))
	case "LREAL":
		x = toFloat(v)
	default:
		return nil, fmt.Errorf("type %s can't be written", t.Type)
	}
	return ua.NewVariant(x)
}

func (p *uaPLC) WriteTag(t *Tag, v any) error {
	n, err := p.nodeID(t)
	if err != nil {
		return err
	}
	val, err := uaVariantFor(t, v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	resp, err := p.c.Write(ctx, &ua.WriteRequest{NodesToWrite: []*ua.WriteValue{{
		NodeID: n, AttributeID: ua.AttributeIDValue,
		Value: &ua.DataValue{EncodingMask: ua.DataValueValue, Value: val},
	}}})
	if err != nil {
		return fmt.Errorf("write %s: %w", t.Name, err)
	}
	if len(resp.Results) == 1 && resp.Results[0] != ua.StatusOK {
		return fmt.Errorf("write %s: %v", t.Name, resp.Results[0])
	}
	return nil
}

func (p *uaPLC) Close() error { return p.c.Close(context.Background()) }

// ---- browsing ----

// UAVar is one variable found by browsing.
type UAVar struct {
	Node     string `json:"node"`
	Name     string `json:"name"`   // symbolic name, e.g. Line_Data.Motor1.Speed or Start_PB
	Folder   string `json:"folder"` // I, Q, M, DB, iDB, T, C, or "" (other)
	DataType string `json:"data_type"`
	Type     string `json:"type,omitempty"` // tag type; "" if it can't be a tag (struct, array, string, …)
	Comment  string `json:"comment,omitempty"`
	Writable bool   `json:"writable"`
	Array    bool   `json:"array,omitempty"`
	Struct   bool   `json:"struct,omitempty"` // has members (listed separately)
	S7       *S7Ref `json:"s7,omitempty"`     // S7 address, when known from imported names (Scan PLC)
}

// siemensFolders are the folders of a SIMATIC PLC's OPC UA address space.
var siemensFolders = map[string]string{
	"Inputs": "I", "Outputs": "Q", "Memory": "M", "DataBlocksGlobal": "DB",
	"DataBlocksInstance": "iDB", "Timers": "T", "Counters": "C",
}

// uaBuiltinTypes maps the OPC UA built-in data types (namespace 0).
var uaBuiltinTypes = map[uint32][2]string{
	id.Boolean: {"Boolean", "BOOL"}, id.SByte: {"SByte", "SINT"}, id.Byte: {"Byte", "BYTE"},
	id.Int16: {"Int16", "INT"}, id.UInt16: {"UInt16", "UINT"}, id.Int32: {"Int32", "DINT"},
	id.UInt32: {"UInt32", "UDINT"}, id.Int64: {"Int64", ""}, id.UInt64: {"UInt64", ""},
	id.Float: {"Float", "REAL"}, id.Double: {"Double", "LREAL"}, id.String: {"String", ""},
	id.DateTime: {"DateTime", ""}, id.ByteString: {"ByteString", ""}, id.LocalizedText: {"LocalizedText", ""},
}

const maxBrowseVars = 50000

type uaFound struct {
	id     *ua.NodeID
	path   []string
	folder string
}

// browseVars walks the address space below Objects (namespace 0 nodes,
// such as the Server object, are skipped) and returns its variables.
func browseVars(ctx context.Context, c *opcua.Client, progress func(n int)) ([]UAVar, error) {
	objects := ua.NewNumericNodeID(0, id.ObjectsFolder)
	queue := []uaFound{{id: objects}}
	seen := map[string]bool{objects.String(): true}
	var found []uaFound
	parentVar := map[string]bool{} // variables that have member variables
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch := queue[:min(len(queue), 50)]
		queue = queue[len(batch):]
		req := &ua.BrowseRequest{RequestedMaxReferencesPerNode: 1000}
		for _, b := range batch {
			req.NodesToBrowse = append(req.NodesToBrowse, &ua.BrowseDescription{
				NodeID: b.id, BrowseDirection: ua.BrowseDirectionForward,
				ReferenceTypeID: ua.NewNumericNodeID(0, id.HierarchicalReferences), IncludeSubtypes: true,
				NodeClassMask: uint32(ua.NodeClassObject | ua.NodeClassVariable), ResultMask: uint32(ua.BrowseResultMaskAll),
			})
		}
		resp, err := c.Browse(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("browse: %w", err)
		}
		for i, res := range resp.Results {
			if i >= len(batch) {
				break
			}
			refs := res.References
			cp := res.ContinuationPoint
			for len(cp) > 0 {
				nr, err := c.BrowseNext(ctx, &ua.BrowseNextRequest{ContinuationPoints: [][]byte{cp}})
				if err != nil || len(nr.Results) == 0 {
					break
				}
				refs = append(refs, nr.Results[0].References...)
				cp = nr.Results[0].ContinuationPoint
			}
			parent := batch[i]
			for _, r := range refs {
				if r.NodeID == nil || r.NodeID.NodeID == nil || r.NodeID.NodeID.Namespace() == 0 {
					continue
				}
				if r.TypeDefinition != nil && r.TypeDefinition.NodeID != nil && r.TypeDefinition.NodeID.IntID() == id.PropertyType &&
					r.TypeDefinition.NodeID.Namespace() == 0 {
					continue // properties (EURange, …) are not values to monitor
				}
				nid := r.NodeID.NodeID
				key := nid.String()
				if seen[key] {
					continue
				}
				seen[key] = true
				name := ""
				if r.BrowseName != nil {
					name = r.BrowseName.Name
				}
				f := uaFound{id: nid, path: append(append([]string{}, parent.path...), name), folder: parent.folder}
				if f.folder == "" {
					if fl, ok := siemensFolders[name]; ok {
						f.folder, f.path = fl, nil // names start below the folder
					}
				}
				if r.NodeClass == ua.NodeClassVariable {
					found = append(found, f)
					if parent.id != objects && len(parent.path) > 0 {
						parentVar[parent.id.String()] = true
					}
					if len(found) >= maxBrowseVars {
						return nil, fmt.Errorf("more than %d variables; stopped", maxBrowseVars)
					}
				}
				if len(f.path) < 20 {
					queue = append(queue, f)
				}
			}
		}
		if progress != nil {
			progress(len(found))
		}
	}

	// Attributes of all variables, 100 at a time.
	vars := make([]UAVar, len(found))
	typeIDs := map[string]*ua.NodeID{}
	dtOf := make([]*ua.NodeID, len(found))
	attrs := []ua.AttributeID{ua.AttributeIDDataType, ua.AttributeIDDescription, ua.AttributeIDAccessLevel, ua.AttributeIDValueRank}
	for i := 0; i < len(found); i += 100 {
		part := found[i:min(i+100, len(found))]
		req := &ua.ReadRequest{TimestampsToReturn: ua.TimestampsToReturnNeither}
		for _, f := range part {
			for _, a := range attrs {
				req.NodesToRead = append(req.NodesToRead, &ua.ReadValueID{NodeID: f.id, AttributeID: a})
			}
		}
		resp, err := c.Read(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("read attributes: %w", err)
		}
		for j, f := range part {
			v := UAVar{Node: f.id.String(), Folder: f.folder, Name: uaSymbolName(f)}
			res := func(k int) *ua.DataValue {
				if n := j*len(attrs) + k; n < len(resp.Results) && resp.Results[n].Status == ua.StatusOK {
					return resp.Results[n]
				}
				return nil
			}
			if r := res(0); r != nil && r.Value != nil {
				if dt, ok := r.Value.Value().(*ua.NodeID); ok {
					dtOf[i+j] = dt
					if dt.Namespace() == 0 {
						if b, ok := uaBuiltinTypes[dt.IntID()]; ok {
							v.DataType, v.Type = b[0], b[1]
						}
					}
					if v.DataType == "" {
						typeIDs[dt.String()] = dt
					}
				}
			}
			if r := res(1); r != nil && r.Value != nil {
				if lt, ok := r.Value.Value().(*ua.LocalizedText); ok && lt != nil {
					v.Comment = lt.Text
				}
			}
			if r := res(2); r != nil && r.Value != nil {
				if al, ok := r.Value.Value().(uint8); ok {
					v.Writable = ua.AccessLevelType(al)&ua.AccessLevelTypeCurrentWrite != 0
				}
			}
			if r := res(3); r != nil && r.Value != nil {
				if vr, ok := r.Value.Value().(int32); ok && vr >= 0 {
					v.Array = true
				}
			}
			v.Struct = parentVar[v.Node]
			vars[i+j] = v
		}
	}

	// Names of the other data types (structures, Siemens types).
	if len(typeIDs) > 0 {
		names := map[string]string{}
		var ids []*ua.NodeID
		for _, t := range typeIDs {
			ids = append(ids, t)
		}
		for i := 0; i < len(ids); i += 200 {
			part := ids[i:min(i+200, len(ids))]
			req := &ua.ReadRequest{}
			for _, t := range part {
				req.NodesToRead = append(req.NodesToRead, &ua.ReadValueID{NodeID: t, AttributeID: ua.AttributeIDBrowseName})
			}
			resp, err := c.Read(ctx, req)
			if err != nil {
				break
			}
			for j, r := range resp.Results {
				if j < len(part) && r.Status == ua.StatusOK && r.Value != nil {
					if q, ok := r.Value.Value().(*ua.QualifiedName); ok && q != nil {
						names[part[j].String()] = q.Name
					}
				}
			}
		}
		for i := range vars {
			if vars[i].DataType == "" && dtOf[i] != nil {
				n := names[dtOf[i].String()]
				if n == "" {
					n = dtOf[i].String()
				}
				vars[i].DataType = n
				vars[i].Type = tagTypeOf[strings.ToUpper(n)]
			}
		}
	}
	for i := range vars {
		if vars[i].Array || vars[i].Struct {
			vars[i].Type = ""
		}
	}
	return vars, nil
}

// uaSymbolName is the variable's symbolic name. Siemens node IDs are the
// symbol itself ("Line_Data"."Motor1"."Speed"); otherwise the browse path.
func uaSymbolName(f uaFound) string {
	if f.folder != "" && f.id.Type() == ua.NodeIDTypeString {
		s := f.id.StringID()
		if strings.Contains(s, `"`) {
			return strings.ReplaceAll(strings.ReplaceAll(s, `"."`, "."), `"`, "")
		}
	}
	return strings.Join(f.path, ".")
}

// identifyUA asks an OPC UA server (opc.tcp://ip:4840 in a scan) for its endpoints and, when
// guest access without security is allowed, for the device's identity
// (OPC UA Devices: Objects/DeviceSet/<device>) and whether it publishes the
// PLC's tags. Reading only.
func identifyUA(ctx context.Context, endpoint string, timeout time.Duration) *UAIdent {
	id := &UAIdent{Endpoint: endpoint}
	t := max(timeout*4, 2*time.Second)
	ctx, cancel := context.WithTimeout(ctx, 3*t)
	defer cancel()
	eps, err := opcua.GetEndpoints(ctx, id.Endpoint, opcua.DialTimeout(t))
	if err != nil {
		id.Error = "port 4840 open, but no OPC UA answer: " + err.Error()
		return id
	}
	plain := false
	seen := map[string]bool{}
	for _, e := range eps {
		pol := strings.TrimPrefix(e.SecurityPolicyURI, "http://opcfoundation.org/UA/SecurityPolicy#")
		s := "None"
		switch e.SecurityMode {
		case ua.MessageSecurityModeSign:
			s = pol + " sign"
		case ua.MessageSecurityModeSignAndEncrypt:
			s = pol + " sign & encrypt"
		}
		if !seen[s] {
			seen[s] = true
			id.Security = append(id.Security, s)
		}
		for _, tk := range e.UserIdentityTokens {
			if tk.TokenType == ua.UserTokenTypeAnonymous {
				id.Anonymous = true
				if e.SecurityMode == ua.MessageSecurityModeNone {
					plain = true
				}
			}
		}
	}
	if !plain {
		return id // identity and tags need a secure or a user session: not tried here
	}
	c, err := uaConnect(ctx, OPCUAConfig{Endpoint: id.Endpoint, Timeout: t})
	if err != nil {
		id.Error = err.Error()
		if strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "forcibly closed") {
			id.Error = "the server closed the connection without security – it probably accepts only known clients " +
				"(a trusted certificate or a user name), not a guest scan; endpoints and security above are what it offers"
		}
		return id
	}
	defer c.Close(context.Background())
	// The server's clock (a PLC's own clock, as UTC).
	if resp, err := c.Read(ctx, &ua.ReadRequest{NodesToRead: []*ua.ReadValueID{
		{NodeID: ua.NewNumericNodeID(0, uaCurrentTime), AttributeID: ua.AttributeIDValue}}}); err == nil &&
		len(resp.Results) == 1 && resp.Results[0].Status == ua.StatusOK && resp.Results[0].Value != nil {
		if t, ok := resp.Results[0].Value.Value().(time.Time); ok && !t.IsZero() {
			id.Clock = clockOf(t, true)
		}
	}
	children := func(n *ua.NodeID) []*ua.ReferenceDescription {
		resp, err := c.Browse(ctx, &ua.BrowseRequest{RequestedMaxReferencesPerNode: 200, NodesToBrowse: []*ua.BrowseDescription{{
			NodeID: n, BrowseDirection: ua.BrowseDirectionForward,
			ReferenceTypeID: ua.NewNumericNodeID(0, uaHierRefs), IncludeSubtypes: true,
			NodeClassMask: uint32(ua.NodeClassObject | ua.NodeClassVariable), ResultMask: uint32(ua.BrowseResultMaskAll)}}})
		if err != nil || len(resp.Results) == 0 {
			return nil
		}
		return resp.Results[0].References
	}
	name := func(r *ua.ReferenceDescription) string {
		if r.BrowseName != nil {
			return r.BrowseName.Name
		}
		return ""
	}
	var devices []*ua.ReferenceDescription
	for _, r := range children(ua.NewNumericNodeID(0, uaObjectsFolder)) {
		if name(r) == "DeviceSet" && r.NodeID != nil {
			devices = children(r.NodeID.NodeID)
		}
	}
	for _, d := range devices {
		if d.NodeClass != ua.NodeClassObject || d.NodeID == nil {
			continue
		}
		want := map[string]*string{"Model": &id.Model, "OrderNumber": &id.Order, "SoftwareRevision": &id.Firmware, "SerialNumber": &id.Serial}
		req := &ua.ReadRequest{}
		var dst []*string
		folders := false
		kids := children(d.NodeID.NodeID)
		relevant := false
		for _, r := range kids {
			if want[name(r)] != nil && r.NodeClass == ua.NodeClassVariable || siemensFolders[name(r)] != "" && r.NodeClass == ua.NodeClassObject {
				relevant = true
			}
		}
		if !relevant {
			continue // not the PLC's device object
		}
		id.Device = name(d)
		if d.DisplayName != nil && d.DisplayName.Text != "" {
			id.Device = d.DisplayName.Text
		}
		for _, r := range kids {
			if p := want[name(r)]; p != nil && r.NodeClass == ua.NodeClassVariable {
				req.NodesToRead = append(req.NodesToRead, &ua.ReadValueID{NodeID: r.NodeID.NodeID, AttributeID: ua.AttributeIDValue})
				dst = append(dst, p)
			}
			if _, ok := siemensFolders[name(r)]; ok && r.NodeClass == ua.NodeClassObject {
				folders = true
			}
		}
		if len(req.NodesToRead) > 0 {
			if resp, err := c.Read(ctx, req); err == nil {
				for i, res := range resp.Results {
					if i < len(dst) && res.Status == ua.StatusOK && res.Value != nil {
						v := res.Value.Value()
						if lt, ok := v.(*ua.LocalizedText); ok && lt != nil {
							v = lt.Text
						}
						*dst[i] = strings.TrimSpace(fmt.Sprint(v))
					}
				}
			}
		}
		if folders {
			id.Tags = "yes"
		} else if uaBrowseHint(ctx, c, nil) == uaHintSimatic {
			id.Tags = "no"
		}
		break // the first device: the PLC itself
	}
	return id
}

// Node IDs used where a variable named id hides the package.
const (
	uaHierRefs      = id.HierarchicalReferences
	uaObjectsFolder = id.ObjectsFolder
	uaCurrentTime   = id.Server_ServerStatus_CurrentTime
)

// UABrowser browses a PLC and keeps the session open for live values.
type UABrowser struct {
	mu       sync.Mutex
	st       UABrowseState
	client   *opcua.Client
	lastUsed time.Time
	cancel   context.CancelFunc
}

type UABrowseState struct {
	Running  bool      `json:"running"`
	Count    int       `json:"count"` // variables found so far
	Endpoint string    `json:"endpoint"`
	Error    string    `json:"error,omitempty"`
	Hint     string    `json:"hint,omitempty"` // why no tags were found, when it can be told
	Time     time.Time `json:"time"`           // when the last browse finished
	Vars     []UAVar   `json:"vars"`
}

func NewUABrowser() *UABrowser {
	b := &UABrowser{st: UABrowseState{Vars: []UAVar{}}}
	go func() { // close an idle session
		for range time.Tick(30 * time.Second) {
			b.mu.Lock()
			if b.client != nil && !b.st.Running && time.Since(b.lastUsed) > 2*time.Minute {
				b.client.Close(context.Background())
				b.client = nil
			}
			b.mu.Unlock()
		}
	}()
	return b
}

func (b *UABrowser) State() UABrowseState {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.st
	st.Vars = append([]UAVar{}, b.st.Vars...)
	return st
}

// Close ends the browse session.
func (b *UABrowser) Close() {
	b.Stop()
	b.mu.Lock()
	if b.client != nil {
		b.client.Close(context.Background())
		b.client = nil
	}
	b.mu.Unlock()
}

func (b *UABrowser) Stop() {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
	}
	b.mu.Unlock()
}

// Start browses the server in the background.
func (b *UABrowser) Start(o OPCUAConfig) error {
	o.applyDefaults()
	if err := o.validate(); err != nil {
		return err
	}
	if o.Endpoint == "" {
		return errors.New("enter the PLC's OPC UA endpoint, e.g. opc.tcp://192.168.0.1:4840")
	}
	b.mu.Lock()
	if b.st.Running {
		b.mu.Unlock()
		return errors.New("already browsing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	b.cancel = cancel
	old := b.st.Vars
	b.st = UABrowseState{Running: true, Endpoint: o.Endpoint, Vars: old}
	if b.client != nil {
		b.client.Close(context.Background())
		b.client = nil
	}
	b.mu.Unlock()
	go func() {
		defer cancel()
		c, err := uaConnect(ctx, o)
		var vars []UAVar
		if err == nil {
			vars, err = browseVars(ctx, c, func(n int) {
				b.mu.Lock()
				b.st.Count = n
				b.mu.Unlock()
			})
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		b.st.Running, b.st.Time = false, time.Now()
		if err != nil {
			b.st.Error = err.Error()
			if c != nil {
				c.Close(context.Background())
			}
			return
		}
		b.st.Vars, b.st.Count = vars, len(vars)
		b.st.Hint = uaBrowseHint(ctx, c, vars)
		b.client, b.lastUsed = c, time.Now()
	}()
	return nil
}

// Hints for a browse that found no tags.
const (
	uaHintSimatic = "The PLC's OPC UA server is on, but it publishes no tags: the folders Inputs, Outputs, Memory and " +
		"DataBlocksGlobal are missing. In the PLC engineering software: CPU properties → OPC UA → Server → tick \"Enable " +
		"standard server interface\" (1200-series firmware 4.5+, 1500-series 2.9+: under Server interfaces), check that the tags and data " +
		"blocks have \"Accessible from HMI/OPC UA\" ticked, then download Hardware and software to the CPU."
	uaHintEmpty = "The server publishes no variables below Objects. Check that the user (or guest access) may browse, " +
		"and which variables the server exposes."
)

// uaBrowseHint says why a browse found no tags, or "". A SIMATIC PLC
// without its standard server interface shows only its device object.
func uaBrowseHint(ctx context.Context, c *opcua.Client, vars []UAVar) string {
	for _, v := range vars {
		if v.Folder != "" {
			return ""
		}
	}
	simatic := false
	resp, err := c.Read(ctx, &ua.ReadRequest{NodesToRead: []*ua.ReadValueID{
		{NodeID: ua.NewNumericNodeID(0, id.Server_NamespaceArray), AttributeID: ua.AttributeIDValue}}})
	if err == nil && len(resp.Results) == 1 && resp.Results[0].Value != nil {
		if ns, ok := resp.Results[0].Value.Value().([]string); ok {
			for _, n := range ns {
				if strings.Contains(strings.ToLower(n), "simatic") {
					simatic = true
				}
			}
		}
	}
	switch {
	case simatic:
		return uaHintSimatic
	case len(vars) == 0:
		return uaHintEmpty
	}
	return ""
}

// UAValue is a live value read for the browser.
type UAValue struct {
	Value any    `json:"value"`
	Error string `json:"error,omitempty"`
}

// Read reads values of nodes on the browse session.
func (b *UABrowser) Read(nodes []string) ([]UAValue, error) {
	if len(nodes) > 2000 {
		return nil, errors.New("at most 2000 nodes at a time")
	}
	b.mu.Lock()
	c := b.client
	b.lastUsed = time.Now()
	b.mu.Unlock()
	if c == nil {
		return nil, errors.New("not connected: browse again")
	}
	out := make([]UAValue, len(nodes))
	req := &ua.ReadRequest{TimestampsToReturn: ua.TimestampsToReturnNeither}
	var idx []int
	for i, n := range nodes {
		nid, err := ua.ParseNodeID(n)
		if err != nil {
			out[i].Error = "bad node ID"
			continue
		}
		req.NodesToRead = append(req.NodesToRead, &ua.ReadValueID{NodeID: nid, AttributeID: ua.AttributeIDValue})
		idx = append(idx, i)
	}
	if len(idx) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := c.Read(ctx, req)
	if err != nil {
		return nil, err
	}
	for k, r := range resp.Results {
		if k >= len(idx) {
			break
		}
		i := idx[k]
		if r.Status != ua.StatusOK {
			out[i].Error = r.Status.Error()
			continue
		}
		if r.Value != nil {
			out[i].Value = uaShow(r.Value.Value())
		}
	}
	return out, nil
}

// uaShow makes a value fit for JSON.
func uaShow(v any) any {
	switch x := v.(type) {
	case nil, bool, string, int8, uint8, int16, uint16, int32, uint32, int64, uint64:
		return x
	case float32:
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return fmt.Sprint(x)
		}
		return x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Sprint(x)
		}
		return x
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case *ua.LocalizedText:
		if x == nil {
			return nil
		}
		return x.Text
	case []byte:
		return fmt.Sprintf("%X", x)
	}
	s := fmt.Sprint(v)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// S7Ref is the S7 address of a variable, from the names imported in Scan PLC
// (OPC UA does not give absolute addresses).
type S7Ref struct {
	Area    string `json:"area"`
	DB      int    `json:"db,omitempty"`
	Offset  int    `json:"offset"`
	Bit     int    `json:"bit"`
	Type    string `json:"type"`
	Address string `json:"address"`
}

// addS7 fills in the S7 address of browsed variables whose name matches an
// imported data block (with its DB number) or tag table entry.
func addS7(vars []UAVar, st SymState) {
	byName := map[string]*S7Ref{}
	for _, b := range st.Blocks {
		if b.Kind == "db" && b.DB == 0 {
			continue
		}
		for _, s := range b.Symbols {
			if s.Type == "" {
				continue
			}
			name := s.Name
			if b.Kind == "db" {
				name = b.Name + "." + s.Name
			}
			t := Tag{Name: name, Area: s.Area, DB: b.DB, Offset: s.Offset, Bit: s.Bit, Type: s.Type}
			if t.normalize(ProtoS7) != nil {
				continue
			}
			byName[strings.ToLower(name)] = &S7Ref{Area: t.Area, DB: t.DB, Offset: t.Offset, Bit: t.Bit, Type: t.Type, Address: t.Address()}
		}
	}
	for i := range vars {
		if r := byName[strings.ToLower(vars[i].Name)]; r != nil && vars[i].Type != "" {
			vars[i].S7 = r
		}
	}
}
