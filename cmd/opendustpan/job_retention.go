package main

import (
	"errors"
	"flag"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/history"
)

// errJobRetention reports a plan or job log retention that is not a Go
// duration from history.MinRetention to history.MaxRetention.
var errJobRetention = errors.New(
	"retention must be a Go duration from 24h to 87600h, for example 9600h: " +
		"set --plan-retention (DUSTPAN_PLAN_RETENTION) or --job-log-retention (DUSTPAN_JOB_LOG_RETENTION)",
)

// registerJobRetention registers the retention flags of plans and job logs.
func (f *serveFlags) registerJobRetention(fs *flag.FlagSet) {
	cli.ByFlag(history.PlanRetentionFlag).String(fs, &f.planRetention)
	cli.ByFlag(history.JobLogRetentionFlag).String(fs, &f.jobLogRetention)
}

// checkJobRetention parses the two retention flags and stores the periods in
// f.jobRetention. It refuses text that is not a duration and a period outside
// the range of history.RetentionSettings.Validate. The error names the
// catalog code and the flag.
func (f *serveFlags) checkJobRetention() error {
	plan, err := parseJobRetention(history.PlanRetentionFlag, f.planRetention)
	if err != nil {
		return err
	}

	jobLog, err := parseJobRetention(history.JobLogRetentionFlag, f.jobLogRetention)
	if err != nil {
		return err
	}

	f.jobRetention = history.RetentionSettings{Plan: plan, JobLog: jobLog}

	return nil
}

// parseJobRetention parses the text of the flag name and checks the period.
func parseJobRetention(name, text string) (time.Duration, error) {
	period, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("%s: --%s %q: %w: %w", errcode.SettingsRetentionInvalid, name, text, errJobRetention, err)
	}

	if err := history.ValidateRetention(name, period); err != nil {
		return 0, fmt.Errorf("%s: %w: %w", errcode.SettingsRetentionInvalid, errJobRetention, err)
	}

	return period, nil
}
