// Package i18n übersetzt Meldungen und Oberfläche (Deutsch, Englisch).
//
// Die Texte stehen in de.json und en.json, gemeinsam für Server und Browser. Platzhalter heißen
// {0}, {1} … – in Go wie in JavaScript.
package i18n

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type Lang string

const (
	DE Lang = "de"
	EN Lang = "en"
)

// Langs sind alle unterstützten Sprachen.
var Langs = []Lang{DE, EN}

// CookieName speichert die in der Oberfläche gewählte Sprache.
const CookieName = "wolweb_lang"

//go:embed de.json en.json
var files embed.FS

var catalogs = map[Lang]map[string]string{}

func init() {
	for _, l := range Langs {
		b, err := files.ReadFile(string(l) + ".json")
		if err != nil {
			panic(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			panic(fmt.Sprintf("i18n/%s.json: %v", l, err))
		}
		catalogs[l] = m
	}
}

// Parse erkennt "de", "de-DE", "en_US" usw.
func Parse(s string) (Lang, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, l := range Langs {
		if s == string(l) || strings.HasPrefix(s, string(l)+"-") || strings.HasPrefix(s, string(l)+"_") {
			return l, true
		}
	}
	return "", false
}

// FromRequest wählt die Sprache einer Anfrage: ?lang=, dann das Cookie des Umschalters, dann
// Accept-Language (erste unterstützte Sprache), sonst def.
func FromRequest(r *http.Request, def Lang) Lang {
	if r == nil {
		return def
	}
	if l, ok := Parse(r.URL.Query().Get("lang")); ok {
		return l
	}
	if c, err := r.Cookie(CookieName); err == nil {
		if l, ok := Parse(c.Value); ok {
			return l
		}
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, _, _ := strings.Cut(part, ";")
		if l, ok := Parse(tag); ok {
			return l
		}
	}
	return def
}

// T übersetzt key in l. Fehlt er dort, gilt Deutsch, sonst der Schlüssel selbst. Argumente, die
// selbst übersetzbar sind (Msg, Fehler mit Msg), werden in l übersetzt.
func T(l Lang, key string, args ...any) string {
	s, ok := catalogs[l][key]
	if !ok {
		if s, ok = catalogs[DE][key]; !ok {
			s = key
		}
	}
	for i, a := range args {
		s = strings.ReplaceAll(s, "{"+strconv.Itoa(i)+"}", Text(l, a))
	}
	return s
}

// Catalog liefert alle Texte einer Sprache (für die Oberfläche).
func Catalog(l Lang) map[string]string { return catalogs[l] }

// Text macht aus einem Wert Text in l – übersetzbare Meldungen und Fehler werden übersetzt.
func Text(l Lang, v any) string {
	switch x := v.(type) {
	case Msg:
		return x.In(l)
	case interface{ In(Lang) string }: // z. B. Typen, die eine Msg einbetten
		return x.In(l)
	case error:
		var m Msg
		if errors.As(x, &m) {
			return m.In(l)
		}
		return x.Error()
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

// Msg ist eine noch nicht übersetzte Meldung. Sie ist auch ein error (Text auf Deutsch), damit
// sie durch Fehlerketten wandern und erst bei der Antwort übersetzt werden kann.
type Msg struct {
	Key  string
	Args []any
}

// M baut eine Meldung.
func M(key string, args ...any) Msg { return Msg{Key: key, Args: args} }

func (m Msg) In(l Lang) string { return T(l, m.Key, m.Args...) }

func (m Msg) Error() string { return m.In(DE) }

// Unwrap gibt eingebettete Fehler frei (errors.Is/As funktionieren durch die Meldung hindurch).
func (m Msg) Unwrap() []error {
	var out []error
	for _, a := range m.Args {
		if e, ok := a.(error); ok {
			out = append(out, e)
		}
	}
	return out
}

// ErrorText übersetzt einen Fehler; mit errors.Join verbundene Fehler stehen je in einer Zeile.
func ErrorText(l Lang, err error) string {
	if err == nil {
		return ""
	}
	var m Msg
	if errors.As(err, &m) && fmt.Sprint(err) == m.Error() {
		return m.In(l)
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var parts []string
		for _, e := range j.Unwrap() {
			parts = append(parts, ErrorText(l, e))
		}
		return strings.Join(parts, "\n")
	}
	return err.Error()
}
