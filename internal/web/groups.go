package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"wolweb/internal/i18n"
	"wolweb/internal/schedule"
	"wolweb/internal/store"
)

// --- Gruppen ---

// groupKey ist der Schlüssel des öffentlichen Gruppen-Wecklinks für den Namen name (auch einen
// früheren Namen). Er wird aus dem Geheimnis der Installation und dem Salz der Gruppe abgeleitet:
// Eine gelöschte und neu angelegte Gruppe gleichen Namens hat ein anderes Salz, alte Links gelten
// dann nicht. Gruppen ohne Salz (aus 1.3 und früher) behalten ihren bisherigen Schlüssel.
func (s *Server) groupKey(name string) string {
	msg := "gruppe:" + strings.ToLower(name)
	if g, ok := s.store.FindGroup(name); ok && g.Salt != "" {
		msg += "|" + g.Salt
	}
	m := hmac.New(sha256.New, s.store.Secret())
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))[:24]
}

type groupView struct {
	Name    string       `json:"name"`
	Path    string       `json:"path"`
	Key     string       `json:"key,omitempty"`
	Members []deviceView `json:"members"`
}

// groupView: Was vis ausblendet, fehlt auch bei den Mitgliedern (Privatsphäre).
func (s *Server) groupView(name string, members []store.Device, withKey bool, vis visibility) groupView {
	g := groupView{Name: name, Path: "wake-group/" + pathEscape(name), Members: []deviceView{}}
	if withKey {
		g.Key = s.groupKey(name)
	}
	for _, d := range members {
		g.Members = append(g.Members, s.view(d, vis))
	}
	return g
}

// listGroups liefert alle Gruppen in ihrer Reihenfolge, auch leere.
func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	out := []groupView{}
	vis := s.visibility(r)
	for _, name := range s.store.Groups() {
		out = append(out, s.groupView(name, s.store.GroupMembers(name), s.conf().Auth.WakeKeys, vis))
	}
	writeJSON(w, http.StatusOK, out)
}

type groupInput struct {
	Name    string   `json:"name"`
	Members []string `json:"members"` // Geräte-IDs; fehlt es (null), bleiben die Mitglieder
	// TakeOverName bestätigt, dass ein früherer Name einer anderen Gruppe übernommen wird – deren alte
	// Gruppen-Wecklinks wecken danach diese.
	TakeOverName bool `json:"take_over_name"`
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var in groupInput
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	g, err := s.store.CreateGroup(in.Name, in.Members, in.TakeOverName)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	log.Printf("Group created: %s (%d devices)", g.Name, len(in.Members))
	s.devicesChanged()
	writeJSON(w, http.StatusCreated, s.groupView(g.Name, s.store.GroupMembers(g.Name), s.conf().Auth.WakeKeys, s.visibility(r)))
}

func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	var in groupInput
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	old := r.PathValue("name")
	g, err := s.store.UpdateGroup(old, in.Name, in.Members, in.TakeOverName)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if !strings.EqualFold(old, g.Name) {
		log.Printf("Group renamed: %s -> %s", old, g.Name)
	} else {
		log.Printf("Group changed: %s", g.Name)
	}
	s.devicesChanged()
	writeJSON(w, http.StatusOK, s.groupView(g.Name, s.store.GroupMembers(g.Name), s.conf().Auth.WakeKeys, s.visibility(r)))
}

// renewGroupKey gibt der Gruppe einen neuen Wecklink-Schlüssel; bisherige Links gelten nicht mehr.
func (s *Server) renewGroupKey(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.RenewGroupKey(r.PathValue("name"))
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	log.Printf("Group wake link key renewed: %s", g.Name)
	s.devicesChanged()
	writeJSON(w, http.StatusOK, s.groupView(g.Name, s.store.GroupMembers(g.Name), true, s.visibility(r)))
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteGroup(r.PathValue("name")); err != nil {
		s.storeError(w, r, err)
		return
	}
	log.Printf("Group deleted: %s", r.PathValue("name"))
	s.devicesChanged()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// arrange übernimmt die Reihenfolge aus der Übersicht (Ziehen und Ablegen, Tastatur).
func (s *Server) arrange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Groups  []string          `json:"groups"`
		Devices []store.Placement `json:"devices"`
	}
	if err := readJSON(r, &in); err != nil {
		s.storeError(w, r, err)
		return
	}
	if err := s.store.Arrange(in.Groups, in.Devices); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.devicesChanged()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// groupByName findet eine Gruppe samt Mitgliedern über ihren Namen oder einen früheren Namen.
func (s *Server) groupByName(name string) (string, []store.Device, bool) {
	g, ok := s.store.FindGroup(name)
	if !ok {
		return "", nil, false
	}
	members := s.store.GroupMembers(g.Name)
	return g.Name, members, len(members) > 0
}

type groupResult struct {
	Success bool         `json:"success"`
	Message string       `json:"message"`
	Group   string       `json:"group"`
	Results []wakeResult `json:"results"`
	Watch   string       `json:"watch,omitempty"` // wie bei wakeResult: Start verfolgen trotz ausgeblendetem Status
}

// wakeGroup weckt alle Geräte einer Gruppe; Erfolg nur, wenn jedes Paket rausging.
func (s *Server) wakeGroup(r *http.Request, name string, members []store.Device, source string) (groupResult, int) {
	res := groupResult{Success: true, Group: name}
	ok := 0
	var attempts []wakeAttempt
	for _, d := range members {
		if wr, _, busy := s.alreadyWaking(r, d); busy { // eben erst geweckt (einzeln oder als Gruppe) oder gescheitert
			res.Results = append(res.Results, wr)
			if wr.Success {
				ok++
			} else {
				res.Success = false
			}
			continue
		}
		s.supersedeRetry(d.ID)
		wr, _, a := s.sendWake(r, d, source, name)
		if !wr.Success {
			s.limiter.fail(d.ID, wr)
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
	name, members, ok := s.groupByName(r.PathValue("name"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "err.group_not_found")
		return
	}
	res, code := s.wakeGroup(r, name, members, srcGroup)
	if !s.statusVisible(r) { // die Übersicht verfolgt den Start jedes geweckten Mitglieds einzeln
		for i := range res.Results {
			if res.Results[i].Success {
				res.Results[i].Watch = s.watchToken("device:" + res.Results[i].ID)
			}
		}
	}
	writeJSON(w, code, res)
}

// findGroupForLink: wie findForLink, mit dem abgeleiteten Gruppenschlüssel. Nach dem Umbenennen gilt
// ein alter Link mit dem Schlüssel seines Namens weiter.
func (s *Server) findGroupForLink(r *http.Request) (string, []store.Device, bool) {
	name, members, ok := s.groupByName(r.PathValue("name"))
	if !ok {
		return "", nil, false
	}
	if s.needsKey(r) && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("key")), []byte(s.groupKey(r.PathValue("name")))) != 1 {
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
		vis := s.visibility(r)
		g := s.groupView(name, members, false, vis)
		pd := pageData{Group: &g, Timeout: int(s.rt().wakeTimeout.Seconds()), Retry: int(s.rt().wakeRetry.Seconds()), StatusHidden: !vis.status,
			Confirm: s.confirmWake(r, s.groupKey(r.PathValue("name")))}
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

// watchGroup: Ziel des watchToken einer Gruppe.
func watchGroup(name string) string { return "group:" + name }

func (s *Server) groupWakeNow(w http.ResponseWriter, r *http.Request, name string, members []store.Device, source string) {
	res, code := s.wakeGroup(r, name, members, source)
	if code == http.StatusOK && source == srcGroupPage && !s.statusVisible(r) {
		res.Watch = s.watchToken(watchGroup(name)) // die Gruppenseite verfolgt damit den Start
	}
	// Wer weckt, sieht auch bei ausgeblendetem Online-Status, ob die Geräte starten.
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
	name, members, ok := s.findGroupForLink(r)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "err.group_not_found")
		return
	}
	if !s.checkVisible(r) && !s.watching(r, watchGroup(name)) {
		s.hidden(w, r, "err.status_hidden")
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
		if d.SchedulePaused {
			continue
		}
		entries, _ := schedule.ParseList(d.Schedule)
		for _, e := range entries {
			if e.Due(t) && s.schedRan.first(d.ID+"|"+e.String()+"|"+day, t) {
				log.Printf("Schedule: waking %s (%s)", d.Name, e)
				s.supersedeRetry(d.ID)
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

// nextWake liefert die nächste geplante Weckzeit eines Geräts (nil, wenn keine oder pausiert).
func nextWake(d store.Device) *time.Time {
	if d.SchedulePaused {
		return nil
	}
	entries, _ := schedule.ParseList(d.Schedule)
	if t := schedule.NextOf(entries, time.Now()); !t.IsZero() {
		return &t
	}
	return nil
}

// schedulePreview prüft einen Zeitplan, ohne etwas zu speichern, und liefert die einheitliche
// Schreibweise, die nächste Weckzeit und die Zeitzone des Servers – für die Zusammenfassung im
// Gerätedialog. Die Zeiten gelten in der Ortszeit des Servers, nicht des Browsers.
func (s *Server) schedulePreview(w http.ResponseWriter, r *http.Request) {
	entries, err := schedule.ParseList(r.URL.Query()["s"])
	if err != nil {
		s.storeError(w, r, store.ValidationError{Msg: i18n.M("val.schedule", err), Field: "schedule"})
		return
	}
	texts := make([]string, 0, len(entries))
	for _, e := range entries {
		texts = append(texts, e.String())
	}
	out := map[string]any{"schedule": texts, "zone": localZone()}
	if t := schedule.NextOf(entries, time.Now()); !t.IsZero() {
		out["next"] = t
	}
	writeJSON(w, http.StatusOK, out)
}

// localZone nennt die Zeitzone, in der Zeitpläne laufen: den Namen (TZ, /etc/localtime), wenn er
// sich ermitteln lässt, sonst das Kürzel (z. B. "UTC" in einem Container ohne Zeitzonendaten).
func localZone() string {
	if tz := strings.TrimPrefix(os.Getenv("TZ"), ":"); tz != "" {
		if _, name, ok := strings.Cut(tz, "zoneinfo/"); ok {
			return name
		}
		if !strings.HasPrefix(tz, "/") {
			return tz
		}
	}
	if p, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(p, "zoneinfo/"); ok {
			return name
		}
	}
	name, _ := time.Now().Zone()
	return name
}
