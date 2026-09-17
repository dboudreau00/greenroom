// Package example embeds the bundled acme-demo environment so that
// greenroom init can scaffold a working workspace. The same files live under
// examples/acme-demo in the repository; a test keeps the two in sync.
package example

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Name is the environment name of the bundled example.
const Name = "acme-demo"

//go:embed all:acme-demo
var files embed.FS

// FS returns the embedded tree rooted at the environment directory.
func FS() fs.FS {
	sub, _ := fs.Sub(files, Name)
	return sub
}

// Extract writes the example environment into dest and returns the files
// written. Existing files are left alone unless force is set.
func Extract(dest string, force bool) ([]string, error) {
	root := FS()
	var written []string
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if _, err := os.Stat(target); err == nil && !force {
			return fmt.Errorf("%s already exists (use --force to overwrite)", target)
		}
		b, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(p, ".sh") {
			b = []byte(strings.ReplaceAll(string(b), "\r\n", "\n"))
		}
		if err := os.WriteFile(target, b, 0o644); err != nil {
			return err
		}
		written = append(written, target)
		return nil
	})
	return written, err
}
