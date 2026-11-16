package seeds

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dboudreau00/greenroom/internal/config"
)

// Errors returned by Bump and Pin.
var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	ErrInvalid  = errors.New("invalid")
)

// Versions lists the version numbers of persona's set directories, sorted.
func Versions(envDir, persona string) ([]int, error) {
	entries, err := os.ReadDir(filepath.Join(envDir, config.SeedsDirName, persona))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []int
	for _, e := range entries {
		if m := versionRe.FindStringSubmatch(e.Name()); m != nil && e.IsDir() {
			n, _ := strconv.Atoi(m[1])
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out, nil
}

// Bump copies seeds/<persona>/v<from>/ (from 0 means the latest) to
// v<N+1>, where N is the highest existing version, and sets "version" (and
// "title" when given) in the copy's seed.json without reformatting it. The
// copy is built in a hidden directory and renamed into place only when it
// loads with no problems the source did not already have.
func Bump(envDir, persona string, from int, title string) (newDir string, version int, err error) {
	if !config.ValidName(persona) {
		return "", 0, fmt.Errorf("%w: persona %q is not a valid name", ErrInvalid, persona)
	}
	if from < 0 {
		return "", 0, fmt.Errorf("%w: version %d", ErrInvalid, from)
	}
	if strings.ContainsAny(title, "\x00\r\n") || len(title) > 200 {
		return "", 0, fmt.Errorf("%w: title must be one line of at most 200 bytes", ErrInvalid)
	}
	versions, err := Versions(envDir, persona)
	if err != nil {
		return "", 0, err
	}
	if len(versions) == 0 {
		return "", 0, fmt.Errorf("%w: persona %q has no seed sets", ErrNotFound, persona)
	}
	highest := versions[len(versions)-1]
	if from == 0 {
		from = highest
	} else if !containsInt(versions, from) {
		return "", 0, fmt.Errorf("%w: seed set %s@v%d", ErrNotFound, persona, from)
	}
	pdir := filepath.Join(envDir, config.SeedsDirName, persona)
	src := filepath.Join(pdir, "v"+strconv.Itoa(from))
	version = highest + 1
	newDir = filepath.Join(pdir, "v"+strconv.Itoa(version))
	if _, err := os.Lstat(newDir); err == nil {
		return "", 0, fmt.Errorf("%w: %s@v%d", ErrExists, persona, version)
	}
	manifest, err := os.ReadFile(filepath.Join(src, config.SeedFile))
	if err != nil {
		return "", 0, fmt.Errorf("%s@v%d: %w", persona, from, err)
	}
	manifest, err = SetTopLevel(manifest, "version", version)
	if err == nil && title != "" {
		manifest, err = SetTopLevel(manifest, "title", title)
	}
	if err != nil {
		return "", 0, fmt.Errorf("%s@v%d %s: %w", persona, from, config.SeedFile, err)
	}
	var rb [4]byte
	rand.Read(rb[:])
	tmp := filepath.Join(pdir, ".v"+strconv.Itoa(version)+".new-"+hex.EncodeToString(rb[:]))
	defer func() {
		if err != nil {
			os.RemoveAll(tmp)
		}
	}()
	if err = copyTree(src, tmp); err != nil {
		return "", 0, err
	}
	if err = os.WriteFile(filepath.Join(tmp, config.SeedFile), manifest, filePerm(filepath.Join(src, config.SeedFile))); err != nil {
		return "", 0, err
	}
	if err = checkCopy(src, tmp, persona, version); err != nil {
		return "", 0, err
	}
	if _, serr := os.Lstat(newDir); serr == nil {
		err = fmt.Errorf("%w: %s@v%d", ErrExists, persona, version)
		return "", 0, err
	}
	if err = os.Rename(tmp, newDir); err != nil {
		if _, serr := os.Lstat(newDir); serr == nil {
			err = fmt.Errorf("%w: %s@v%d", ErrExists, persona, version)
		}
		return "", 0, err
	}
	return newDir, version, nil
}

func containsInt(a []int, n int) bool {
	for _, x := range a {
		if x == n {
			return true
		}
	}
	return false
}

// checkCopy loads the copy and refuses it when it has a problem the source
// does not have, or when its manifest does not name the new version.
func checkCopy(src, tmp, persona string, version int) error {
	before, err := Load(src)
	if err != nil {
		return err
	}
	after, err := Load(tmp)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, p := range before.Problems {
		known[p] = true
	}
	var added []string
	for _, p := range after.Problems {
		if !known[p] {
			added = append(added, p)
		}
	}
	if after.Version != version {
		added = append(added, fmt.Sprintf("manifest version is %d, want %d", after.Version, version))
	}
	if after.Persona != "" && after.Persona != persona {
		added = append(added, fmt.Sprintf("manifest persona %q does not match directory %q", after.Persona, persona))
	}
	if len(added) > 0 {
		return fmt.Errorf("%w: the new version has problems: %s", ErrInvalid, strings.Join(added, "; "))
	}
	return nil
}

// copyTree copies the regular files under src to dst, skipping hidden
// directories the way the set digest does.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if path != src && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm(src))
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func filePerm(path string) fs.FileMode {
	if fi, err := os.Stat(path); err == nil {
		return fi.Mode().Perm()
	}
	return 0o644
}

// Pin points environment.json in envDir at persona@v<version>, editing the
// file textually so comments and formatting survive. The persona's set must
// exist.
func Pin(envDir, persona string, version int) error {
	if !config.ValidName(persona) || version <= 0 {
		return fmt.Errorf("%w: pin %s@v%d", ErrInvalid, persona, version)
	}
	if _, err := os.Stat(filepath.Join(envDir, config.SeedsDirName, persona, "v"+strconv.Itoa(version), config.SeedFile)); err != nil {
		return fmt.Errorf("%w: seed set %s@v%d", ErrNotFound, persona, version)
	}
	path := filepath.Join(envDir, config.EnvironmentFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	nb, err := SetPinText(b, persona, version)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var check struct {
		Pin config.Pin `json:"pin"`
	}
	if err := json.Unmarshal(config.StripComments(nb), &check); err != nil {
		return fmt.Errorf("%s: the edit would break the file: %w", path, err)
	}
	if check.Pin.Persona != persona || check.Pin.Version != version {
		return fmt.Errorf("%s: the edit did not take (pin reads %s@v%d)", path, check.Pin.Persona, check.Pin.Version)
	}
	return writeAtomic(path, nb)
}

func writeAtomic(path string, b []byte) error {
	var rb [4]byte
	rand.Read(rb[:])
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp-"+hex.EncodeToString(rb[:]))
	if err := os.WriteFile(tmp, b, filePerm(path)); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
