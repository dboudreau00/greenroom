package example

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/seeds"
)

func normalise(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// The copy under examples/ must match the embedded tree byte for byte.
func TestEmbeddedMatchesRepoCopy(t *testing.T) {
	repo := filepath.Join("..", "..", "examples", "environments", Name)
	if _, err := os.Stat(repo); err != nil {
		t.Skipf("repo copy not present: %v", err)
	}
	root := FS()
	embedded := map[string]bool{}
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		embedded[p] = true
		a, _ := fs.ReadFile(root, p)
		b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(p)))
		if err != nil {
			t.Errorf("%s missing from examples/: %v", p, err)
			return nil
		}
		if !bytes.Equal(normalise(a), normalise(b)) {
			t.Errorf("%s differs between internal/example and examples/", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(repo, p)
		if !embedded[filepath.ToSlash(rel)] {
			t.Errorf("%s exists in examples/ but not in internal/example", rel)
		}
		return nil
	})
}

func TestExtractLoadsAndValidates(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "environments", Name)
	written, err := Extract(dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) < 10 {
		t.Fatalf("only %d files written", len(written))
	}
	env, err := config.LoadEnvironment(dest)
	if err != nil {
		t.Fatal(err)
	}
	if env.Name != Name || len(env.Dependencies) != 6 || env.Pin.Persona != "cfo" {
		t.Fatalf("env %+v", env)
	}
	sets, err := seeds.Discover(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 3 {
		t.Fatalf("seed sets %d", len(sets))
	}
	for _, s := range sets {
		if len(s.Problems) > 0 {
			t.Errorf("%s: %v", s.ID(), s.Problems)
		}
	}
	if _, ok := seeds.Find(sets, env.Pin.Persona, env.Pin.Version); !ok {
		t.Fatal("pin does not resolve")
	}
	if _, err := Extract(dest, false); err == nil {
		t.Fatal("second extract without force should fail")
	}
	if _, err := Extract(dest, true); err != nil {
		t.Fatal(err)
	}
}
