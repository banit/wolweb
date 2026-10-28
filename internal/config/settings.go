package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"wolweb/internal/atomicfile"
)

// Einstellungen aus der Oberfläche liegen in data/settings.json – im Datenverzeichnis, weil das
// immer beschreibbar ist (config.json ist in Docker oft schreibgeschützt eingebunden). Gespeichert
// wird nur, was von config.json abweicht; so wirken spätere Änderungen an config.json weiter, solange
// man denselben Schlüssel nicht auch in der Oberfläche geändert hat.
//
// Reihenfolge, das Spätere gewinnt: Standardwerte, config.json, settings.json, Umgebungsvariablen.

// EditableKeys sind die Schlüssel, die die Oberfläche ändern darf. Adresse, Port, Pfad,
// Datenverzeichnis, Nur-Lesen und Zugangsdaten bleiben config.json und Umgebung vorbehalten: Über
// die Oberfläche könnte man sich sonst aussperren – oder ohne Anmeldung einfach eine setzen.
var EditableKeys = []string{
	"language", "accent", "bcastip", "allowed_hosts",
	"discovery.networks", "discovery.interval",
	"status.interval", "status.ports", "status.wake_timeout", "status.wake_retry",
	"auth.public_wake", "auth.wake_keys",
	"notify.webhook", "notify.ntfy", "notify.events",
}

// Editable sind die Einstellungen, die die Oberfläche ändern darf (Aufbau wie in config.json).
type Editable struct {
	Language     string   `json:"language"`
	Accent       string   `json:"accent"`
	BcastIP      string   `json:"bcastip"`
	AllowedHosts []string `json:"allowed_hosts"`
	Discovery    struct {
		Networks []Network `json:"networks"`
		Interval string    `json:"interval"`
	} `json:"discovery"`
	Status Status `json:"status"`
	Auth   struct {
		PublicWake bool `json:"public_wake"`
		WakeKeys   bool `json:"wake_keys"`
	} `json:"auth"`
	Notify Notify `json:"notify"`
}

// Editable liefert den änderbaren Teil (Listen nie nil, damit leer und "nicht gesetzt" gleich
// aussehen).
func (c Config) Editable() Editable {
	c = c.clone()
	var e Editable
	e.Language, e.Accent, e.BcastIP, e.AllowedHosts = c.Language, c.Accent, c.BcastIP, nonNil(c.AllowedHosts)
	e.Discovery.Networks, e.Discovery.Interval = nonNil(c.Discovery.Networks), c.Discovery.Interval
	e.Status = c.Status
	e.Status.Ports = nonNil(e.Status.Ports)
	e.Auth.PublicWake, e.Auth.WakeKeys = c.Auth.PublicWake, c.Auth.WakeKeys
	e.Notify = c.Notify
	e.Notify.Events = nonNil(e.Notify.Events)
	return e
}

func (e Editable) applyTo(c *Config) {
	c.Language, c.Accent, c.BcastIP, c.AllowedHosts = e.Language, e.Accent, e.BcastIP, slices.Clone(e.AllowedHosts)
	c.Discovery.Networks, c.Discovery.Interval = slices.Clone(e.Discovery.Networks), e.Discovery.Interval
	c.Status = e.Status
	c.Status.Ports = slices.Clone(e.Status.Ports)
	c.Auth.PublicWake, c.Auth.WakeKeys = e.Auth.PublicWake, e.Auth.WakeKeys
	c.Notify = e.Notify
	c.Notify.Events = slices.Clone(e.Notify.Events)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Manager hält die geladenen Einstellungen und schreibt Änderungen aus der Oberfläche.
type Manager struct {
	path, settingsPath string
	found              bool
	base               Config                     // Standardwerte + config.json
	locked             map[string]string          // Schlüssel -> Umgebungsvariable
	mu                 sync.Mutex                 // schützt stored und cur
	stored             map[string]json.RawMessage // settings.json: Schlüssel -> Wert
	cur                Config
}

// Open lädt config.json (path, darf fehlen), data/settings.json und die Umgebungsvariablen. Auch
// bei einem Fehler kommt ein Manager zurück, sofern sich config.json lesen ließ.
func Open(path string) (*Manager, error) {
	m := &Manager{path: path, base: Default(), stored: map[string]json.RawMessage{}}
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			m.found = true
			if err := json.Unmarshal(b, &m.base); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return nil, err
		}
	}
	// Das Datenverzeichnis kommt aus config.json und Umgebung – nie aus settings.json selbst.
	probe := m.base.clone()
	m.locked = applyEnv(&probe)
	m.settingsPath = filepath.Join(probe.DataDir, "settings.json")
	cfg, err := m.build(nil)
	if err != nil {
		m.cur = cfg
		return m, err
	}
	stored, err := readStored(m.settingsPath)
	if err != nil {
		m.cur = cfg
		return m, err
	}
	if cfg, err = m.build(stored); err != nil {
		return m, fmt.Errorf("%s: %w (correct it on the settings page or delete the file)", m.settingsPath, err)
	}
	m.stored, m.cur = stored, cfg
	return m, nil
}

// readStored liest settings.json und behält nur änderbare Schlüssel.
func readStored(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if err := json.Unmarshal(b, &tree); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	flat := flatten(tree)
	out := map[string]json.RawMessage{}
	for k, v := range flat {
		if slices.Contains(EditableKeys, k) {
			out[k] = v
		} else {
			log.Printf("%s: %q is not allowed here and is ignored", path, k)
		}
	}
	return out, nil
}

// flatten macht aus {"status": {"interval": "30s"}} die Schlüssel "status.interval". Werte
// änderbarer Schlüssel bleiben ganz (Listen, Netze), alles andere wird weiter zerlegt.
func flatten(tree map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		if obj, ok := v.(map[string]any); ok && !slices.Contains(EditableKeys, prefix) {
			for k, sub := range obj {
				key := k
				if prefix != "" {
					key = prefix + "." + k
				}
				walk(key, sub)
			}
			return
		}
		b, _ := json.Marshal(v)
		out[prefix] = b
	}
	walk("", tree)
	return out
}

// values liefert die änderbaren Schlüssel einer Konfiguration mit ihrem JSON-Wert.
func values(c Config) map[string]json.RawMessage {
	b, _ := json.Marshal(c.Editable())
	var tree map[string]any
	_ = json.Unmarshal(b, &tree)
	out := map[string]json.RawMessage{}
	for k, v := range flatten(tree) {
		if slices.Contains(EditableKeys, k) {
			out[k] = compact(v)
		}
	}
	return out
}

func compact(b []byte) json.RawMessage {
	var buf bytes.Buffer
	if json.Compact(&buf, b) != nil {
		return b
	}
	return buf.Bytes()
}

// nest baut aus "status.interval" usw. wieder verschachteltes JSON.
func nest(flat map[string]json.RawMessage) map[string]any {
	tree := map[string]any{}
	for k, v := range flat {
		parts := strings.Split(k, ".")
		node := tree
		for _, p := range parts[:len(parts)-1] {
			next, ok := node[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				node[p] = next
			}
			node = next
		}
		node[parts[len(parts)-1]] = v
	}
	return tree
}

// build legt stored über config.json, dann die Umgebung, und prüft das Ergebnis.
func (m *Manager) build(stored map[string]json.RawMessage) (Config, error) {
	c := m.base.clone()
	if len(stored) > 0 {
		b, _ := json.Marshal(nest(stored))
		if err := json.Unmarshal(b, &c); err != nil {
			return c, err
		}
	}
	applyEnv(&c)
	return c, c.normalize()
}

func (m *Manager) Config() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur.clone()
}

// FileFound sagt, ob es config.json gab.
func (m *Manager) FileFound() bool { return m.found }

// Path ist config.json, SettingsPath die Datei mit den Änderungen aus der Oberfläche.
func (m *Manager) Path() string         { return m.path }
func (m *Manager) SettingsPath() string { return m.settingsPath }

// Locked liefert die durch Umgebungsvariablen festgelegten Schlüssel (Schlüssel -> Variable).
func (m *Manager) Locked() map[string]string {
	out := make(map[string]string, len(m.locked))
	for k, v := range m.locked {
		out[k] = v
	}
	return out
}

// Stored liefert die Schlüssel, die in der Oberfläche geändert wurden (settings.json).
func (m *Manager) Stored() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.stored))
	for k := range m.stored {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Save übernimmt die Einstellungen aus der Oberfläche. Gesperrte Schlüssel behalten den Wert der
// Umgebungsvariable; in settings.json landet nur, was von config.json abweicht. Fehler nennen den
// Schlüssel (FieldError).
func (m *Manager) Save(e Editable) (Config, error) {
	want := m.base.clone()
	e.applyTo(&want)
	applyEnv(&want) // gesperrte Felder zählen mit dem Wert der Variable, egal was mitkam
	norm := want.clone()
	if err := norm.normalize(); err != nil {
		return Config{}, err
	}
	base := m.base.clone()
	_ = base.normalize() // Vergleich in derselben Schreibweise ("192.168.1.0/24" statt ".1/24")
	have, wantV := values(base), values(norm)
	stored := map[string]json.RawMessage{}
	for _, k := range EditableKeys {
		if _, ok := m.locked[k]; ok {
			continue
		}
		if !bytes.Equal(have[k], wantV[k]) {
			stored[k] = wantV[k]
		}
	}
	return m.replace(stored)
}

// Reset verwirft alle Änderungen aus der Oberfläche (settings.json wird gelöscht).
func (m *Manager) Reset() (Config, error) { return m.replace(map[string]json.RawMessage{}) }

func (m *Manager) replace(stored map[string]json.RawMessage) (Config, error) {
	cfg, err := m.build(stored)
	if err != nil {
		return Config{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(stored) == 0 {
		if err := os.Remove(m.settingsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Config{}, err
		}
		_ = atomicfile.SyncDir(filepath.Dir(m.settingsPath))
	} else if err := atomicfile.WriteJSON(m.settingsPath, nest(stored), 0o600); err != nil {
		// 0600: Webhook- und ntfy-Adressen enthalten oft geheime Teile.
		return Config{}, err
	}
	m.stored, m.cur = stored, cfg
	return cfg.clone(), nil
}
