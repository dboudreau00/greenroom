package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/state"
	"github.com/dboudreau00/greenroom/internal/targets/docker"
)

// dockerSnapshot is stored in the snapshot record note.
type dockerSnapshot struct {
	Image      string        `json:"image"`
	ImageID    string        `json:"imageId"`
	Name       string        `json:"name"`
	Create     string        `json:"create"` // file holding the create body
	Mounts     []dockerMount `json:"mounts"`
	CapturedAt time.Time     `json:"capturedAt"`
}

type dockerMount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
	File        string `json:"file"`
}

// Snapshot captures a known-good baseline for a dependency.
func (e *Engine) Snapshot(ctx context.Context, envName, depName, actor string) (*state.Run, error) {
	v, err := e.Env(envName)
	if err != nil {
		return nil, err
	}
	dep, ok := v.Config.Dependency(depName)
	if !ok {
		return nil, fmt.Errorf("unknown dependency %q", depName)
	}
	rec := e.newRun(v.Config.Name, state.RunSnapshot, actor)
	if err := e.acquire(v, rec.ID()); err != nil {
		return rec.Finish(err, ""), err
	}
	defer e.release(v)
	return e.runSnapshot(ctx, v, *dep, rec)
}

// StartSnapshot captures a snapshot in the background and returns the run id.
func (e *Engine) StartSnapshot(envName, depName, actor string) (string, error) {
	v, err := e.Env(envName)
	if err != nil {
		return "", err
	}
	dep, ok := v.Config.Dependency(depName)
	if !ok {
		return "", fmt.Errorf("unknown dependency %q", depName)
	}
	rec := e.newRun(v.Config.Name, state.RunSnapshot, actor)
	if err := e.acquire(v, rec.ID()); err != nil {
		rec.Finish(err, "")
		return "", err
	}
	go func() {
		defer e.release(v)
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		e.runSnapshot(ctx, v, *dep, rec)
	}()
	return rec.ID(), nil
}

func (e *Engine) runSnapshot(ctx context.Context, v *Env, dep config.Dependency, rec *recorder) (*state.Run, error) {
	var err error
	strategy := ""
	if dep.Snapshot != nil {
		strategy = dep.Snapshot.Strategy
	}
	if strategy == "" {
		switch dep.Kind {
		case config.KindPostgres:
			strategy = "template"
		case config.KindDocker:
			strategy = "commit"
		}
	}
	step := "snapshot " + dep.Name
	idx := rec.StartStep(step, "snapshot")
	rec.Info(step, "capturing %s with strategy %s", dep.Name, strategy)
	var ref, detail string
	switch {
	case dep.Kind == config.KindPostgres && strategy == "template":
		ref, detail, err = e.snapshotPostgres(ctx, v, dep, rec, step)
	case dep.Kind == config.KindDocker && strategy == "commit":
		ref, detail, err = e.snapshotDocker(ctx, v, dep, rec, step)
	default:
		err = fmt.Errorf("no snapshot strategy %q for kind %s (postgres supports template, docker supports commit)", strategy, dep.Kind)
	}
	rec.EndStep(idx, err, detail)
	if err != nil {
		run := rec.Finish(err, "snapshot failed")
		return run, err
	}
	record := state.SnapshotRecord{Dep: dep.Name, Strategy: strategy, Ref: ref, At: e.Now(), Note: detail}
	if applied, _ := e.store.LoadApplied(v.Config.Name); applied != nil {
		record.Persona, record.Version, record.SetHash = applied.Persona, applied.Version, applied.SetHash
	}
	if err := e.store.SaveSnapshot(v.Config.Name, record); err != nil {
		run := rec.Finish(err, "could not record snapshot")
		return run, err
	}
	run := rec.Finish(nil, fmt.Sprintf("%s snapshot of %s saved as %s", strategy, dep.Name, ref))
	return run, nil
}

func (e *Engine) snapshotPostgres(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, step string) (string, string, error) {
	runner, err := v.Prober.SQLRunner(dep)
	if err != nil {
		return "", "", err
	}
	defer runner.Close()
	db := ""
	if dep.Reset != nil {
		db = dep.Reset.Database
	}
	db, err = databaseName(ctx, runner, db)
	if err != nil {
		return "", "", err
	}
	tpl := ""
	if dep.Snapshot != nil {
		tpl = dep.Snapshot.Template
	}
	if tpl == "" && dep.Reset != nil {
		tpl = dep.Reset.Template
	}
	if tpl == "" {
		tpl = db + "_golden"
	}
	maint, err := runner.WithDatabase("postgres")
	if err != nil {
		return "", "", err
	}
	defer maint.Close()
	runner.Close()
	maint.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s IS_TEMPLATE false", pgIdent(tpl)), nil)
	rec.Info(step, "dropping old template %s", tpl)
	if err := pgDropDatabase(ctx, maint, tpl); err != nil {
		return "", "", err
	}
	rec.Info(step, "creating template %s from %s (other sessions on %s are terminated)", tpl, db, db)
	if err := pgRetry(ctx, func() error {
		return maint.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", pgIdent(tpl), pgIdent(db)), nil)
	}, func() error { return pgTerminate(ctx, maint, db) }); err != nil {
		return "", "", err
	}
	if err := maint.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s IS_TEMPLATE true", pgIdent(tpl)), nil); err != nil {
		rec.Warn(step, "could not mark %s as a template (needs superuser): %v", tpl, err)
	}
	return tpl, "template database " + tpl + " cloned from " + db, nil
}

var shortIDRe = regexp.MustCompile(`^[0-9a-f]{12}$`)

func (e *Engine) snapshotDocker(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, step string) (string, string, error) {
	cli, err := v.Registry.Docker(dep.Target)
	if err != nil {
		return "", "", err
	}
	ct, err := cli.Inspect(ctx, dep.Container)
	if err != nil {
		return "", "", err
	}
	repo := "greenroom/" + v.Config.Name + "-" + dep.Name
	tag := "golden"
	if dep.Snapshot != nil && dep.Snapshot.Tag != "" {
		repo, tag = splitTag(dep.Snapshot.Tag)
	}
	dir := filepath.Join(e.store.Dir(), v.Config.Name, "snapshots", dep.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	wasRunning := ct.State.Running
	if wasRunning {
		rec.Info(step, "stopping %s for a consistent capture", ct.ShortName())
		if err := cli.Stop(ctx, ct.ID, 60); err != nil {
			return "", "", err
		}
	}
	restart := func() {
		if wasRunning {
			rec.Info(step, "starting %s again", ct.ShortName())
			if err := cli.Start(ctx, ct.ID); err != nil {
				rec.Error(step, "start failed: %v", err)
				return
			}
			if _, err := cli.WaitHealthy(ctx, ct.ID, 3*time.Minute); err != nil {
				rec.Warn(step, "%v", err)
			}
		}
	}
	rec.Info(step, "committing filesystem to %s:%s", repo, tag)
	imageID, err := cli.Commit(ctx, ct.ID, repo, tag)
	if err != nil {
		restart()
		return "", "", err
	}
	snap := dockerSnapshot{Image: repo + ":" + tag, ImageID: imageID, Name: ct.ShortName(), CapturedAt: e.Now()}
	for i, m := range ct.Mounts {
		if m.Type != "volume" && m.Type != "bind" {
			continue
		}
		if !m.RW {
			rec.Info(step, "skipping read-only mount %s (host managed)", m.Destination)
			continue
		}
		file := fmt.Sprintf("mount-%d.tar", i)
		rec.Info(step, "archiving %s (%s %s)", m.Destination, m.Type, firstNonEmpty(m.Name, m.Source))
		body, err := cli.Archive(ctx, ct.ID, m.Destination)
		if err != nil {
			restart()
			return "", "", fmt.Errorf("archive %s: %w", m.Destination, err)
		}
		f, err := os.Create(filepath.Join(dir, file))
		if err != nil {
			body.Close()
			restart()
			return "", "", err
		}
		n, err := io.Copy(f, body)
		body.Close()
		f.Close()
		if err != nil {
			restart()
			return "", "", fmt.Errorf("archive %s: %w", m.Destination, err)
		}
		rec.Info(step, "saved %s (%s)", file, humanBytes(n))
		snap.Mounts = append(snap.Mounts, dockerMount{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination, File: file})
	}
	body := createBody(ct, snap.Image)
	snap.Create = "container.json"
	if err := config.WriteJSON(filepath.Join(dir, snap.Create), body); err != nil {
		restart()
		return "", "", err
	}
	restart()
	note, _ := json.Marshal(snap)
	return snap.Image, string(note), nil
}

// createBody derives a container create request from an inspect result.
func createBody(ct *docker.Container, image string) map[string]any {
	body := map[string]any{}
	for k, raw := range ct.Config {
		body[k] = raw
	}
	body["Image"] = image
	if raw, ok := ct.Config["Hostname"]; ok {
		var h string
		json.Unmarshal(raw, &h)
		if shortIDRe.MatchString(h) {
			delete(body, "Hostname")
		}
	}
	body["HostConfig"] = ct.HostConfig
	endpoints := map[string]any{}
	for name, ep := range ct.NetworkSettings.Networks {
		cfg := map[string]any{}
		if raw, ok := ep["Aliases"]; ok {
			var aliases []string
			json.Unmarshal(raw, &aliases)
			var kept []string
			for _, a := range aliases {
				if !shortIDRe.MatchString(a) {
					kept = append(kept, a)
				}
			}
			if len(kept) > 0 {
				cfg["Aliases"] = kept
			}
		}
		for _, k := range []string{"Links", "IPAMConfig", "DriverOpts"} {
			if raw, ok := ep[k]; ok && string(raw) != "null" {
				cfg[k] = raw
			}
		}
		endpoints[name] = cfg
	}
	if len(endpoints) > 0 {
		body["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}
	}
	return body
}

// recreateDocker rebuilds the container from its own image and config,
// discarding the writable layer.
func (e *Engine) recreateDocker(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, step string) (string, error) {
	cli, err := v.Registry.Docker(dep.Target)
	if err != nil {
		return "", err
	}
	ct, err := cli.Inspect(ctx, dep.Container)
	if err != nil {
		return "", err
	}
	body := createBody(ct, ct.ImageRef())
	rec.Info(step, "removing %s", ct.ShortName())
	if err := cli.Remove(ctx, ct.ID, true, false); err != nil {
		return "", err
	}
	rec.Info(step, "creating %s from %s", ct.ShortName(), ct.ImageRef())
	id, err := cli.Create(ctx, ct.ShortName(), body)
	if err != nil {
		return "", err
	}
	if err := cli.Start(ctx, id); err != nil {
		return "", err
	}
	if _, err := cli.WaitHealthy(ctx, id, 3*time.Minute); err != nil {
		return "", err
	}
	return "recreated from " + ct.ImageRef(), nil
}

// restoreDocker replaces the container with the committed snapshot image
// and restores archived mounts.
func (e *Engine) restoreDocker(ctx context.Context, v *Env, dep config.Dependency, rec *recorder, step string) (string, error) {
	cli, err := v.Registry.Docker(dep.Target)
	if err != nil {
		return "", err
	}
	record, err := e.store.Snapshot(v.Config.Name, dep.Name)
	if err != nil {
		return "", err
	}
	if record == nil || record.Strategy != "commit" {
		return "", fmt.Errorf("no docker snapshot for %s (run: greenroom snapshot %s %s)", dep.Name, v.Config.Name, dep.Name)
	}
	var snap dockerSnapshot
	if err := json.Unmarshal([]byte(record.Note), &snap); err != nil {
		return "", fmt.Errorf("snapshot record is unreadable: %w", err)
	}
	dir := filepath.Join(e.store.Dir(), v.Config.Name, "snapshots", dep.Name)
	rawBody, err := os.ReadFile(filepath.Join(dir, snap.Create))
	if err != nil {
		return "", err
	}
	var body map[string]any
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return "", err
	}
	if _, err := cli.ImageInspect(ctx, snap.Image); err != nil {
		return "", fmt.Errorf("snapshot image %s is missing: %w", snap.Image, err)
	}
	name := snap.Name
	if ct, err := cli.Inspect(ctx, dep.Container); err == nil {
		name = ct.ShortName()
		rec.Info(step, "removing current container %s", name)
		if err := cli.Remove(ctx, ct.ID, true, true); err != nil {
			return "", err
		}
	} else if !docker.IsNotFound(err) {
		return "", err
	}
	for _, m := range snap.Mounts {
		if m.Type == "volume" && m.Name != "" {
			rec.Info(step, "dropping volume %s", m.Name)
			if err := cli.VolumeRemove(ctx, m.Name); err != nil {
				return "", fmt.Errorf("volume %s: %w", m.Name, err)
			}
		}
	}
	rec.Info(step, "creating %s from %s", name, snap.Image)
	id, err := cli.Create(ctx, name, body)
	if err != nil {
		return "", err
	}
	for _, m := range snap.Mounts {
		f, err := os.Open(filepath.Join(dir, m.File))
		if err != nil {
			return "", err
		}
		rec.Info(step, "restoring %s from %s", m.Destination, m.File)
		err = cli.PutArchive(ctx, id, path.Dir(m.Destination), f)
		f.Close()
		if err != nil {
			return "", fmt.Errorf("restore %s: %w", m.Destination, err)
		}
	}
	if err := cli.Start(ctx, id); err != nil {
		return "", err
	}
	if _, err := cli.WaitHealthy(ctx, id, 3*time.Minute); err != nil {
		return "", err
	}
	return "restored from " + snap.Image + fmt.Sprintf(" with %d mounts", len(snap.Mounts)), nil
}

func splitTag(ref string) (string, string) {
	if i := strings.LastIndex(ref, ":"); i > 0 && !strings.Contains(ref[i:], "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, "golden"
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
	}
	return fmt.Sprintf("%.2f GB", float64(n)/1024/1024/1024)
}

var errNoSnapshot = errors.New("no snapshot")
