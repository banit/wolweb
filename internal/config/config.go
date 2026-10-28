// Package config lädt die Einstellungen aus config.json und Umgebungsvariablen.
// Die Schlüssel host, port, vdir, bcastip und read_only sowie die Variablen WOLWEBHOST,
// WOLWEBPORT, WOLWEBVDIR, WOLWEBBCASTIP und WOLWEBREADONLY entsprechen dem Original-wolweb.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"wolweb/internal/i18n"
	"wolweb/internal/notify"
)

type Config struct {
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	VDir      string    `json:"vdir"`
	BcastIP   string    `json:"bcastip"` // Ziel, wenn weder Gerät noch Netz eins liefern; leer = automatisch
	ReadOnly  bool      `json:"read_only"`
	DataDir   string    `json:"data_dir"`
	Language  string    `json:"language"` // "de" oder "en": Sprache, wenn Browser bzw. Anfrage keine nennt
	Discovery Discovery `json:"discovery"`
	Status    Status    `json:"status"`
	Auth      Auth      `json:"auth"`
	Notify    Notify    `json:"notify"`
	// AllowedHosts sind zusätzliche Hostnamen, unter denen wolweb aufgerufen werden darf (Schutz
	// vor DNS-Rebinding), z. B. "wol.example.com" oder "*.example.com"; "*" schaltet die Prüfung ab.
	AllowedHosts []string `json:"allowed_hosts"`
}

type Discovery struct {
	// Networks sind die Segmente (CIDR), die gescannt werden dürfen, z. B. "192.168.1.0/24".
	Networks []string `json:"networks"`
	// Interval für den automatischen Hintergrund-Scan, z. B. "30m"; leer oder "0" = aus.
	Interval string `json:"interval"`
	// MaxHosts begrenzt die Anzahl Adressen je Segment.
	MaxHosts int `json:"max_hosts"`
}

type Status struct {
	Interval string `json:"interval"`
	// Ports, die zur Online-Prüfung angefragt werden. "Verbindung abgelehnt" zählt auch als online.
	Ports []int `json:"ports"`
	// WakeTimeout: wie lange die Weckseite auf das Gerät wartet.
	WakeTimeout string `json:"wake_timeout"`
	// WakeRetry: Antwortet ein geweckter Rechner so lange nicht, geht das Magic Packet einmal
	// erneut raus. "0" = nie.
	WakeRetry string `json:"wake_retry"`
}

// Notify: Benachrichtigungen bei Ereignissen.
type Notify struct {
	// Webhook bekommt jedes Ereignis als JSON per POST (z. B. Home Assistant, Node-RED).
	Webhook string `json:"webhook"`
	// Ntfy ist die Themen-URL eines ntfy-Servers, z. B. "https://ntfy.sh/mein-geheimes-thema".
	Ntfy string `json:"ntfy"`
	// Events: "online", "offline", "wake", "wake_failed"; leer = alle.
	Events []string `json:"events"`
}

type Auth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// APIToken erlaubt Automationen den Zugriff per "Authorization: Bearer <token>".
	APIToken string `json:"api_token"`
	// PublicWake: feste Wecklinks (/wake/...) funktionieren ohne Anmeldung.
	PublicWake bool `json:"public_wake"`
	// WakeKeys: Wecklinks ohne Anmeldung brauchen zusätzlich den Schlüssel des Geräts (?key=…),
	// damit niemand Geräte über erratene Namen ("NAS", "PC") weckt.
	WakeKeys bool `json:"wake_keys"`
}

func Default() Config {
	return Config{
		Host:     "0.0.0.0",
		Port:     8089,
		VDir:     "/",
		DataDir:  "data",
		Language: "de",
		Discovery: Discovery{
			Interval: "30m",
			MaxHosts: 1024,
		},
		Status: Status{
			Interval:    "30s",
			Ports:       []int{22, 80, 443, 445, 139, 3389, 5900, 8006, 8080, 53},
			WakeTimeout: "3m",
			WakeRetry:   "45s",
		},
		Auth: Auth{PublicWake: true},
	}
}

// Load liest die Datei (falls vorhanden), wendet Umgebungsvariablen an und prüft das Ergebnis.
func Load(path string) (Config, bool, error) {
	c := Default()
	found := false
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			found = true
			if err := json.Unmarshal(b, &c); err != nil {
				return c, found, fmt.Errorf("%s: %w", path, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return c, found, err
		}
	}
	applyEnv(&c)
	return c, found, c.normalize()
}

func applyEnv(c *Config) {
	str := func(dst *string, keys ...string) {
		for _, k := range keys {
			if v, ok := os.LookupEnv(k); ok {
				*dst = v
			}
		}
	}
	boolean := func(dst *bool, keys ...string) {
		for _, k := range keys {
			if v, ok := os.LookupEnv(k); ok {
				if b, err := strconv.ParseBool(v); err == nil {
					*dst = b
				}
			}
		}
	}
	str(&c.Host, "WOLWEBHOST", "WOLWEB_HOST")
	if v, ok := os.LookupEnv("WOLWEBPORT"); ok {
		c.Port, _ = strconv.Atoi(v)
	}
	if v, ok := os.LookupEnv("WOLWEB_PORT"); ok {
		c.Port, _ = strconv.Atoi(v)
	}
	str(&c.VDir, "WOLWEBVDIR", "WOLWEB_VDIR")
	str(&c.BcastIP, "WOLWEBBCASTIP", "WOLWEB_BCASTIP")
	boolean(&c.ReadOnly, "WOLWEBREADONLY", "WOLWEB_READONLY")
	str(&c.DataDir, "WOLWEB_DATA_DIR")
	str(&c.Language, "WOLWEB_LANGUAGE")
	if v, ok := os.LookupEnv("WOLWEB_NETWORKS"); ok {
		c.Discovery.Networks = splitList(v)
	}
	str(&c.Discovery.Interval, "WOLWEB_SCAN_INTERVAL")
	str(&c.Status.Interval, "WOLWEB_STATUS_INTERVAL")
	str(&c.Status.WakeTimeout, "WOLWEB_WAKE_TIMEOUT")
	str(&c.Status.WakeRetry, "WOLWEB_WAKE_RETRY")
	str(&c.Auth.Username, "WOLWEB_USER")
	str(&c.Auth.Password, "WOLWEB_PASSWORD")
	str(&c.Auth.APIToken, "WOLWEB_TOKEN")
	boolean(&c.Auth.PublicWake, "WOLWEB_PUBLIC_WAKE")
	boolean(&c.Auth.WakeKeys, "WOLWEB_WAKE_KEYS")
	str(&c.Notify.Webhook, "WOLWEB_WEBHOOK")
	str(&c.Notify.Ntfy, "WOLWEB_NTFY")
	if v, ok := os.LookupEnv("WOLWEB_NOTIFY_EVENTS"); ok {
		c.Notify.Events = splitList(v)
	}
	if v, ok := os.LookupEnv("WOLWEB_ALLOWED_HOSTS"); ok {
		c.AllowedHosts = splitList(v)
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func (c *Config) normalize() error {
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("ungültiger Port %d", c.Port)
	}
	if l, ok := i18n.Parse(c.Language); ok {
		c.Language = string(l)
	} else if c.Language != "" {
		return fmt.Errorf("language: %q wird nicht unterstützt (de, en)", c.Language)
	} else {
		c.Language = string(i18n.DE)
	}
	v := "/" + strings.Trim(strings.TrimSpace(c.VDir), "/")
	if v != "/" {
		v += "/"
	}
	c.VDir = v // immer mit Schrägstrich am Ende: "/" oder "/wolweb/"
	if c.BcastIP != "" {
		if _, err := NormalizeBroadcast(c.BcastIP); err != nil {
			return fmt.Errorf("bcastip: %w", err)
		}
	}
	for i, n := range c.Discovery.Networks {
		p, err := netip.ParsePrefix(n)
		if err != nil || !p.Addr().Is4() {
			return fmt.Errorf("discovery.networks: %q ist kein IPv4-Netz (CIDR)", n)
		}
		c.Discovery.Networks[i] = p.Masked().String()
	}
	for _, p := range c.Status.Ports {
		if p <= 0 || p > 65535 {
			return fmt.Errorf("status.ports: ungültiger Port %d", p)
		}
	}
	if c.Discovery.MaxHosts <= 0 {
		c.Discovery.MaxHosts = 1024
	}
	for _, d := range []struct {
		name, val string
	}{{"discovery.interval", c.Discovery.Interval}, {"status.interval", c.Status.Interval}, {"status.wake_timeout", c.Status.WakeTimeout}, {"status.wake_retry", c.Status.WakeRetry}} {
		if _, err := ParseDuration(d.val); err != nil {
			return fmt.Errorf("%s: %w", d.name, err)
		}
	}
	for _, u := range []struct{ name, val string }{{"notify.webhook", c.Notify.Webhook}, {"notify.ntfy", c.Notify.Ntfy}} {
		if u.val == "" {
			continue
		}
		if p, err := url.Parse(u.val); err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
			return fmt.Errorf("%s: %q ist keine http(s)-Adresse", u.name, u.val)
		}
	}
	for i, e := range c.Notify.Events {
		e = strings.TrimSpace(e)
		c.Notify.Events[i] = e // so vergleicht notify.Send später auch " wake" richtig
		if !notify.ValidKind(e) {
			return fmt.Errorf("notify.events: unbekanntes Ereignis %q (online, offline, wake, wake_failed)", e)
		}
	}
	if (c.Auth.Username == "") != (c.Auth.Password == "") {
		return errors.New("auth: username und password nur gemeinsam setzen")
	}
	return nil
}

// ParseDuration akzeptiert Go-Dauern ("30s", "5m"); leer oder "0" bedeutet 0.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

func (c Config) Listen() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

func (c Config) AuthEnabled() bool { return c.Auth.Username != "" || c.Auth.APIToken != "" }

// NormalizeBroadcast prüft "ip" oder "ip:port" und ergänzt Port 9.
func NormalizeBroadcast(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		host, port = s, "9"
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Is4() {
		return "", i18n.M("val.not_ipv4", host)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 || p > 65535 {
		return "", i18n.M("val.bad_port", port)
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(p)), nil
}
