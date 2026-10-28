// wolweb – Wake-on-LAN mit Weboberfläche, Online-Status und Netzwerksuche.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
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

	settings, err := config.Open(*configPath)
	if err != nil {
		log.Fatalf("Konfiguration: %v", err)
	}
	cfg, found := settings.Config(), settings.FileFound()
	if *healthcheck {
		os.Exit(runHealthcheck(cfg))
	}
	log.Printf("wolweb %s", version)
	if !cfg.AuthEnabled() && len(cfg.AllowedHosts) == 0 {
		log.Print("Hinweis: Ohne Anmeldung nimmt wolweb nur Aufrufe über IP-Adressen und lokale Namen (nas, *.lan, " +
			"*.local, *.fritz.box …) an – Schutz vor DNS-Rebinding. Läuft es hinter einem Reverse-Proxy mit eigener " +
			`Domain, diese erlauben: in den Einstellungen unter Sicherheit oder mit "allowed_hosts" bzw. WOLWEB_ALLOWED_HOSTS.`)
	}
	if cfg.Auth.WakeKeys && !cfg.AuthEnabled() {
		log.Print("Hinweis: auth.wake_keys wirkt nur zusammen mit einer Anmeldung (auth.username/password oder api_token)")
	}
	if found {
		log.Printf("Konfiguration aus %s geladen", *configPath)
	} else {
		log.Printf("Keine %s gefunden – Standardwerte und Umgebungsvariablen", *configPath)
	}
	if keys := settings.Stored(); len(keys) > 0 {
		log.Printf("In der Oberfläche geändert (%s): %v", settings.SettingsPath(), keys)
	}

	legacy, _ := filepath.Abs(*legacyPath)
	store.AppVersion = version
	st, notes, err := store.Open(cfg.DataDir, legacy)
	if errors.Is(err, fs.ErrPermission) {
		log.Fatalf("Datenverzeichnis %s ist nicht beschreibbar: %v\n"+
			"Docker: Der Container hat keine Sonderrechte – der Ordner muss root gehören (sudo chown -R root:root data).\n"+
			"systemd: Der Dienstbenutzer braucht Schreibrecht (chown -R wolweb: data).", cfg.DataDir, err)
	}
	if err != nil {
		log.Fatalf("Datenverzeichnis %s: %v", cfg.DataDir, err)
	}
	for _, n := range notes {
		log.Print(n)
	}
	devices, _ := st.Devices()
	log.Printf("%d Gerät(e) geladen aus %s", len(devices), cfg.DataDir)

	scanInterval, statusInterval, _, _ := cfg.Durations()
	prober := probe.New(cfg.Status.Ports, 1500*time.Millisecond)
	prober.OnLink = discovery.OnLink
	if prober.ICMPAvailable() {
		log.Print("Online-Prüfung: ICMP-Ping und TCP")
	} else {
		log.Print("Online-Prüfung: nur TCP (für Ping fehlt das Recht CAP_NET_RAW bzw. ping_group_range)")
	}
	mon := status.New(st, prober, statusInterval)
	scanner := discovery.New(st, cfg.Discovery.ScanCIDRs(), cfg.Discovery.MaxHosts)
	scanner.SetInterval(scanInterval)
	ws := web.New(cfg, st, mon, scanner, prober.ICMPAvailable(), version) // hängt sich an mon und scanner
	ws.SetSettings(settings)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go mon.Run(ctx)
	go ws.RunSchedules(ctx)
	go st.RunFlush(ctx)
	switch home := discovery.HomeNets(cfg.Discovery.MaxHosts); {
	case len(cfg.Discovery.Networks) > 0:
	case len(home) == 1:
		log.Printf("Netzwerksuche: Heimnetz %s (%s) automatisch erkannt", home[0].Prefix, home[0].Interface)
	case len(home) > 1:
		log.Printf("Netzwerksuche: %d mögliche Heimnetze – in der Oberfläche unter „Geräte suchen“ eins wählen", len(home))
	default:
		log.Print("Netzwerksuche: kein Heimnetz erkannt – in den Einstellungen ein Netz eintragen")
	}
	if nets := scanner.Networks(); scanInterval > 0 && len(nets) > 0 {
		log.Printf("Automatische Suche alle %s in %v", scanInterval, nets)
	}
	go scanner.RunAuto(ctx) // ruht, solange kein Abstand eingestellt ist

	srv := &http.Server{
		Addr:              cfg.Listen(),
		Handler:           ws.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      11 * time.Minute, // ?wait= darf bis zu 10 Minuten warten
		IdleTimeout:       2 * time.Minute,
	}
	srv.RegisterOnShutdown(ws.Close) // offene Live-Verbindungen beenden, sonst wartet Shutdown
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
	if err := st.FlushState(); err != nil {
		log.Printf("Laufzeitdaten speichern: %v", err)
	}
	log.Print("Beendet.")
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
