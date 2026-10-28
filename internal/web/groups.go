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
		out = append(out, s.groupView(name, s.store.GroupMembers(name), s.cfg.Auth.WakeKeys))
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
	for _, d := range members {
		wr, _ := s.wake(r, d, source, name)
		res.Results = append(res.Results, wr)
		if wr.Success {
			ok++
		} else {
			res.Success = false
		}
	}
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
		pd := pageData{Group: &g, Timeout: int(s.wakeTimeout.Seconds())}
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
	if isPublic(r) {
		if wait, ok := s.limiter.allow("gruppe:" + strings.ToLower(name)); !ok {
			secs := int(wait.Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			s.fail(w, r, http.StatusTooManyRequests, "err.group_rate_limited", name, secs)
			return
		}
	}
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
	for {
		next := time.Now().Truncate(time.Minute).Add(time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		s.wakeDue(next)
	}
}

// wakeDue weckt alle Geräte, die zur Minute t eine Weckzeit haben, und liefert ihre Namen.
func (s *Server) wakeDue(t time.Time) []string {
	var woken []string
	devices, _ := s.store.Devices()
	for _, d := range devices {
		entries, _ := schedule.ParseList(d.Schedule)
		for _, e := range entries {
			if e.Due(t) {
				log.Printf("Zeitplan: wecke %s (%s)", d.Name, e)
				s.wake(nil, d, srcSchedule, "")
				woken = append(woken, d.Name)
				break
			}
		}
	}
	return woken
}

// nextWake liefert die nächste geplante Weckzeit eines Geräts (nil, wenn keine).
func nextWake(d store.Device) *time.Time {
	entries, _ := schedule.ParseList(d.Schedule)
	if t := schedule.NextOf(entries, time.Now()); !t.IsZero() {
		return &t
	}
	return nil
}
