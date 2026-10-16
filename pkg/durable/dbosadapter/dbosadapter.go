// Package dbosadapter is the DBOS Transact adapter of the durable port. Each
// kind is a DBOS workflow; each step is a RunAsTransaction on the DBOS system
// pool, so the step's domain write and the DBOS step checkpoint commit
// together (the atomic mode needs the data source to be the system pool).
package dbosadapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	_ "github.com/dbos-inc/dbos-transact-golang/dbos/driver/sqlite" // SQLite system database.
	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/durable"
)

// Config selects the system database and the identity of the instance.
type Config struct {
	Pool       *pgxpool.Pool // PostgreSQL; DBOS closes it on Close
	SQLite     *sql.DB       // SQLite; DBOS closes it on Close
	Schema     string        // PostgreSQL schema of the DBOS tables
	ExecutorID string
	AppVersion string
	Logger     *slog.Logger
}

// Engine is one DBOS instance.
type Engine struct {
	ctx      dbos.Context
	ds       *dbos.DataSource
	postgres bool
	kinds    map[string]durable.Kind
}

// call is the input of the one registered workflow. DBOS identifies a
// workflow by its Go function name, so one generic workflow dispatches by
// kind: closures made per kind share one name and fail to register.
type call struct {
	Kind  string `json:"kind"`
	Input []byte `json:"input"`
}

var _ durable.Engine = (*Engine)(nil)

// New builds and launches a DBOS instance with the kinds and schedules.
func New(ctx context.Context, cfg Config, kinds []durable.Kind, sched []durable.Schedule) (*Engine, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	dcfg := dbos.Config{
		AppName:            "spike",
		SystemDBPool:       cfg.Pool,
		SQLiteSystemDB:     cfg.SQLite,
		DatabaseSchema:     cfg.Schema,
		ExecutorID:         cfg.ExecutorID,
		ApplicationVersion: cfg.AppVersion,
		Logger:             logger,
		// The default polls schedules every 30 s; the spike needs faster.
		SchedulerPollingInterval: 200 * time.Millisecond,
	}

	dctx, err := dbos.NewContext(ctx, dcfg)
	if err != nil {
		return nil, fmt.Errorf("dbos context: %w", err)
	}

	e := &Engine{ctx: dctx, postgres: cfg.Pool != nil, kinds: map[string]durable.Kind{}}
	for _, k := range kinds {
		e.kinds[k.Name] = k
	}

	if cfg.Pool != nil {
		e.ds, err = dbos.NewDataSource(dctx, cfg.Pool)
	} else {
		e.ds, err = dbos.NewDataSource(dctx, cfg.SQLite)
	}

	if err != nil {
		return nil, fmt.Errorf("dbos data source: %w", err)
	}

	dbos.RegisterWorkflow(dctx, e.runCall, dbos.WithWorkflowName("durable-run"))
	dbos.RegisterWorkflow(dctx, e.runScheduled, dbos.WithWorkflowName("durable-scheduled"))

	if err := dbos.Launch(dctx); err != nil {
		return nil, fmt.Errorf("dbos launch: %w", err)
	}

	for _, s := range sched {
		spec := dbos.ScheduleSpec{
			ScheduleName: s.Name,
			WorkflowName: "durable-scheduled",
			Schedule:     "*/" + strconv.Itoa(int(s.Every.Seconds())) + " * * * * *",
			Context:      s.Kind,
		}
		// CreateSchedule is not idempotent: a second instance gets a unique
		// violation, which here means the schedule exists.
		if err := dbos.CreateSchedule(dctx, spec); err != nil && !isDuplicate(err) {
			return nil, fmt.Errorf("dbos schedule: %w", err)
		}
	}

	return e, nil
}

func (e *Engine) runCall(c dbos.Context, in call) (string, error) {
	return e.runKind(c, in.Kind, in.Input)
}

func (e *Engine) runScheduled(c dbos.Context, in dbos.ScheduledWorkflowInput) (string, error) {
	var kind string
	if err := json.Unmarshal(in.Context, &kind); err != nil {
		return "", fmt.Errorf("schedule context: %w", err)
	}

	return e.runKind(c, kind, nil)
}

func (e *Engine) runKind(c dbos.Context, kind string, input []byte) (string, error) {
	k, ok := e.kinds[kind]
	if !ok {
		return "", fmt.Errorf("unknown kind %q", kind)
	}

	{
		for _, s := range k.Steps {
			step := s

			_, err := dbos.RunAsTransaction(c, e.ds, func(sctx context.Context, tx dbos.Tx) (string, error) {
				return "", step.Run(sctx, txAdapter{tx: tx, postgres: e.postgres}, input)
			}, dbos.WithStepName(step.Name))
			if err != nil {
				return "", err //nolint:wrapcheck // Spike.
			}
		}

		return "done", nil
	}
}

// Start implements durable.Engine: the key is the workflow ID, so a second
// start with the same key answers the same workflow.
func (e *Engine) Start(_ context.Context, key, kind string, input []byte) (string, error) {
	if _, ok := e.kinds[kind]; !ok {
		return "", fmt.Errorf("unknown kind %q", kind)
	}

	if _, err := dbos.RunWorkflow(e.ctx, e.runCall, call{Kind: kind, Input: input}, dbos.WithWorkflowID(key)); err != nil {
		return "", fmt.Errorf("dbos start: %w", err)
	}

	return key, nil
}

// Get implements durable.Engine.
func (e *Engine) Get(_ context.Context, id string) (durable.Status, error) {
	list, err := dbos.ListWorkflows(e.ctx, dbos.WithFilterWorkflowIDs(id))
	if err != nil {
		return durable.Status{}, fmt.Errorf("dbos get: %w", err)
	}

	if len(list) == 0 {
		return durable.Status{}, durable.ErrNotFound
	}

	w := list[0]

	steps, err := dbos.GetWorkflowSteps(e.ctx, id)
	if err != nil {
		return durable.Status{}, fmt.Errorf("dbos steps: %w", err)
	}

	done := 0

	for _, s := range steps {
		if !s.CompletedAt.IsZero() && s.Error == nil {
			done++
		}
	}

	st := durable.Status{ID: w.ID, Kind: w.Name, StepsDone: done, Holder: w.ExecutorID}

	switch w.Status {
	case dbos.WorkflowStatusSuccess:
		st.State = durable.StateSucceeded
	case dbos.WorkflowStatusError:
		st.State = durable.StateFailed
	case dbos.WorkflowStatusEnqueued:
		st.State = durable.StatePending
	default:
		st.State = durable.StateRunning
	}

	if w.Error != nil {
		st.Error = w.Error.Error()
	}

	return st, nil
}

// Close implements durable.Engine.
func (e *Engine) Close(_ context.Context) error {
	if err := dbos.Shutdown(e.ctx, 10*time.Second); err != nil {
		return fmt.Errorf("dbos shutdown: %w", err)
	}

	return nil
}

type txAdapter struct {
	tx       dbos.Tx
	postgres bool
}

func (t txAdapter) q(s string) string {
	if !t.postgres {
		return s
	}

	var b strings.Builder

	n := 0

	for _, r := range s {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))

			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}

func (t txAdapter) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	res, err := t.tx.Exec(ctx, t.q(query), args...)
	if err != nil {
		return 0, err //nolint:wrapcheck // Spike.
	}

	return res.RowsAffected() //nolint:wrapcheck // Spike.
}

func (t txAdapter) QueryRow(ctx context.Context, query string, args ...any) durable.Row {
	return t.tx.QueryRow(ctx, t.q(query), args...)
}

// ErrUnsupported marks a port feature the adapter does not give.
var ErrUnsupported = errors.New("unsupported by the DBOS adapter")

func isDuplicate(err error) bool {
	m := err.Error()

	return strings.Contains(m, "already exists") || strings.Contains(m, "duplicate key") ||
		strings.Contains(m, "UNIQUE constraint")
}
