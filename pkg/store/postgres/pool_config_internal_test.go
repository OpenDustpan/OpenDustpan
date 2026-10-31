package postgres

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// poolCase is one URL query and the pool settings that it must give.
type poolCase struct {
	name         string
	query        string
	wantLifetime time.Duration
	wantHealth   time.Duration
	wantValidate bool
}

// TestPoolConfigKeepsFailoverSettings proves that the settings of the
// database URL for a failover reach the pool as the URL gives them, and that
// the store only adds the search path of each connection.
func TestPoolConfigKeepsFailoverSettings(t *testing.T) {
	t.Parallel()

	const base = "postgres://db.example:5432/dustpan?"

	tests := []poolCase{
		{"defaults", "sslmode=disable", time.Hour, time.Minute, false},
		{"read-write", "sslmode=disable&target_session_attrs=read-write", time.Hour, time.Minute, true},
		{"read-only is kept", "sslmode=disable&target_session_attrs=read-only", time.Hour, time.Minute, true},
		{
			"lifetime and health check", "sslmode=disable&pool_max_conn_lifetime=5m&pool_health_check_period=10s",
			5 * time.Minute, 10 * time.Second, false,
		},
		{
			"all three",
			"sslmode=disable&target_session_attrs=read-write&pool_max_conn_lifetime=30m&pool_health_check_period=15s",
			30 * time.Minute, 15 * time.Second, true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := poolConfig(base + tc.query)
			if err != nil {
				t.Fatalf("poolConfig: %v", err)
			}

			checkPoolConfig(t, cfg, tc)
		})
	}
}

// checkPoolConfig checks the pool settings of cfg.
func checkPoolConfig(t *testing.T, cfg *pgxpool.Config, want poolCase) {
	t.Helper()

	if cfg.MaxConnLifetime != want.wantLifetime {
		t.Errorf("MaxConnLifetime = %s, want %s", cfg.MaxConnLifetime, want.wantLifetime)
	}

	if cfg.HealthCheckPeriod != want.wantHealth {
		t.Errorf("HealthCheckPeriod = %s, want %s", cfg.HealthCheckPeriod, want.wantHealth)
	}

	if got := cfg.ConnConfig.ValidateConnect != nil; got != want.wantValidate {
		t.Errorf("ValidateConnect set = %t, want %t", got, want.wantValidate)
	}

	if cfg.AfterConnect == nil {
		t.Error("AfterConnect is nil: the store must set the search path of each connection")
	}
}

// TestPoolConfigRefusesBadSetting proves that a wrong value of a failover
// setting fails the open, not a silent default.
func TestPoolConfigRefusesBadSetting(t *testing.T) {
	t.Parallel()

	for _, query := range []string{
		"target_session_attrs=writable",
		"pool_max_conn_lifetime=soon",
		"pool_health_check_period=often",
	} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()

			if _, err := poolConfig("postgres://db.example:5432/dustpan?sslmode=disable&" + query); err == nil {
				t.Fatalf("poolConfig(%q) answered no error", query)
			}
		})
	}
}
