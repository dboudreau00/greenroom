package seeds

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Diff limits. A file larger than MaxDiffInput is diffed on its first
// MaxDiffInput bytes; a file diff stops after MaxDiffLines output lines.
const (
	MaxDiffInput   = 256 * 1024
	MaxDiffLines   = 2000
	diffContext    = 3
	binarySniffLen = 8 * 1024
)

// File statuses in a Diff.
const (
	FileAdded     = "added"
	FileRemoved   = "removed"
	FileChanged   = "changed"
	FileUnchanged = "unchanged"
)

// SetDiff compares two seed sets of one environment.
type SetDiff struct {
	From      string       `json:"from"`
	To        string       `json:"to"`
	FromHash  string       `json:"fromHash"`
	ToHash    string       `json:"toHash"`
	Identical bool         `json:"identical"`
	Stat      DiffStat     `json:"stat"`
	Files     []FileChange `json:"files"`
	Manifest  ManifestDiff `json:"manifest"`
}

// DiffStat counts files by status.
type DiffStat struct {
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
}

// FileChange is one file of either set.
type FileChange struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	OldSize   int64  `json:"oldSize,omitempty"`
	NewSize   int64  `json:"newSize,omitempty"`
	OldHash   string `json:"oldHash,omitempty"`
	NewHash   string `json:"newHash,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	// Note explains a changed file with no hunks, such as a change in line
	// endings only.
	Note  string `json:"note,omitempty"`
	Hunks []Hunk `json:"hunks,omitempty"`
}

// Hunk is one unified diff hunk. Each line starts with ' ', '-' or '+'.
type Hunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Header   string   `json:"header"`
	Lines    []string `json:"lines"`
}

// ManifestDiff summarizes seed.json changes by meaning rather than text.
type ManifestDiff struct {
	Title       *ValueChange `json:"title,omitempty"`
	Description *ValueChange `json:"description,omitempty"`
	// Fields lists other top-level fields that differ (vertical, author,
	// tags, dependencies).
	Fields []string  `json:"fields,omitempty"`
	Steps  ItemsDiff `json:"steps"`
	Verify ItemsDiff `json:"verify"`
}

// ValueChange is a string field before and after.
type ValueChange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ItemsDiff compares steps or verify checks by name.
type ItemsDiff struct {
	Added     []string     `json:"added"`
	Removed   []string     `json:"removed"`
	Changed   []ItemChange `json:"changed"`
	Reordered bool         `json:"reordered,omitempty"`
}

// ItemChange names a step or check and the JSON fields that differ.
type ItemChange struct {
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
}

// Empty reports whether nothing differs.
func (d ItemsDiff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0 && !d.Reordered
}

// Empty reports whether the manifests mean the same thing.
func (m ManifestDiff) Empty() bool {
	return m.Title == nil && m.Description == nil && len(m.Fields) == 0 && m.Steps.Empty() && m.Verify.Empty()
}

// Diff compares seed set a (from) with b (to). Both must belong to the same
// environment; the personas may differ.
func Diff(a, b *Set) (*SetDiff, error) {
	if a == nil || b == nil {
		return nil, errors.New("diff needs two seed sets")
	}
	if envOf(a.Dir) != envOf(b.Dir) {
		return nil, fmt.Errorf("%s and %s belong to different environments", a.ID(), b.ID())
	}
	d := &SetDiff{From: a.ID(), To: b.ID(), FromHash: a.Hash, ToHash: b.Hash, Files: []FileChange{}}
	old := map[string]FileDigest{}
	for _, f := range a.Files {
		old[f.Path] = f
	}
	cur := map[string]FileDigest{}
	for _, f := range b.Files {
		cur[f.Path] = f
	}
	var paths []string
	for p := range old {
		paths = append(paths, p)
	}
	for p := range cur {
		if _, ok := old[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		of, inOld := old[p]
		nf, inNew := cur[p]
		fc := FileChange{Path: p}
		switch {
		case !inNew:
			fc.Status, fc.OldSize, fc.OldHash = FileRemoved, of.Size, of.Hash
			d.Stat.Removed++
		case !inOld:
			fc.Status, fc.NewSize, fc.NewHash = FileAdded, nf.Size, nf.Hash
			d.Stat.Added++
		case of.Hash == nf.Hash:
			fc.Status, fc.OldSize, fc.NewSize = FileUnchanged, of.Size, nf.Size
			d.Stat.Unchanged++
		default:
			fc.Status, fc.OldSize, fc.NewSize, fc.OldHash, fc.NewHash = FileChanged, of.Size, nf.Size, of.Hash, nf.Hash
			d.Stat.Changed++
			if err := diffFile(&fc, a, b); err != nil {
				return nil, err
			}
		}
		d.Files = append(d.Files, fc)
	}
	d.Manifest = diffManifest(&a.Manifest, &b.Manifest)
	d.Identical = d.Stat.Added == 0 && d.Stat.Removed == 0 && d.Stat.Changed == 0
	return d, nil
}

// envOf returns the environment directory of a set directory
// (<env>/seeds/<persona>/v<N>).
func envOf(dir string) string {
	return filepath.Clean(filepath.Join(dir, "..", "..", ".."))
}

func diffFile(fc *FileChange, a, b *Set) error {
	ob, err := os.ReadFile(filepath.Join(a.Dir, filepath.FromSlash(fc.Path)))
	if err != nil {
		return err
	}
	nb, err := os.ReadFile(filepath.Join(b.Dir, filepath.FromSlash(fc.Path)))
	if err != nil {
		return err
	}
	if IsBinary(ob) || IsBinary(nb) {
		fc.Binary = true
		return nil
	}
	fc.Hunks, fc.Truncated = UnifiedHunks(ob, nb)
	if len(fc.Hunks) == 0 {
		if bytes.Equal(normalizeEOL(ob), normalizeEOL(nb)) {
			fc.Note = "line endings only"
		} else {
			fc.Note = "trailing newline only"
		}
	}
	return nil
}

// IsBinary reports whether b looks binary: a NUL byte in the first 8 KiB or
// content that is not valid UTF-8.
func IsBinary(b []byte) bool {
	head := b
	if len(head) > binarySniffLen {
		head = head[:binarySniffLen]
	}
	return bytes.IndexByte(head, 0) >= 0 || !utf8.Valid(b)
}

func normalizeEOL(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// splitLines normalizes CRLF to LF and splits into lines without their
// terminators. A trailing newline does not produce an empty last line.
func splitLines(b []byte) []string {
	s := string(normalizeEOL(b))
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// clip cuts b to MaxDiffInput bytes at the last line break.
func clip(b []byte) ([]byte, bool) {
	if len(b) <= MaxDiffInput {
		return b, false
	}
	b = b[:MaxDiffInput]
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		b = b[:i+1]
	}
	return b, true
}

type opKind byte

const (
	opEqual  opKind = ' '
	opDelete opKind = '-'
	opInsert opKind = '+'
)

type edit struct {
	kind opKind
	text string
	ai   int // lines of a before this edit
	bi   int // lines of b before this edit
}

// UnifiedHunks returns the unified diff hunks (3 lines of context) between
// two texts, after CRLF normalization, and whether the output was cut at
// MaxDiffInput bytes of input or MaxDiffLines lines of output.
func UnifiedHunks(oldText, newText []byte) ([]Hunk, bool) {
	ob, t1 := clip(oldText)
	nb, t2 := clip(newText)
	edits, t3 := lineEdits(splitLines(ob), splitLines(nb))
	hunks, t4 := buildHunks(edits)
	return hunks, t1 || t2 || t3 || t4
}

// maxEditDistance bounds the Myers search. An edit script longer than this
// would overflow MaxDiffLines anyway, so the middle is reported as one
// replacement instead.
const maxEditDistance = MaxDiffLines

// lineEdits computes a shortest edit script with Myers' O(ND) algorithm
// after trimming the common prefix and suffix.
func lineEdits(a, b []string) ([]edit, bool) {
	ids := map[string]int{}
	intern := func(lines []string) []int {
		out := make([]int, len(lines))
		for i, l := range lines {
			id, ok := ids[l]
			if !ok {
				id = len(ids)
				ids[l] = id
			}
			out[i] = id
		}
		return out
	}
	x, y := intern(a), intern(b)
	pre := 0
	for pre < len(x) && pre < len(y) && x[pre] == y[pre] {
		pre++
	}
	suf := 0
	for suf < len(x)-pre && suf < len(y)-pre && x[len(x)-1-suf] == y[len(y)-1-suf] {
		suf++
	}
	var edits []edit
	for i := 0; i < pre; i++ {
		edits = append(edits, edit{opEqual, a[i], i, i})
	}
	mid, ok := myers(x[pre:len(x)-suf], y[pre:len(y)-suf])
	truncated := false
	if !ok {
		truncated = true
		mid = nil
		for i := pre; i < len(x)-suf; i++ {
			mid = append(mid, edit{kind: opDelete})
		}
		for j := pre; j < len(y)-suf; j++ {
			mid = append(mid, edit{kind: opInsert})
		}
	}
	ai, bi := pre, pre
	for _, e := range mid {
		switch e.kind {
		case opEqual:
			edits = append(edits, edit{opEqual, a[ai], ai, bi})
			ai++
			bi++
		case opDelete:
			edits = append(edits, edit{opDelete, a[ai], ai, bi})
			ai++
		case opInsert:
			edits = append(edits, edit{opInsert, b[bi], ai, bi})
			bi++
		}
	}
	for i := 0; i < suf; i++ {
		edits = append(edits, edit{opEqual, a[ai], ai, bi})
		ai++
		bi++
	}
	return edits, truncated
}

// myers returns the edit kinds (text and positions unset) turning a into b,
// or false when the edit distance exceeds maxEditDistance.
func myers(a, b []int) ([]edit, bool) {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil, true
	}
	max := n + m
	off := max + 1
	v := make([]int, 2*max+3)
	// trace[d] holds v[-d-1 .. d+1] as it was before step d.
	var trace [][]int32
	for d := 0; d <= max; d++ {
		if d > maxEditDistance {
			return nil, false
		}
		snap := make([]int32, 2*d+3)
		for k := -d - 1; k <= d+1; k++ {
			snap[k+d+1] = int32(v[off+k])
		}
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, n, m), true
			}
		}
	}
	return nil, false
}

func backtrack(trace [][]int32, n, m int) []edit {
	var rev []edit
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		snap := trace[d]
		at := func(k int) int { return int(snap[k+d+1]) }
		k := x - y
		var prevK int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := at(prevK)
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			rev = append(rev, edit{kind: opEqual})
			x--
			y--
		}
		if d > 0 {
			if x == prevX {
				rev = append(rev, edit{kind: opInsert})
			} else {
				rev = append(rev, edit{kind: opDelete})
			}
		}
		x, y = prevX, prevY
	}
	out := make([]edit, len(rev))
	for i, e := range rev {
		out[len(rev)-1-i] = e
	}
	return out
}

// buildHunks groups edits into hunks with diffContext lines of context and
// stops after MaxDiffLines output lines.
func buildHunks(edits []edit) ([]Hunk, bool) {
	var changes []int
	for i, e := range edits {
		if e.kind != opEqual {
			changes = append(changes, i)
		}
	}
	var hunks []Hunk
	emitted := 0
	for ci := 0; ci < len(changes); {
		start := changes[ci] - diffContext
		if start < 0 {
			start = 0
		}
		last := changes[ci]
		cj := ci + 1
		for cj < len(changes) && changes[cj]-last <= 2*diffContext+1 {
			last = changes[cj]
			cj++
		}
		end := last + diffContext + 1
		if end > len(edits) {
			end = len(edits)
		}
		h := Hunk{OldStart: edits[start].ai, NewStart: edits[start].bi}
		for _, e := range edits[start:end] {
			if emitted >= MaxDiffLines {
				break
			}
			h.Lines = append(h.Lines, string(e.kind)+e.text)
			emitted++
			if e.kind != opInsert {
				h.OldLines++
			}
			if e.kind != opDelete {
				h.NewLines++
			}
		}
		if h.OldLines > 0 {
			h.OldStart++
		}
		if h.NewLines > 0 {
			h.NewStart++
		}
		h.Header = fmt.Sprintf("@@ -%s +%s @@", hunkRange(h.OldStart, h.OldLines), hunkRange(h.NewStart, h.NewLines))
		hunks = append(hunks, h)
		if emitted >= MaxDiffLines {
			return hunks, cj < len(changes) || len(h.Lines) < end-start
		}
		ci = cj
	}
	return hunks, false
}

func hunkRange(start, n int) string {
	if n == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, n)
}

// Unified renders a changed file as a unified diff with a/ and b/ headers.
func (fc FileChange) Unified() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n", fc.Path, fc.Path)
	for _, h := range fc.Hunks {
		sb.WriteString(h.Header)
		sb.WriteByte('\n')
		for _, l := range h.Lines {
			sb.WriteString(l)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func diffManifest(a, b *Manifest) ManifestDiff {
	var md ManifestDiff
	if a.Title != b.Title {
		md.Title = &ValueChange{From: a.Title, To: b.Title}
	}
	if a.Description != b.Description {
		md.Description = &ValueChange{From: a.Description, To: b.Description}
	}
	for _, f := range []struct {
		name string
		x, y any
	}{
		{"vertical", a.Vertical, b.Vertical},
		{"author", a.Author, b.Author},
		{"tags", a.Tags, b.Tags},
		{"dependencies", a.Dependencies, b.Dependencies},
	} {
		if !sameJSON(f.x, f.y) {
			md.Fields = append(md.Fields, f.name)
		}
	}
	md.Steps = diffItems(namedItems(a.Steps, func(s Step) string { return s.Name }), namedItems(b.Steps, func(s Step) string { return s.Name }))
	md.Verify = diffItems(namedItems(a.Verify, func(c Check) string { return c.Name }), namedItems(b.Verify, func(c Check) string { return c.Name }))
	return md
}

type namedItem struct {
	key    string
	fields map[string]json.RawMessage
}

// namedItems keys items by name; unnamed items become "#<position>" and a
// repeated name gets a "#<n>" suffix, so every key is unique.
func namedItems[T any](items []T, name func(T) string) []namedItem {
	seen := map[string]int{}
	out := make([]namedItem, 0, len(items))
	for i, it := range items {
		key := name(it)
		if key == "" {
			key = fmt.Sprintf("#%d", i+1)
		}
		seen[key]++
		if n := seen[key]; n > 1 {
			key = fmt.Sprintf("%s#%d", key, n)
		}
		b, _ := json.Marshal(it)
		fields := map[string]json.RawMessage{}
		json.Unmarshal(b, &fields)
		out = append(out, namedItem{key: key, fields: fields})
	}
	return out
}

func diffItems(a, b []namedItem) ItemsDiff {
	d := ItemsDiff{Added: []string{}, Removed: []string{}, Changed: []ItemChange{}}
	old := map[string]namedItem{}
	for _, it := range a {
		old[it.key] = it
	}
	cur := map[string]bool{}
	for _, it := range b {
		cur[it.key] = true
		o, ok := old[it.key]
		if !ok {
			d.Added = append(d.Added, it.key)
			continue
		}
		if fields := changedFields(o.fields, it.fields); len(fields) > 0 {
			d.Changed = append(d.Changed, ItemChange{Name: it.key, Fields: fields})
		}
	}
	for _, it := range a {
		if !cur[it.key] {
			d.Removed = append(d.Removed, it.key)
		}
	}
	// Reordered: the items present in both appear in a different order.
	var oa, ob []string
	for _, it := range a {
		if cur[it.key] {
			oa = append(oa, it.key)
		}
	}
	for _, it := range b {
		if _, ok := old[it.key]; ok {
			ob = append(ob, it.key)
		}
	}
	d.Reordered = strings.Join(oa, "\x00") != strings.Join(ob, "\x00")
	return d
}

func changedFields(a, b map[string]json.RawMessage) []string {
	var out []string
	for k, va := range a {
		if vb, ok := b[k]; !ok || !bytes.Equal(va, vb) {
			out = append(out, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func sameJSON(x, y any) bool {
	bx, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return bytes.Equal(bx, by)
}
