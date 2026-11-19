package store

import (
	"net/url"
	"strings"
	"unicode"
)

// MailSettings: SMTP-Server für "Passwort vergessen" und Einladungen (state.json, nie im Export).
type MailSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"` // starttls, tls, none
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	From     string `json:"from"`
	// Link ist die Adresse, unter der Benutzer wolweb öffnen – daraus entstehen die Links in den Mails.
	// Fest eingetragen statt aus der Anfrage genommen: Sonst bestimmte der Aufrufer, wohin der Link zeigt.
	Link string `json:"link"`
}

// Ready: Es lassen sich Mails verschicken.
func (m MailSettings) Ready() bool { return m.Host != "" && m.From != "" && m.Link != "" }

func noSpace(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

// validMail prüft die Angaben. Leerer Server = Mailversand aus (dann ist alles andere egal).
func validMail(m MailSettings) (MailSettings, error) {
	m.Host, m.Username, m.From, m.Link = strings.TrimSpace(m.Host), strings.TrimSpace(m.Username), strings.TrimSpace(m.From), strings.TrimSpace(m.Link)
	if m.Host == "" {
		return MailSettings{}, nil
	}
	if !noSpace(m.Host) || len(m.Host) > 253 {
		return m, invalid("host", "val.mail_host")
	}
	switch m.Security {
	case "starttls", "tls", "none":
	case "":
		m.Security = "starttls"
	default:
		return m, invalid("security", "val.mail_host")
	}
	if m.Port == 0 {
		m.Port = map[string]int{"starttls": 587, "tls": 465, "none": 25}[m.Security]
	}
	if m.Port < 1 || m.Port > 65535 {
		return m, invalid("port", "val.mail_port")
	}
	if m.From == "" && strings.Contains(m.Username, "@") {
		m.From = m.Username
	}
	if _, err := validEmail(addressOf(m.From)); err != nil || m.From == "" || strings.ContainsAny(m.From, "\r\n") {
		return m, invalid("from", "val.mail_from")
	}
	if strings.ContainsAny(m.Username, "\r\n") || strings.ContainsAny(m.Password, "\r\n") || len(m.Username) > 254 || len(m.Password) > 254 {
		return m, invalid("username", "val.mail_login")
	}
	u, err := url.Parse(m.Link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return m, invalid("link", "val.mail_link")
	}
	if !strings.HasSuffix(m.Link, "/") {
		m.Link += "/"
	}
	return m, nil
}

func addressOf(from string) string {
	if i, j := strings.LastIndex(from, "<"), strings.LastIndex(from, ">"); i >= 0 && j > i {
		return strings.TrimSpace(from[i+1 : j])
	}
	return strings.TrimSpace(from)
}

// Mail liefert die Einstellungen für den Mailversand.
func (s *Store) Mail() MailSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state.Mail == nil {
		return MailSettings{}
	}
	return *s.state.Mail
}

// SetMail speichert die Einstellungen. Ein leeres Passwort behält das bisherige (die Oberfläche
// bekommt es nie zurück), solange Server und Benutzername gleich bleiben.
func (s *Store) SetMail(m MailSettings) (MailSettings, error) {
	m, err := validMail(m)
	if err != nil {
		return MailSettings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.state.Mail
	if m.Password == "" && prev != nil && prev.Host == m.Host && prev.Username == m.Username {
		m.Password = prev.Password
	}
	if m.Host == "" {
		s.state.Mail = nil
	} else {
		s.state.Mail = &m
	}
	if err := s.saveState(); err != nil {
		s.state.Mail = prev
		return MailSettings{}, err
	}
	return m, nil
}
