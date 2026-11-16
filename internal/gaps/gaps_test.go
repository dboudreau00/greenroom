package gaps

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

func TestStoreAndIDs(t *testing.T) {
	s := Open(t.TempDir())
	g := &Gap{Title: "Scheduled exports", Kind: "feature", Status: StatusOpen}
	if err := s.Create(g); err != nil {
		t.Fatal(err)
	}
	if !ValidID(g.ID) {
		t.Fatalf("id %q", g.ID)
	}
	got, err := s.Get(g.ID)
	if err != nil || got.Title != "Scheduled exports" {
		t.Fatalf("get: %v %+v", err, got)
	}
	for _, bad := range []string{"", "../x", "gap-123", "gap-0123456z", "GAP-01234567", "gap-01234567/../../x"} {
		if ValidID(bad) {
			t.Fatalf("%q passed the id check", bad)
		}
		if _, err := s.Get(bad); err == nil {
			t.Fatalf("get %q", bad)
		}
	}
	if _, err := s.Update(g.ID, func(g *Gap) error { g.Status = "planned"; return nil }); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].Status != "planned" {
		t.Fatalf("list: %v %+v", err, list)
	}
	if err := s.Delete(g.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatal("not deleted")
	}
}

func TestScoreCountsProspectsOnce(t *testing.T) {
	day := time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)
	g := &Gap{ID: "gap-00000001", Title: "SSO with SCIM", Kind: "security", Status: "open"}
	ms := []Mention{
		{GapID: g.ID, Demo: "d1", Prospect: "Larkspur", Severity: "low", Env: "acme-crm", Persona: "cfo", Start: day},
		{GapID: g.ID, Demo: "d2", Prospect: "larkspur ", Severity: "high", Env: "acme-crm", Persona: "it", Start: day.Add(48 * time.Hour)},
		{GapID: g.ID, Demo: "d3", Prospect: "Tidewell", Severity: "medium", Env: "warehouse", Start: day.Add(24 * time.Hour)},
		{GapID: g.ID, Demo: "d4", Severity: "blocker", Env: "acme-crm", Start: day},
		{GapID: g.ID, Demo: "d5", Severity: "low", Env: "acme-crm", Start: day},
	}
	r := Stats(g, ms)
	// Larkspur max high 3, Tidewell medium 2, two demos without a prospect: 5 and 1.
	if r.Score != 11 || r.Prospects != 4 || r.Mentions != 5 || r.TopSeverity != "blocker" {
		t.Fatalf("stats %+v", r)
	}
	if !r.LastMentioned.Equal(day.Add(48*time.Hour)) || strings.Join(r.Envs, ",") != "acme-crm,warehouse" || strings.Join(r.Personas, ",") != "cfo,it" {
		t.Fatalf("stats %+v %v", r.Envs, r.Personas)
	}
	other := &Gap{ID: "gap-00000002", Title: "Bulk import", Kind: "data", Status: "open"}
	rows := Rows([]*Gap{other, g}, append(ms, Mention{GapID: other.ID, Demo: "d1", Prospect: "Larkspur", Severity: "blocker", Start: day}))
	if rows[0].ID != g.ID || rows[1].Score != 5 {
		t.Fatalf("order %s %d", rows[0].ID, rows[1].Score)
	}
}

func TestSuggestRanksBySimilarity(t *testing.T) {
	list := []*Gap{
		{ID: "gap-00000001", Title: "Scheduled exports to S3", Kind: "feature", Status: "open"},
		{ID: "gap-00000002", Title: "Export the orders report on a schedule", Kind: "feature", Status: "planned"},
		{ID: "gap-00000003", Title: "SAML single sign-on", Kind: "security", Status: "open"},
		{ID: "gap-00000004", Title: "Scheduled exports", Kind: "feature", Status: "done"},
		{ID: "gap-00000005", Title: "Scheduled export", Kind: "feature", Status: "open", MergedInto: "gap-00000001"},
	}
	got := Suggest(list, "Scheduled exports", 5)
	if len(got) != 1 || got[0].ID != "gap-00000001" || got[0].Score != 0.67 {
		t.Fatalf("suggest %+v", got)
	}
	got = Suggest(list, "the ability to schedule an export of reports", 5)
	if len(got) == 0 || got[0].ID != "gap-00000002" {
		t.Fatalf("suggest by words %+v", got)
	}
	if got := Suggest(list, "the and of", 5); len(got) != 0 {
		t.Fatalf("stop words only %+v", got)
	}
	if s := Similarity("Scheduled exports", "scheduled EXPORT"); s != 1 {
		t.Fatalf("plural and case: %v", s)
	}
	if strings.Join(Tokens("Can it do SSO, please?"), " ") != "sso please" {
		t.Fatalf("tokens %q", Tokens("Can it do SSO, please?"))
	}
}

func TestResolveFollowsMerges(t *testing.T) {
	all := map[string]*Gap{
		"gap-00000001": {ID: "gap-00000001", MergedInto: "gap-00000002"},
		"gap-00000002": {ID: "gap-00000002", MergedInto: "gap-00000003"},
		"gap-00000003": {ID: "gap-00000003"},
		"gap-00000004": {ID: "gap-00000004", MergedInto: "gap-00000005"},
		"gap-00000005": {ID: "gap-00000005", MergedInto: "gap-00000004"},
	}
	if Resolve(all, "gap-00000001") != "gap-00000003" || Resolve(all, "gap-00000009") != "gap-00000009" {
		t.Fatal("resolve")
	}
	Resolve(all, "gap-00000004") // a loop must end
	if n := AppendNote(strings.Repeat("x", MaxNotes), "merged"); len([]rune(n)) > MaxNotes || !strings.HasSuffix(n, "merged") {
		t.Fatalf("note cap: %d", len(n))
	}
}

func TestMarkdownEscapes(t *testing.T) {
	day := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	g := &Gap{ID: "gap-0000000a", Title: `Export | pipe \ and <script>`, Kind: "feature", Status: "open", Notes: "line one\nline two"}
	env := &Gap{ID: "gap-0000000b", Title: "Seed data for EMEA", Kind: "environment", Status: "planned", Env: "acme-crm", Module: "billing"}
	ms := []Mention{
		{GapID: g.ID, Demo: "20261112-0900-ab12", Title: "Larkspur | intro", Prospect: "Larkspur", Env: "acme-crm", Start: day, Severity: "high",
			Detail: "Wants CSV\nand XLSX", Quote: "We export every Monday.\nBy hand."},
		{GapID: env.ID, Demo: "20261112-0900-ab12", Title: "Larkspur | intro", Prospect: "Larkspur", Env: "acme-crm", Start: day, Severity: "low"},
	}
	rows := Rows([]*Gap{g, env}, ms)
	md := string(Markdown(rows, ByGap(ms), day, ""))
	for _, want := range []string{
		"# Project gaps\n",
		"| Rank | Gap | Kind | Score | Prospects | Mentions | Status |",
		`| 1 | Export \| pipe \\ and &lt;script> | feature | 3 | 1 | 1 | open |`,
		"\n## Features\n", "\n## Demo environment\n",
		"### 1. Export | pipe \\ and &lt;script>",
		"- Environment: acme-crm, module billing",
		": Wants CSV and XLSX\n",
		"  > We export every Monday.\n  > By hand.\n",
		"> line one\n> line two\n",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "<script>") {
		t.Fatal("raw HTML in the markdown")
	}
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(l, "| ") && strings.Count(strings.ReplaceAll(l, `\|`, ""), "|") != 8 {
			t.Fatalf("table row with a broken column count: %q", l)
		}
	}
}

func TestCSVQuotesAndDefusesFormulas(t *testing.T) {
	g := &Gap{ID: "gap-0000000c", Title: `=HYPERLINK("http://x.example","a, b")`, Kind: "feature", Status: "open", Owner: "+ops", Notes: "two\nlines"}
	h := &Gap{ID: "gap-0000000d", Title: "-1 off", Kind: "data", Status: "open", Owner: "@sam", Notes: "\tindent"}
	out := CSV(Rows([]*Gap{g, h}, nil))
	if !strings.HasPrefix(string(out), strings.Join(CSVHeader, ",")+"\r\n") {
		t.Fatalf("header: %q", out)
	}
	recs, err := csv.NewReader(strings.NewReader(string(out))).ReadAll()
	if err != nil || len(recs) != 3 {
		t.Fatalf("parse: %v %d", err, len(recs))
	}
	byID := map[string][]string{recs[1][1]: recs[1], recs[2][1]: recs[2]}
	if r := byID[g.ID]; r[2] != `'=HYPERLINK("http://x.example","a, b")` || r[12] != "'+ops" || r[15] != "two\nlines" {
		t.Fatalf("formula row: %q", r)
	}
	if r := byID[h.ID]; r[2] != "'-1 off" || r[12] != "'@sam" || r[15] != "'\tindent" {
		t.Fatalf("minus row: %q", r)
	}
	if !strings.Contains(string(out), `"'=HYPERLINK(""http://x.example"",""a, b"")"`) {
		t.Fatalf("quoting: %s", out)
	}
	if SafeCell("\rx") != "'\rx" || SafeCell("plain") != "plain" || SafeCell("") != "" {
		t.Fatal("SafeCell")
	}
}
