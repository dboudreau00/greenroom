package gaps

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// Mention is one debrief's reference to a gap, with the demo it came from.
// The engine derives mentions from demo files on every read.
type Mention struct {
	GapID    string    `json:"gapId"`
	Demo     string    `json:"demo"`
	Title    string    `json:"title"`
	Prospect string    `json:"prospect,omitempty"`
	Brand    string    `json:"brand,omitempty"`
	Env      string    `json:"env"`
	Persona  string    `json:"persona,omitempty"`
	Start    time.Time `json:"start"`
	Severity string    `json:"severity"`
	Detail   string    `json:"detail,omitempty"`
	Quote    string    `json:"quote,omitempty"`
}

// ProspectKey groups mentions by prospect, case-insensitively. A demo with
// no prospect counts as a prospect of its own.
func ProspectKey(m Mention) string {
	if p := strings.ToLower(strings.TrimSpace(m.Prospect)); p != "" {
		return p
	}
	return "demo:" + m.Demo
}

// Row is a gap on the board with the stats derived from its mentions.
type Row struct {
	*Gap
	Score         int        `json:"score"`
	Mentions      int        `json:"mentions"`
	Prospects     int        `json:"prospects"`
	TopSeverity   string     `json:"topSeverity,omitempty"`
	LastMentioned *time.Time `json:"lastMentioned,omitempty"`
	Envs          []string   `json:"environments"`
	Personas      []string   `json:"personas"`
}

// Stats fills a row from the mentions of its gap. The score is the sum,
// over distinct prospects, of the highest severity weight each one gave.
func Stats(g *Gap, mentions []Mention) Row {
	r := Row{Gap: g, Envs: []string{}, Personas: []string{}}
	best := map[string]int{}
	envs, personas := map[string]bool{}, map[string]bool{}
	top := 0
	for _, m := range mentions {
		r.Mentions++
		w := Weight(m.Severity)
		k := ProspectKey(m)
		if cur, ok := best[k]; !ok || w > cur {
			best[k] = w
		}
		if w > top {
			top, r.TopSeverity = w, m.Severity
		}
		if r.LastMentioned == nil || m.Start.After(*r.LastMentioned) {
			t := m.Start
			r.LastMentioned = &t
		}
		if m.Env != "" && !envs[m.Env] {
			envs[m.Env] = true
			r.Envs = append(r.Envs, m.Env)
		}
		if m.Persona != "" && !personas[m.Persona] {
			personas[m.Persona] = true
			r.Personas = append(r.Personas, m.Persona)
		}
	}
	for _, w := range best {
		r.Score += w
	}
	r.Prospects = len(best)
	sort.Strings(r.Envs)
	sort.Strings(r.Personas)
	return r
}

// ByGap groups mentions by the gap they point at.
func ByGap(mentions []Mention) map[string][]Mention {
	out := map[string][]Mention{}
	for _, m := range mentions {
		out[m.GapID] = append(out[m.GapID], m)
	}
	return out
}

// Rows builds a row per gap and sorts the board: score, then prospects,
// then mentions, then the latest mention, then the title.
func Rows(list []*Gap, mentions []Mention) []Row {
	by := ByGap(mentions)
	out := make([]Row, 0, len(list))
	for _, g := range list {
		out = append(out, Stats(g, by[g.ID]))
	}
	Sort(out)
	return out
}

// Sort orders rows the way the board shows them by default.
func Sort(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Prospects != b.Prospects {
			return a.Prospects > b.Prospects
		}
		if a.Mentions != b.Mentions {
			return a.Mentions > b.Mentions
		}
		la, lb := lastOf(a), lastOf(b)
		if !la.Equal(lb) {
			return la.After(lb)
		}
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	})
}

func lastOf(r Row) time.Time {
	if r.LastMentioned == nil {
		return time.Time{}
	}
	return *r.LastMentioned
}

// Suggestion is a gap that looks like the title someone is typing.
type Suggestion struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Kind   string  `json:"kind"`
	Status string  `json:"status"`
	Env    string  `json:"env,omitempty"`
	Module string  `json:"module,omitempty"`
	Score  float64 `json:"score"`
}

// SuggestThreshold is the least similarity a suggestion needs.
const SuggestThreshold = 0.3

// Suggest returns active, unmerged gaps whose titles share enough words
// with q, most similar first, at most limit of them.
func Suggest(list []*Gap, q string, limit int) []Suggestion {
	out := []Suggestion{}
	qt := tokenSet(q)
	if len(qt) == 0 {
		return out
	}
	for _, g := range list {
		if g.MergedInto != "" || !Active(g.Status) {
			continue
		}
		s := jaccard(qt, tokenSet(g.Title))
		if s < SuggestThreshold {
			continue
		}
		out = append(out, Suggestion{ID: g.ID, Title: g.Title, Kind: g.Kind, Status: g.Status, Env: g.Env, Module: g.Module, Score: float64(int(s*100+0.5)) / 100})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Similarity is the Jaccard overlap of the normalized words of a and b.
func Similarity(a, b string) float64 { return jaccard(tokenSet(a), tokenSet(b)) }

var stopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be but by can could do does for from has have how i if in into is it its
		of on or our so than that the their them then there they this to too us was we were what when where which who will with
		would you your no not need needs want wants should able ask asked support`) {
		stopWords[w] = true
	}
}

// Tokens lowercases s, splits it on anything but letters and digits, drops
// stop words and one-letter words, and trims a plural s.
func Tokens(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) < 2 || stopWords[w] {
			continue
		}
		if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = strings.TrimSuffix(w, "s")
		}
		out = append(out, w)
	}
	return out
}

func tokenSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, t := range Tokens(s) {
		m[t] = true
	}
	return m
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}
