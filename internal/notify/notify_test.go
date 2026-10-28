package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wolweb/internal/i18n"
)

func TestWebhookAndNtfy(t *testing.T) {
	type got struct {
		path, query, body, ctype string
	}
	ch := make(chan got, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- got{r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("Content-Type")}
	}))
	defer srv.Close()
	n := New(srv.URL+"/hook", srv.URL+"/wolweb-test", []string{Online, WakeFailed}, "test", i18n.DE)
	n.Send(Event{Event: Wake, Device: "NAS", Message: "nicht abonniert"})
	n.Send(Event{Event: Online, Device: "Büro-PC", Message: "Büro-PC ist online"})
	var hook, ntfy got
	for range 2 {
		select {
		case g := <-ch:
			if g.path == "/hook" {
				hook = g
			} else {
				ntfy = g
			}
		case <-time.After(3 * time.Second):
			t.Fatal("keine Benachrichtigung")
		}
	}
	var ev Event
	if err := json.Unmarshal([]byte(hook.body), &ev); err != nil || ev.Event != Online || ev.Device != "Büro-PC" || hook.ctype != "application/json" {
		t.Errorf("Webhook: %+v %v", hook, err)
	}
	if ntfy.path != "/wolweb-test" || ntfy.body != "Büro-PC ist online" || ntfy.query == "" {
		t.Errorf("ntfy: %+v", ntfy)
	}
	select {
	case g := <-ch:
		t.Errorf("nicht abonniertes Ereignis verschickt: %+v", g)
	case <-time.After(200 * time.Millisecond):
	}
	var none *Notifier
	none.Send(Event{Event: Online}) // nil darf nicht abstürzen
	if New("", "", nil, "x", i18n.DE) != nil {
		t.Error("ohne Ziel sollte New nil liefern")
	}
}

// Eine Weiterleitung (307/308 behalten POST und Inhalt) wird nicht befolgt – sonst ginge die
// Nachricht an ein Ziel, das niemand eingetragen hat, etwa im internen Netz.
func TestRedirectNotFollowed(t *testing.T) {
	hit := make(chan string, 2)
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit <- r.URL.Path }))
	defer inner.Close()
	for _, code := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, inner.URL+"/intern/geheim", code)
		}))
		for _, n := range []*Notifier{New(outer.URL+"/hook", "", nil, "test", i18n.DE), New("", outer.URL+"/thema", nil, "test", i18n.DE)} {
			err := n.Test(Event{Event: Test, Device: "wolweb", Message: "Test"})
			if err == nil || !strings.Contains(err.Error(), "Weiterleitung") || strings.Contains(err.Error(), "geheim") {
				t.Errorf("HTTP %d: %v", code, err)
			}
			n.Close()
		}
		outer.Close()
	}
	select {
	case p := <-hit:
		t.Errorf("Weiterleitung befolgt: %s", p)
	default:
	}
}
