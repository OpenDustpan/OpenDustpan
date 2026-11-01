package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolBase is the database URL that each case of the pool tests extends.
const poolBase = "postgres://db.example:5432/dustpan?sslmode=disable"

// poolCase is one URL query and the pool settings that it must give.
type poolCase struct {
	name        string
	query       string
	wantTimeout time.Duration
	wantLife    time.Duration
	wantJitter  time.Duration
	wantHealth  time.Duration
	wantSources map[string]string
}

// defaultSources are the sources of a URL that names no failover setting.
func defaultSources() map[string]string {
	return map[string]string{
		settingTargetSessionAttrs: SourceDefault,
		settingConnectTimeout:     SourceDefault,
		settingMaxConnLifetime:    SourceDefault,
		settingLifetimeJitter:     SourceDefault,
		settingHealthCheckPeriod:  SourceDefault,
	}
}

// withSources returns the default sources with the named settings from the
// URL.
func withSources(names ...string) map[string]string {
	sources := defaultSources()
	for _, name := range names {
		sources[name] = SourceURL
	}

	return sources
}

// TestPoolConfigAppliesFailoverSettings proves that the pool gets the default
// of each failover setting that the URL does not name, and the value of each
// setting that the URL names inside its bounds. Each new connection gets the
// read-write check, also for target_session_attrs=primary.
func TestPoolConfigAppliesFailoverSettings(t *testing.T) {
	t.Parallel()

	tests := []poolCase{
		{
			"defaults", "", 5 * time.Second, 30 * time.Minute, time.Minute, 10 * time.Second,
			defaultSources(),
		},
		{
			"read-write", "&target_session_attrs=read-write", 5 * time.Second, 30 * time.Minute, time.Minute,
			10 * time.Second, withSources(settingTargetSessionAttrs),
		},
		{
			"primary gets the read-write check", "&target_session_attrs=primary", 5 * time.Second,
			30 * time.Minute, time.Minute, 10 * time.Second, withSources(settingTargetSessionAttrs),
		},
		{
			"lower edges", "&connect_timeout=1&pool_max_conn_lifetime=1m&pool_max_conn_lifetime_jitter=0s" +
				"&pool_health_check_period=1s",
			time.Second, time.Minute, 0, time.Second,
			withSources(settingConnectTimeout, settingMaxConnLifetime, settingLifetimeJitter, settingHealthCheckPeriod),
		},
		{
			"upper edges", "&connect_timeout=60&pool_max_conn_lifetime=24h&pool_max_conn_lifetime_jitter=24h" +
				"&pool_health_check_period=10m",
			time.Minute, 24 * time.Hour, 24 * time.Hour, 10 * time.Minute,
			withSources(settingConnectTimeout, settingMaxConnLifetime, settingLifetimeJitter, settingHealthCheckPeriod),
		},
		{
			"default jitter stays below a short lifetime", "&pool_max_conn_lifetime=1m", 5 * time.Second,
			time.Minute, time.Minute, 10 * time.Second, withSources(settingMaxConnLifetime),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, report, err := poolConfig(poolBase + tc.query)
			if err != nil {
				t.Fatalf("poolConfig: %v", err)
			}

			checkPoolConfig(t, cfg, tc)
			checkReport(t, report, tc.wantSources)
		})
	}
}

// checkPoolConfig checks the pool settings of cfg.
func checkPoolConfig(t *testing.T, cfg *pgxpool.Config, want poolCase) {
	t.Helper()

	if cfg.ConnConfig.ConnectTimeout != want.wantTimeout {
		t.Errorf("ConnectTimeout = %s, want %s", cfg.ConnConfig.ConnectTimeout, want.wantTimeout)
	}

	if cfg.MaxConnLifetime != want.wantLife {
		t.Errorf("MaxConnLifetime = %s, want %s", cfg.MaxConnLifetime, want.wantLife)
	}

	if cfg.MaxConnLifetimeJitter != want.wantJitter {
		t.Errorf("MaxConnLifetimeJitter = %s, want %s", cfg.MaxConnLifetimeJitter, want.wantJitter)
	}

	if cfg.HealthCheckPeriod != want.wantHealth {
		t.Errorf("HealthCheckPeriod = %s, want %s", cfg.HealthCheckPeriod, want.wantHealth)
	}

	if cfg.ConnConfig.ValidateConnect == nil {
		t.Error("ValidateConnect is nil: each new connection must pass the read-write check")
	}

	if cfg.ConnConfig.DialFunc == nil {
		t.Error("DialFunc is nil: the pool must dial with its keepalive")
	}

	if !strings.Contains(cfg.ConnConfig.RuntimeParams["search_path"], "_control") {
		t.Errorf("search_path start-up parameter = %q, want the live and the control schema",
			cfg.ConnConfig.RuntimeParams["search_path"])
	}

	for name, want := range map[string]string{
		"statement_timeout": "25000", "idle_in_transaction_session_timeout": "60000",
		"client_connection_check_interval": "5000",
	} {
		if got := cfg.ConnConfig.RuntimeParams[name]; got != want {
			t.Errorf("start-up parameter %s = %q, want %q", name, got, want)
		}
	}
}

// checkReport checks the source of each setting of the report, and that the
// report holds no part of the URL besides the values.
func checkReport(t *testing.T, report FailoverSettings, want map[string]string) {
	t.Helper()

	if report.ConnectCheck != ConnectCheckReadWrite {
		t.Errorf("ConnectCheck = %q, want %q", report.ConnectCheck, ConnectCheckReadWrite)
	}

	if report.StatementDeadline != statementDeadline {
		t.Errorf("StatementDeadline = %s, want %s", report.StatementDeadline, statementDeadline)
	}

	if len(report.Settings) != len(want) {
		t.Fatalf("report has %d settings, want %d", len(report.Settings), len(want))
	}

	for _, setting := range report.Settings {
		if setting.Source != want[setting.Name] {
			t.Errorf("source of %s = %q, want %q", setting.Name, setting.Source, want[setting.Name])
		}

		if strings.Contains(setting.Value, "db.example") {
			t.Errorf("value of %s holds the host: %q", setting.Name, setting.Value)
		}
	}
}

// TestPoolConfigRefusesFailoverSetting proves that each value outside the
// allowlist fails the open with ErrFailoverSetting, and that the error names
// the setting and holds no part of the URL besides the value.
func TestPoolConfigRefusesFailoverSetting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		query   string
		setting string
	}{
		{"any", "target_session_attrs=any", settingTargetSessionAttrs},
		{"read-only", "target_session_attrs=read-only", settingTargetSessionAttrs},
		{"standby", "target_session_attrs=standby", settingTargetSessionAttrs},
		{"prefer-standby", "target_session_attrs=prefer-standby", settingTargetSessionAttrs},
		{"unknown attrs", "target_session_attrs=writable", settingTargetSessionAttrs},
		{"empty attrs", "target_session_attrs=", settingTargetSessionAttrs},
		{"two attrs", "target_session_attrs=read-write&target_session_attrs=any", settingTargetSessionAttrs},
		{"timeout zero", "connect_timeout=0", settingConnectTimeout},
		{"timeout above", "connect_timeout=61", settingConnectTimeout},
		{"timeout negative", "connect_timeout=-1", settingConnectTimeout},
		{"timeout not a number", "connect_timeout=5s", settingConnectTimeout},
		{"lifetime below", "pool_max_conn_lifetime=59s", settingMaxConnLifetime},
		{"lifetime above", "pool_max_conn_lifetime=25h", settingMaxConnLifetime},
		{"lifetime text", "pool_max_conn_lifetime=soon", settingMaxConnLifetime},
		{"jitter negative", "pool_max_conn_lifetime_jitter=-1s", settingLifetimeJitter},
		{"jitter above lifetime", "pool_max_conn_lifetime=5m&pool_max_conn_lifetime_jitter=6m", settingLifetimeJitter},
		{"health below", "pool_health_check_period=500ms", settingHealthCheckPeriod},
		{"health above", "pool_health_check_period=11m", settingHealthCheckPeriod},
		{"health text", "pool_health_check_period=often", settingHealthCheckPeriod},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The password is a marker: the error must not hold it.
			marker := "hunter2"
			withPassword := "postgres://user:" + marker + "@db.example:5432/dustpan?sslmode=disable&"

			_, _, err := poolConfig(withPassword + tc.query)
			if !errors.Is(err, ErrFailoverSetting) {
				t.Fatalf("poolConfig(%q) = %v, want ErrFailoverSetting", tc.query, err)
			}

			if !strings.Contains(err.Error(), tc.setting) {
				t.Errorf("error %q does not name %s", err, tc.setting)
			}

			if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "db.example") {
				t.Errorf("error %q holds a part of the URL", err)
			}
		})
	}
}

// TestPoolConfigRefusesConnectionStringThatIsNotAURL proves the check at the
// point of use: a keyword/value connection string, a URL of another scheme
// and an opaque URL fail the open with ErrFailoverSetting. A keyword/value
// string parses as a URL with an empty query, so without the check its
// settings would skip the allowlist. A socket URL with no host passes.
func TestPoolConfigRefusesConnectionStringThatIsNotAURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		conn    string
		refused bool
	}{
		{"keyword/value", "host=db.example target_session_attrs=any connect_timeout=600", true},
		{"other scheme", "mysql://db.example/dustpan", true},
		{"opaque", "postgres:db.example", true},
		{"no scheme", "db.example:5432/dustpan", true},
		{"socket URL", "postgres:///dustpan?host=/var/run/postgresql", false},
		{"postgresql scheme", "postgresql://db.example/dustpan", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := poolConfig(tc.conn)
			if got := errors.Is(err, ErrFailoverSetting); got != tc.refused {
				t.Fatalf("poolConfig(%q) = %v, want refused %t", tc.conn, err, tc.refused)
			}
		})
	}
}

// TestWritableAnswerFailsClosed proves that an answer of the read-write check
// of a new connection with an unexpected shape or value counts as a server
// that refuses writes, so the pool does not keep the connection.
func TestWritableAnswerFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rows [][][]byte
		want error
	}{
		{"writable", [][][]byte{{[]byte("f"), []byte("off")}}, nil},
		{"in recovery", [][][]byte{{[]byte("t"), []byte("on")}}, errNotWritable},
		{"read-only default", [][][]byte{{[]byte("f"), []byte("on")}}, errNotWritable},
		{"recovery with writes", [][][]byte{{[]byte("t"), []byte("off")}}, errNotWritable},
		{"no row", nil, errNotWritable},
		{"two rows", [][][]byte{{[]byte("f"), []byte("off")}, {[]byte("f"), []byte("off")}}, errNotWritable},
		{"one column", [][][]byte{{[]byte("f")}}, errNotWritable},
		{"null values", [][][]byte{{nil, nil}}, errNotWritable},
		{"unknown value", [][][]byte{{[]byte("maybe"), []byte("off")}}, errNotWritable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := new(pgconn.Result)
			result.Rows = tc.rows

			err := writableAnswer([]*pgconn.Result{result})
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("writableAnswer = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestWritableAnswerRefusesNoResult proves that an answer with no result, or
// with a nil result, counts as a server that refuses writes.
func TestWritableAnswerRefusesNoResult(t *testing.T) {
	t.Parallel()

	for _, results := range [][]*pgconn.Result{nil, {nil}} {
		if err := writableAnswer(results); !errors.Is(err, errNotWritable) {
			t.Fatalf("writableAnswer(%v) = %v, want errNotWritable", results, err)
		}
	}
}

// TestPoolConfigRefusesURLThatDoesNotParse proves that a URL that does not
// parse fails the open, and that the error holds no part of the URL.
func TestPoolConfigRefusesURLThatDoesNotParse(t *testing.T) {
	t.Parallel()

	_, _, err := poolConfig("postgres://user:hunter2@db.example:bad port/dustpan")
	if !errors.Is(err, ErrFailoverSetting) {
		t.Fatalf("poolConfig = %v, want ErrFailoverSetting", err)
	}

	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error %q holds the password", err)
	}
}
