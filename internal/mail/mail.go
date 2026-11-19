// Package mail verschickt einfache Text-Mails über einen SMTP-Server (Passwort zurücksetzen,
// Einladungen). Nur die Standardbibliothek: STARTTLS (meist Port 587), TLS (Port 465) oder
// unverschlüsselt (nur im eigenen Netz sinnvoll, dann ohne Anmeldung).
package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Verschlüsselung der Verbindung.
const (
	StartTLS = "starttls"
	TLS      = "tls"
	None     = "none"
)

type Server struct {
	Host     string
	Port     int
	Security string
	Username string
	Password string
	From     string
}

// Timeout gilt für die ganze Zustellung an den Server.
var Timeout = 20 * time.Second

func clean(s string) bool { return !strings.ContainsAny(s, "\r\n") }

// Send schickt eine Text-Mail an to.
func Send(ctx context.Context, s Server, to, subject, body string) error {
	if s.Host == "" || s.From == "" {
		return errors.New("mail server not configured")
	}
	if !clean(to) || !clean(s.From) || !clean(subject) {
		return errors.New("invalid address or subject")
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	tlsConf := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	if s.Security == TLS {
		tc := tls.Client(conn, tlsConf)
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return err
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if err := c.Hello(helloName()); err != nil {
		return err
	}
	if s.Security == StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("server does not offer STARTTLS – choose TLS (port 465) or check the port")
		}
		if err := c.StartTLS(tlsConf); err != nil {
			return err
		}
	}
	if s.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("server does not offer a login (AUTH)")
		}
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(address(s.From)); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(message(s.From, to, subject, body)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// address holt die reine Adresse aus "Name <adresse>".
func address(from string) string {
	if i, j := strings.LastIndex(from, "<"), strings.LastIndex(from, ">"); i >= 0 && j > i {
		return strings.TrimSpace(from[i+1 : j])
	}
	return strings.TrimSpace(from)
}

func helloName() string {
	return "wolweb.localdomain"
}

func message(from, to, subject, body string) []byte {
	var b strings.Builder
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := "wolweb"
	if at := strings.LastIndex(address(from), "@"); at >= 0 {
		domain = address(from)[at+1:]
	}
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), domain)
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	_ = qp.Close()
	return []byte(b.String())
}
