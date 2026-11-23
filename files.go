package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// FileKind selects the network or the tag configuration for File menu actions.
type FileKind string

const (
	KindNetwork FileKind = "network"
	KindTags    FileKind = "tags"
)

func parseFileKind(s string) (FileKind, error) {
	switch FileKind(s) {
	case KindNetwork, KindTags:
		return FileKind(s), nil
	}
	return "", fmt.Errorf("unknown configuration kind %q (want network or tags)", s)
}

// DefaultExt is the conventional file suffix for each kind.
func (k FileKind) DefaultExt() string {
	if k == KindNetwork {
		return ".net.yaml"
	}
	return ".tags.yaml"
}

// networkFile is the content of a network configuration file.
type networkFile struct {
	Protocol       string        `yaml:"protocol"`
	Simulate       bool          `yaml:"simulate"`
	PLC            PLCConfig     `yaml:"plc"`
	Modbus         ModbusConfig  `yaml:"modbus"`
	OPCUA          OPCUAConfig   `yaml:"opcua,omitempty"`
	PollInterval   time.Duration `yaml:"poll_interval"`
	ReconnectDelay time.Duration `yaml:"reconnect_delay"`
	ReconnectMax   time.Duration `yaml:"reconnect_max"`
}

// tagFile is the content of a tag configuration file.
type tagFile struct {
	Tags []Tag `yaml:"tags"`
}

func marshalNetwork(c *Config) []byte {
	data, _ := yaml.Marshal(networkFile{
		Protocol: c.Protocol, Simulate: c.fileSimulate, PLC: c.PLC, Modbus: c.Modbus, OPCUA: c.OPCUA,
		PollInterval: c.PollInterval, ReconnectDelay: c.ReconnectDelay, ReconnectMax: c.ReconnectMax,
	})
	return append([]byte("# PLC Monitor network configuration\n"), data...)
}

func marshalTags(c *Config) []byte {
	tags := c.Tags
	if tags == nil {
		tags = []Tag{}
	}
	data, _ := yaml.Marshal(tagFile{Tags: tags})
	return append([]byte("# PLC Monitor tag configuration\n"), data...)
}

func (k FileKind) marshal(c *Config) []byte {
	if k == KindNetwork {
		return marshalNetwork(c)
	}
	return marshalTags(c)
}

// load replaces the kind's part of c with the file content.
func (k FileKind) load(c *Config, data []byte) error {
	if k == KindNetwork {
		var nf networkFile
		if err := yaml.Unmarshal(data, &nf); err != nil {
			return errNotA(k) // the YAML error can quote file content; don't echo it
		}
		if nf.Protocol == "" && nf.PLC == (PLCConfig{}) && nf.Modbus == (ModbusConfig{}) {
			return errNotA(k)
		}
		c.Protocol, c.PLC, c.Modbus, c.OPCUA = nf.Protocol, nf.PLC, nf.Modbus, nf.OPCUA
		c.PollInterval, c.ReconnectDelay, c.ReconnectMax = nf.PollInterval, nf.ReconnectDelay, nf.ReconnectMax
		c.Simulate, c.fileSimulate = nf.Simulate, nf.Simulate
		return nil
	}
	var tf tagFile
	var keys map[string]any
	if yaml.Unmarshal(data, &keys) != nil || !hasKey(keys, "tags") {
		return errNotA(k)
	}
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return errNotA(k)
	}
	c.Tags = tf.Tags
	return nil
}

func hasKey(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

func errNotA(k FileKind) error {
	return fmt.Errorf("not a valid %s file (expected YAML like the files written by Save as)", k)
}

// reset puts the kind's part of c back to defaults. A new network keeps the
// protocol (so the tag list stays valid), has no address yet and the
// simulator off.
func (k FileKind) reset(c *Config) {
	if k == KindNetwork {
		c.PLC, c.Modbus, c.OPCUA = PLCConfig{Slot: 1}, ModbusConfig{}, OPCUAConfig{}
		c.PollInterval, c.ReconnectDelay, c.ReconnectMax = 0, 0, 0
		c.Simulate, c.fileSimulate = false, false
		return
	}
	c.Tags = nil
}

func (k FileKind) path(c *Config) *string {
	if k == KindNetwork {
		return &c.NetworkFile
	}
	return &c.TagsFile
}

func (m *Monitor) setBase(k FileKind, data []byte) {
	m.mu.Lock()
	if k == KindNetwork {
		m.netBase = string(data)
	} else {
		m.tagBase = string(data)
	}
	m.mu.Unlock()
}

// FileNew starts an untitled network or tag configuration.
func (m *Monitor) FileNew(k FileKind) error {
	err := m.Update(func(c *Config) error {
		k.reset(c)
		*k.path(c) = ""
		return nil
	})
	if err == nil {
		m.setBase(k, k.marshal(m.Config()))
		log.Printf("new %s configuration", k)
	}
	return err
}

// FileOpen loads a network or tag file and applies it at once.
func (m *Monitor) FileOpen(k FileKind, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	err = m.Update(func(c *Config) error {
		if err := k.load(c, data); err != nil {
			return err
		}
		*k.path(c) = abs
		return nil
	})
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(abs), err)
	}
	m.setBase(k, k.marshal(m.Config()))
	log.Printf("opened %s configuration %s", k, abs)
	return nil
}

// FileSave writes the current network or tag configuration to path, or to
// its current file when path is empty ("Save" vs "Save as").
func (m *Monitor) FileSave(k FileKind, path string) error {
	cur := m.Config()
	if path == "" {
		path = *k.path(cur)
		if path == "" {
			return fmt.Errorf("no file yet; use Save as")
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	data := k.marshal(cur)
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, abs); err != nil {
		return err
	}
	if *k.path(cur) != abs {
		if err := m.Update(func(c *Config) error { *k.path(c) = abs; return nil }); err != nil {
			return err
		}
	}
	m.setBase(k, data)
	log.Printf("saved %s configuration to %s", k, abs)
	return nil
}

// loadBaselines records the saved state of the files named in the config, so
// unsaved changes made before the last exit still show as unsaved.
func (m *Monitor) loadBaselines() {
	cur := m.Config()
	for _, k := range []FileKind{KindNetwork, KindTags} {
		p := *k.path(cur)
		if p == "" {
			m.setBase(k, k.marshal(cur)) // untitled: nothing to compare with
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			log.Printf("%s file %s: %v", k, p, err)
			continue // base stays empty, so it shows as unsaved
		}
		c := cur.clone()
		if err := k.load(c, data); err != nil {
			log.Printf("%s file %s: %v", k, p, err)
			continue
		}
		c.applyDefaults()
		if err := c.validate(); err != nil {
			log.Printf("%s file %s: %v", k, p, err)
			continue
		}
		m.setBase(k, k.marshal(c))
	}
}
