package probe

import (
	"fmt"
	"net"
	"os"
	"testing"
)

// Die Ablehnung wird an der Fehlernummer erkannt, nicht am (übersetzbaren) Text.
func TestIsRefusedByNumber(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	_, err = net.Dial("tcp4", addr)
	if err == nil {
		t.Skip("Port unerwartet offen")
	}
	if !isRefused(err) {
		t.Fatalf("Ablehnung nicht erkannt: %v (%T)", err, err)
	}
	// Ein Fehler mit "refused" im Text, aber ohne passende Nummer, ist keine Ablehnung.
	if isRefused(&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", fmt.Errorf("connection refused (nur Text)"))}) {
		t.Error("Textvergleich statt Fehlernummer")
	}
}
