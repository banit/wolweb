package web

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// metrics liefert Kennzahlen im Textformat von Prometheus (auch für Grafana, Home Assistant …).
// Mit Anmeldung braucht der Abruf sie ebenfalls – Prometheus kann ein Bearer-Token schicken.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	head := func(name, help, typ string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
	}
	head("wolweb_info", "wolweb version.", "gauge")
	fmt.Fprintf(&b, "wolweb_info{version=\"%s\"} 1\n", escapeLabel(s.version))

	devices, _ := s.store.Devices()
	states := s.monitor.All()
	labels := func(id, name, mac string) string {
		return fmt.Sprintf(`id="%s",name="%s",mac="%s"`, escapeLabel(id), escapeLabel(name), escapeLabel(mac))
	}
	head("wolweb_device_up", "1 = online, 0 = offline (devices without a known IP are missing).", "gauge")
	for _, d := range devices {
		if st, ok := states[d.ID]; ok && st.State != "unknown" {
			up := 0
			if st.State == "online" {
				up = 1
			}
			fmt.Fprintf(&b, "wolweb_device_up{%s} %d\n", labels(d.ID, d.Name, d.MAC), up)
		}
	}
	head("wolweb_device_rtt_seconds", "Response time of the last successful check.", "gauge")
	for _, d := range devices {
		if st, ok := states[d.ID]; ok && st.State == "online" {
			fmt.Fprintf(&b, "wolweb_device_rtt_seconds{%s} %g\n", labels(d.ID, d.Name, d.MAC), float64(st.RTTMillis)/1000)
		}
	}
	head("wolweb_device_last_seen_timestamp_seconds", "Last seen online (Unix time).", "gauge")
	for _, d := range devices {
		if t := s.store.LastSeen(d.ID); !t.IsZero() {
			fmt.Fprintf(&b, "wolweb_device_last_seen_timestamp_seconds{%s} %d\n", labels(d.ID, d.Name, d.MAC), t.Unix())
		}
	}
	head("wolweb_device_last_wake_timestamp_seconds", "Last successful wake (Unix time).", "gauge")
	for _, d := range devices {
		if t := s.store.LastWake(d.ID); !t.IsZero() {
			fmt.Fprintf(&b, "wolweb_device_last_wake_timestamp_seconds{%s} %d\n", labels(d.ID, d.Name, d.MAC), t.Unix())
		}
	}
	head("wolweb_wakes_total", "Wake attempts since wolweb started.", "counter")
	names := map[string]devLabel{}
	for _, d := range devices {
		names[d.ID] = devLabel{d.Name, d.MAC}
	}
	s.wakesMu.Lock()
	keys := make([][2]string, 0, len(s.wakes))
	for k := range s.wakes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, k := range keys {
		if n, ok := names[k[0]]; ok {
			fmt.Fprintf(&b, "wolweb_wakes_total{%s,result=\"%s\"} %d\n", labels(k[0], n.name, n.mac), k[1], s.wakes[k])
		}
	}
	s.wakesMu.Unlock()

	counts := map[string]int{}
	for _, f := range s.store.FoundList() {
		switch {
		case f.DeviceID != "":
			counts["known"]++
		case f.Ignored:
			counts["ignored"]++
		default:
			counts["new"]++
		}
	}
	head("wolweb_discovery_found", "Network scan results (new = not added yet).", "gauge")
	for _, k := range []string{"new", "known", "ignored"} {
		fmt.Fprintf(&b, "wolweb_discovery_found{kind=\"%s\"} %d\n", k, counts[k])
	}
	if j := s.scanner.Job(); !j.FinishedAt.IsZero() {
		head("wolweb_discovery_last_scan_timestamp_seconds", "End of the last scan (Unix time).", "gauge")
		fmt.Fprintf(&b, "wolweb_discovery_last_scan_timestamp_seconds %d\n", j.FinishedAt.Unix())
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

type devLabel struct{ name, mac string }

// escapeLabel maskiert Werte für Prometheus-Labels (Backslash, Anführungszeichen, Zeilenumbruch).
func escapeLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}
