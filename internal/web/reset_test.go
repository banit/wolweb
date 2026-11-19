package web

import (
	"bufio"
	"io"
	"mime/quotedprintable"
	"net"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"wolweb/internal/config"
)

// smtpSink ist ein winziger Mailserver ohne Verschlüsselung: Er nimmt alles an und liefert den Text
// jeder Mail (dekodiert) über den Kanal.
func smtpSink(t *testing.T) (port string, mails chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	mails = make(chan string, 10)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				io.WriteString(c, "220 sink\r\n")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
					case strings.HasPrefix(cmd, "DATA"):
						io.WriteString(c, "354 go\r\n")
						var b strings.Builder
						for {
							l, err := r.ReadString('\n')
							if err != nil || l == ".\r\n" {
								break
							}
							b.WriteString(l)
						}
						_, body, _ := strings.Cut(b.String(), "\r\n\r\n")
						dec, _ := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
						mails <- string(dec)
						io.WriteString(c, "250 ok\r\n")
					case strings.HasPrefix(cmd, "QUIT"):
						io.WriteString(c, "221 bye\r\n")
						return
					default:
						io.WriteString(c, "250 ok\r\n")
					}
				}
			}()
		}
	}()
	_, port, _ = net.SplitHostPort(ln.Addr().String())
	return port, mails
}

func waitMail(t *testing.T, mails chan string) string {
	t.Helper()
	select {
	case m := <-mails:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("keine Mail angekommen")
	}
	return ""
}

var linkRE = regexp.MustCompile(`http://wol\.test/reset#token=([0-9a-f.]+)`)

func TestPasswordResetByMail(t *testing.T) {
	port, mails := smtpSink(t)
	h, _ := newTestServer(t, func(c *config.Config) {})
	admin := &session{t: t, h: h}
	admin.want(http.StatusCreated, "POST", "/api/v1/users", `{"name":"chef","email":"chef@example.org","password":"chef-passwort"}`)
	bob := &session{t: t, h: h}

	// Ohne Mailserver gibt es kein Zurücksetzen.
	bob.want(http.StatusConflict, "POST", "/api/v1/password-reset", `{"name":"chef"}`)
	if strings.Contains(string(bob.want(http.StatusOK, "GET", "/login", "")), `href="reset"`) {
		t.Fatal("Link ohne Mailserver")
	}
	admin.want(http.StatusBadRequest, "PUT", "/api/v1/mail", `{"host":"127.0.0.1","port":`+port+`,"security":"none","from":"wolweb@example.org","link":"kein-link"}`)
	if v := string(admin.want(http.StatusOK, "PUT", "/api/v1/mail", `{"host":"127.0.0.1","port":`+port+`,"security":"none","from":"wolweb@example.org","link":"http://wol.test"}`)); strings.Contains(v, `"password":"x`) {
		t.Fatalf("Mail-Einstellungen: %s", v)
	}
	bob.want(http.StatusUnauthorized, "GET", "/api/v1/mail", "")
	if !strings.Contains(string(bob.want(http.StatusOK, "GET", "/login", "")), `href="reset"`) {
		t.Fatal("kein Link „Passwort vergessen?“")
	}
	admin.want(http.StatusOK, "POST", "/api/v1/mail/test", "")
	waitMail(t, mails)

	// Einladung: Benutzer mit E-Mail, ohne Passwort.
	if v := string(admin.want(http.StatusCreated, "POST", "/api/v1/users", `{"name":"bob","email":"bob@example.org"}`)); !strings.Contains(v, `"invited":true`) {
		t.Fatalf("keine Einladung: %s", v)
	}
	m := linkRE.FindStringSubmatch(waitMail(t, mails))
	if m == nil {
		t.Fatal("Einladung ohne Link")
	}
	bob.want(http.StatusOK, "POST", "/api/v1/password-reset/confirm", `{"token":"`+m[1]+`","password":"bob-passwort"}`)
	bob.want(http.StatusOK, "GET", "/api/v1/account", "") // gleich angemeldet
	bob.want(http.StatusForbidden, "POST", "/api/v1/password-reset/confirm", `{"token":"`+m[1]+`","password":"noch-eins-1"}`)

	// Passwort vergessen: unbekannte Namen bekommen dieselbe Antwort, aber keine Mail.
	anon := &session{t: t, h: h}
	anon.want(http.StatusOK, "POST", "/api/v1/password-reset", `{"name":"niemand"}`)
	anon.want(http.StatusOK, "POST", "/api/v1/password-reset", `{"name":"BOB@example.org"}`)
	m = linkRE.FindStringSubmatch(waitMail(t, mails))
	if m == nil {
		t.Fatal("Mail ohne Link")
	}
	anon.want(http.StatusOK, "POST", "/api/v1/password-reset", `{"name":"bob"}`) // zu bald: keine zweite Mail
	select {
	case <-mails:
		t.Fatal("zweite Mail trotz Pause")
	case <-time.After(300 * time.Millisecond):
	}
	anon.want(http.StatusBadRequest, "POST", "/api/v1/password-reset/confirm", `{"token":"`+m[1]+`","password":"kurz"}`)
	anon.want(http.StatusOK, "POST", "/api/v1/password-reset/confirm", `{"token":"`+m[1]+`","password":"neues-passwort"}`)
	bob.want(http.StatusUnauthorized, "GET", "/api/v1/account", "") // alte Sitzung abgemeldet
	(&session{t: t, h: h}).want(http.StatusOK, "POST", "/api/v1/login", `{"name":"bob","password":"neues-passwort"}`)
	anon.want(http.StatusForbidden, "POST", "/api/v1/password-reset/confirm", `{"token":"kaputt","password":"neues-passwort"}`)
}
