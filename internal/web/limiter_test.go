package web

import (
	"testing"
	"time"
)

// Fehlerpause und Reservierung hängen an derselben Sperre: Nach einem Fehlschlag reserviert niemand
// innerhalb der Pause, alle bekommen das gespeicherte Ergebnis.
func TestWakeLimiterFailureAtomic(t *testing.T) {
	var l wakeLimiter
	if ok, _ := l.claim("a"); !ok {
		t.Fatal("erste Reservierung")
	}
	l.fail("a", wakeResult{Device: "A", Message: "kaputt"})
	if ok, failed := l.claim("a"); ok || failed == nil || failed.Message != "kaputt" {
		t.Fatalf("in der Fehlerpause: ok=%v failed=%+v", ok, failed)
	}
	l.failed["a"] = wakeFailure{at: time.Now().Add(-wakeFailBackoff)}
	if ok, _ := l.claim("a"); !ok {
		t.Fatal("nach der Pause keine Reservierung")
	}
	if ok, failed := l.claim("a"); ok || failed != nil {
		t.Fatalf("eben reserviert: ok=%v failed=%v", ok, failed)
	}
}
