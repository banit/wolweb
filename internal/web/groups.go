package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"wolweb/internal/i18n"
	"wolweb/internal/schedule"
	"wolweb/internal/store"
)

// --- Gruppen ---

// groupKey ist der Schlüssel des öffentlichen Gruppen-Wecklinks. Er wird aus dem Geheimnis der
// Installation abgeleitet, muss also nirgends gespeichert werden.
func (s *Server) groupKey(name string) string {
	m := hmac.New(sha256.New, s.store.Secret())
	m.Write([]byte("gruppe:" + strings.ToLower(name)))
	return hex.EncodeToString(m.Sum(nil))[:24]
}

type groupView struct {
	Name    string       `json:"name"`
	Path    string       `json:"path"`
	Key     string       `json:"key,omitempty"`
	Members []deviceView `json:"members"`
}

func (s *Server) groupView(name string, members []store.Device, withKey bool) groupView {
	g := groupView{Name: name, Path: "wake-group/" + pathEscape(name), Members: []deviceView{}}
	if withKey {
		g.Key = s.groupKey(name)
	}
	for _, d := range members {
		g.Members = append(g.Members, s.view(d))
	}
	return g
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	out := []groupView{}
	for _, name := range s.store.Groups() {
		out = append(out, s.groupView(name, s.store.GroupMembers(name), s.conf().Auth.WakeKeys))
	}
	writeJSON(w, http.StatusOK, out)
}

type groupResult struct {
	Success bool         `json:"success"`
	Message string       `json:"message"`
	Group   string       `json:"group"`
	Results []wakeResult `json:"results"`
}

// wakeGroup weckt alle Geräte einer Gruppe; Erfolg nur, wenn jedes Paket rausging.
func (s *Server) wakeGroup(r *http.Request, name string, members []store.Device, source string) (groupResult, int) {
	res := groupResult{Success: true, Group: name}
	ok := 0
	var attempts []wakeAttempt
	for _, d := range members {
		if wr, busy := s.alreadyWaking(r, d); busy { // eben erst geweckt (einzeln oder als Gruppe)
			res.Results = append(res.Results, wr)
			ok++
			continue
		}
		wr, _, a := s.sendWake(r, d, source, name)
		if !wr.Success {
			s.limiter.release(d.ID)
		}
		attempts = append(attempts, a)
		res.Results = append(res.Results, wr)
		if wr.Success {
			ok++
		} else {
			res.Success = false
		}
	}
	s.recordWakes(attempts...) // ein Schreibvorgang, ein Ereignis für die ganze Gruppe
	res.Message = i18n.T(s.lang(r), "msg.group_sent", ok, len(members), name)
	code := http.StatusOK
	if ok == 0 {
		code = http.StatusInternalServerError
	}
	return res, code
}

func (s *Server) wakeGroupAPI(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	members := s.store.GroupMembers(name)
	if len(members) == 0 {
		s.fail(w, r, http.StatusNotFound, "err.group_not_found")
		return
	}
	res, code := s.wakeGroup(r, members[0].Group, members, srcGroup)
	writeJSON(w, code, res)
}

// findGroupForLink: wie findForLink, mit dem abgeleiteten Gruppenschlüssel.
func (s *Server) findGroupForLink(r *http.Request) (string, []store.Device, bool) {
	members := s.store.GroupMembers(r.PathValue("name"))
	if len(members) == 0 {
		return "", nil, false
	}
	name := members[0].Group
	if s.needsKey(r) && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("key")), []byte(s.groupKey(name))) != 1 {
		return "", nil, false
	}
	return name, members, true
}

// groupLink: wie wakeLink – Browser bekommen die Gruppenseite, Automationen wecken sofort.
func (s *Server) groupLink(w http.ResponseWriter, r *http.Request) {
	name, members, ok := s.findGroupForLink(r)
	if sfs := r.Header.Get("Sec-Fetch-Site"); r.Method == http.MethodHead || wantsHTML(r) || sfs == "cross-site" || sfs == "same-site" {
		if !ok {
			s.render(w, r, http.StatusNotFound, "wake.html", pageData{Missing: r.PathValue("name")})
			return
		}
		g := s.groupView(name, members, false)
		pd := pageData{Group: &g, Timeout: int(s.rt().wakeTimeout.Seconds()), Retry: int(s.rt().wakeRetry.Seconds())}
		if s.needsKey(r) {
			pd.Key = s.groupKey(name)
		}
		s.render(w, r, http.StatusOK, "group.html", pd)
		return
	}
	if !ok {
		s.fail(w, r, http.StatusNotFound, "err.group_not_found_name", r.PathValue("name"))
		return
	}
	s.groupWakeNow(w, r, name, members, srcGroupLink)
}

func (s *Server) groupLinkPost(w http.ResponseWriter, r *http.Request) {
	name, members, ok := s.findGroupForLink(r)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "err.group_not_found")
		return
	}
	s.groupWakeNow(w, r, name, members, srcGroupPage)
}

func (s *Server) groupWakeNow(w http.ResponseWriter, r *http.Request, name string, members []store.Device, source string) {
	res, code := s.wakeGroup(r, name, members, source)
	if wait, _ := strconv.Atoi(r.URL.Query().Get("wait")); wait > 0 && code == http.StatusOK {
		// Alle Geräte mit IP gleichzeitig abwarten.
		var wg sync.WaitGroup
		for i, d := range members {
			if ip, _ := s.monitor.Target(d); !ip.IsValid() || !res.Results[i].Success {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				online := s.waitOnline(r.Context(), d, time.Duration(min(wait, 600))*time.Second)
				res.Results[i].Online = &online
			}()
		}
		wg.Wait()
	}
	writeJSON(w, code, res)
}

func (s *Server) groupLinkStatus(w http.ResponseWriter, r *http.Request) {
	_, members, ok := s.findGroupForLink(r)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "err.group_not_found")
		return
	}
	type memberStatus struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status any    `json:"status"`
	}
	out := make([]memberStatus, len(members))
	var wg sync.WaitGroup
	for i, d := range members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = memberStatus{ID: d.ID, Name: d.Name, Status: s.freshStatus(r.Context(), d)}
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

// --- Zeitpläne ---

// RunSchedules weckt Geräte zu ihren Weckzeiten (Ortszeit). Geprüft wird zu jeder vollen Minute;
// verpasste Minuten (Rechner im Ruhezustand) werden nicht nachgeholt.
func (s *Server) RunSchedules(ctx context.Context) {
	var last time.Time
	for {
		next := time.Now().Truncate(time.Minute).Add(time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		if scheduleMinuteDue(next, time.Now(), last) {
			last = next
			s.wakeDue(next)
		}
	}
}

// scheduleMinuteDue sagt, ob die Minute next jetzt (now) geweckt werden soll. Der Zeitgeber läuft
// auf der monotonen Uhr, die im Ruhezustand steht: Nach dem Aufwachen um 12:00 käme sonst noch die
// Minute 07:30 an die Reihe. Und stellt NTP die Uhr zurück, käme dieselbe Minute zweimal.
// Daher nur, wenn die Wanduhr wirklich in dieser Minute steht und sie noch nicht dran war.
func scheduleMinuteDue(next, now, last time.Time) bool {
	return now.Truncate(time.Minute).Equal(next) && next.After(last)
}

// wakeDue weckt alle Geräte, die zur Minute t eine Weckzeit haben, und liefert ihre Namen. Je
// Gerät, Planeintrag und Kalendertag höchstens einmal – auch wenn dieselbe Minute (Uhr zurückgestellt)
// noch einmal vorbeikommt.
func (s *Server) wakeDue(t time.Time) []string {
	var woken []string
	var attempts []wakeAttempt
	devices, _ := s.store.Devices()
	day := t.Format("2006-01-02")
	for _, d := range devices {
		entries, _ := schedule.ParseList(d.Schedule)
		for _, e := range entries {
			if e.Due(t) && s.schedRan.first(d.ID+"|"+e.String()+"|"+day, t) {
				log.Printf("Zeitplan: wecke %s (%s)", d.Name, e)
				_, _, a := s.sendWake(nil, d, srcSchedule, "")
				attempts = append(attempts, a)
				woken = append(woken, d.Name)
				break
			}
		}
	}
	s.recordWakes(attempts...)
	return woken
}

// ranSchedules merkt sich, welche Weckzeiten (Gerät|Eintrag|Tag) schon ausgeführt wurden.
type ranSchedules struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// first sagt, ob key zum ersten Mal vorkommt, und merkt ihn sich; Einträge älter als zwei Tage fallen weg.
func (r *ranSchedules) first(key string, t time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen == nil {
		r.seen = map[string]time.Time{}
	}
	if _, ok := r.seen[key]; ok {
		return false
	}
	r.seen[key] = t
	for k, at := range r.seen {
		if t.Sub(at) > 48*time.Hour {
			delete(r.seen, k)
		}
	}
	return true
}

// nextWake liefert die nächste geplante Weckzeit eines Geräts (nil, wenn keine).
func nextWake(d store.Device) *time.Time {
	entries, _ := schedule.ParseList(d.Schedule)
	if t := schedule.NextOf(entries, time.Now()); !t.IsZero() {
		return &t
	}
	return nil
}
