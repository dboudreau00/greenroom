package gaps

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Markdown renders rows as a spec-ready document: a summary table ranked by
// score, then one section per gap grouped by kind, with every mention's
// detail and quote. by holds the mentions of each gap.
func Markdown(rows []Row, by map[string][]Mention, now time.Time, scope string) []byte {
	var b bytes.Buffer
	b.WriteString("# Project gaps\n\n")
	fmt.Fprintf(&b, "Generated %s from Greenroom demo debriefs", now.UTC().Format("2006-01-02 15:04 UTC"))
	if scope != "" {
		fmt.Fprintf(&b, ", status %s", mdInline(scope))
	}
	fmt.Fprintf(&b, ". %d gap%s. Score: per prospect, the highest severity they gave (low 1, medium 2, high 3, blocker 5), summed over prospects.\n\n", len(rows), plural(len(rows)))
	if len(rows) == 0 {
		b.WriteString("No gaps.\n")
		return b.Bytes()
	}
	rank := map[string]int{}
	b.WriteString("## Summary\n\n")
	b.WriteString("| Rank | Gap | Kind | Score | Prospects | Mentions | Status |\n")
	b.WriteString("| ---: | --- | --- | ---: | ---: | ---: | --- |\n")
	for i, r := range rows {
		rank[r.ID] = i + 1
		fmt.Fprintf(&b, "| %d | %s | %s | %d | %d | %d | %s |\n", i+1, mdCell(r.Title), mdCell(r.Kind), r.Score, r.Prospects, r.Mentions, mdCell(r.Status))
	}
	for _, kind := range kindOrder(rows) {
		fmt.Fprintf(&b, "\n## %s\n", mdInline(kindTitle(kind)))
		for _, r := range rows {
			if r.Kind != kind {
				continue
			}
			writeGap(&b, r, rank[r.ID], by[r.ID])
		}
	}
	return b.Bytes()
}

func writeGap(b *bytes.Buffer, r Row, rank int, mentions []Mention) {
	fmt.Fprintf(b, "\n### %d. %s\n\n", rank, mdInline(r.Title))
	fmt.Fprintf(b, "- Id: `%s`\n", r.ID)
	status := "- Status: " + mdInline(r.Status)
	if r.Owner != "" {
		status += ", owner " + mdInline(r.Owner)
	}
	b.WriteString(status + "\n")
	last := "never"
	if r.LastMentioned != nil {
		last = r.LastMentioned.UTC().Format("2006-01-02")
	}
	fmt.Fprintf(b, "- Score %d from %d prospect%s, %d mention%s, last mentioned %s\n", r.Score, r.Prospects, plural(r.Prospects), r.Mentions, plural(r.Mentions), last)
	if r.Env != "" {
		where := mdInline(r.Env)
		if r.Module != "" {
			where += ", module " + mdInline(r.Module)
		}
		fmt.Fprintf(b, "- Environment: %s\n", where)
	}
	if len(r.Envs) > 0 {
		fmt.Fprintf(b, "- Demoed on: %s\n", mdInline(strings.Join(r.Envs, ", ")))
	}
	if len(r.Personas) > 0 {
		fmt.Fprintf(b, "- Personas: %s\n", mdInline(strings.Join(r.Personas, ", ")))
	}
	if r.Notes != "" {
		b.WriteString("\nNotes:\n\n")
		mdQuote(b, "", r.Notes)
	}
	if len(mentions) == 0 {
		return
	}
	b.WriteString("\nMentions:\n\n")
	for _, m := range mentions {
		who := m.Prospect
		if who == "" {
			who = "no prospect"
		}
		fmt.Fprintf(b, "- **%s**, %s, %s, demo `%s` (%s) on %s", mdInline(who), mdInline(m.Severity), m.Start.UTC().Format("2006-01-02"), m.Demo, mdInline(m.Title), mdInline(m.Env))
		if m.Detail != "" {
			b.WriteString(": " + mdInline(m.Detail))
		}
		b.WriteString("\n")
		if m.Quote != "" {
			b.WriteString("\n")
			mdQuote(b, "  ", m.Quote)
			b.WriteString("\n")
		}
	}
}

func kindOrder(rows []Row) []string {
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Kind] = true
	}
	var out []string
	for _, k := range Kinds {
		if seen[k] {
			out = append(out, k)
			delete(seen, k)
		}
	}
	for _, r := range rows {
		if seen[r.Kind] {
			out = append(out, r.Kind)
			delete(seen, r.Kind)
		}
	}
	return out
}

func kindTitle(k string) string {
	switch k {
	case "feature":
		return "Features"
	case "data":
		return "Data"
	case "integration":
		return "Integrations"
	case "environment":
		return "Demo environment"
	case "content":
		return "Content"
	case "performance":
		return "Performance"
	case "security":
		return "Security"
	case "other":
		return "Other"
	}
	return k
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// mdInline keeps free text on one line and stops it from opening HTML.
func mdInline(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "<", "&lt;").Replace(s)
	return strings.TrimSpace(s)
}

// mdCell is mdInline for a table cell: backslashes and pipes are escaped
// so a value can not end its cell or start a new column.
func mdCell(s string) string {
	return strings.NewReplacer(`\`, `\\`, "|", `\|`).Replace(mdInline(s))
}

// mdQuote writes s as a block quote, one quoted line per line of text.
func mdQuote(b *bytes.Buffer, indent, s string) {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "<", "&lt;")
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			b.WriteString(indent + ">\n")
			continue
		}
		b.WriteString(indent + "> " + line + "\n")
	}
}

// CSVHeader is the first row of the CSV export.
var CSVHeader = []string{"rank", "id", "title", "kind", "status", "score", "prospects", "mentions", "top_severity", "last_mentioned",
	"environments", "personas", "owner", "env", "module", "notes"}

// CSV renders one row per gap with its stats: RFC 4180 quoting and CRLF
// line ends, and every cell that a spreadsheet would read as a formula
// prefixed with a single quote.
func CSV(rows []Row) []byte {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	w.UseCRLF = true
	w.Write(CSVHeader)
	for i, r := range rows {
		last := ""
		if r.LastMentioned != nil {
			last = r.LastMentioned.UTC().Format(time.RFC3339)
		}
		rec := []string{strconv.Itoa(i + 1), r.ID, r.Title, r.Kind, r.Status, strconv.Itoa(r.Score), strconv.Itoa(r.Prospects), strconv.Itoa(r.Mentions),
			r.TopSeverity, last, strings.Join(r.Envs, ";"), strings.Join(r.Personas, ";"), r.Owner, r.Env, r.Module, r.Notes}
		for k := range rec {
			rec[k] = SafeCell(rec[k])
		}
		w.Write(rec)
	}
	w.Flush()
	return b.Bytes()
}

// SafeCell defuses a spreadsheet formula: a cell starting with =, +, -, @,
// a tab or a carriage return gets a leading single quote.
func SafeCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
