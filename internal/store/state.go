package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"wolweb/internal/atomicfile"
	"wolweb/internal/i18n"
)

// Laufzeitdaten – was wolweb selbst beobachtet (zuletzt geweckt, zuletzt gesehen, Verlauf) –
// liegen in state.json, getrennt von den Geräten in devices.json. devices.json bleibt so reine
// Konfiguration (Export, Sicherung, Versionsverwaltung) und wird nicht bei jedem Wecken neu
// geschrieben.

// Event ist ein Eintrag im Weckverlauf.
type Event struct {
	Time    time.Time `json:"time"`
	ID      string    `json:"id,omitempty"`
	Device  string    `json:"device"`
	Source  string    `json:"source"`           // Schlüssel: ui, page, link, api, schedule, group, group_page, group_link
	Group   string    `json:"group,omitempty"`  // bei Gruppen deren Name
	Client  string    `json:"client,omitempty"` // IP-Adresse des Aufrufers
	User    string    `json:"user,omitempty"`
	Target  string    `json:"target,omitempty"`
	Success bool      `json:"success"`
	Error   string    `json:"error,omitempty"` // Text in der Sprache beim Wecken (Rückfall für alte Einträge)
	// ErrorKey und ErrorArgs halten übersetzbare Fehler fest, damit der Verlauf sie in der jeweils
	// gewählten Sprache zeigt (siehe Localized).
	ErrorKey  string   `json:"error_key,omitempty"`
	ErrorArgs []string `json:"error_args,omitempty"`
	Retry     bool     `json:"retry,omitempty"` // automatische Wiederholung, weil das Gerät nicht antwortete
	// Ergebnis des Starts: Checkable – eine IP war bekannt, der Start ließ sich also prüfen;
	// WasOnline – das Gerät lief schon; Started – wann es danach erreichbar war.
	Checkable bool       `json:"checkable,omitempty"`
	WasOnline bool       `json:"was_online,omitempty"`
	Started   *time.Time `json:"started,omitempty"`
}

// SetError hält einen Fehler fest – als Schlüssel und Parameter, wenn er übersetzbar ist.
func (ev *Event) SetError(l i18n.Lang, err error) {
	ev.Error = i18n.Text(l, err)
	var m i18n.Msg
	if errors.As(err, &m) && m.Error() == err.Error() { // die Meldung selbst, nicht nur ein Teil davon
		ev.ErrorKey, ev.ErrorArgs = m.Key, nil
		for _, a := range m.Args {
			ev.ErrorArgs = append(ev.ErrorArgs, i18n.Text(l, a))
		}
	}
}

// Localized liefert den Eintrag mit dem Fehler in Sprache l.
func (ev Event) Localized(l i18n.Lang) Event {
	if ev.ErrorKey != "" {
		args := make([]any, len(ev.ErrorArgs))
		for i, a := range ev.ErrorArgs {
			args[i] = a
		}
		ev.Error = i18n.T(l, ev.ErrorKey, args...)
	}
	return ev
}

// HistoryMax begrenzt den Verlauf (älteste Einträge fallen heraus).
const HistoryMax = 500

type stateFile struct {
	Version int    `json:"version"`
	Secret  string `json:"secret,omitempty"` // auch im Export: Gruppen-Wecklinks überstehen so eine Wiederherstellung
	// LocalSecret signiert Entsperrungen und watch-Werte. Es verlässt die Installation nie (nicht im
	// Export), damit eine alte Exportdatei keine dieser Berechtigungen nachbilden kann.
	LocalSecret string `json:"local_secret,omitempty"`
	// Password ist der Hash des Passworts für Änderungen (siehe password.go), leer = keins.
	Password string               `json:"edit_password,omitempty"`
	LastWake map[string]time.Time `json:"last_wake,omitempty"`
	LastSeen map[string]time.Time `json:"last_seen,omitempty"`
	History  []Event              `json:"history,omitempty"`
}

func (s *Store) statePath() string { return filepath.Join(s.dir, "state.json") }

// loadState liest state.json; fehlt sie, beginnt ein leerer Zustand mit neuem Geheimnis.
func (s *Store) loadState() (changed bool, err error) {
	b, err := os.ReadFile(s.statePath())
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s.state); err != nil {
			return false, fmt.Errorf("%s: %w (repair or delete the file)", s.statePath(), err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return false, fmt.Errorf("%s: %w", s.statePath(), err)
	}
	if s.state.LastWake == nil {
		s.state.LastWake = map[string]time.Time{}
	}
	if s.state.LastSeen == nil {
		s.state.LastSeen = map[string]time.Time{}
	}
	// Fehlt das Geheimnis oder ist es kaputt (etwa von Hand gekürzt), wird ein neues erzeugt. Ein
	// leerer Schlüssel machte die Gruppen-Wecklinks für jeden berechenbar.
	for _, sec := range []*string{&s.state.Secret, &s.state.LocalSecret} {
		if b, err := hex.DecodeString(*sec); err != nil || len(b) < 16 {
			b := make([]byte, 32)
			_, _ = rand.Read(b)
			*sec, changed = hex.EncodeToString(b), true
		}
	}
	s.state.Version = 1
	return changed, nil
}

// saveState muss unter s.mu (Schreibsperre) aufgerufen werden.
func (s *Store) saveState() error {
	if err := writeAtomic(s.statePath(), s.state); err != nil {
		return err
	}
	s.stateDirty = false
	return nil
}

// RecordWake trägt Weckversuche in den Verlauf ein und merkt sich bei Erfolg den Zeitpunkt –
// mehrere (Gruppe, Zeitplan) mit einem einzigen Schreibvorgang.
func (s *Store) RecordWake(evs ...Event) error {
	if len(evs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.noRecord {
		return nil
	}
	// Erst als ungespeichert markieren: Scheitert das Schreiben (Platte voll, Rechte), holt
	// FlushState es nach, statt dass der Verlauf beim Beenden verloren geht.
	s.stateDirty = true
	for _, ev := range evs {
		if ev.Time.IsZero() {
			ev.Time = time.Now().UTC()
		}
		s.state.History = append(s.state.History, ev)
		if ev.Success && ev.ID != "" && !ev.Retry { // die Wiederholung verriete sonst, dass es offline blieb
			s.state.LastWake[ev.ID] = ev.Time
		}
	}
	if n := len(s.state.History); n > HistoryMax {
		s.state.History = slices.Clone(s.state.History[n-HistoryMax:])
	}
	return s.saveState()
}

// ConfirmStart vermerkt, dass ein Gerät erreichbar wurde: Erfolgreiche, prüfbare Weckrufe der
// letzten window, deren Start noch offen ist, bekommen den Zeitpunkt (auch eine Wiederholung).
// Gespeichert wird gesammelt (FlushState). Liefert, ob sich etwas änderte.
func (s *Store) ConfirmStart(id string, at time.Time, window time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i := len(s.state.History) - 1; i >= 0; i-- {
		ev := &s.state.History[i]
		if at.Sub(ev.Time) > window {
			break
		}
		if ev.ID != id || !ev.Success || !ev.Checkable || ev.WasOnline || ev.Started != nil || ev.Time.After(at) {
			continue
		}
		t := at.UTC()
		ev.Started = &t
		changed = true
	}
	if changed {
		s.stateDirty = true
	}
	return changed
}

// LastWake liefert den Zeitpunkt des letzten erfolgreichen Weckens (Null, wenn nie – oder nicht
// mehr aufbewahrt).
func (s *Store) LastWake(id string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.kept(s.state.LastWake[id])
}

// SeenAt merkt sich, wann ein Gerät zuletzt online war. Gespeichert wird gesammelt (FlushState),
// nicht bei jeder Prüfung.
func (s *Store) SeenAt(id string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.noRecord && t.After(s.state.LastSeen[id]) {
		s.state.LastSeen[id] = t
		s.stateDirty = true
	}
}

// LastSeen liefert, wann ein Gerät zuletzt online war (auch über Neustarts hinweg; Null, wenn
// nicht aufbewahrt).
func (s *Store) LastSeen(id string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.kept(s.state.LastSeen[id])
}

// History liefert die letzten limit Einträge, neueste zuerst (nur, was noch aufbewahrt wird).
func (s *Store) History(limit int) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.state.History
	if s.noRecord {
		h = nil
	}
	if s.keep > 0 {
		cut := time.Now().Add(-s.keep)
		h = slices.DeleteFunc(slices.Clone(h), func(ev Event) bool { return ev.Time.Before(cut) })
	}
	if limit > 0 && len(h) > limit {
		h = h[len(h)-limit:]
	}
	out := append([]Event{}, h...) // leer als [] statt null
	slices.Reverse(out)
	return out
}

// Secret ist ein zufälliger Schlüssel dieser Installation für Gruppen-Wecklinks. Er steht im Export.
func (s *Store) Secret() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, _ := hex.DecodeString(s.state.Secret)
	return b
}

// LocalSecret ist ein zufälliger Schlüssel nur dieser Installation (Entsperrung, watch-Werte). Er steht
// nie im Export und wird beim Import nicht übernommen.
func (s *Store) LocalSecret() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, _ := hex.DecodeString(s.state.LocalSecret)
	return b
}

// FlushState schreibt gesammelte Änderungen (zuletzt gesehen) auf die Platte.
func (s *Store) FlushState() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stateDirty {
		return nil
	}
	return s.saveState()
}

// RunFlush löscht alle fünf Minuten, was nicht mehr aufbewahrt wird, und schreibt die gesammelten
// Laufzeitdaten. Beim Beenden ruft main FlushState selbst auf (synchron, damit nichts verloren
// geht).
func (s *Store) RunFlush(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			if s.pruneLocked(time.Now()) {
				s.stateDirty = true
			}
			s.mu.Unlock()
			_ = s.FlushState()
			_ = s.pruneBackups(time.Now())
		}
	}
}

// --- Aufzeichnung: Weckverlauf, „zuletzt geweckt“ und „zuletzt online“ ---
//
// Diese Daten sind personenbezogen (wer wann geweckt hat, wann ein Arbeitsplatz lief). Sie lassen
// sich abschalten und verfallen nach einer einstellbaren Zahl von Tagen (privacy.recording,
// privacy.retention_days). Die Abfragen blenden Abgelaufenes sofort aus; aus state.json – und aus
// den Kopien davon in den Sicherungen beim Update (backup-*/) – gelöscht wird es beim Einstellen und
// danach alle fünf Minuten (RunFlush).

// SetRecording legt fest, ob aufgezeichnet wird und wie lange (keep, 0 = ohne Zeitgrenze).
// Ausgeschaltet oder abgelaufen wird sofort gelöscht und gespeichert, auch in den Sicherungen.
func (s *Store) SetRecording(on bool, keep time.Duration) error {
	s.mu.Lock()
	s.noRecord, s.keep = !on, keep
	var err error
	if s.pruneLocked(time.Now()) {
		s.stateDirty = true // scheitert das Schreiben, holt FlushState es nach
		err = s.saveState()
	}
	s.mu.Unlock()
	return errors.Join(err, s.pruneBackups(time.Now()))
}

// kept: t, solange es aufbewahrt wird, sonst Null. Unter s.mu (Lesesperre genügt).
func (s *Store) kept(t time.Time) time.Time {
	if s.noRecord || (s.keep > 0 && t.Before(time.Now().Add(-s.keep))) {
		return time.Time{}
	}
	return t
}

// pruneLocked löscht, was nicht (mehr) aufbewahrt wird, und meldet, ob sich etwas änderte. Unter
// s.mu (Schreibsperre).
func (s *Store) pruneLocked(now time.Time) bool {
	return s.state.prune(s.noRecord, s.keep, now)
}

// prune wendet Aufzeichnung (noRecord) und Aufbewahrung (keep, 0 = ohne Zeitgrenze) auf einen Stand
// an und meldet, ob sich etwas änderte.
func (st *stateFile) prune(noRecord bool, keep time.Duration, now time.Time) bool {
	n := len(st.History) + len(st.LastWake) + len(st.LastSeen)
	if noRecord {
		st.History = nil
		clear(st.LastWake)
		clear(st.LastSeen)
		return n > 0
	}
	if keep <= 0 {
		return false
	}
	cut := now.Add(-keep)
	old := func(t time.Time) bool { return t.Before(cut) }
	st.History = slices.DeleteFunc(st.History, func(ev Event) bool { return old(ev.Time) })
	maps.DeleteFunc(st.LastWake, func(_ string, t time.Time) bool { return old(t) })
	maps.DeleteFunc(st.LastSeen, func(_ string, t time.Time) bool { return old(t) })
	return len(st.History)+len(st.LastWake)+len(st.LastSeen) != n
}

// pruneBackups wendet dieselben Regeln auf state.json in den Sicherungen beim Update an. Die
// Geräteliste, Einstellungen und Suchtreffer darin bleiben, wie sie beim Update waren.
func (s *Store) pruneBackups(now time.Time) error {
	s.mu.RLock()
	noRecord, keep := s.noRecord, s.keep
	s.mu.RUnlock()
	paths, _ := filepath.Glob(filepath.Join(s.dir, "backup-*", "state.json"))
	var errs []error
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var st stateFile
		if err := json.Unmarshal(b, &st); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
			continue
		}
		if st.prune(noRecord, keep, now) {
			errs = append(errs, atomicfile.WriteJSON(p, st, 0o600))
		}
	}
	return errors.Join(errs...)
}
