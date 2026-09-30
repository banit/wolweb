// wolweb – Wake-on-LAN mit Weboberfläche, Online-Status und Netzwerksuche.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"wolweb/internal/config"
	"wolweb/internal/discovery"
	"wolweb/internal/probe"
	"wolweb/internal/status"
	"wolweb/internal/store"
	"wolweb/internal/web"
)

var version = "dev"

func main() {
	configPath := flag.String("c", "config.json", "Konfigurationsdatei (fehlt sie, gelten Standardwerte)")
	legacyPath := flag.String("d", "devices.json", "devices.json des alten wolweb, wird beim ersten Start übernommen")
	healthcheck := flag.Bool("healthcheck", false, "prüft /health des laufenden Servers (für Docker) und beendet sich")
	flag.Parse()

	cfg, found, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Konfiguration: %v", err)
	}
	if *healthcheck {
		os.Exit(runHealthcheck(cfg))
	}
	log.Printf("wolweb %s", version)
	if found {
		log.Printf("Konfiguration aus %s geladen", *configPath)
	} else {
		log.Printf("Keine %s gefunden – Standardwerte und Umgebungsvariablen", *configPath)
	}

	legacy, _ := filepath.Abs(*legacyPath)
	st, notes, err := store.Open(cfg.DataDir, legacy)
	if err != nil {
		log.Fatalf("Datenverzeichnis %s: %v", cfg.DataDir, err)
	}
	for _, n := range notes {
		log.Print(n)
	}
	devices, _ := st.Devices()
	log.Printf("%d Gerät(e) geladen aus %s", len(devices), cfg.DataDir)

	statusInterval, _ := config.ParseDuration(cfg.Status.Interval)
	prober := probe.New(cfg.Status.Ports, 1500*time.Millisecond)
	if prober.ICMPAvailable() {
		log.Print("Online-Prüfung: ICMP-Ping und TCP")
	} else {
		log.Print("Online-Prüfung: nur TCP (für Ping fehlt das Recht CAP_NET_RAW bzw. ping_group_range)")
	}
	mon := status.New(st, prober, statusInterval)
	scanner := discovery.New(st, cfg.Discovery.Networks, cfg.Discovery.MaxHosts)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go mon.Run(ctx)
	if iv, _ := config.ParseDuration(cfg.Discovery.Interval); iv > 0 && len(cfg.Discovery.Networks) > 0 {
		log.Printf("Automatische Suche alle %s in %v", iv, cfg.Discovery.Networks)
		go autoScan(ctx, scanner, iv)
	}

	srv := &http.Server{
		Addr:              cfg.Listen(),
		Handler:           web.New(cfg, st, mon, scanner, prober.ICMPAvailable(), version).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      11 * time.Minute, // ?wait= darf bis zu 10 Minuten warten
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		log.Printf("Weboberfläche auf http://%s%s", cfg.Listen(), cfg.VDir)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Webserver: %v", err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	log.Print("Beendet.")
}

func autoScan(ctx context.Context, sc *discovery.Scanner, every time.Duration) {
	select { // kurz nach dem Start einmal suchen
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := sc.Start(true); err != nil && !errors.Is(err, discovery.ErrBusy) {
			log.Printf("Automatische Suche: %v", err)
		}
		select {
		case <-ctx.Done():
			sc.Cancel()
			return
		case <-t.C:
		}
	}
}

func runHealthcheck(cfg config.Config) int {
	host := cfg.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://%s%shealth", netJoin(host, cfg.Port), cfg.VDir))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func netJoin(host string, port int) string {
	c := config.Config{Host: host, Port: port}
	return c.Listen()
}
