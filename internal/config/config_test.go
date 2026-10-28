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
