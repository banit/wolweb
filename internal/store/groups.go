package store

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"wolweb/internal/i18n"
)

// Gruppen sind eigene Einträge in devices.json: Name, frühere Namen und ihre Reihenfolge (die der
// Liste). Ein Gerät nennt seine Gruppe beim Namen; zu jedem genannten Namen gibt es einen Eintrag
// (syncGroups). Eine Gruppe darf leer sein – angelegt auf der Einstellungsseite, bevor Geräte
// hineinkommen.

type Group struct {
	Name string `json:"name"`
	// Aliases sind frühere Namen: Gruppen-Wecklinks mit dem alten Namen gelten nach dem Umbenennen weiter.
	Aliases []string `json:"aliases,omitempty"`
}

// ErrGroupNotFound: Es gibt keine Gruppe dieses Namens (die Oberfläche bekommt err.group_not_found).
var ErrGroupNotFound = errors.New("group not found")

func groupIndex(groups []Group, name string) int {
	return slices.IndexFunc(groups, func(g Group) bool { return strings.EqualFold(g.Name, name) })
}

func hasAlias(aliases []string, name string) bool {
	return slices.ContainsFunc(aliases, func(a string) bool { return strings.EqualFold(a, name) })
}

// aliasIndex sucht die Gruppe (außer except), deren früherer Name name ist.
func aliasIndex(groups []Group, name string, except int) int {
	for i, g := range groups {
		if i != except && hasAlias(g.Aliases, name) {
			return i
		}
	}
	return -1
}

// releaseGroupAlias nimmt name aus den früheren Namen der anderen Gruppen – nur nach ausdrücklicher
// Bestätigung (takeOver in CreateGroup/UpdateGroup): Dann meint ein alter Link ab jetzt diese Gruppe.
func releaseGroupAlias(groups []Group, name string, except int) {
	for i := range groups {
		if i != except {
			groups[i].Aliases = withoutAlias(groups[i].Aliases, name)
		}
	}
}

// syncGroups ordnet jedes Gerät seinem Gruppeneintrag zu und gleicht die Schreibweise an. Ein früherer
// Name gilt als die Gruppe, die ihn trägt – wie im Gruppen-Wecklink; so lenkt ein getippter alter
// Name (Gerätedialog, Mehrfachauswahl, Import) keine Links um. Für unbekannte Namen entsteht ein
// Eintrag (alphabetisch hinten angefügt, wie Gruppen bis 1.2 angezeigt wurden). devs wird dabei
// geändert, groups bekommt eine neue Liste.
func syncGroups(devs []Device, groups []Group) []Group {
	groups = slices.Clone(groups)
	var missing []string
	for _, d := range devs {
		if d.Group != "" && groupIndex(groups, d.Group) < 0 && aliasIndex(groups, d.Group, -1) < 0 &&
			!slices.ContainsFunc(missing, func(m string) bool { return strings.EqualFold(m, d.Group) }) {
			missing = append(missing, d.Group)
		}
	}
	slices.SortFunc(missing, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	for _, name := range missing {
		groups = append(groups, Group{Name: name})
	}
	for i := range devs {
		if devs[i].Group == "" {
			continue
		}
		j := groupIndex(groups, devs[i].Group)
		if j < 0 {
			j = aliasIndex(groups, devs[i].Group, -1)
		}
		devs[i].Group = groups[j].Name
	}
	return groups
}

// decodeGroups liest die Gruppen aus devices.json (oder einem Export). Ungültige und doppelte Namen
// fallen weg; frühere Namen nur, soweit sie keine andere Gruppe trägt.
func decodeGroups(b []byte) []Group {
	var f struct {
		Groups []Group `json:"groups"`
	}
	_ = json.Unmarshal(b, &f)
	return mergeGroups(nil, f.Groups)
}

// mergeGroups fügt add an groups an, soweit es die Namen noch nicht gibt – weder als Namen noch als
// früheren Namen (dessen Geräte ordnet syncGroups dann jener Gruppe zu). Vorhandene frühere Namen
// bleiben unangetastet: Ein Import lenkt keine alten Links um.
func mergeGroups(groups, add []Group) []Group {
	for _, g := range add {
		name, err := validGroup(g.Name)
		if err != nil || name == "" || groupIndex(groups, name) >= 0 || aliasIndex(groups, name, -1) >= 0 {
			continue
		}
		var aliases []string
		for _, a := range g.Aliases {
			if a, err := validGroup(a); err == nil && a != "" && !strings.EqualFold(a, name) && groupIndex(groups, a) < 0 &&
				!slices.ContainsFunc(groups, func(x Group) bool { return hasAlias(x.Aliases, a) }) && len(aliases) < maxAliases {
				aliases = append(aliases, a)
			}
		}
		groups = append(groups, Group{Name: name, Aliases: aliases})
	}
	return groups
}

// GroupList liefert die Gruppen in ihrer Reihenfolge.
func (s *Store) GroupList() []Group {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := slices.Clone(s.groups)
	for i := range out {
		out[i].Aliases = slices.Clone(out[i].Aliases)
	}
	return out
}

// Groups liefert die Gruppennamen in ihrer Reihenfolge.
func (s *Store) Groups() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.groups))
	for i, g := range s.groups {
		out[i] = g.Name
	}
	return out
}

// FindGroup sucht eine Gruppe über ihren Namen, sonst über einen früheren Namen (Groß-/Kleinschreibung egal).
func (s *Store) FindGroup(name string) (Group, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i := groupIndex(s.groups, name)
	if i < 0 {
		i = slices.IndexFunc(s.groups, func(g Group) bool { return hasAlias(g.Aliases, name) })
	}
	if i < 0 || name == "" {
		return Group{}, false
	}
	g := s.groups[i]
	g.Aliases = slices.Clone(g.Aliases)
	return g, true
}

// GroupMembers liefert die Geräte einer Gruppe (Groß-/Kleinschreibung egal) in der Reihenfolge der Übersicht.
func (s *Store) GroupMembers(group string) []Device {
	devs, _ := s.Devices()
	return slices.DeleteFunc(devs, func(d Device) bool { return d.Group == "" || !strings.EqualFold(d.Group, group) })
}

// groupName prüft den Namen einer Gruppe, die angelegt oder umbenannt wird (except: ihr bisheriger
// Platz). War der Name früher der einer anderen Gruppe, wecken deren alte Links bisher jene – die
// neue bekommt ihn nur mit takeOver (wie bei Geräten, siehe takeOverName).
func groupName(groups []Group, name string, except int, takeOver bool) (string, error) {
	name, err := validGroup(name)
	if err != nil {
		return name, invalid("name", "val.group")
	}
	if name == "" {
		return name, invalid("name", "val.group_required")
	}
	if i := groupIndex(groups, name); i >= 0 && i != except {
		return name, invalid("name", "val.group_taken", groups[i].Name)
	}
	if i := aliasIndex(groups, name, except); i >= 0 && !takeOver {
		return name, ValidationError{Msg: i18n.M("val.group_was_alias", name, groups[i].Name), Field: "name", Confirm: "take_over_name"}
	}
	return name, nil
}

// setMembers macht genau die Geräte ids zu Mitgliedern der Gruppe name; bisherige Mitglieder, die
// fehlen, sind danach ohne Gruppe. Geändert wird next.
func setMembers(next []Device, name string, ids []string, now time.Time) error {
	for _, id := range ids {
		if !slices.ContainsFunc(next, func(d Device) bool { return d.ID == id }) {
			return ErrNotFound
		}
	}
	for i := range next {
		want := ""
		if slices.Contains(ids, next[i].ID) {
			want = name
		} else if !strings.EqualFold(next[i].Group, name) {
			continue // in einer anderen Gruppe (oder keiner): bleibt
		}
		if !strings.EqualFold(next[i].Group, want) {
			next[i].Order = 0 // in der neuen Gruppe hinten
		}
		if next[i].Group != want {
			next[i].Group, next[i].UpdatedAt = want, now
		}
	}
	return nil
}

// CreateGroup legt eine Gruppe am Ende der Liste an; members (Geräte-IDs) kommen hinein, auch aus
// einer anderen Gruppe. takeOver bestätigt, dass ein früherer Name einer anderen Gruppe übernommen wird.
func (s *Store) CreateGroup(name string, members []string, takeOver bool) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, err := groupName(s.groups, name, -1, takeOver)
	if err != nil {
		return Group{}, err
	}
	groups := append(slices.Clone(s.groups), Group{Name: name})
	releaseGroupAlias(groups, name, len(groups)-1)
	next := slices.Clone(s.devices)
	if err := setMembers(next, name, members, time.Now().UTC()); err != nil {
		return Group{}, err
	}
	return Group{Name: name}, s.commitGroups(next, groups)
}

// UpdateGroup benennt die Gruppe name um (newName; der alte Name bleibt als früherer Name für
// Wecklinks) und setzt ihre Mitglieder, wenn members nicht nil ist. takeOver wie bei CreateGroup.
func (s *Store) UpdateGroup(name, newName string, members []string, takeOver bool) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := groupIndex(s.groups, name)
	if i < 0 {
		return Group{}, ErrGroupNotFound
	}
	newName, err := groupName(s.groups, newName, i, takeOver)
	if err != nil {
		return Group{}, err
	}
	groups := slices.Clone(s.groups)
	old := groups[i]
	g := Group{Name: newName, Aliases: formerNames(old.Aliases, old.Name, newName)}
	groups[i] = g
	releaseGroupAlias(groups, newName, i)
	next := slices.Clone(s.devices)
	now := time.Now().UTC()
	for j := range next {
		if strings.EqualFold(next[j].Group, old.Name) && next[j].Group != newName {
			next[j].Group, next[j].UpdatedAt = newName, now
		}
	}
	if members != nil {
		if err := setMembers(next, newName, members, now); err != nil {
			return Group{}, err
		}
	}
	return g, s.commitGroups(next, groups)
}

// DeleteGroup löscht eine Gruppe; ihre Geräte bleiben, nur ohne Gruppe.
func (s *Store) DeleteGroup(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := groupIndex(s.groups, name)
	if i < 0 {
		return ErrGroupNotFound
	}
	groups := slices.Delete(slices.Clone(s.groups), i, i+1)
	next := slices.Clone(s.devices)
	if err := setMembers(next, s.groups[i].Name, nil, time.Now().UTC()); err != nil {
		return err
	}
	return s.commitGroups(next, groups)
}

// Placement ist die Reihenfolge der Geräte einer Gruppe ("" = ohne Gruppe) in der Übersicht.
type Placement struct {
	Group string   `json:"group"`
	IDs   []string `json:"ids"`
}

// Arrange übernimmt die Reihenfolge aus der Übersicht: groups nennt Gruppen in ihrer neuen Reihenfolge
// (nicht genannte folgen in der bisherigen), lists je Gruppe die Geräte. Steht ein Gerät in der Liste
// einer anderen Gruppe als bisher, wechselt es dorthin.
func (s *Store) Arrange(groups []string, lists []Placement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	order := make([]Group, 0, len(s.groups))
	for _, name := range groups {
		i := groupIndex(s.groups, name)
		if i < 0 {
			return invalid("group", "val.group_unknown", name)
		}
		if groupIndex(order, name) < 0 {
			order = append(order, s.groups[i])
		}
	}
	for _, g := range s.groups {
		if groupIndex(order, g.Name) < 0 {
			order = append(order, g)
		}
	}
	next := slices.Clone(s.devices)
	now := time.Now().UTC()
	seen := map[string]bool{}
	for _, l := range lists {
		group := ""
		if l.Group != "" {
			i := groupIndex(order, l.Group)
			if i < 0 {
				return invalid("group", "val.group_unknown", l.Group)
			}
			group = order[i].Name
		}
		for pos, id := range l.IDs {
			j := slices.IndexFunc(next, func(d Device) bool { return d.ID == id })
			if j < 0 {
				return ErrNotFound
			}
			if seen[id] {
				return Invalid("val.order_twice", next[j].Name)
			}
			seen[id] = true
			next[j].Order = pos + 1
			if next[j].Group != group {
				next[j].Group, next[j].UpdatedAt = group, now
			}
		}
	}
	return s.commitGroups(next, order)
}

// commitGroups speichert geänderte Geräte und Gruppen zusammen (bei einem Fehler bleibt alles beim Alten).
func (s *Store) commitGroups(next []Device, groups []Group) error {
	prevD, prevG := s.devices, s.groups
	s.devices, s.groups = next, groups
	if err := s.saveDevices(); err != nil {
		s.devices, s.groups = prevD, prevG
		return err
	}
	s.revision++
	return nil
}
