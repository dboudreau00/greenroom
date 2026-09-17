package sqlrun

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/targets"
)

func TestWithDatabase(t *testing.T) {
	cases := []struct{ kind, dsn, db, want string }{
		{config.KindPostgres, "postgres://u:p@h:5432/demo?sslmode=disable", "postgres", "postgres://u:p@h:5432/postgres?sslmode=disable"},
		{config.KindPostgres, "host=h user=u dbname=demo", "postgres", "host=h user=u dbname=postgres"},
		{config.KindPostgres, "host=h user=u", "postgres", "host=h user=u dbname=postgres"},
		{config.KindMySQL, "u:p@tcp(h:3306)/demo?parseTime=true", "other", "u:p@tcp(h:3306)/other?parseTime=true"},
		{config.KindMySQL, "mysql://u:p@h/demo", "other", "u:p@tcp(h:3306)/other"},
	}
	for _, c := range cases {
		got, err := WithDatabase(c.kind, c.dsn, c.db)
		if err != nil || got != c.want {
			t.Errorf("WithDatabase(%s, %q) = %q, %v; want %q", c.kind, c.dsn, got, err, c.want)
		}
	}
	if _, err := WithDatabase("oracle", "x", "y"); err == nil {
		t.Fatal("unsupported kind accepted")
	}
}

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"postgres://u:secret@h/db":        "postgres://u:xxxxx@h/db",
		"u:secret@tcp(h:3306)/db":         "u:xxxxx@tcp(h:3306)/db",
		"host=h password=secret user=u":   "host=h password=xxxxx user=u",
		"postgres://u@h/db":               "postgres://u@h/db",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseNumber(t *testing.T) {
	for in, want := range map[string]float64{" 12 ": 12, "3.5": 3.5, "t": 1, "false": 0, "1e3": 1000} {
		if got, err := ParseNumber(in); err != nil || got != want {
			t.Errorf("ParseNumber(%q) = %v %v", in, got, err)
		}
	}
	if _, err := ParseNumber("twelve"); err == nil {
		t.Fatal("text accepted")
	}
	if _, err := ParseNumber(""); err == nil {
		t.Fatal("empty accepted")
	}
}

type recordingExec struct {
	spec targets.RunSpec
	out  string
	code int
}

func (r *recordingExec) Run(ctx context.Context, spec targets.RunSpec, sink targets.LineSink) (targets.Result, error) {
	r.spec = spec
	if sink != nil {
		sink("stdout", r.out)
	}
	return targets.Result{ExitCode: r.code, Stdout: r.out}, nil
}

func TestCLIRunner(t *testing.T) {
	ex := &recordingExec{out: "42\nignored\n"}
	dep := &config.Dependency{Name: "db", Kind: config.KindPostgres, Via: &config.Via{Target: "docker", Container: "pg", DSN: "postgres://demo@localhost/demo"}}
	r, err := Open(dep, ex)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Describe(), "docker/pg") {
		t.Fatalf("describe %q", r.Describe())
	}
	v, err := r.Scalar(context.Background(), "select 42")
	if err != nil || v != "42" {
		t.Fatalf("scalar %q %v", v, err)
	}
	cmd := ex.spec.Argv[2]
	if !strings.Contains(cmd, "psql postgres://demo@localhost/demo -X -tA -c 'select 42'") || ex.spec.Container != "pg" {
		t.Fatalf("command %q container %q", cmd, ex.spec.Container)
	}
	if err := r.Exec(context.Background(), "create table x(); insert into x default values;", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ex.spec.Argv[2], "-v ON_ERROR_STOP=1 -f -") || ex.spec.Stdin == nil {
		t.Fatalf("exec command %q", ex.spec.Argv[2])
	}
	maint, err := r.WithDatabase("postgres")
	if err != nil {
		t.Fatal(err)
	}
	maint.Scalar(context.Background(), "select 1")
	if !strings.Contains(ex.spec.Argv[2], "postgres://demo@localhost/postgres") {
		t.Fatalf("with database %q", ex.spec.Argv[2])
	}
	// No DSN: defaults for the official image, including the maintenance db.
	dep2 := &config.Dependency{Name: "db", Kind: config.KindPostgres, Via: &config.Via{Target: "docker", Container: "pg"}}
	r2, _ := Open(dep2, ex)
	r2.Scalar(context.Background(), "select 1")
	if !strings.Contains(ex.spec.Argv[2], `-U "${POSTGRES_USER:-postgres}"`) {
		t.Fatalf("default psql flags %q", ex.spec.Argv[2])
	}
	m2, _ := r2.WithDatabase("postgres")
	m2.Exec(context.Background(), "select 1", nil)
	if !strings.Contains(ex.spec.Argv[2], "-d postgres") {
		t.Fatalf("maintenance default %q", ex.spec.Argv[2])
	}
	ex.code = 2
	ex.out = "ERROR: boom"
	if _, err := r.Scalar(context.Background(), "select 1"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error propagation: %v", err)
	}
	// mysql with a URL DSN renders flags and keeps the password out of argv.
	ex.code = 0
	ex.out = "7\n"
	my := &config.Dependency{Name: "my", Kind: config.KindMySQL, Via: &config.Via{Target: "docker", Container: "mysql", DSN: "mysql://root:pw@db.local:3307/shop"}}
	rm, _ := Open(my, ex)
	if v, err := rm.Scalar(context.Background(), "select 7"); err != nil || v != "7" {
		t.Fatalf("mysql scalar %q %v", v, err)
	}
	cmd = ex.spec.Argv[2]
	if !strings.Contains(cmd, "MYSQL_PWD=pw mysql -h db.local -P 3307 -u root shop -N -B -e 'select 7'") {
		t.Fatalf("mysql command %q", cmd)
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open(&config.Dependency{Kind: config.KindHTTP}, nil); err == nil {
		t.Fatal("http accepted")
	}
	if _, err := Open(&config.Dependency{Kind: config.KindPostgres}, nil); err == nil {
		t.Fatal("no dsn accepted")
	}
	if _, err := Open(&config.Dependency{Kind: config.KindPostgres, Via: &config.Via{Target: "x"}}, nil); err == nil {
		t.Fatal("via without executor accepted")
	}
	if _, err := Open(&config.Dependency{Kind: config.KindMySQL, DSN: "this is not a dsn"}, nil); err == nil {
		t.Fatal("bad mysql dsn accepted")
	}
}

// TestLivePostgres runs when GREENROOM_TEST_PG_DSN points at a database.
func TestLivePostgres(t *testing.T) {
	dsn := os.Getenv("GREENROOM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("GREENROOM_TEST_PG_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := Open(&config.Dependency{Name: "pg", Kind: config.KindPostgres, DSN: dsn}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if v, err := r.Ping(ctx); err != nil || !strings.HasPrefix(v, "PostgreSQL") {
		t.Fatalf("ping %q %v", v, err)
	}
	if v, err := r.Scalar(ctx, "SELECT 1 + 1"); err != nil || v != "2" {
		t.Fatalf("scalar %q %v", v, err)
	}
	var lines []string
	err = r.Exec(ctx, "DROP TABLE IF EXISTS greenroom_t; CREATE TABLE greenroom_t(id int); INSERT INTO greenroom_t VALUES (1), (2);", func(_, l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatal(err)
	}
	if v, err := r.Scalar(ctx, "SELECT count(*) FROM greenroom_t"); err != nil || v != "2" {
		t.Fatalf("count %q %v", v, err)
	}
	if v, _ := r.Scalar(ctx, "SELECT count(*) > 1 FROM greenroom_t"); v != "t" {
		t.Fatalf("bool text %q", v)
	}
	if err := r.Exec(ctx, "SELECT * FROM missing_table", nil); err == nil {
		t.Fatal("bad sql accepted")
	}
	maint, err := r.WithDatabase("postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer maint.Close()
	if v, err := maint.Scalar(ctx, "SELECT current_database()"); err != nil || v != "postgres" {
		t.Fatalf("maintenance db %q %v", v, err)
	}
	r.Exec(ctx, "DROP TABLE greenroom_t", nil)
}
