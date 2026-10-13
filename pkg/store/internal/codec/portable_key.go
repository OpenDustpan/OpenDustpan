package codec

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/secret"
)

// PeekPortableSecretKey returns the first data key of wrapperID in the
// secret_keys file of a portable backup, without loading any store. The file
// is read to its end, so its descriptor is checked too (ADR 42, decision 4).
// The file orders its rows by namespace and stack ID, so the first match is
// stable. It returns ErrCorrupt when no row holds wrapperID.
func PeekPortableSecretKey(
	ctx context.Context, open backup.PortableOpener, file backup.PortableFile, wrapperID string,
) (secret.WrappedKey, error) {
	var none secret.WrappedKey

	table, ok := secretKeysSpec()
	if !ok {
		return none, fmt.Errorf("portable spec holds no secret_keys table: %w", backup.ErrCoverage)
	}

	columns := make(map[string]int, len(table.Columns))
	for i, column := range table.Columns {
		columns[column.Name] = i
	}

	var (
		found bool
		row   SecretKeyRow
	)

	err := backup.ReadPortableTable(ctx, open, file, table, func(values []any) error {
		if found || values[columns["wrapper_id"]] != wrapperID {
			return nil
		}

		key, err := secretKeyFromValues(values, columns)
		if err != nil {
			return err
		}

		row = key
		found = true

		return nil
	})
	if err != nil {
		return none, fmt.Errorf("read backup data key: %w", err)
	}

	if !found {
		return none, fmt.Errorf("backup holds no data key of wrapper %q: %w", wrapperID, backup.ErrCorrupt)
	}

	return FromSecretKeyRow(row), nil
}

// secretKeysSpec returns the portable description of the secret_keys table.
func secretKeysSpec() (backup.PortableTable, bool) {
	for _, table := range backup.PortableTables() {
		if table.Table == "secret_keys" {
			return table, true
		}
	}

	var none backup.PortableTable

	return none, false
}

// secretKeyFromValues converts one decoded row of secret_keys to its row type. A
// value of an unexpected Go type is a corrupt file.
func secretKeyFromValues(values []any, columns map[string]int) (SecretKeyRow, error) {
	namespaceName, nsOK := values[columns["namespace"]].(string)
	stackID, stackOK := values[columns["stack_id"]].(string)
	wrapperID, wrapOK := values[columns["wrapper_id"]].(string)
	ciphertext, cipherOK := values[columns["ciphertext"]].([]byte)
	createdAt, createdOK := values[columns["created_at_ms"]].(int64)

	if !nsOK || !stackOK || !wrapOK || !cipherOK || !createdOK {
		return SecretKeyRow{}, fmt.Errorf("secret key row of the backup is malformed: %w", backup.ErrCorrupt)
	}

	return SecretKeyRow{
		Namespace:   namespaceName,
		StackID:     stackID,
		WrapperID:   wrapperID,
		Ciphertext:  ciphertext,
		CreatedAtMs: createdAt,
	}, nil
}
