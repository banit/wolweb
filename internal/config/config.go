// Package config lädt die Einstellungen: Standardwerte, darüber config.json, darüber die in der
// Oberfläche geänderten (data/settings.json, siehe Manager), darüber Umgebungsvariablen.
// Die Schlüssel host, port, vdir, bcastip und read_only sowie die Variablen WOLWEBHOST,
// WOLWEBPORT, WOLWEBVDIR, WOLWEBBCASTIP und WOLWEBREADONLY entsprechen dem Original-wolweb.
package config

import (
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	// Networks sind die Netze (oft VLANs), die wolweb kennt und – mit Scan – durchsuchen darf.
	Networks []Network `json:"networks"`
	// Interval für den automatischen Hintergrund-Scan, z. B. "30m"; leer oder "0" = aus.
	Interval string `json:"interval"`
	// MaxHosts begrenzt die Anzahl Adressen je Segment.
	MaxHosts int `json:"max_hosts"`
}

// Network ist ein Netzsegment, oft ein VLAN. In config.json genügt "192.168.1.0/24"; mit Namen:
// {"cidr": "192.168.20.0/24", "name": "IoT", "vlan": 20, "scan": false}.
type Network struct {
	CIDR string `json:"cidr"`
	Name string `json:"name,omitempty"`
	// VLAN ist nur zur Anzeige: wolweb taggt selbst nichts, es braucht eine Schnittstelle im Netz.
	VLAN int `json:"vlan,omitempty"`
	// Scan: in der Netzwerksuche durchsuchen (Standard). false = nur anzeigen und zuordnen.
	Scan bool `json:"scan"`
}

func (n *Network) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*n = Network{CIDR: s, Scan: true}
		return nil
	}
	var v struct {
		CIDR string `json:"cidr"`
		Name string `json:"name"`
		VLAN int    `json:"vlan"`
		Scan *bool  `json:"scan"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*n = Network{CIDR: v.CIDR, Name: v.Name, VLAN: v.VLAN, Scan: v.Scan == nil || *v.Scan}
	return nil
}

// MarshalJSON schreibt ein Netz ohne Zusätze kurz als "192.168.1.0/24".
func (n Network) MarshalJSON() ([]byte, error) {
	if n.Name == "" && n.VLAN == 0 && n.Scan {
		return json.Marshal(n.CIDR)
	}
	type plain Network
	return json.Marshal(plain(n))
}

// Label ist der Name des Netzes für die Anzeige: "IoT (VLAN 20)", ohne Namen das CIDR.
func (n Network) Label() string {
	s := n.Name
	if s == "" {
		s = n.CIDR
	}
	if n.VLAN > 0 {
		s += " (VLAN " + strconv.Itoa(n.VLAN) + ")"
	}
	return s
}

// ScanCIDRs sind die Netze, die durchsucht werden dürfen.
func (d Discovery) ScanCIDRs() []string {
	var out []string
	for _, n := range d.Networks {
		if n.Scan {
			out = append(out, n.CIDR)
		}
	}
	return out
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

// clone kopiert auch die Listen – normalize und json.Unmarshal schreiben in vorhandene hinein.
func (c Config) clone() Config {
	c.Discovery.Networks = slices.Clone(c.Discovery.Networks)
	c.Status.Ports = slices.Clone(c.Status.Ports)
	c.Notify.Events = slices.Clone(c.Notify.Events)
	c.AllowedHosts = slices.Clone(c.AllowedHosts)
	return c
}

// Load liest config.json (falls vorhanden) samt data/settings.json und Umgebungsvariablen und
// prüft das Ergebnis. found sagt, ob es config.json gab.
func Load(path string) (Config, bool, error) {
	m, err := Open(path)
	if m == nil {
		return Default(), false, err
	}
	return m.Config(), m.FileFound(), err
}

// envKeys ordnet Umgebungsvariablen den Schlüsseln zu. Gesetzte Variablen haben Vorrang vor
// allem anderen; die Oberfläche zeigt solche Einstellungen gesperrt an.
var envKeys = []struct {
	key  string
	vars []string
}{
	{"host", []string{"WOLWEBHOST", "WOLWEB_HOST"}},
	{"port", []string{"WOLWEBPORT", "WOLWEB_PORT"}},
	{"vdir", []string{"WOLWEBVDIR", "WOLWEB_VDIR"}},
	{"bcastip", []string{"WOLWEBBCASTIP", "WOLWEB_BCASTIP"}},
	{"read_only", []string{"WOLWEBREADONLY", "WOLWEB_READONLY"}},
	{"data_dir", []string{"WOLWEB_DATA_DIR"}},
	{"language", []string{"WOLWEB_LANGUAGE"}},
	{"discovery.networks", []string{"WOLWEB_NETWORKS"}},
	{"discovery.interval", []string{"WOLWEB_SCAN_INTERVAL"}},
	{"status.interval", []string{"WOLWEB_STATUS_INTERVAL"}},
	{"status.wake_timeout", []string{"WOLWEB_WAKE_TIMEOUT"}},
	{"status.wake_retry", []string{"WOLWEB_WAKE_RETRY"}},
	{"auth.username", []string{"WOLWEB_USER"}},
	{"auth.password", []string{"WOLWEB_PASSWORD"}},
	{"auth.api_token", []string{"WOLWEB_TOKEN"}},
	{"auth.public_wake", []string{"WOLWEB_PUBLIC_WAKE"}},
	{"auth.wake_keys", []string{"WOLWEB_WAKE_KEYS"}},
	{"notify.webhook", []string{"WOLWEB_WEBHOOK"}},
	{"notify.ntfy", []string{"WOLWEB_NTFY"}},
	{"notify.events", []string{"WOLWEB_NOTIFY_EVENTS"}},
	{"allowed_hosts", []string{"WOLWEB_ALLOWED_HOSTS"}},
}

// applyEnv wendet gesetzte Umgebungsvariablen an und liefert, welcher Schlüssel von welcher
// Variable kommt (bei mehreren gesetzten gilt die letzte der Liste).
func applyEnv(c *Config) map[string]string {
	locked := map[string]string{}
	for _, e := range envKeys {
		for _, name := range e.vars {
			v, ok := os.LookupEnv(name)
			if !ok {
				continue
			}
			if setEnv(c, e.key, v) {
				locked[e.key] = name
			}
		}
	}
	return locked
}

// setEnv setzt einen Schlüssel aus dem Text einer Umgebungsvariable. Unlesbare Wahrheitswerte
// werden wie bisher übergangen (und sperren dann auch nichts).
func setEnv(c *Config, key, v string) bool {
	str := map[string]*string{
		"host": &c.Host, "vdir": &c.VDir, "bcastip": &c.BcastIP, "data_dir": &c.DataDir, "language": &c.Language,
		"discovery.interval": &c.Discovery.Interval, "status.interval": &c.Status.Interval,
		"status.wake_timeout": &c.Status.WakeTimeout, "status.wake_retry": &c.Status.WakeRetry,
		"auth.username": &c.Auth.Username, "auth.password": &c.Auth.Password, "auth.api_token": &c.Auth.APIToken,
		"notify.webhook": &c.Notify.Webhook, "notify.ntfy": &c.Notify.Ntfy,
	}
	boolean := map[string]*bool{"read_only": &c.ReadOnly, "auth.public_wake": &c.Auth.PublicWake, "auth.wake_keys": &c.Auth.WakeKeys}
	if p, ok := str[key]; ok {
		*p = v
		return true
	}
	if p, ok := boolean[key]; ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false
		}
		*p = b
		return true
	}
	switch key {
	case "port":
		c.Port, _ = strconv.Atoi(v)
	case "discovery.networks":
		c.Discovery.Networks = nil
		for _, n := range splitList(v) {
			c.Discovery.Networks = append(c.Discovery.Networks, Network{CIDR: n, Scan: true})
		}
	case "notify.events":
		c.Notify.Events = splitList(v)
	case "allowed_hosts":
		c.AllowedHosts = splitList(v)
	default:
		return false
	}
	return true
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' }) {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// FieldError ist ein Fehler in einer Einstellung. Field ist der Schlüssel ("discovery.networks"),
// Err die übersetzbare Meldung.
type FieldError struct {
	Field string
	Err   error
}

func (e FieldError) Error() string { return e.Field + ": " + e.Err.Error() }
func (e FieldError) Unwrap() error { return e.Err }

func invalid(field, key string, args ...any) error {
	return FieldError{Field: field, Err: i18n.M(key, args...)}
}

// Untergrenzen, damit sich niemand über die Oberfläche das Netz oder die Geräte zuschüttet.
const (
	minStatusInterval = 5 * time.Second
	minScanInterval   = time.Minute
	minWakeTimeout    = 5 * time.Second
	maxWakeTimeout    = 10 * time.Minute
	minWakeRetry      = time.Second
)

func (c *Config) normalize() error {
	if c.Port <= 0 || c.Port > 65535 {
		return invalid("port", "cfg.port", c.Port)
	}
	if l, ok := i18n.Parse(c.Language); ok {
		c.Language = string(l)
	} else if c.Language != "" {
		return invalid("language", "cfg.language", c.Language)
	} else {
		c.Language = string(i18n.DE)
	}
	v := "/" + strings.Trim(strings.TrimSpace(c.VDir), "/")
	if v != "/" {
		v += "/"
	}
	c.VDir = v // immer mit Schrägstrich am Ende: "/" oder "/wolweb/"
	c.BcastIP = strings.TrimSpace(c.BcastIP)
	if c.BcastIP != "" {
		if _, err := NormalizeBroadcast(c.BcastIP); err != nil {
			return FieldError{Field: "bcastip", Err: err}
		}
	}
	seen := map[string]bool{}
	for i := range c.Discovery.Networks {
		n := &c.Discovery.Networks[i]
		p, err := netip.ParsePrefix(strings.TrimSpace(n.CIDR))
		if err != nil || !p.Addr().Is4() {
			return invalid("discovery.networks", "cfg.network", n.CIDR)
		}
		n.CIDR = p.Masked().String()
		if seen[n.CIDR] {
			return invalid("discovery.networks", "cfg.network_dup", n.CIDR)
		}
		seen[n.CIDR] = true
		n.Name = strings.TrimSpace(n.Name)
		if utf8.RuneCountInString(n.Name) > 40 {
			return invalid("discovery.networks", "cfg.network_name", n.Name)
		}
		if n.VLAN < 0 || n.VLAN > 4094 {
			return invalid("discovery.networks", "cfg.vlan", n.VLAN)
		}
	}
	for _, p := range c.Status.Ports {
		if p <= 0 || p > 65535 {
			return invalid("status.ports", "cfg.status_port", p)
		}
	}
	if c.Discovery.MaxHosts <= 0 {
		c.Discovery.MaxHosts = 1024
	}
	for _, d := range []struct {
		key      string
		val      *string
		min, max time.Duration
	}{
		{"discovery.interval", &c.Discovery.Interval, minScanInterval, 0},
		{"status.interval", &c.Status.Interval, minStatusInterval, 0},
		{"status.wake_timeout", &c.Status.WakeTimeout, minWakeTimeout, maxWakeTimeout},
		{"status.wake_retry", &c.Status.WakeRetry, minWakeRetry, 0},
	} {
		*d.val = strings.TrimSpace(*d.val)
		t, err := ParseDuration(*d.val)
		if err != nil || t < 0 {
			return invalid(d.key, "cfg.duration", *d.val)
		}
		if t > 0 && t < d.min {
			return invalid(d.key, "cfg.duration_min", *d.val, d.min.String())
		}
		if d.max > 0 && t > d.max {
			return invalid(d.key, "cfg.duration_max", *d.val, d.max.String())
		}
	}
	for _, u := range []struct {
		key string
		val *string
	}{{"notify.webhook", &c.Notify.Webhook}, {"notify.ntfy", &c.Notify.Ntfy}} {
		*u.val = strings.TrimSpace(*u.val)
		if err := CheckURL(u.key, *u.val); err != nil {
			return err
		}
	}
	for i, e := range c.Notify.Events {
		e = strings.TrimSpace(e)
		c.Notify.Events[i] = e // so vergleicht notify.Send später auch " wake" richtig
		if !notify.ValidKind(e) {
			return invalid("notify.events", "cfg.event", e)
		}
	}
	hosts := c.AllowedHosts[:0]
	for _, h := range c.AllowedHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h == "" {
			continue
		}
		if strings.ContainsAny(h, "/: ") {
			return invalid("allowed_hosts", "cfg.host", h)
		}
		hosts = append(hosts, h)
	}
	c.AllowedHosts = hosts
	if (c.Auth.Username == "") != (c.Auth.Password == "") {
		return invalid("auth.username", "cfg.auth_pair")
	}
	return nil
}

// CheckURL prüft eine Adresse für Benachrichtigungen (leer ist erlaubt).
func CheckURL(field, v string) error {
	if v == "" {
		return nil
	}
	if p, err := url.Parse(v); err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		return invalid(field, "cfg.url", v)
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

// Durations liefert die Dauern der Konfiguration (geprüft durch normalize).
func (c Config) Durations() (scan, status, wakeTimeout, wakeRetry time.Duration) {
	scan, _ = ParseDuration(c.Discovery.Interval)
	status, _ = ParseDuration(c.Status.Interval)
	wakeTimeout, _ = ParseDuration(c.Status.WakeTimeout)
	if wakeTimeout <= 0 {
		wakeTimeout = 3 * time.Minute
	}
	wakeRetry, _ = ParseDuration(c.Status.WakeRetry)
	return
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
