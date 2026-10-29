package managed

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/wyx2685/v2node/conf"
)

func TestPrepareUsesSnapshotAndScopedIdentity(t *testing.T) {
	m := Manifest{HostID: 2, Revision: "test-revision", Enabled: true, Nodes: []Node{{ID: 5, Enabled: true, Config: json.RawMessage(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":24443,"tls":0,"network":"tcp"}`)}}}
	p, err := Prepare(m, Identity{APIHost: "https://panel.example", HostID: 2, Key: "pn_host_fixture"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := conf.New()
	if err := c.LoadFromPath(p.ConfigPath); err != nil {
		t.Fatal(err)
	}
	if len(c.NodeConfigs) != 1 || c.NodeConfigs[0].Key != "pn_host_fixture" || c.NodeConfigs[0].ConfigFile == "" {
		t.Fatalf("missing scoped local snapshot: %+v", c)
	}
	data, err := os.ReadFile(c.NodeConfigs[0].ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if json.Unmarshal(data, &wire) != nil || wire["server_port"] != float64(24443) {
		t.Fatal("snapshot not preserved")
	}
	info, _ := os.Stat(p.ConfigPath)
	if info.Mode().Perm() != 0600 {
		t.Fatal("worker identity must be private")
	}
}

func TestPrepareRejectsConflictingPortsAndMissingTLSCertificate(t *testing.T) {
	m := Manifest{HostID: 2, Revision: "bad", Enabled: true, Nodes: []Node{
		{ID: 5, Enabled: true, Config: json.RawMessage(`{"protocol":"vless","server_port":24443,"tls":0,"network":"tcp"}`)},
		{ID: 6, Enabled: true, Config: json.RawMessage(`{"protocol":"trojan","server_port":24443,"tls":0,"network":"tcp"}`)},
	}}
	identity := Identity{APIHost: "https://panel.example", HostID: 2, Key: "pn_host_fixture"}
	if _, err := Prepare(m, identity, t.TempDir()); err == nil {
		t.Fatal("accepted duplicate listener")
	}
	m.Nodes = []Node{{ID: 7, Enabled: true, Config: json.RawMessage(`{"protocol":"hysteria2","server_port":24443,"tls":1}`)}}
	if _, err := Prepare(m, identity, t.TempDir()); err == nil {
		t.Fatal("accepted TLS node with no managed certificate")
	}
}

func TestDisabledHostProducesNoListeners(t *testing.T) {
	m := Manifest{HostID: 2, Revision: "disabled", Enabled: false, Nodes: []Node{{ID: 5, Enabled: true, Config: json.RawMessage(`{"protocol":"vless","server_port":24443}`)}}}
	p, err := Prepare(m, Identity{APIHost: "https://panel.example", HostID: 2, Key: "pn_host_fixture"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := conf.New()
	if err := c.LoadFromPath(p.ConfigPath); err != nil {
		t.Fatal(err)
	}
	if len(c.NodeConfigs) != 0 {
		t.Fatal("disabled host retained listeners")
	}
}
