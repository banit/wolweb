package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
	n := New(srv.URL+"/hook", srv.URL+"/wolweb-test", []string{Online, WakeFailed}, "test")
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
	if New("", "", nil, "x") != nil {
		t.Error("ohne Ziel sollte New nil liefern")
	}
}
