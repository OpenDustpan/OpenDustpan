package backup_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
)

// truncateAfterManifest truncates the first table file of the attempt as soon
// as its manifest lands, as a truncated target write does mid-step.
type truncateAfterManifest struct {
	*memStore
}

func (d *truncateAfterManifest) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if err := d.memStore.Put(ctx, key, r); err != nil {
		return err
	}

	if !strings.HasSuffix(key.String(), "/manifest.json") {
		return nil
	}

	table, err := blob.ParseKey(
		strings.TrimSuffix(key.String(), "manifest.json") + backup.PortablePath(backup.Tables[0]),
	)
	if err != nil {
		return err
	}

	reader, err := d.Get(ctx, table)
	if err != nil {
		return fmt.Errorf("read table file before truncation: %w", err)
	}

	data, err := io.ReadAll(reader)
	closeErr := reader.Close()

	if err != nil {
		return fmt.Errorf("read table file before truncation: %w", err)
	}

	if closeErr != nil {
		return fmt.Errorf("close table file before truncation: %w", closeErr)
	}

	if err := d.Delete(ctx, table); err != nil {
		return fmt.Errorf("delete table file before truncation: %w", err)
	}

	if err := d.Put(ctx, table, bytes.NewReader(data[:1])); err != nil {
		return fmt.Errorf("write truncated table file: %w", err)
	}

	return nil
}

// TestFinishRefusesTruncatedFile proves that the finish step reuses Check and
// fails a backup whose attempt prefix holds a truncated table file: the row
// ends failed with backup.corrupt, never succeeded.
func TestFinishRefusesTruncatedFile(t *testing.T) {
	t.Parallel()

	setup := newDurableSetup()
	id := backup.ID("bkp-truncated")
	target := &truncateAfterManifest{memStore: setup.target}
	svc := setup.serviceOver(t, &stubExporter{sum: testSummary(), data: tableData, err: nil}, target)

	_, err := svc.Run(t.Context(), id)
	if !errors.Is(err, backup.ErrFailed) || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("run = %v, want ErrFailed with corrupt", err)
	}

	row, getErr := setup.reg.GetBackup(t.Context(), id)
	if getErr != nil || row.State != backup.StateFailed {
		t.Fatalf("row = %+v, %v, want failed", row, getErr)
	}

	if !strings.Contains(row.Error, "corrupt") {
		t.Fatalf("row error = %q, want corrupt", row.Error)
	}

	// The finish check runs over the attempt prefix of the claim, so the
	// error names the truncated file under that prefix.
	wantKey := "backups/" + id.String() + "/1/" + backup.PortablePath(backup.Tables[0])

	if !strings.Contains(row.Error, wantKey) {
		t.Fatalf("row error = %q, want it to name the attempt file %q", row.Error, wantKey)
	}
}
