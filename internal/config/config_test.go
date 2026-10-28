package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPortsValidated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for ports, ok := range map[string]bool{"[22, 445]": true, "[0]": false, "[70000]": false, "[-1]": false} {
		os.WriteFile(path, []byte(`{"status":{"ports":`+ports+`}}`), 0o644)
		if _, _, err := Load(path); (err == nil) != ok {
			t.Errorf("status.ports %s: Fehler %v", ports, err)
		}
	}
}

func TestDefaultBroadcastIsAutomatic(t *testing.T) {
	t.Setenv("WOLWEBBCASTIP", "")
	c, _, err := Load("")
	if err != nil || c.BcastIP != "" {
		t.Errorf("bcastip %q, Fehler %v", c.BcastIP, err)
	}
}

func TestNotifyValidated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for js, ok := range map[string]bool{
		`{"notify":{"ntfy":"https://ntfy.sh/x","events":["online","wake_failed"]}}`: true,
		`{"notify":{"webhook":"ftp://x"}}`:                                          false,
		`{"notify":{"webhook":"https://ha.lan/api/webhook/x","events":["boom"]}}`:   false,
	} {
		os.WriteFile(path, []byte(js), 0o644)
		if _, _, err := Load(path); (err == nil) != ok {
			t.Errorf("%s: Fehler %v", js, err)
		}
	}
}

// Die Beispielkonfiguration muss sich laden lassen (sie wird oft unverändert übernommen).
func TestExampleLoads(t *testing.T) {
	c, found, err := Load("../../config.example.json")
	if err != nil || !found || c.Language != "de" || len(c.Discovery.Networks) != 2 || len(c.Discovery.ScanCIDRs()) != 1 || c.Discovery.Networks[1].Label() != "IoT (VLAN 20)" {
		t.Fatalf("%v %v %+v", err, found, c)
	}
}

func TestNotifyEventsTrimmed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"notify":{"webhook":"https://ha.lan/x","events":["online"," wake "]}}`), 0o644)
	c, _, err := Load(path)
	if err != nil || c.Notify.Events[1] != "wake" {
		t.Errorf("%q %v", c.Notify.Events, err)
	}
}
