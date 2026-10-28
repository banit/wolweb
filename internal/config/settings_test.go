package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNetworkJSON(t *testing.T) {
	var d Discovery
	if err := json.Unmarshal([]byte(`{"networks":["192.168.1.0/24",{"cidr":"10.0.0.0/25","name":"IoT","vlan":20},{"cidr":"10.0.0.128/25","scan":false}]}`), &d); err != nil {
		t.Fatal(err)
	}
	want := []Network{{CIDR: "192.168.1.0/24", Scan: true}, {CIDR: "10.0.0.0/25", Name: "IoT", VLAN: 20, Scan: true}, {CIDR: "10.0.0.128/25"}}
	for i, n := range want {
		if d.Networks[i] != n {
			t.Errorf("Netz %d: %+v, erwartet %+v", i, d.Networks[i], n)
		}
	}
	if got := strings.Join(d.ScanCIDRs(), " "); got != "192.168.1.0/24 10.0.0.0/25" {
		t.Errorf("durchsucht: %s", got)
	}
	b, _ := json.Marshal(d.Networks)
	if string(b) != `["192.168.1.0/24",{"cidr":"10.0.0.0/25","name":"IoT","vlan":20,"scan":true},{"cidr":"10.0.0.128/25","scan":false}]` {
		t.Errorf("geschrieben: %s", b)
	}
	if l := d.Networks[1].Label(); l != "IoT (VLAN 20)" {
		t.Errorf("Name: %s", l)
	}
}

// setup legt config.json mit eigenem Datenverzeichnis an.
func setup(t *testing.T, cfg string) (path, dataDir string) {
	t.Helper()
	dir := t.TempDir()
	dataDir = filepath.Join(dir, "data")
	os.Mkdir(dataDir, 0o755)
	path = filepath.Join(dir, "config.json")
	var m map[string]any
	if err := json.Unmarshal([]byte(cfg), &m); err != nil {
		t.Fatal(err)
	}
	m["data_dir"] = dataDir
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, dataDir
}

func TestSettingsLayers(t *testing.T) {
	path, dataDir := setup(t, `{"discovery":{"networks":["192.168.1.0/24"],"interval":"30m"},"status":{"interval":"30s"}}`)
	t.Setenv("WOLWEB_STATUS_INTERVAL", "45s") // gesperrt
	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Config().Status.Interval != "45s" || m.Locked()["status.interval"] != "WOLWEB_STATUS_INTERVAL" {
		t.Fatalf("Umgebung: %+v %v", m.Config().Status, m.Locked())
	}

	e := m.Config().Editable()
	e.Discovery.Interval = "1h"
	e.Discovery.Networks = append(e.Discovery.Networks, Network{CIDR: "10.0.0.1/25", Name: " IoT ", VLAN: 20, Scan: true})
	e.Status.Interval = "5m" // gesperrt: zählt nicht
	cfg, err := m.Save(e)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Discovery.Interval != "1h" || cfg.Status.Interval != "45s" || cfg.Discovery.Networks[1] != (Network{CIDR: "10.0.0.0/25", Name: "IoT", VLAN: 20, Scan: true}) {
		t.Fatalf("übernommen: %+v", cfg.Discovery)
	}
	// Nur Abweichungen von config.json, nichts Gesperrtes
	var stored map[string]any
	b, _ := os.ReadFile(filepath.Join(dataDir, "settings.json"))
	json.Unmarshal(b, &stored)
	if got := strings.Join(m.Stored(), " "); got != "discovery.interval discovery.networks" {
		t.Errorf("gespeichert: %s (%s)", got, b)
	}
	if _, ok := stored["status"]; ok {
		t.Errorf("gesperrter Wert in settings.json: %s", b)
	}

	// Neu laden: gleicher Stand; eine spätere Änderung an config.json wirkt, wo nichts überschrieben ist
	os.WriteFile(path, []byte(strings.Replace(string(mustRead(t, path)), `"30s"`, `"20s"`, 1)), 0o644)
	t.Setenv("WOLWEB_STATUS_INTERVAL", "")
	os.Unsetenv("WOLWEB_STATUS_INTERVAL")
	m2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if c := m2.Config(); c.Discovery.Interval != "1h" || len(c.Discovery.Networks) != 2 || c.Status.Interval != "20s" {
		t.Errorf("neu geladen: %+v %+v", c.Discovery, c.Status)
	}

	// Zurück auf den Stand von config.json: settings.json verschwindet
	e = m2.Config().Editable()
	e.Discovery.Interval = "30m"
	e.Discovery.Networks = e.Discovery.Networks[:1]
	if _, err := m2.Save(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "settings.json")); !errors.Is(err, os.ErrNotExist) || len(m2.Stored()) != 0 {
		t.Errorf("settings.json sollte weg sein: %v %v", err, m2.Stored())
	}
}

func mustRead(t *testing.T, p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSettingsFieldErrors(t *testing.T) {
	path, _ := setup(t, `{}`)
	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for field, mod := range map[string]func(*Editable){
		"discovery.networks":  func(e *Editable) { e.Discovery.Networks = []Network{{CIDR: "192.168.1.300/24"}} },
		"status.interval":     func(e *Editable) { e.Status.Interval = "1s" },
		"status.wake_timeout": func(e *Editable) { e.Status.WakeTimeout = "1h" },
		"discovery.interval":  func(e *Editable) { e.Discovery.Interval = "bald" },
		"notify.webhook":      func(e *Editable) { e.Notify.Webhook = "ftp://x" },
		"notify.events":       func(e *Editable) { e.Notify.Events = []string{"boom"} },
		"allowed_hosts":       func(e *Editable) { e.AllowedHosts = []string{"https://wol.example.com"} },
		"bcastip":             func(e *Editable) { e.BcastIP = "irgendwo" },
	} {
		e := m.Config().Editable()
		mod(&e)
		_, err := m.Save(e)
		var fe FieldError
		if !errors.As(err, &fe) || fe.Field != field {
			t.Errorf("%s: %v", field, err)
		}
	}
	e := m.Config().Editable()
	e.Discovery.Networks = []Network{{CIDR: "10.0.0.0/24"}, {CIDR: "10.0.0.7/24"}}
	if _, err := m.Save(e); err == nil || !strings.Contains(err.Error(), "doppelt") {
		t.Errorf("doppeltes Netz: %v", err)
	}
	if len(m.Stored()) != 0 {
		t.Errorf("fehlerhafte Eingaben gespeichert: %v", m.Stored())
	}
}

// settings.json darf nur änderbare Schlüssel setzen – ein Passwort von dort gilt nicht.
func TestSettingsIgnoresForeignKeys(t *testing.T) {
	path, dataDir := setup(t, `{}`)
	os.WriteFile(filepath.Join(dataDir, "settings.json"), []byte(`{"auth":{"password":"x","wake_keys":true},"port":1,"language":"en"}`), 0o600)
	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := m.Config()
	if c.Auth.Password != "" || c.Port != 8089 || !c.Auth.WakeKeys || c.Language != "en" {
		t.Errorf("%+v", c)
	}
}

func TestSettingsReset(t *testing.T) {
	path, dataDir := setup(t, `{"language":"de"}`)
	m, _ := Open(path)
	e := m.Config().Editable()
	e.Language = "en"
	if _, err := m.Save(e); err != nil {
		t.Fatal(err)
	}
	// Windows kennt keine Unix-Rechte; dort genügt, dass die Datei existiert.
	if fi, err := os.Stat(filepath.Join(dataDir, "settings.json")); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("settings.json: %v %v", fi, err)
	}
	c, err := m.Reset()
	if err != nil || c.Language != "de" {
		t.Fatalf("%v %v", c.Language, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("settings.json nicht gelöscht")
	}
}

// Eine kaputte settings.json verhindert den Start mit einem Hinweis, wo der Fehler steckt.
func TestBrokenSettingsNamed(t *testing.T) {
	path, dataDir := setup(t, `{}`)
	os.WriteFile(filepath.Join(dataDir, "settings.json"), []byte(`{"status":{"interval":"1s"}}`), 0o600)
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "settings.json") {
		t.Errorf("%v", err)
	}
}
