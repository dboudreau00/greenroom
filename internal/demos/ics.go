package demos

import (
	"strings"
	"time"
	"unicode/utf8"
)

const icsTime = "20060102T150405Z"

// ICS renders booked, non-cancelled demos as an RFC 5545 calendar: CRLF
// line endings, TEXT values escaped, lines folded at 75 octets, times in
// UTC. now is the DTSTAMP of every event.
func ICS(list []*Demo, now time.Time) []byte {
	var b strings.Builder
	line := func(name, value string) { b.WriteString(fold(name + ":" + value)) }
	line("BEGIN", "VCALENDAR")
	line("VERSION", "2.0")
	line("PRODID", "-//Greenroom//Demos//EN")
	line("CALSCALE", "GREGORIAN")
	line("METHOD", "PUBLISH")
	line("X-WR-CALNAME", "Greenroom demos")
	for _, d := range list {
		if !d.Booked || d.Status == Cancelled {
			continue
		}
		line("BEGIN", "VEVENT")
		line("UID", d.ID+"@greenroom")
		line("DTSTAMP", now.UTC().Format(icsTime))
		line("DTSTART", d.Start.UTC().Format(icsTime))
		line("DTEND", d.End.UTC().Format(icsTime))
		if !d.UpdatedAt.IsZero() {
			line("LAST-MODIFIED", d.UpdatedAt.UTC().Format(icsTime))
		}
		line("SUMMARY", EscapeText(d.Title))
		desc := []string{"Environment: " + d.Env}
		if d.Persona != "" {
			desc = append(desc, "Persona: "+d.SetID())
		}
		if d.Prospect != "" {
			desc = append(desc, "Prospect: "+d.Prospect)
		}
		if d.Brand != "" {
			desc = append(desc, "Brand kit: "+d.Brand)
		}
		desc = append(desc, "Stage: "+d.Stage, "Status: "+d.Status, "Greenroom demo "+d.ID)
		line("DESCRIPTION", EscapeText(strings.Join(desc, "\n")))
		line("CATEGORIES", "Demo")
		line("END", "VEVENT")
	}
	line("END", "VCALENDAR")
	return []byte(b.String())
}

// EscapeText escapes an RFC 5545 TEXT value: backslash, semicolon, comma
// and newline. Carriage returns are dropped.
func EscapeText(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", "")
	return r.Replace(s)
}

// fold splits a content line into chunks of at most 75 octets, never inside
// a UTF-8 sequence; continuation lines start with one space, which counts
// toward their 75.
func fold(s string) string {
	var b strings.Builder
	limit := 75
	for len(s) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		b.WriteString(s[:cut])
		b.WriteString("\r\n ")
		s = s[cut:]
		limit = 74
	}
	b.WriteString(s)
	b.WriteString("\r\n")
	return b.String()
}
