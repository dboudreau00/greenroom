// Package sqlrun runs SQL against a dependency either directly through a
// driver, or through the psql / mysql command line tools on a target
// (docker exec, kubernetes exec, SSM, ssh). The second route is how a
// database that only listens behind a bastion is reached.
package sqlrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/targets"
	mysqldrv "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runner executes SQL for one dependency.
type Runner interface {
	Kind() string
	// Ping connects and returns the server version.
	Ping(ctx context.Context) (string, error)
	// Scalar returns the first column of the first row as text.
	Scalar(ctx context.Context, query string) (string, error)
	// Exec runs a script that may hold many statements.
	Exec(ctx context.Context, script string, sink targets.LineSink) error
	// WithDatabase returns a runner for another database on the same server.
	WithDatabase(db string) (Runner, error)
	// Describe returns a redacted description for logs.
	Describe() string
	Close() error
}

// Open builds the runner for a dependency. ex is the executor for the via
// target when the dependency routes through one.
func Open(dep *config.Dependency, ex targets.Executor) (Runner, error) {
	if dep.Kind != config.KindPostgres && dep.Kind != config.KindMySQL {
		return nil, fmt.Errorf("dependency %s is %s, not a database", dep.Name, dep.Kind)
	}
	if dep.Via != nil {
		if ex == nil {
			return nil, fmt.Errorf("dependency %s: via target %s has no executor", dep.Name, dep.Via.Target)
		}
		return newCLI(dep.Kind, *dep.Via, ex), nil
	}
	if dep.DSN == "" {
		return nil, fmt.Errorf("dependency %s has no dsn", dep.Name)
	}
	return newDirect(dep.Kind, dep.DSN)
}

// ParseNumber turns a scalar result into a float.
func ParseNumber(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty result")
	}
	if strings.EqualFold(s, "t") || strings.EqualFold(s, "true") {
		return 1, nil
	}
	if strings.EqualFold(s, "f") || strings.EqualFold(s, "false") {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}

// Redact hides the password in a DSN for logs.
func Redact(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
		return u.String()
	}
	if strings.Contains(dsn, "@") {
		// user:pass@tcp(...) form
		if i := strings.Index(dsn, "@"); i > 0 {
			if j := strings.Index(dsn[:i], ":"); j > 0 {
				return dsn[:j+1] + "xxxxx" + dsn[i:]
			}
		}
	}
	var parts []string
	for _, f := range strings.Fields(dsn) {
		if strings.HasPrefix(strings.ToLower(f), "password=") {
			f = "password=xxxxx"
		}
		parts = append(parts, f)
	}
	return strings.Join(parts, " ")
}

// WithDatabase rewrites the database name inside a DSN of any supported form.
func WithDatabase(kind, dsn, db string) (string, error) {
	switch kind {
	case config.KindPostgres:
		if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
			u, err := url.Parse(dsn)
			if err != nil {
				return "", err
			}
			u.Path = "/" + db
			return u.String(), nil
		}
		var parts []string
		found := false
		for _, f := range strings.Fields(dsn) {
			if strings.HasPrefix(strings.ToLower(f), "dbname=") {
				f = "dbname=" + db
				found = true
			}
			parts = append(parts, f)
		}
		if !found {
			parts = append(parts, "dbname="+db)
		}
		return strings.Join(parts, " "), nil
	case config.KindMySQL:
		cfg, err := parseMySQLDSN(dsn)
		if err != nil {
			return "", err
		}
		cfg.DBName = db
		return cfg.FormatDSN(), nil
	}
	return "", fmt.Errorf("unsupported kind %s", kind)
}

// parseMySQLDSN accepts the Go driver form or a mysql:// URL.
func parseMySQLDSN(dsn string) (*mysqldrv.Config, error) {
	if strings.HasPrefix(dsn, "mysql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return nil, err
		}
		cfg := mysqldrv.NewConfig()
		cfg.Net = "tcp"
		cfg.Addr = u.Host
		if u.Port() == "" {
			cfg.Addr = u.Hostname() + ":3306"
		}
		if u.User != nil {
			cfg.User = u.User.Username()
			cfg.Passwd, _ = u.User.Password()
		}
		cfg.DBName = strings.TrimPrefix(u.Path, "/")
		cfg.Params = map[string]string{}
		for k, vs := range u.Query() {
			if len(vs) > 0 {
				switch k {
				case "parseTime":
					cfg.ParseTime = vs[0] == "true"
				case "tls":
					cfg.TLSConfig = vs[0]
				case "timeout":
					cfg.Timeout, _ = time.ParseDuration(vs[0])
				default:
					cfg.Params[k] = vs[0]
				}
			}
		}
		return cfg, nil
	}
	return mysqldrv.ParseDSN(dsn)
}

// ---- direct runner ----

type direct struct {
	kind string
	dsn  string
	mu   sync.Mutex
	pg   *pgxpool.Pool
	my   *sql.DB
}

func newDirect(kind, dsn string) (Runner, error) {
	d := &direct{kind: kind, dsn: dsn}
	if kind == config.KindMySQL {
		cfg, err := parseMySQLDSN(dsn)
		if err != nil {
			return nil, fmt.Errorf("mysql dsn: %w", err)
		}
		cfg.MultiStatements = true
		if cfg.Timeout == 0 {
			cfg.Timeout = 10 * time.Second
		}
		d.dsn = cfg.FormatDSN()
	}
	return d, nil
}

func (d *direct) Kind() string     { return d.kind }
func (d *direct) Describe() string { return d.kind + " " + Redact(d.dsn) }

func (d *direct) pgPool(ctx context.Context) (*pgxpool.Pool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pg != nil {
		return d.pg, nil
	}
	cfg, err := pgxpool.ParseConfig(d.dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres dsn: %w", err)
	}
	cfg.MaxConns = 3
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second
	// The simple protocol returns every value as text (so scalars can be
	// returned verbatim) and accepts multi-statement seed scripts.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	d.pg = pool
	return pool, nil
}

func (d *direct) myDB() (*sql.DB, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.my != nil {
		return d.my, nil
	}
	db, err := sql.Open("mysql", d.dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(3)
	db.SetConnMaxLifetime(5 * time.Minute)
	d.my = db
	return db, nil
}

func (d *direct) Ping(ctx context.Context) (string, error) {
	v, err := d.Scalar(ctx, "SELECT version()")
	if err != nil {
		return "", err
	}
	if i := strings.Index(v, " on "); i > 0 && d.kind == config.KindPostgres {
		v = v[:i]
	}
	if len(v) > 80 {
		v = v[:80]
	}
	return v, nil
}

func (d *direct) Scalar(ctx context.Context, query string) (string, error) {
	switch d.kind {
	case config.KindPostgres:
		pool, err := d.pgPool(ctx)
		if err != nil {
			return "", err
		}
		// No arguments means pgx uses the simple protocol, so values arrive
		// as text and can be returned verbatim whatever their type.
		rows, err := pool.Query(ctx, query)
		if err != nil {
			return "", err
		}
		defer rows.Close()
		if !rows.Next() {
			if rows.Err() != nil {
				return "", rows.Err()
			}
			return "", errors.New("query returned no rows")
		}
		raw := rows.RawValues()
		if len(raw) == 0 {
			return "", errors.New("query returned no columns")
		}
		v := string(raw[0])
		rows.Close()
		return v, rows.Err()
	case config.KindMySQL:
		db, err := d.myDB()
		if err != nil {
			return "", err
		}
		var v sql.NullString
		if err := db.QueryRowContext(ctx, query).Scan(&v); err != nil {
			return "", err
		}
		return v.String, nil
	}
	return "", fmt.Errorf("unsupported kind %s", d.kind)
}

func (d *direct) Exec(ctx context.Context, script string, sink targets.LineSink) error {
	switch d.kind {
	case config.KindPostgres:
		pool, err := d.pgPool(ctx)
		if err != nil {
			return err
		}
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return err
		}
		defer conn.Release()
		tag, err := conn.Exec(ctx, script)
		if err != nil {
			var pgErr interface{ Error() string }
			if errors.As(err, &pgErr) {
				return fmt.Errorf("%w", err)
			}
			return err
		}
		if sink != nil {
			sink("stdout", tag.String())
		}
		return nil
	case config.KindMySQL:
		db, err := d.myDB()
		if err != nil {
			return err
		}
		res, err := db.ExecContext(ctx, script)
		if err != nil {
			return err
		}
		if sink != nil {
			if n, err := res.RowsAffected(); err == nil {
				sink("stdout", fmt.Sprintf("ok (%d rows affected by last statement)", n))
			}
		}
		return nil
	}
	return fmt.Errorf("unsupported kind %s", d.kind)
}

func (d *direct) WithDatabase(db string) (Runner, error) {
	dsn, err := WithDatabase(d.kind, d.dsn, db)
	if err != nil {
		return nil, err
	}
	return newDirect(d.kind, dsn)
}

func (d *direct) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pg != nil {
		d.pg.Close()
		d.pg = nil
	}
	if d.my != nil {
		err := d.my.Close()
		d.my = nil
		return err
	}
	return nil
}

// ---- cli runner ----

type cli struct {
	kind string
	via  config.Via
	ex   targets.Executor
}

func newCLI(kind string, via config.Via, ex targets.Executor) Runner {
	return &cli{kind: kind, via: via, ex: ex}
}

func (c *cli) Kind() string { return c.kind }

func (c *cli) Describe() string {
	loc := c.via.Target
	switch {
	case c.via.Container != "":
		loc += "/" + c.via.Container
	case c.via.Pod != "":
		loc += "/" + c.via.Pod
	case c.via.InstanceID != "":
		loc += "/" + c.via.InstanceID
	}
	return fmt.Sprintf("%s via %s (%s)", c.kind, loc, Redact(c.via.DSN))
}

func (c *cli) spec() targets.RunSpec {
	return targets.RunSpec{Container: c.via.Container, Pod: c.via.Pod, Namespace: c.via.Namespace, InstanceID: c.via.InstanceID}
}

// command builds the client invocation. Without a DSN the defaults match the
// official postgres and mysql images, where the tool runs inside the
// database container itself.
func (c *cli) command(scalar string) (script string) {
	switch c.kind {
	case config.KindPostgres:
		tool := c.via.Tool
		if tool == "" {
			tool = "psql"
		}
		conn := ""
		if c.via.DSN != "" {
			conn = targets.ShellQuote(c.via.DSN)
		} else {
			conn = `-U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-${POSTGRES_USER:-postgres}}"`
		}
		if scalar != "" {
			return fmt.Sprintf("%s %s -X -tA -c %s", tool, conn, targets.ShellQuote(scalar))
		}
		return fmt.Sprintf("%s %s -X -q -v ON_ERROR_STOP=1 -f -", tool, conn)
	case config.KindMySQL:
		tool := c.via.Tool
		if tool == "" {
			tool = "mysql"
		}
		conn := ""
		if c.via.DSN != "" {
			cfg, err := parseMySQLDSN(c.via.DSN)
			if err == nil {
				host, port, _ := strings.Cut(cfg.Addr, ":")
				conn = "-h " + targets.ShellQuote(host)
				if port != "" {
					conn += " -P " + targets.ShellQuote(port)
				}
				if cfg.User != "" {
					conn += " -u " + targets.ShellQuote(cfg.User)
				}
				if cfg.Passwd != "" {
					conn = "MYSQL_PWD=" + targets.ShellQuote(cfg.Passwd) + " " + tool + " " + conn
					tool = ""
				}
				if cfg.DBName != "" {
					conn += " " + targets.ShellQuote(cfg.DBName)
				}
			}
		} else {
			conn = `-uroot -p"$MYSQL_ROOT_PASSWORD" "$MYSQL_DATABASE"`
		}
		prefix := tool
		if prefix != "" {
			prefix += " "
		}
		if scalar != "" {
			return fmt.Sprintf("%s%s -N -B -e %s", prefix, conn, targets.ShellQuote(scalar))
		}
		return fmt.Sprintf("%s%s --batch", prefix, conn)
	}
	return "false"
}

func (c *cli) Ping(ctx context.Context) (string, error) {
	v, err := c.Scalar(ctx, "SELECT version()")
	if err != nil {
		return "", err
	}
	if len(v) > 80 {
		v = v[:80]
	}
	return v, nil
}

func (c *cli) Scalar(ctx context.Context, query string) (string, error) {
	spec := c.spec()
	spec.Argv = []string{"sh", "-c", c.command(query)}
	spec.Timeout = 60 * time.Second
	res, err := c.ex.Run(ctx, spec, nil)
	if err != nil {
		return "", err
	}
	if !res.OK() {
		return "", res.Error()
	}
	out := strings.TrimSpace(res.Stdout)
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	return out, nil
}

func (c *cli) Exec(ctx context.Context, script string, sink targets.LineSink) error {
	spec := c.spec()
	spec.Argv = []string{"sh", "-c", c.command("")}
	spec.Stdin = strings.NewReader(script)
	spec.StdinLen = int64(len(script))
	res, err := c.ex.Run(ctx, spec, sink)
	if err != nil {
		return err
	}
	return res.Error()
}

func (c *cli) WithDatabase(db string) (Runner, error) {
	via := c.via
	if via.DSN == "" {
		switch c.kind {
		case config.KindPostgres:
			via.DSN = "postgres:///" + db
			// Inside the official image a bare URL uses the socket and the
			// POSTGRES_USER default; keep the user explicit.
			via.DSN = "-U \"${POSTGRES_USER:-postgres}\" -d " + db
			return &rawCLI{cli: cli{kind: c.kind, via: via, ex: c.ex}}, nil
		case config.KindMySQL:
			via.DSN = ""
			return &rawCLI{cli: cli{kind: c.kind, via: via, ex: c.ex}, mysqlDB: db}, nil
		}
	}
	dsn, err := WithDatabase(c.kind, via.DSN, db)
	if err != nil {
		return nil, err
	}
	via.DSN = dsn
	return newCLI(c.kind, via, c.ex), nil
}

func (c *cli) Close() error { return nil }

// rawCLI is a cli whose DSN is already a rendered flag string (used by
// WithDatabase for the in-container defaults).
type rawCLI struct {
	cli
	mysqlDB string
}

func (r *rawCLI) command(scalar string) string {
	switch r.kind {
	case config.KindPostgres:
		tool := r.via.Tool
		if tool == "" {
			tool = "psql"
		}
		if scalar != "" {
			return fmt.Sprintf("%s %s -X -tA -c %s", tool, r.via.DSN, targets.ShellQuote(scalar))
		}
		return fmt.Sprintf("%s %s -X -q -v ON_ERROR_STOP=1 -f -", tool, r.via.DSN)
	case config.KindMySQL:
		tool := r.via.Tool
		if tool == "" {
			tool = "mysql"
		}
		conn := `-uroot -p"$MYSQL_ROOT_PASSWORD" ` + targets.ShellQuote(r.mysqlDB)
		if scalar != "" {
			return fmt.Sprintf("%s %s -N -B -e %s", tool, conn, targets.ShellQuote(scalar))
		}
		return fmt.Sprintf("%s %s --batch", tool, conn)
	}
	return "false"
}

func (r *rawCLI) Scalar(ctx context.Context, query string) (string, error) {
	spec := r.spec()
	spec.Argv = []string{"sh", "-c", r.command(query)}
	spec.Timeout = 60 * time.Second
	res, err := r.ex.Run(ctx, spec, nil)
	if err != nil {
		return "", err
	}
	if !res.OK() {
		return "", res.Error()
	}
	out := strings.TrimSpace(res.Stdout)
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	return out, nil
}

func (r *rawCLI) Exec(ctx context.Context, script string, sink targets.LineSink) error {
	spec := r.spec()
	spec.Argv = []string{"sh", "-c", r.command("")}
	spec.Stdin = strings.NewReader(script)
	spec.StdinLen = int64(len(script))
	res, err := r.ex.Run(ctx, spec, sink)
	if err != nil {
		return err
	}
	return res.Error()
}

// Compile-time check that pgx is linked (pgxpool already implies it).
var _ = pgx.ErrNoRows
