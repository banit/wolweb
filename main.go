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
	configPath := flag.String("c", "config.json", "configuration file (defaults apply if it is missing)")
	legacyPath := flag.String("d", "devices.json", "devices.json of the original wolweb, imported on first start")
	healthcheck := flag.Bool("healthcheck", false, "checks /health of the running server (for Docker) and exits")
	resetPassword := flag.Bool("reset-password", false, "removes the password for changes and exits (stop the server first)")
	flag.Parse()

	settings, err := config.Open(*configPath)
	if err != nil {
		log.Fatalf("Configuration: %v", err)
	}
	cfg, found := settings.Config(), settings.FileFound()
	if *healthcheck {
		os.Exit(runHealthcheck(cfg))
	}
	log.Printf("wolweb %s", version)
	if !cfg.AuthEnabled() && len(cfg.AllowedHosts) == 0 {
		log.Print("Note: without login, wolweb only accepts requests via IP addresses and local names (nas, *.lan, " +
			"*.local, *.fritz.box …) – protection against DNS rebinding. Behind a reverse proxy with its own domain, " +
			`allow that domain: in the settings under Security, or with "allowed_hosts" or WOLWEB_ALLOWED_HOSTS.`)
	}
	if p := cfg.Auth.Password; p != "" && len([]rune(p)) < 12 {
		log.Print("Note: auth.password is short – the login can be guessed; use at least 12 characters")
	}
	if t := cfg.Auth.APIToken; t != "" && len(t) < 20 {
		log.Print("Note: auth.api_token is short – use a long random value (e.g. openssl rand -hex 24)")
	}
	if cfg.Auth.WakeKeys && !cfg.AuthEnabled() {
		log.Print("Note: auth.wake_keys only takes effect together with a login (auth.username/password or api_token)")
	}
	if found {
		log.Printf("Configuration loaded from %s", *configPath)
	} else {
		log.Printf("No %s found – using defaults and environment variables", *configPath)
	}
	if keys := settings.Stored(); len(keys) > 0 {
		log.Printf("Changed on the settings page (%s): %v", settings.SettingsPath(), keys)
	}

	legacy, _ := filepath.Abs(*legacyPath)
	store.AppVersion = version
	st, notes, err := store.Open(cfg.DataDir, legacy)
	if errors.Is(err, fs.ErrPermission) {
		log.Fatalf("Data directory %s is not writable: %v\n"+
			"Docker: the container has no extra privileges – the folder must belong to root (sudo chown -R root:root data).\n"+
			"systemd: the service user needs write access (chown -R wolweb: data).", cfg.DataDir, err)
	}
	if err != nil {
		log.Fatalf("Data directory %s: %v", cfg.DataDir, err)
	}
	for _, n := range notes {
		log.Print(n)
	}
	if *resetPassword {
		if err := st.SetPassword(""); err != nil {
			log.Fatalf("Removing the password: %v", err)
		}
		log.Print("Password for changes removed")
		return
	}
	devices, _ := st.Devices()
	log.Printf("%d device(s) loaded from %s", len(devices), cfg.DataDir)

	scanInterval, statusInterval, _, _ := cfg.Durations()
	prober := probe.New(cfg.Status.Ports, 1500*time.Millisecond)
	prober.OnLink = discovery.OnLink
	if prober.ICMPAvailable() {
		log.Print("Online check: ICMP ping and TCP")
	} else {
		log.Print("Online check: TCP only (ping needs CAP_NET_RAW or ping_group_range)")
	}
	mon := status.New(st, prober, statusInterval)
	scanner := discovery.New(st, cfg.Discovery.ScanCIDRs(), cfg.Discovery.MaxHosts)
	if err := scanner.Configure(cfg.Discovery.ScanCIDRs(), len(cfg.Discovery.Networks) > 0); err != nil {
		log.Fatalf("Configuration: %v", err)
	}
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
		log.Printf("Network scan: home network %s (%s) detected automatically", home[0].Prefix, home[0].Interface)
	case len(home) > 1:
		log.Printf("Network scan: %d possible home networks – choose one under “Find devices” in the web interface", len(home))
	default:
		log.Print("Network scan: no home network detected – add a network on the settings page")
	}
	if nets := scanner.Networks(); scanInterval > 0 && len(nets) > 0 {
		log.Printf("Automatic scan every %s in %v", scanInterval, nets)
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
		log.Printf("Web interface on http://%s%s", cfg.Listen(), cfg.VDir)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Web server: %v", err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	if err := st.FlushState(); err != nil {
		log.Printf("Saving runtime data: %v", err)
	}
	log.Print("Stopped.")
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
