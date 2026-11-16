package engine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/seeds"
	"github.com/dboudreau00/greenroom/internal/templates"
)

// Templates lists the built-in templates and the workspace ones.
func (e *Engine) Templates() ([]*templates.Template, error) {
	return templates.List(e.ws.TemplatesDir)
}

// ErrExists is returned when an environment of that name already exists.
var ErrExists = errors.New("environment already exists")

// CreateEnvironment renders a template into a new environment directory.
// The files are written to a hidden directory first and loaded with the
// same code a reload uses; only an environment that loads without problems
// is moved into place. With dryRun nothing is written.
func (e *Engine) CreateEnvironment(templateName string, req templates.Request, dryRun bool) (*templates.Plan, error) {
	list, err := e.Templates()
	if err != nil && list == nil {
		return nil, err
	}
	tpl, ok := templates.Find(list, templateName)
	if !ok {
		return nil, fmt.Errorf("template %q not found", templateName)
	}
	plan, err := tpl.Render(req)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return plan, nil
	}
	e.createMu.Lock()
	defer e.createMu.Unlock()
	if _, err := e.Env(req.Name); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrExists, req.Name)
	}
	final := filepath.Join(e.ws.EnvironmentsDir, req.Name)
	if _, err := os.Stat(final); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrExists, final)
	}
	if err := os.MkdirAll(e.ws.EnvironmentsDir, 0o755); err != nil {
		return nil, err
	}
	var b [4]byte
	rand.Read(b[:])
	tmp := filepath.Join(e.ws.EnvironmentsDir, "."+req.Name+".new-"+hex.EncodeToString(b[:]))
	if err := plan.Write(tmp); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	if err := validateNewEnv(tmp); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	if err := e.Reload(); err != nil {
		e.log.Warn("reload after create", "env", req.Name, "err", err)
	}
	if _, err := e.Env(req.Name); err != nil {
		return plan, fmt.Errorf("created %s but it did not load: %w", final, err)
	}
	e.log.Info("environment created", "env", req.Name, "template", templateName, "modules", strings.Join(plan.Modules, ","))
	return plan, nil
}

func validateNewEnv(dir string) error {
	env, err := config.LoadEnvironment(dir)
	if err != nil {
		return err
	}
	sets, err := seeds.Discover(dir)
	if err != nil {
		return err
	}
	var problems []string
	for _, s := range sets {
		for _, p := range s.Problems {
			problems = append(problems, s.ID()+": "+p)
		}
	}
	if _, ok := seeds.Find(sets, env.Pin.Persona, env.Pin.Version); !ok {
		problems = append(problems, fmt.Sprintf("pin %s@v%d has no seed set", env.Pin.Persona, env.Pin.Version))
	}
	if len(problems) > 0 {
		return errors.New("the rendered environment has problems: " + strings.Join(problems, "; "))
	}
	return nil
}
