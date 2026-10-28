package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
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
	Error   string    `json:"error,omitempty"`
	Retry   bool      `json:"retry,omitempty"` // automatische Wiederholung, weil das Gerät nicht antwortete
}

// HistoryMax begrenzt den Verlauf (älteste Einträge fallen heraus).
const HistoryMax = 500

type stateFile struct {
	Version  int                  `json:"version"`
	Secret   string               `json:"secret,omitempty"`
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
			return false, fmt.Errorf("%s: %w (Datei reparieren oder löschen)", s.statePath(), err)
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
	if b, err := hex.DecodeString(s.state.Secret); err != nil || len(b) < 16 {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		s.state.Secret, changed = hex.EncodeToString(b), true
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
	for _, ev := range evs {
		if ev.Time.IsZero() {
			ev.Time = time.Now().UTC()
		}
		s.state.History = append(s.state.History, ev)
		if ev.Success && ev.ID != "" {
			s.state.LastWake[ev.ID] = ev.Time
		}
	}
	if n := len(s.state.History); n > HistoryMax {
		s.state.History = slices.Clone(s.state.History[n-HistoryMax:])
	}
	return s.saveState()
}

// LastWake liefert den Zeitpunkt des letzten erfolgreichen Weckens (Null, wenn nie).
func (s *Store) LastWake(id string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.LastWake[id]
}

// SeenAt merkt sich, wann ein Gerät zuletzt online war. Gespeichert wird gesammelt (FlushState),
// nicht bei jeder Prüfung.
func (s *Store) SeenAt(id string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.After(s.state.LastSeen[id]) {
		s.state.LastSeen[id] = t
		s.stateDirty = true
	}
}

// LastSeen liefert, wann ein Gerät zuletzt online war (auch über Neustarts hinweg).
func (s *Store) LastSeen(id string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.LastSeen[id]
}

// History liefert die letzten limit Einträge, neueste zuerst.
func (s *Store) History(limit int) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.state.History
	if limit > 0 && len(h) > limit {
		h = h[len(h)-limit:]
	}
	out := slices.Clone(h)
	slices.Reverse(out)
	return out
}

// Secret ist ein zufälliger Schlüssel dieser Installation (z. B. für Gruppen-Wecklinks).
func (s *Store) Secret() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, _ := hex.DecodeString(s.state.Secret)
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

// RunFlush schreibt alle fünf Minuten die gesammelten Laufzeitdaten. Beim Beenden ruft main
// FlushState selbst auf (synchron, damit nichts verloren geht).
func (s *Store) RunFlush(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = s.FlushState()
		}
	}
}
