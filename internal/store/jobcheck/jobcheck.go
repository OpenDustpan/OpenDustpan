// Package jobcheck checks the job rows of a backup with the rules of the job
// store, the same rules that a restore applies to its staged rows.
package jobcheck

import (
	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Checker implements backup.JobRowChecker with jobstore.CheckBackupRow.
type Checker struct{}

// CheckJobRow returns nil for a row that the job store reads, and a
// *backup.DamagedJobError for a row that it does not read.
func (Checker) CheckJobRow(row backup.JobRow) error {
	stored := jobstore.Row{
		ID:             job.ID(row.ID),
		Namespace:      namespace.Name(row.Namespace),
		NamespaceID:    namespace.ID(row.NamespaceID),
		UnitID:         row.UnitID,
		Seq:            row.Seq,
		State:          job.State(row.State),
		Wait:           job.WaitReason(row.Wait),
		Phase:          int(row.Phase),
		Changes:        row.Changes,
		Operation:      row.Operation,
		Key:            row.Key,
		Version:        row.Version,
		Body:           row.Body,
		CreatedMs:      row.CreatedMs,
		UpdatedMs:      row.UpdatedMs,
		StateChangedMs: row.ChangedMs,
	}

	return jobstore.CheckBackupRow(&stored)
}
