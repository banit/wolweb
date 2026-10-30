package discovery

import (
	"encoding/hex"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Eine Antwort, deren Datensatz länger angekündigt ist als die Nachricht, darf die Auswertung
// nicht in einer Schleife festhalten.
func TestPTRNameStopsOnBrokenRecord(t *testing.T) {
	// Kopf mit einer Antwort, Datensatz "a" Typ A, RDLENGTH 0xFFFF ohne Daten.
	resp, _ := hex.DecodeString("0000840000000001000000000161000001000100000000ffff")
	done := make(chan string, 1)
	go func() { done <- ptrName(resp) }()
	select {
	case n := <-done:
		if n != "" {
			t.Fatalf("Name %q aus kaputter Antwort", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ptrName kehrt nicht zurück")
	}
}

func TestPTRNameSkipsOtherRecords(t *testing.T) {
	name := dnsmessage.MustNewName("drucker.local.")
	other := dnsmessage.MustNewName("x.local.")
	msg := dnsmessage.Message{
		Header: dnsmessage.Header{Response: true},
		Answers: []dnsmessage.Resource{
			{Header: dnsmessage.ResourceHeader{Name: other, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
				Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}},
			{Header: dnsmessage.ResourceHeader{Name: other, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET},
				Body: &dnsmessage.PTRResource{PTR: name}},
		},
	}
	b, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if n := ptrName(b); n != "drucker" {
		t.Fatalf("Name %q", n)
	}
}
