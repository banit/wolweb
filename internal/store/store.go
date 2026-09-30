// Package store hält Geräte und Suchergebnisse und speichert sie atomar als JSON im Datenverzeichnis.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"wolweb/internal/config"
	"wolweb/internal/oui"
	"wolweb/internal/wol"
)

type Device struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	MAC       string     `json:"mac"`
	IP        string     `json:"ip,omitempty"`        // Adresse des Geräts (für die Online-Prüfung)
	Broadcast string     `json:"broadcast,omitempty"` // Ziel des Magic Packets "ip:port", leer = Standard
	Interface string     `json:"interface,omitempty"`
	Icon      string     `json:"icon,omitempty"`
	Note      string     `json:"note,omitempty"`
	Vendor    string     `json:"vendor,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	LastWake  *time.Time `json:"last_wake,omitempty"`
}

// Found ist ein Gerät, das die Netzwerksuche gesehen hat.
type Found struct {
	MAC            string    `json:"mac"`
	IP             string    `json:"ip"`
	Hostname       string    `json:"hostname,omitempty"`
	HostnameSource string    `json:"hostname_source,omitempty"`
	Vendor         string    `json:"vendor,omitempty"`
	Interface      string    `json:"interface,omitempty"`
	Network        string    `json:"network,omitempty"`
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
	Ignored        bool      `json:"ignored,omitempty"`
}

var (
	ErrNotFound = errors.New("nicht gefunden")
	ErrConflict = errors.New("die Daten wurden inzwischen geändert – bitte neu laden")
)

// ValidationError wird als HTTP 400 mit Text an die Oberfläche gegeben.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

type Store struct {
	mu       sync.RWMutex
	dir      string
	devices  []Device
	found    map[string]*Found
	revision int64
}

type devicesFile struct {
	Version int      `json:"version"`
	Devices []Device `json:"devices"`
}

type legacyFile struct {
	Devices []struct {
		Name      string `json:"name"`
		MAC       string `json:"mac"`
		IP        string `json:"ip"` // im Original: Broadcast-Adresse mit Port
		Interface string `json:"interface"`
	} `json:"devices"`
}

// Open lädt das Datenverzeichnis. legacyPath (devices.json des Original-wolweb) wird übernommen,
// wenn noch keine eigenen Daten existieren.
func Open(dir, legacyPath string) (*Store, []string, error) {
	var notes []string
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, nil, err
	}
	s := &Store{dir: dir, found: map[string]*Found{}}
	path := filepath.Join(dir, "devices.json")
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		n, err := s.decode(b)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		if n != "" {
			notes = append(notes, n)
			if err := s.saveDevices(); err != nil {
				return nil, nil, err
			}
		}
	case errors.Is(err, os.ErrNotExist):
		if legacyPath != "" {
			if lb, err := os.ReadFile(legacyPath); err == nil {
				n, err := s.decode(lb)
				if err != nil {
					return nil, nil, fmt.Errorf("%s: %w", legacyPath, err)
				}
				notes = append(notes, fmt.Sprintf("%d Gerät(e) aus %s übernommen", len(s.devices), legacyPath))
				_ = n
			}
		}
		if err := s.saveDevices(); err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, err
	}
	if b, err := os.ReadFile(filepath.Join(dir, "discovered.json")); err == nil {
		var list []*Found
		if err := json.Unmarshal(b, &list); err == nil {
			for _, f := range list {
				s.found[f.MAC] = f
			}
		}
	}
	return s, notes, nil
}

// decode liest das eigene Format oder das des Original-wolweb.
func (s *Store) decode(b []byte) (string, error) {
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return "", err
	}
	if probe.Version >= 2 {
		var f devicesFile
		if err := json.Unmarshal(b, &f); err != nil {
			return "", err
		}
		s.devices = f.Devices
		return "", nil
	}
	var lf legacyFile
	if err := json.Unmarshal(b, &lf); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	used := map[string]bool{}
	for _, d := range lf.Devices {
		hw, err := wol.ParseMAC(d.MAC)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(d.Name)
		for base, i := name, 2; used[strings.ToLower(name)] || name == ""; i++ {
			name = fmt.Sprintf("%s %d", base, i)
		}
		used[strings.ToLower(name)] = true
		bc, _ := config.NormalizeBroadcast(d.IP)
		s.devices = append(s.devices, Device{
			ID: newID(), Name: name, MAC: wol.FormatMAC(hw), Broadcast: bc, Interface: d.Interface,
			Vendor: oui.Lookup(hw), CreatedAt: now, UpdatedAt: now,
		})
	}
	return fmt.Sprintf("%d Gerät(e) aus dem alten wolweb-Format umgewandelt", len(s.devices)), nil
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// saveDevices muss unter s.mu (Schreibsperre) oder vor der Freigabe des Stores aufgerufen werden.
func (s *Store) saveDevices() error {
	return writeAtomic(filepath.Join(s.dir, "devices.json"), devicesFile{Version: 2, Devices: s.devices})
}

func (s *Store) saveFound() error {
	list := make([]*Found, 0, len(s.found))
	for _, f := range s.found {
		list = append(list, f)
	}
	slices.SortFunc(list, func(a, b *Found) int { return compareIP(a.IP, b.IP) })
	return writeAtomic(filepath.Join(s.dir, "discovered.json"), list)
}

func compareIP(a, b string) int {
	pa, errA := netip.ParseAddr(a)
	pb, errB := netip.ParseAddr(b)
	if errA != nil || errB != nil {
		return strings.Compare(a, b)
	}
	return pa.Compare(pb)
}

// Devices liefert eine Kopie der Liste (nach Name sortiert) und die aktuelle Revision.
func (s *Store) Devices() ([]Device, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := slices.Clone(s.devices)
	slices.SortFunc(out, func(a, b Device) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, s.revision
}

func (s *Store) Get(id string) (Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.devices {
		if d.ID == id {
			return d, true
		}
	}
	return Device{}, false
}

// FindByName sucht zuerst exakt, dann ohne Groß-/Kleinschreibung, zuletzt nach ID.
func (s *Store) FindByName(name string) (Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.devices {
		if d.Name == name {
			return d, true
		}
	}
	for _, d := range s.devices {
		if strings.EqualFold(d.Name, name) {
			return d, true
		}
	}
	for _, d := range s.devices {
		if d.ID == name {
			return d, true
		}
	}
	return Device{}, false
}

// Input sind die Felder, die die Oberfläche setzen darf.
type Input struct {
	Name      string `json:"name"`
	MAC       string `json:"mac"`
	IP        string `json:"ip"`
	Broadcast string `json:"broadcast"`
	Interface string `json:"interface"`
	Icon      string `json:"icon"`
	Note      string `json:"note"`
}

var icons = []string{"desktop", "laptop", "server", "nas", "tv", "console", "router", "printer", "other"}

func (in *Input) validate() (Device, error) {
	var d Device
	d.Name = strings.TrimSpace(in.Name)
	if d.Name == "" {
		return d, ValidationError{"Bitte einen Namen angeben."}
	}
	if utf8.RuneCountInString(d.Name) > 64 {
		return d, ValidationError{"Der Name darf höchstens 64 Zeichen haben."}
	}
	if strings.ContainsAny(d.Name, "/\\?#%") {
		return d, ValidationError{`Der Name darf keine der Zeichen / \ ? # % enthalten (er wird Teil des Wecklinks).`}
	}
	hw, err := wol.ParseMAC(in.MAC)
	if err != nil {
		return d, ValidationError{"Die MAC-Adresse ist ungültig (Beispiel: 00:11:22:33:44:55)."}
	}
	d.MAC = wol.FormatMAC(hw)
	d.Vendor = oui.Lookup(hw)
	if ip := strings.TrimSpace(in.IP); ip != "" {
		a, err := netip.ParseAddr(ip)
		if err != nil || !a.Is4() {
			return d, ValidationError{"Die IP-Adresse ist ungültig (nur IPv4)."}
		}
		d.IP = a.String()
	}
	if d.Broadcast, err = config.NormalizeBroadcast(in.Broadcast); err != nil {
		return d, ValidationError{"Broadcast-Adresse: " + err.Error()}
	}
	d.Interface = strings.TrimSpace(in.Interface)
	d.Icon = in.Icon
	if !slices.Contains(icons, d.Icon) {
		d.Icon = "desktop"
	}
	d.Note = strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(d.Note) > 500 {
		return d, ValidationError{"Die Notiz ist zu lang (höchstens 500 Zeichen)."}
	}
	return d, nil
}

func (s *Store) nameTaken(name, exceptID string) bool {
	for _, d := range s.devices {
		if d.ID != exceptID && strings.EqualFold(d.Name, name) {
			return true
		}
	}
	return false
}

func (s *Store) Create(in Input) (Device, error) {
	d, err := in.validate()
	if err != nil {
		return d, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nameTaken(d.Name, "") {
		return d, ValidationError{fmt.Sprintf("Es gibt schon ein Gerät namens %q.", d.Name)}
	}
	now := time.Now().UTC()
	d.ID, d.CreatedAt, d.UpdatedAt = newID(), now, now
	prev := s.devices
	s.devices = append(slices.Clone(s.devices), d)
	if err := s.saveDevices(); err != nil {
		s.devices = prev
		return d, err
	}
	s.revision++
	return d, nil
}

// Update ändert ein Gerät. ifUpdated (Zeitstempel updated_at, den der Client kannte) schützt
// davor, Änderungen aus einem anderen Browserfenster stillschweigend zu überschreiben.
func (s *Store) Update(id string, in Input, ifUpdated *time.Time) (Device, error) {
	d, err := in.validate()
	if err != nil {
		return d, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.devices, func(x Device) bool { return x.ID == id })
	if i < 0 {
		return d, ErrNotFound
	}
	old := s.devices[i]
	if ifUpdated != nil && !ifUpdated.Equal(old.UpdatedAt) {
		return d, ErrConflict
	}
	if s.nameTaken(d.Name, id) {
		return d, ValidationError{fmt.Sprintf("Es gibt schon ein Gerät namens %q.", d.Name)}
	}
	d.ID, d.CreatedAt, d.LastWake, d.UpdatedAt = old.ID, old.CreatedAt, old.LastWake, time.Now().UTC()
	prev := s.devices
	s.devices = slices.Clone(s.devices)
	s.devices[i] = d
	if err := s.saveDevices(); err != nil {
		s.devices = prev
		return d, err
	}
	s.revision++
	return d, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.devices, func(x Device) bool { return x.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	prev := s.devices
	s.devices = slices.Delete(slices.Clone(s.devices), i, i+1)
	if err := s.saveDevices(); err != nil {
		s.devices = prev
		return err
	}
	s.revision++
	return nil
}

// MarkWoken merkt sich den Zeitpunkt des letzten Weckens (Fehler beim Speichern sind hier unkritisch).
func (s *Store) MarkWoken(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.devices {
		if s.devices[i].ID == id {
			now := time.Now().UTC()
			s.devices = slices.Clone(s.devices)
			s.devices[i].LastWake = &now
			_ = s.saveDevices()
			return
		}
	}
}

// --- Suchergebnisse ---

// MergeFound übernimmt die Treffer eines Scans und liefert die Anzahl neu entdeckter MACs.
func (s *Store) MergeFound(hits []Found) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	newCount := 0
	for _, h := range hits {
		f, ok := s.found[h.MAC]
		if !ok {
			h.FirstSeen = h.LastSeen
			c := h
			s.found[h.MAC] = &c
			if !s.knownMAC(h.MAC) {
				newCount++
			}
			continue
		}
		f.IP, f.LastSeen, f.Vendor, f.Interface, f.Network = h.IP, h.LastSeen, h.Vendor, h.Interface, h.Network
		if h.Hostname != "" {
			f.Hostname, f.HostnameSource = h.Hostname, h.HostnameSource
		}
	}
	return newCount, s.saveFound()
}

func (s *Store) knownMAC(mac string) bool {
	return slices.ContainsFunc(s.devices, func(d Device) bool { return d.MAC == mac })
}

// FoundEntry ist ein Suchtreffer samt Bezug zu einem angelegten Gerät.
type FoundEntry struct {
	Found
	DeviceID   string `json:"device_id,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
}

func (s *Store) FoundList() []FoundEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]FoundEntry, 0, len(s.found))
	for _, f := range s.found {
		e := FoundEntry{Found: *f}
		for _, d := range s.devices {
			if d.MAC == f.MAC {
				e.DeviceID, e.DeviceName = d.ID, d.Name
				break
			}
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b FoundEntry) int { return compareIP(a.IP, b.IP) })
	return out
}

func (s *Store) SetIgnored(mac string, ignored bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.found[mac]
	if !ok {
		return ErrNotFound
	}
	f.Ignored = ignored
	return s.saveFound()
}

func (s *Store) ForgetFound(mac string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.found[mac]; !ok {
		return ErrNotFound
	}
	delete(s.found, mac)
	return s.saveFound()
}

// FoundByMAC liefert den Suchtreffer zu einer MAC (für die IP-Aktualisierung angelegter Geräte).
func (s *Store) FoundByMAC(mac string) (Found, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if f, ok := s.found[mac]; ok {
		return *f, true
	}
	return Found{}, false
}
