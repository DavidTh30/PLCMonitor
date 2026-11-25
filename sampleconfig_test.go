package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A new installation starts with the look chosen as the default: HMI Gray,
// the button row folded, compact rows, charts in their own window – and no tags
// of anyone's project.
func TestSampleConfigDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, sampleConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	l := c.Layout
	if c.Web.Theme != "hmi-gray" || l.ChartPlace != "window" || !l.HeaderFolded || l.RowSpacing != "compact" || l.TagTree {
		t.Errorf("theme %q, layout %+v", c.Web.Theme, l)
	}
	for _, tg := range c.Tags {
		if tg.Name == "Motor_UDT.Run" {
			t.Error("the sample has the developer's own tags")
		}
	}
}
