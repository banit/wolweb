package config

import (
	"os"
	"path/filepath"
	"strings"
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
	// Keine Netze: wolweb erkennt das Heimnetz selbst – ein festes Beispielnetz passte bei kaum jemandem.
	if err != nil || !found || c.Language != "en" || len(c.Discovery.Networks) != 0 {
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

// Ein Fehler in der Webhook- oder ntfy-Adresse landet im Log – ohne Thema, Token oder Passwort.
func TestURLErrorsRedacted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, js := range []string{
		`{"notify":{"webhook":"ftp://admin:geheim@ha.example.com/api/webhook/token123"}}`,
		`{"notify":{"ntfy":"ntfy://ntfy.example.com/mein-geheimes-thema?auth=token123"}}`,
	} {
		os.WriteFile(path, []byte(js), 0o644)
		_, _, err := Load(path)
		if err == nil {
			t.Fatalf("%s: kein Fehler", js)
		}
		msg := err.Error()
		if strings.Contains(msg, "geheim") || strings.Contains(msg, "token123") || !strings.Contains(msg, "notify.") || !strings.Contains(msg, "example.com") {
			t.Errorf("%s → %s", js, msg)
		}
	}
}

// Beide Konfigurationstabellen (README.md englisch, README.de.md deutsch) nennen jede
// Umgebungsvariable – auch beide Schreibweisen wie WOLWEBPORT und WOLWEB_PORT.
func TestReadmeNamesEveryVariable(t *testing.T) {
	for file, head := range map[string]string{"README.md": "| Key | Variable |", "README.de.md": "| Schlüssel | Variable |"} {
		b, err := os.ReadFile("../../" + file)
		if err != nil {
			t.Skip(err)
		}
		readme := string(b)
		i := strings.Index(readme, head)
		if i < 0 {
			t.Fatalf("%s: Tabelle %q fehlt", file, head)
		}
		table, _, _ := strings.Cut(readme[i:], "\n\n")
		for _, e := range envKeys {
			for _, v := range e.vars {
				if !strings.Contains(table, "`"+v+"`") {
					t.Errorf("%s: %s (%s) fehlt", head, v, e.key)
				}
			}
		}
	}
}

func TestAccentValidated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for js, want := range map[string]string{`{}`: "indigo", `{"accent":" Teal "}`: "teal", `{"accent":""}`: "indigo", `{"accent":"neon"}`: ""} {
		os.WriteFile(path, []byte(js), 0o644)
		c, _, err := Load(path)
		if want == "" {
			if err == nil || !strings.Contains(err.Error(), "neon") {
				t.Errorf("%s: Fehler %v", js, err)
			}
			continue
		}
		if err != nil || c.Accent != want {
			t.Errorf("%s: %q, Fehler %v", js, c.Accent, err)
		}
	}
	t.Setenv("WOLWEB_ACCENT", "rose")
	m, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil || m.Config().Accent != "rose" || m.Locked()["accent"] != "WOLWEB_ACCENT" {
		t.Errorf("Umgebung: %v", err)
	}
	if AccentColor("rose") != "#be123c" || AccentColor("gibtsnicht") != AccentColor(DefaultAccent) {
		t.Error("AccentColor")
	}
}
