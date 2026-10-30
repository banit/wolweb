package web

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wolweb/internal/store"
)

// Viele gleichzeitige Abfragen desselben Geräts teilen sich eine Prüfung.
func TestSharedCheckCoalesces(t *testing.T) {
	s, _, _ := settingsServer(t, `{"port": 8089}`)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var accepts atomic.Int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			time.Sleep(50 * time.Millisecond)
			c.Close()
		}
	}()
	d, err := s.store.Create(store.Input{Name: "NAS", MAC: "02:00:00:00:00:01", IP: "127.0.0.1", Ports: []int{l.Addr().(*net.TCPAddr).Port}, Broadcast: udpSink(t)})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st, ok := s.sharedCheck(context.Background(), d); !ok || st.State != "online" {
				t.Errorf("Prüfung: %v %+v", ok, st)
			}
		}()
	}
	wg.Wait()
	if n := accepts.Load(); n > 3 {
		t.Fatalf("%d Verbindungen für 20 gleichzeitige Abfragen", n)
	}
}

// Sind alle Warteplätze belegt, prüft ?wait= nur einmal statt zu warten.
func TestWaitOnlineLimited(t *testing.T) {
	s, _, _ := settingsServer(t, `{"port": 8089}`)
	d, _ := s.store.Create(store.Input{Name: "Aus", MAC: "02:00:00:00:00:02", IP: "192.0.2.1", Ports: []int{9}, Broadcast: udpSink(t)})
	for range maxWaiters {
		s.waitSlots <- struct{}{}
	}
	start := time.Now()
	if s.waitOnline(context.Background(), d, time.Minute) {
		t.Fatal("offline gilt als online")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("hat trotz voller Plätze gewartet: %s", time.Since(start))
	}
}
