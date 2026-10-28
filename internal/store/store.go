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
	"unicode"
	"unicode/utf8"

	"wolweb/internal/atomicfile"
	"wolweb/internal/config"
	"wolweb/internal/i18n"
	"wolweb/internal/oui"
	"wolweb/internal/schedule"
	"wolweb/internal/wol"
)

type Device struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	IP        string   `json:"ip,omitempty"`        // Adresse des Geräts (für die Online-Prüfung)
	Broadcast string   `json:"broadcast,omitempty"` // Ziel des Magic Packets "ip:port", leer = Standard
	Interface string   `json:"interface,omitempty"`
	Icon      string   `json:"icon,omitempty"`
	Note      string   `json:"note,omitempty"`
	Ports     []int    `json:"ports,omitempty"` // eigene Ports für die Online-Prüfung, leer = Standard
	SecureOn  string   `json:"secureon,omitempty"`
	WakeKey   string   `json:"wake_key,omitempty"` // Schlüssel für Wecklinks ohne Anmeldung (auth.wake_keys)
	Group     string   `json:"group,omitempty"`
	Schedule  []string `json:"schedule,omitempty"` // Weckzeiten, z. B. "Mo-Fr 07:30"
	// SchedulePaused setzt den Zeitplan aus, ohne die Zeiten zu vergessen.
	SchedulePaused bool   `json:"schedule_paused,omitempty"`
	Vendor         string `json:"vendor,omitempty"`
	// Aliases sind frühere Namen: Wecklinks mit dem alten Namen gelten nach dem Umbenennen weiter.
	Aliases []string `json:"aliases,omitempty"`
	// Order ist der Platz in der Gruppe, von Hand sortiert (1, 2, …); 0 = nicht sortiert, nach dem
	// Namen hinter den sortierten.
	Order     int       `json:"order,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

// Fehler zum Vergleichen mit errors.Is; die Oberfläche bekommt sie übersetzt (err.device_not_found,
// err.conflict). Eine i18n.Msg ginge hier nicht – sie ist nicht vergleichbar.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("the data has changed in the meantime – please reload")
)

// ValidationError wird als HTTP 400 an die Oberfläche gegeben: Meldung (übersetzbar) und das
// betroffene Eingabefeld ("name", "mac", "ports" …), damit es markiert werden kann.
type ValidationError struct {
	i18n.Msg
	Field string
	// Confirm nennt ein Feld der Eingabe, mit dem der Aufrufer bewusst fortfahren kann (etwa
	// "take_over_name"); leer bei gewöhnlichen Eingabefehlern.
	Confirm string
}

func invalid(field, key string, args ...any) ValidationError {
	return ValidationError{Msg: i18n.M(key, args...), Field: field}
}

// Invalid baut einen Eingabefehler ohne bestimmtes Feld (z. B. für ungültiges JSON).
func Invalid(key string, args ...any) ValidationError { return invalid("", key, args...) }

type Store struct {
	mu         sync.RWMutex
	dir        string
	devices    []Device
	groups     []Group // in ihrer Reihenfolge (groups.go)
	found      map[string]*Found
	revision   int64
	state      stateFile
	stateDirty bool
	rewrite    bool // nach einem Versionswechsel devices.json einmal neu schreiben
}

type devicesFile struct {
	Version int `json:"version"`
	// Writer ist die Programmversion, die die Datei zuletzt geschrieben hat. Fehlt sie, stammt die
	// Datei von 1.0.x (ohne state.json) oder 1.1.x.
	Writer  string   `json:"writer,omitempty"`
	Devices []Device `json:"devices"`
	Groups  []Group  `json:"groups,omitempty"`
}

// AppVersion ist die Version dieses Programms (setzt main). Wechselt sie, sichert Open die Dateien
// vorher – eine ältere Version verwirft beim Speichern, was sie nicht kennt.
var AppVersion = "dev"

// dataFiles sind die Dateien im Datenverzeichnis, die eine Sicherung umfasst.
var dataFiles = []string{"devices.json", "state.json", "discovered.json", "settings.json"}

// backup kopiert die vorhandenen Dateien nach backup-<label>-<Zeitpunkt>/ und liefert den Ordner.
// Jede Kopie, der Ordner und das Datenverzeichnis werden synchronisiert, bevor die Migration
// weiterschreibt – sonst könnte nach einem Stromausfall eine halbe Sicherung als fertig gelten.
func backup(dir, label string) (string, error) {
	dst := filepath.Join(dir, "backup-"+label+"-"+time.Now().Format("20060102-150405"))
	if err := os.Mkdir(dst, 0o750); err != nil {
		return "", err
	}
	for _, f := range dataFiles {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := atomicfile.WriteSynced(filepath.Join(dst, f), b, 0o600); err != nil {
			return "", err
		}
	}
	if err := atomicfile.SyncDir(dst); err != nil {
		return "", err
	}
	return dst, atomicfile.SyncDir(dir)
}

// previousWriter bestimmt, welche Version die vorhandenen Daten geschrieben hat.
func previousWriter(dir string, b []byte) string {
	var f struct {
		Writer string `json:"writer"`
	}
	if json.Unmarshal(b, &f) == nil && f.Writer != "" {
		return f.Writer
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err == nil {
		return "1.1.x"
	}
	return "1.0.x"
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
	var migratedWake map[string]time.Time // last_wake aus devices.json von 1.0.x
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		devs, lw, n, problems, err := decodeDevices(b)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		s.devices, migratedWake = devs, lw
		s.groups = decodeGroups(b)
		notes = append(notes, problemNotes(path, problems)...)
		// Die Dateien werden gleich im neuen Format geschrieben. Eine ältere Version (1.0.x, das
		// Original-wolweb) verwürfe dabei, was sie nicht kennt – vorher alles sichern, damit der
		// Weg zurück offen bleibt (Sicherung zurückspielen, siehe README).
		label := "original"
		if n == "" {
			label = previousWriter(dir, b)
		}
		if label != AppVersion {
			bak, err := backup(dir, label)
			if err != nil {
				return nil, nil, fmt.Errorf("backup before the update: %w", err)
			}
			notes = append(notes, fmt.Sprintf("Data of wolweb %s backed up to %s", label, bak))
			s.rewrite = true
		}
		if n != "" {
			notes = append(notes, n)
			if err := s.saveDevices(); err != nil {
				return nil, nil, err
			}
		}
	case errors.Is(err, os.ErrNotExist):
		if legacyPath != "" {
			lb, err := os.ReadFile(legacyPath)
			switch {
			case err == nil:
				devs, _, _, problems, err := decodeDevices(lb)
				if err != nil {
					return nil, nil, fmt.Errorf("%s: %w", legacyPath, err)
				}
				s.devices = devs
				notes = append(notes, fmt.Sprintf("%d device(s) imported from %s", len(s.devices), legacyPath))
				notes = append(notes, problemNotes(legacyPath, problems)...)
			case !errors.Is(err, os.ErrNotExist):
				// Nicht still mit leerer Liste starten – sonst würde der Import nie wieder versucht.
				return nil, nil, fmt.Errorf("old device file %s: %w", legacyPath, err)
			}
		}
		if err := s.saveDevices(); err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, err
	}
	fpath := filepath.Join(dir, "discovered.json")
	b, err = os.ReadFile(fpath)
	switch {
	case err == nil:
		var list []*Found
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, nil, fmt.Errorf("%s: %w (repair or delete the file)", fpath, err)
		}
		for _, f := range list {
			if f == nil {
				continue
			}
			hw, err := wol.ParseMAC(f.MAC)
			if err != nil {
				continue
			}
			f.MAC = wol.FormatMAC(hw)
			s.found[f.MAC] = f
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, nil, fmt.Errorf("%s: %w", fpath, err)
	}
	changed, err := s.loadState()
	if err != nil {
		return nil, nil, err
	}
	for id, t := range migratedWake {
		if _, ok := s.state.LastWake[id]; !ok {
			s.state.LastWake[id], changed = t, true
		}
	}
	if changed {
		if err := s.saveState(); err != nil {
			return nil, nil, err
		}
	}
	missingKey := false
	for i := range s.devices {
		if s.devices[i].WakeKey == "" {
			s.devices[i].WakeKey, missingKey = newWakeKey(), true
		}
	}
	// Bis 1.2 gab es Gruppen nur als Namen am Gerät: Einträge anlegen (alphabetisch, wie bisher gezeigt).
	nGroups := len(s.groups)
	s.groups = syncGroups(s.devices, s.groups)
	missingKey = missingKey || len(s.groups) != nGroups
	// last_wake erst nach dem Speichern von state.json aus devices.json entfernen. Nach einem
	// Versionswechsel einmal neu schreiben, damit die Datei die neue Version nennt (und die
	// Sicherung nicht bei jedem Start erneut entsteht).
	if len(migratedWake) > 0 || missingKey || s.rewrite {
		if err := s.saveDevices(); err != nil {
			return nil, nil, err
		}
	}
	return s, notes, nil
}

// cleanName macht importierte Namen linktauglich (gleiche Regeln wie Input.validate).
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\?#%`, r) {
			return '-'
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if s == "." || s == ".." {
		s = ""
	}
	if r := []rune(s); len(r) > 64 {
		s = string(r[:64])
	}
	return s
}

// decodeDevices liest das eigene Format oder das des Original-wolweb. lastWake enthält die
// Weckzeitpunkte, die wolweb 1.0.x noch in devices.json abgelegt hat; note ist gesetzt, wenn
// das alte Format umgewandelt wurde.
// decodeDevices liest devices.json im eigenen oder im alten Format. problems nennt Einträge des alten
// Formats, die nicht übernommen wurden (ungültige MAC, ungültiges Ziel) – nie still ersetzt.
func decodeDevices(b []byte) (devs []Device, lastWake map[string]time.Time, note string, problems []error, err error) {
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, nil, "", nil, err
	}
	if probe.Version >= 2 {
		var f struct {
			Devices []struct {
				Device
				LastWake *time.Time `json:"last_wake"`
			} `json:"devices"`
		}
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, nil, "", nil, err
		}
		for _, d := range f.Devices {
			devs = append(devs, d.Device)
			if d.LastWake != nil && d.ID != "" {
				if lastWake == nil {
					lastWake = map[string]time.Time{}
				}
				lastWake[d.ID] = *d.LastWake
			}
		}
		return devs, lastWake, "", nil, nil
	}
	var lf legacyFile
	if err := json.Unmarshal(b, &lf); err != nil {
		return nil, nil, "", nil, err
	}
	now := time.Now().UTC()
	used := map[string]bool{}
	for _, d := range lf.Devices {
		hw, err := wol.ParseMAC(d.MAC)
		if err != nil {
			problems = append(problems, i18n.M("imp.bad_mac", d.Name, d.MAC))
			continue
		}
		// Das Ziel (im Original "ip") wörtlich übernehmen – ein ungültiges nie still durch den
		// Standard ersetzen, sonst ginge der Weckruf unbemerkt woandershin.
		bc, err := config.NormalizeBroadcast(d.IP)
		if err != nil {
			problems = append(problems, i18n.M("imp.bad_target", d.Name, d.IP, err))
			continue
		}
		name := cleanName(d.Name)
		if name == "" {
			name = "Device"
		}
		for base, i := name, 2; used[strings.ToLower(name)]; i++ {
			name = fmt.Sprintf("%s %d", base, i)
		}
		used[strings.ToLower(name)] = true
		devs = append(devs, Device{
			ID: newID(), Name: name, MAC: wol.FormatMAC(hw), Broadcast: bc, Interface: d.Interface,
			Vendor: oui.Lookup(hw), CreatedAt: now, UpdatedAt: now,
		})
	}
	return devs, nil, fmt.Sprintf("%d device(s) converted from the original wolweb format", len(devs)), problems, nil
}

// problemNotes macht aus nicht übernommenen Einträgen Hinweise fürs Log.
func problemNotes(src string, problems []error) []string {
	var out []string
	for _, p := range problems {
		out = append(out, src+": not imported – "+p.Error())
	}
	return out
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newWakeKey() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeAtomic(path string, v any) error { return atomicfile.WriteJSON(path, v, 0o640) }

// saveDevices muss unter s.mu (Schreibsperre) oder vor der Freigabe des Stores aufgerufen werden.
func (s *Store) saveDevices() error {
	return writeAtomic(filepath.Join(s.dir, "devices.json"), devicesFile{Version: 2, Writer: AppVersion, Devices: s.devices, Groups: s.groups})
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

// Devices liefert eine Kopie der Liste in der Reihenfolge der Übersicht (von Hand sortierte zuerst,
// dann nach Name) und die aktuelle Revision.
func (s *Store) Devices() ([]Device, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := slices.Clone(s.devices)
	slices.SortFunc(out, compareDevices)
	return out, s.revision
}

func compareDevices(a, b Device) int {
	switch {
	case a.Order > 0 && b.Order > 0 && a.Order != b.Order:
		return a.Order - b.Order
	case a.Order > 0 && b.Order == 0:
		return -1
	case a.Order == 0 && b.Order > 0:
		return 1
	}
	return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
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
	for _, d := range s.devices {
		if slices.ContainsFunc(d.Aliases, func(a string) bool { return strings.EqualFold(a, name) }) {
			return d, true
		}
	}
	return Device{}, false
}

// IsAlias sagt, ob name nur ein früherer Name von d ist (nicht der aktuelle, nicht die ID).
func IsAlias(d Device, name string) bool {
	return !strings.EqualFold(d.Name, name) && d.ID != name &&
		slices.ContainsFunc(d.Aliases, func(a string) bool { return strings.EqualFold(a, name) })
}

const maxAliases = 10

// renamed führt die früheren Namen fort: Der bisherige Name kommt dazu, der neue fällt heraus.
// Groß-/Kleinschreibung zählt nicht als Umbenennen – Links finden das Gerät ohnehin.
func renamed(old Device, name string) []string {
	out := slices.DeleteFunc(slices.Clone(old.Aliases), func(a string) bool {
		return strings.EqualFold(a, name) || strings.EqualFold(a, old.Name)
	})
	if !strings.EqualFold(old.Name, name) {
		out = append(out, old.Name)
	}
	if len(out) > maxAliases {
		out = out[len(out)-maxAliases:]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// aliasOwner sucht das Gerät (außer exceptID), dessen früherer Name name ist.
func aliasOwner(list []Device, name, exceptID string) (Device, bool) {
	i := slices.IndexFunc(list, func(d Device) bool { return d.ID != exceptID && IsAlias(d, name) })
	if i < 0 {
		return Device{}, false
	}
	return list[i], true
}

// takeOverName prüft, ob name früher einem anderen Gerät gehörte. Dann wecken Lesezeichen und
// Automationen mit diesem Namen bisher jenes Gerät – das neue bekommt den Namen nur auf ausdrückliche
// Bestätigung (Input.TakeOverName), sonst wechselte das Ziel alter Links unbemerkt.
func takeOverName(list []Device, in Input, name, exceptID string) error {
	if o, ok := aliasOwner(list, name, exceptID); ok && !in.TakeOverName {
		return ValidationError{Msg: i18n.M("val.name_was_alias", name, o.Name), Field: "name", Confirm: "take_over_name"}
	}
	return nil
}

// releaseAlias nimmt name aus den früheren Namen anderer Geräte: Wer einen Namen neu vergibt (und das
// bestätigt hat, siehe takeOverName), meint mit dem Link ab jetzt dieses Gerät.
func releaseAlias(list []Device, name, exceptID string) {
	for i := range list {
		if list[i].ID == exceptID || !slices.ContainsFunc(list[i].Aliases, func(a string) bool { return strings.EqualFold(a, name) }) {
			continue
		}
		list[i].Aliases = slices.DeleteFunc(slices.Clone(list[i].Aliases), func(a string) bool { return strings.EqualFold(a, name) })
		if len(list[i].Aliases) == 0 {
			list[i].Aliases = nil
		}
	}
}

// Input sind die Felder, die die Oberfläche setzen darf.
type Input struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	IP        string   `json:"ip"`
	Broadcast string   `json:"broadcast"`
	Interface string   `json:"interface"`
	Icon      string   `json:"icon"`
	Note      string   `json:"note"`
	Ports     []int    `json:"ports"`
	SecureOn  string   `json:"secureon"`
	Group     string   `json:"group"`
	Schedule  []string `json:"schedule"`
	// SchedulePaused fehlt bei älteren Aufrufern: dann läuft der Zeitplan (false).
	SchedulePaused bool `json:"schedule_paused"`
	// TakeOverName bestätigt, dass ein früherer Name eines anderen Geräts übernommen werden soll –
	// dessen alte Wecklinks wecken danach dieses Gerät.
	TakeOverName bool `json:"take_over_name"`
}

var icons = []string{"desktop", "laptop", "server", "nas", "tv", "console", "router", "printer", "other"}

func (in *Input) validate() (Device, error) {
	var d Device
	d.Name = strings.TrimSpace(in.Name)
	if d.Name == "" {
		return d, invalid("name", "val.name_required")
	}
	if utf8.RuneCountInString(d.Name) > 64 {
		return d, invalid("name", "val.name_too_long")
	}
	if d.Name == "." || d.Name == ".." {
		return d, invalid("name", "val.name_dots")
	}
	if strings.ContainsAny(d.Name, "/\\?#%") || hasControl(d.Name) {
		return d, invalid("name", "val.name_chars")
	}
	hw, err := wol.ParseMAC(in.MAC)
	if err != nil {
		return d, invalid("mac", "val.mac")
	}
	d.MAC = wol.FormatMAC(hw)
	d.Vendor = oui.Lookup(hw)
	if ip := strings.TrimSpace(in.IP); ip != "" {
		a, err := netip.ParseAddr(ip)
		if err != nil || !a.Is4() {
			return d, invalid("ip", "val.ip")
		}
		d.IP = a.String()
	}
	if d.Broadcast, err = config.NormalizeBroadcast(in.Broadcast); err != nil {
		return d, invalid("broadcast", "val.broadcast", err)
	}
	d.Interface = strings.TrimSpace(in.Interface)
	d.Icon = in.Icon
	if !slices.Contains(icons, d.Icon) {
		d.Icon = "desktop"
	}
	d.Note = strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(d.Note) > 500 {
		return d, invalid("note", "val.note_too_long")
	}
	for _, p := range in.Ports {
		if p <= 0 || p > 65535 {
			return d, invalid("ports", "val.port", p)
		}
		if !slices.Contains(d.Ports, p) {
			d.Ports = append(d.Ports, p)
		}
	}
	if len(d.Ports) > 16 {
		return d, invalid("ports", "val.too_many_ports")
	}
	pass, err := wol.ParseSecureOn(in.SecureOn)
	if err != nil {
		return d, invalid("secureon", "val.secureon", in.SecureOn)
	}
	if pass != nil {
		d.SecureOn = wol.FormatMAC(pass)
	}
	if d.Group, err = validGroup(in.Group); err != nil {
		return d, err
	}
	entries, err := schedule.ParseList(in.Schedule)
	if err != nil {
		return d, invalid("schedule", "val.schedule", err)
	}
	for _, e := range entries {
		d.Schedule = append(d.Schedule, e.String())
	}
	d.SchedulePaused = in.SchedulePaused && len(d.Schedule) > 0 // ohne Zeiten gibt es nichts zu pausieren
	return d, nil
}

func hasControl(s string) bool { return strings.ContainsFunc(s, unicode.IsControl) }

// validGroup prüft einen Gruppennamen (leer = keine Gruppe).
func validGroup(g string) (string, error) {
	g = strings.TrimSpace(g)
	// "." und ".." würde der Browser im Gruppen-Wecklink als Pfad auflösen (wie beim Namen).
	if utf8.RuneCountInString(g) > 32 || strings.ContainsAny(g, `/\?#%`) || hasControl(g) || g == "." || g == ".." {
		return g, invalid("group", "val.group")
	}
	return g, nil
}

// SetGroup ordnet mehrere Geräte auf einmal einer Gruppe zu (leer: aus ihrer Gruppe nehmen). Ist
// eine ID unbekannt, ändert sich nichts. Gibt es die Gruppe schon, gilt ihre Schreibweise.
func (s *Store) SetGroup(ids []string, group string) ([]Device, error) {
	group, err := validGroup(group)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := groupIndex(s.groups, group); i >= 0 {
		group = s.groups[i].Name
	}
	next := slices.Clone(s.devices)
	var changed []Device
	now := time.Now().UTC()
	for _, id := range ids {
		i := slices.IndexFunc(next, func(x Device) bool { return x.ID == id })
		if i < 0 {
			return nil, ErrNotFound
		}
		if !strings.EqualFold(next[i].Group, group) {
			next[i].Order = 0 // in der neuen Gruppe hinten
		}
		if next[i].Group != group {
			next[i].Group, next[i].UpdatedAt = group, now
		}
		changed = append(changed, next[i])
	}
	if err := s.commitGroups(next, syncGroups(next, s.groups)); err != nil {
		return nil, err
	}
	return changed, nil
}

func (s *Store) nameTaken(name, exceptID string) bool { return nameTakenIn(s.devices, name, exceptID) }

func nameTakenIn(list []Device, name, exceptID string) bool {
	return slices.ContainsFunc(list, func(d Device) bool { return d.ID != exceptID && strings.EqualFold(d.Name, name) })
}

// inputOf liefert die vom Benutzer gepflegten Felder eines Geräts (für Import und Prüfung).
func inputOf(d Device) Input {
	return Input{Name: d.Name, MAC: d.MAC, IP: d.IP, Broadcast: d.Broadcast, Interface: d.Interface,
		Icon: d.Icon, Note: d.Note, Ports: d.Ports, SecureOn: d.SecureOn, Group: d.Group, Schedule: d.Schedule,
		SchedulePaused: d.SchedulePaused}
}

func (s *Store) Create(in Input) (Device, error) {
	d, err := in.validate()
	if err != nil {
		return d, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nameTaken(d.Name, "") {
		return d, invalid("name", "val.name_taken", d.Name)
	}
	if err := takeOverName(s.devices, in, d.Name, ""); err != nil {
		return d, err
	}
	now := time.Now().UTC()
	d.ID, d.CreatedAt, d.UpdatedAt, d.WakeKey = newID(), now, now, newWakeKey()
	next := append(slices.Clone(s.devices), d)
	releaseAlias(next, d.Name, d.ID)
	groups := syncGroups(next, s.groups)
	if err := s.commitGroups(next, groups); err != nil {
		return d, err
	}
	return next[len(next)-1], nil
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
		return d, invalid("name", "val.name_taken", d.Name)
	}
	if err := takeOverName(s.devices, in, d.Name, id); err != nil {
		return d, err
	}
	d.ID, d.CreatedAt, d.UpdatedAt, d.WakeKey = old.ID, old.CreatedAt, time.Now().UTC(), old.WakeKey
	d.Aliases = renamed(old, d.Name)
	if strings.EqualFold(d.Group, old.Group) {
		d.Order = old.Order // Platz behalten; in einer anderen Gruppe kommt es hinten an
	}
	next := slices.Clone(s.devices)
	next[i] = d
	releaseAlias(next, d.Name, d.ID)
	if err := s.commitGroups(next, syncGroups(next, s.groups)); err != nil {
		return d, err
	}
	return next[i], nil
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
	delete(s.state.LastWake, id)
	delete(s.state.LastSeen, id)
	s.stateDirty = true
	return nil
}

// RotateWakeKey gibt einem Gerät einen neuen Weckschlüssel; der alte Wecklink gilt nicht mehr.
func (s *Store) RotateWakeKey(id string) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.devices, func(x Device) bool { return x.ID == id })
	if i < 0 {
		return Device{}, ErrNotFound
	}
	prev := s.devices
	s.devices = slices.Clone(s.devices)
	s.devices[i].WakeKey = newWakeKey()
	s.devices[i].UpdatedAt = time.Now().UTC()
	if err := s.saveDevices(); err != nil {
		s.devices = prev
		return Device{}, err
	}
	s.revision++
	return s.devices[i], nil
}

// Export liefert alle Geräte im Format von devices.json (zum Sichern oder Umziehen).
// Mit dabei ist das Geheimnis der Installation: Bei einer Wiederherstellung in eine leere
// Installation bleiben so auch die Gruppen-Wecklinks gültig (die Weckschlüssel der Geräte stehen
// ohnehin am Gerät). Der Export ist damit so vertraulich wie devices.json und state.json.
func (s *Store) Export() ([]byte, error) {
	devs, _ := s.Devices()
	groups := s.GroupList()
	s.mu.RLock()
	secret := s.state.Secret
	s.mu.RUnlock()
	b, err := json.MarshalIndent(exportFile{devicesFile: devicesFile{Version: 2, Devices: devs, Groups: groups}, Secret: secret}, "", "  ")
	return append(b, '\n'), err
}

type exportFile struct {
	devicesFile
	Secret string `json:"secret,omitempty"`
}

// validWakeKey: so sehen Schlüssel aus, die newWakeKey erzeugt (Hex, ausreichend lang).
func validWakeKey(k string) bool {
	b, err := hex.DecodeString(k)
	return err == nil && len(b) >= 8 && len(b) <= 32
}

// ImportResult beschreibt das Ergebnis eines Imports.
type ImportResult struct {
	Added          int      `json:"added"`
	Skipped        int      `json:"skipped"`
	Errors         []string `json:"errors,omitempty"`
	SecretRestored bool     `json:"secret_restored,omitempty"` // Gruppen-Wecklinks der Sicherung gelten wieder
	// Items nennt jeden Eintrag der Datei mit seinem Ergebnis – für eine vollständige Liste statt
	// nur der Zahlen.
	Items []ImportItem `json:"items"`
}

// ImportItem ist ein Eintrag der Importdatei: übernommen ("added", ggf. unter neuem Namen),
// schon vorhanden ("skipped", mit dem vorhandenen Gerät) oder fehlerhaft ("error", mit Grund).
type ImportItem struct {
	Name    string `json:"name"`
	Result  string `json:"result"`
	NewName string `json:"new_name,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Import übernimmt Geräte aus einem Export (oder einer devices.json des alten wolweb). Geräte,
// deren MAC schon angelegt ist, werden übersprungen; doppelte Namen bekommen eine Nummer.
// Fehler einzelner Einträge stehen in der Sprache l im Ergebnis.
func (s *Store) Import(b []byte, l i18n.Lang) (ImportResult, error) {
	devs, _, _, problems, err := decodeDevices(b)
	if err != nil {
		return ImportResult{}, Invalid("val.import_invalid", err)
	}
	var exp struct {
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal(b, &exp)
	res := ImportResult{Items: []ImportItem{}}
	for _, p := range problems {
		res.Errors = append(res.Errors, i18n.Text(l, p))
		res.Items = append(res.Items, ImportItem{Result: "error", Detail: i18n.Text(l, p)})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	restore := len(s.devices) == 0 // leere Installation: Sicherung wiederherstellen
	next := slices.Clone(s.devices)
	now := time.Now().UTC()
	for _, src := range devs {
		in := inputOf(src)
		in.Name = cleanName(in.Name)
		d, err := in.validate()
		if err != nil {
			res.Errors = append(res.Errors, src.Name+": "+i18n.Text(l, err))
			res.Items = append(res.Items, ImportItem{Name: src.Name, Result: "error", Detail: i18n.Text(l, err)})
			continue
		}
		if i := slices.IndexFunc(next, func(x Device) bool { return x.MAC == d.MAC }); i >= 0 {
			res.Skipped++
			res.Items = append(res.Items, ImportItem{Name: src.Name, Result: "skipped", Detail: next[i].Name})
			continue
		}
		base := string([]rune(d.Name)[:min(utf8.RuneCountInString(d.Name), 60)])
		// Auch frühere Namen anderer Geräte gelten als vergeben: Der Import soll keine alten Links umlenken.
		for i := 2; nameTakenIn(next, d.Name, "") || slices.ContainsFunc(next, func(x Device) bool { return IsAlias(x, d.Name) }); i++ {
			d.Name = fmt.Sprintf("%s %d", base, i)
		}
		d.ID, d.CreatedAt, d.UpdatedAt, d.WakeKey = newID(), now, now, newWakeKey()
		// Den Schlüssel aus der Sicherung behalten, sonst gelten gespeicherte Wecklinks
		// (Lesezeichen, Automationen) nach einer Wiederherstellung nicht mehr.
		if validWakeKey(src.WakeKey) && !slices.ContainsFunc(next, func(x Device) bool { return x.WakeKey == src.WakeKey }) {
			d.WakeKey = src.WakeKey
		}
		// Frühere Namen ebenso, soweit kein anderes Gerät sie trägt.
		for _, a := range src.Aliases {
			if a = cleanName(a); a != "" && !strings.EqualFold(a, d.Name) && !nameTakenIn(next, a, "") && len(d.Aliases) < maxAliases &&
				!slices.ContainsFunc(next, func(x Device) bool { return IsAlias(x, a) }) {
				d.Aliases = append(d.Aliases, a)
			}
		}
		if restore {
			d.Order = max(src.Order, 0) // Reihenfolge der Sicherung
		}
		releaseAlias(next, d.Name, "")
		next = append(next, d)
		res.Added++
		item := ImportItem{Name: src.Name, Result: "added"}
		if d.Name != src.Name {
			item.NewName = d.Name // Name war vergeben (oder hatte unerlaubte Zeichen)
		}
		res.Items = append(res.Items, item)
	}
	if res.Added == 0 {
		return res, nil
	}
	// Gruppen der Datei mit Reihenfolge und früheren Namen übernehmen, soweit es sie noch nicht gibt.
	if err := s.commitGroups(next, syncGroups(next, mergeGroups(slices.Clone(s.groups), decodeGroups(b)))); err != nil {
		return ImportResult{}, err
	}
	if sec, err := hex.DecodeString(exp.Secret); restore && err == nil && len(sec) >= 16 && exp.Secret != s.state.Secret {
		old := s.state.Secret
		s.state.Secret = exp.Secret
		if err := s.saveState(); err != nil {
			s.state.Secret = old
			return res, err
		}
		res.SecretRestored = true
	}
	return res, nil
}

// --- Suchergebnisse ---

// FoundRetention: So lange bleibt ein Suchtreffer ohne neue Sichtung erhalten. Ohne Grenze
// sammelten sich z. B. Handys mit wechselnder Zufalls-MAC dauerhaft unter "Neu".
// Ignorierte Treffer bleiben, sonst tauchten sie beim nächsten Mal wieder als neu auf.
const FoundRetention = 30 * 24 * time.Hour

// MergeFound übernimmt die Treffer eines Scans, entfernt lange nicht gesehene Treffer und
// liefert die Anzahl neu entdeckter MACs.
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
	for mac, f := range s.found {
		if !f.Ignored && time.Since(f.LastSeen) > FoundRetention {
			delete(s.found, mac)
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
	old := f.Ignored
	f.Ignored = ignored
	if err := s.saveFound(); err != nil {
		f.Ignored = old
		return err
	}
	return nil
}

func (s *Store) ForgetFound(mac string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.found[mac]
	if !ok {
		return ErrNotFound
	}
	delete(s.found, mac)
	if err := s.saveFound(); err != nil {
		s.found[mac] = f
		return err
	}
	return nil
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
