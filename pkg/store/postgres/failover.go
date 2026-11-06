package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The names of the failover settings that the database URL can set.
const (
	settingTargetSessionAttrs = "target_session_attrs"
	settingConnectTimeout     = "connect_timeout"
	settingMaxConnLifetime    = "pool_max_conn_lifetime"
	settingLifetimeJitter     = "pool_max_conn_lifetime_jitter"
	settingHealthCheckPeriod  = "pool_health_check_period"
)

// The sources of the value of a failover setting.
const (
	// SourceURL means that the database URL names the setting.
	SourceURL = "url"
	// SourceDefault means that the URL does not name the setting, so the
	// server sets its own default.
	SourceDefault = "default"
)

// ConnectCheckReadWrite is the check that each new connection of the pool
// passes: the server is not in recovery and its transactions are not
// read-only.
const ConnectCheckReadWrite = "read-write"

// The defaults and the bounds of the failover settings. The server sets a
// default when the URL does not name the setting, and refuses a value outside
// the bounds.
const (
	defaultTargetSessionAttrs = "read-write"
	defaultConnectTimeout     = 5 * time.Second
	minConnectTimeout         = time.Second
	maxConnectTimeout         = 60 * time.Second
	defaultMaxConnLifetime    = 30 * time.Minute
	minMaxConnLifetime        = time.Minute
	maxMaxConnLifetime        = 24 * time.Hour
	defaultLifetimeJitter     = time.Minute
	defaultHealthCheckPeriod  = 10 * time.Second
	minHealthCheckPeriod      = time.Second
	maxHealthCheckPeriod      = 10 * time.Minute
)

// failoverSettingCount is the number of failover settings.
const failoverSettingCount = 5

// The TCP keepalive of each connection of the pool. A dead idle connection
// closes after about idle + interval * count.
const (
	keepAliveIdle     = 15 * time.Second
	keepAliveInterval = 5 * time.Second
	keepAliveCount    = 3
)

// ErrFailoverSetting reports a failover setting of the database URL that is
// not in the allowlist. The server does not start.
var ErrFailoverSetting = errors.New("failover setting is not valid")

// errNotWritable reports a server that refuses writes: it is in recovery, or
// its transactions are read-only by default.
var errNotWritable = errors.New("database server does not accept writes")

// FailoverSetting is the effective value of one failover setting of the pool.
type FailoverSetting struct {
	// Name is the name of the setting in the database URL.
	Name string
	// Value is the value that applies, as the URL gives it or as the default.
	Value string
	// Source is SourceURL or SourceDefault.
	Source string
}

// FailoverSettings are the effective failover settings of the pool. They hold
// no host, user, password or other part of the URL.
type FailoverSettings struct {
	// Settings holds one entry for each failover setting, in a fixed order.
	Settings []FailoverSetting
	// ConnectCheck is the check that each new connection passes. It is
	// ConnectCheckReadWrite for both allowed values of target_session_attrs.
	ConnectCheck string
	// StatementDeadline bounds each round trip of the API paths.
	StatementDeadline time.Duration
	// ServerStatementTimeout is the statement_timeout of each connection.
	ServerStatementTimeout time.Duration
	// ServerIdleInTransactionTimeout is the
	// idle_in_transaction_session_timeout of each connection.
	ServerIdleInTransactionTimeout time.Duration
	// ClientConnectionCheckInterval is the client_connection_check_interval
	// of each connection.
	ClientConnectionCheckInterval time.Duration
}

// failoverPlan is the result of the check of the failover settings: the
// values to apply to the pool and the settings to report.
type failoverPlan struct {
	connectTimeout time.Duration
	lifetime       time.Duration
	jitter         time.Duration
	healthCheck    time.Duration
	report         FailoverSettings
}

// readFailoverSettings reads the failover settings of the database URL raw
// and checks each against its allowlist. A setting that the URL does not name
// gets its default. It never changes a value to make it valid: a value outside
// the allowlist is an error that wraps ErrFailoverSetting and names the
// setting, the value and the allowed values.
func readFailoverSettings(raw string) (failoverPlan, error) {
	u, err := url.Parse(raw)
	if err != nil {
		// The error of url.Parse holds the URL, and the URL can hold a
		// password, so the error names only the cause.
		return failoverPlan{}, fmt.Errorf("%w: the database URL does not parse", ErrFailoverSetting)
	}

	// Defense in depth: serve passes only postgres:// and postgresql:// URLs
	// to the store, and Open is also an SDK entry point. A keyword/value
	// string parses as a URL with an empty query, so its settings would skip
	// the allowlist and get defaults without notice. The check stays here so
	// that a new caller cannot skip it.
	if (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Opaque != "" {
		return failoverPlan{}, fmt.Errorf("%w: the database URL must be a postgres:// or postgresql:// URL",
			ErrFailoverSetting)
	}

	query := u.Query()
	plan := failoverPlan{
		connectTimeout: defaultConnectTimeout,
		lifetime:       defaultMaxConnLifetime,
		jitter:         defaultLifetimeJitter,
		healthCheck:    defaultHealthCheckPeriod,
		report: FailoverSettings{
			Settings:                       make([]FailoverSetting, 0, failoverSettingCount),
			ConnectCheck:                   ConnectCheckReadWrite,
			StatementDeadline:              statementDeadline,
			ServerStatementTimeout:         serverStatementTimeout,
			ServerIdleInTransactionTimeout: serverIdleInTxTimeout,
			ClientConnectionCheckInterval:  clientCheckInterval,
		},
	}

	steps := []func(url.Values, *failoverPlan) error{
		readTargetSessionAttrs, readConnectTimeout, readLifetime, readJitter, readHealthCheck,
	}
	for _, step := range steps {
		if stepErr := step(query, &plan); stepErr != nil {
			return failoverPlan{}, stepErr
		}
	}

	return plan, nil
}

// lookup returns the single value of name in query, and whether the query
// names it. A name that appears more than once is an error: the server does
// not choose one of the values.
func lookup(query url.Values, name string) (string, bool, error) {
	values, ok := query[name]
	if !ok {
		return "", false, nil
	}

	if len(values) != 1 {
		return "", false, fmt.Errorf(
			"%w: %s appears %d times in the database URL",
			ErrFailoverSetting,
			name,
			len(values),
		)
	}

	return values[0], true, nil
}

// readTargetSessionAttrs accepts read-write and primary. Both get the
// read-write check of each new connection.
func readTargetSessionAttrs(query url.Values, plan *failoverPlan) error {
	value, named, err := lookup(query, settingTargetSessionAttrs)
	if err != nil {
		return err
	}

	if !named {
		plan.add(settingTargetSessionAttrs, defaultTargetSessionAttrs, SourceDefault)

		return nil
	}

	switch value {
	case "read-write", "primary":
		plan.add(settingTargetSessionAttrs, value, SourceURL)

		return nil
	default:
		return fmt.Errorf("%w: %s=%q: allowed values are read-write and primary",
			ErrFailoverSetting, settingTargetSessionAttrs, value)
	}
}

// readConnectTimeout accepts a whole number of seconds in its bounds.
func readConnectTimeout(query url.Values, plan *failoverPlan) error {
	value, named, err := lookup(query, settingConnectTimeout)
	if err != nil {
		return err
	}

	if !named {
		plan.add(settingConnectTimeout, strconv.Itoa(int(defaultConnectTimeout/time.Second)), SourceDefault)

		return nil
	}

	seconds, parseErr := strconv.Atoi(value)

	timeout := time.Duration(seconds) * time.Second
	if parseErr != nil || timeout < minConnectTimeout || timeout > maxConnectTimeout {
		return fmt.Errorf("%w: %s=%q: allowed values are whole seconds from %d to %d",
			ErrFailoverSetting, settingConnectTimeout, value,
			int(minConnectTimeout/time.Second), int(maxConnectTimeout/time.Second))
	}

	plan.connectTimeout = timeout
	plan.add(settingConnectTimeout, value, SourceURL)

	return nil
}

// durationRule is the default and the bounds of one duration setting.
type durationRule struct {
	name      string
	def       time.Duration
	low, high time.Duration
}

// readDuration reads the duration setting of rule.
func readDuration(query url.Values, plan *failoverPlan, rule durationRule) (time.Duration, error) {
	name, def, low, high := rule.name, rule.def, rule.low, rule.high

	value, named, err := lookup(query, name)
	if err != nil {
		return 0, err
	}

	if !named {
		plan.add(name, def.String(), SourceDefault)

		return def, nil
	}

	d, parseErr := time.ParseDuration(value)
	if parseErr != nil || d < low || d > high {
		return 0, fmt.Errorf("%w: %s=%q: allowed values are durations from %s to %s",
			ErrFailoverSetting, name, value, low, high)
	}

	plan.add(name, value, SourceURL)

	return d, nil
}

// readLifetime reads the maximum lifetime of a connection.
func readLifetime(query url.Values, plan *failoverPlan) error {
	d, err := readDuration(query, plan, durationRule{
		name: settingMaxConnLifetime, def: defaultMaxConnLifetime, low: minMaxConnLifetime, high: maxMaxConnLifetime,
	})
	plan.lifetime = d

	return err
}

// readJitter reads the jitter of the lifetime. Its upper bound is the
// lifetime, so it runs after readLifetime.
func readJitter(query url.Values, plan *failoverPlan) error {
	d, err := readDuration(query, plan, durationRule{
		name: settingLifetimeJitter, def: min(defaultLifetimeJitter, plan.lifetime), low: 0, high: plan.lifetime,
	})
	plan.jitter = d

	return err
}

// readHealthCheck reads the period of the health check of idle connections.
func readHealthCheck(query url.Values, plan *failoverPlan) error {
	d, err := readDuration(query, plan, durationRule{
		name: settingHealthCheckPeriod, def: defaultHealthCheckPeriod, low: minHealthCheckPeriod,
		high: maxHealthCheckPeriod,
	})
	plan.healthCheck = d

	return err
}

// add records the effective value of one setting.
func (p *failoverPlan) add(name, value, source string) {
	p.report.Settings = append(p.report.Settings, FailoverSetting{Name: name, Value: value, Source: source})
}

// apply sets the failover settings of plan on cfg. It replaces the values that
// pgx read from the URL or from the PG environment variables, so the values of
// plan are the ones that apply.
func (p *failoverPlan) apply(cfg *pgxpool.Config) {
	cfg.MaxConnLifetime = p.lifetime
	cfg.MaxConnLifetimeJitter = p.jitter
	cfg.HealthCheckPeriod = p.healthCheck
	cfg.ConnConfig.ConnectTimeout = p.connectTimeout
	cfg.ConnConfig.ValidateConnect = validateWritable

	// The zero dialer has the defaults of package net. The pool sets only its
	// timeout and its keepalive.
	var dialer net.Dialer

	dialer.Timeout = p.connectTimeout
	dialer.KeepAliveConfig = net.KeepAliveConfig{
		Enable:   true,
		Idle:     keepAliveIdle,
		Interval: keepAliveInterval,
		Count:    keepAliveCount,
	}
	cfg.ConnConfig.DialFunc = dialer.DialContext
}

// writableQuery reads whether the server is in recovery and whether its
// transactions are read-only.
const writableQuery = "SELECT pg_is_in_recovery(), current_setting('transaction_read_only')"

// validateWritable is the ValidateConnect function of the pool. It refuses a
// connection to a server in recovery and to a server whose transactions are
// read-only. An answer with an unexpected shape also refuses the connection.
func validateWritable(ctx context.Context, conn *pgconn.PgConn) error {
	results, err := conn.Exec(ctx, writableQuery).ReadAll()
	if err != nil {
		return fmt.Errorf("check that the server accepts writes: %w", err)
	}

	return writableAnswer(results)
}

// writableAnswer checks the answer of writableQuery. It fails closed: any
// other shape or value than a server that accepts writes is errNotWritable.
func writableAnswer(results []*pgconn.Result) error {
	if len(results) != 1 || results[0] == nil || len(results[0].Rows) != 1 || len(results[0].Rows[0]) != 2 {
		return fmt.Errorf("%w: the check gave an answer of an unexpected shape", errNotWritable)
	}

	row := results[0].Rows[0]
	if string(row[0]) != "f" || string(row[1]) != "off" {
		return errNotWritable
	}

	return nil
}
