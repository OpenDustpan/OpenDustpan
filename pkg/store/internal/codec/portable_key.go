package codec

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/secret"
)

// PeekPortableSecretKey returns the first data key of wrapperID in the
// secret_keys file of a portable backup, without loading any store. The file
// is read to its end, so its descriptor is checked too. The file orders its
// rows by namespace and stack ID, so the first match is stable. It returns
// backup.ErrNoDataKey when no row holds wrapperID.
func PeekPortableSecretKey(
	ctx context.Context, open backup.PortableOpener, file backup.PortableFile, wrapperID string,
) (secret.WrappedKey, error) {
	return keyTable{name: "secret_keys", convert: secretKeyFromValues}.peek(ctx, open, file, wrapperID)
}

// PeekPortableInstallationKey returns the first installation key of
// wrapperID in the installation_secret_keys file of a portable backup,
// without loading any store. It reads the file as PeekPortableSecretKey
// does, and returns backup.ErrNoDataKey when no row holds wrapperID.
func PeekPortableInstallationKey(
	ctx context.Context, open backup.PortableOpener, file backup.PortableFile, wrapperID string,
) (secret.WrappedKey, error) {
	table := keyTable{name: "installation_secret_keys", convert: installationKeyFromValues}

	return table.peek(ctx, open, file, wrapperID)
}

// keyTable is a table of wrapped data keys: its name and the conversion of
// one decoded row to its key.
type keyTable struct {
	convert func(values []any, columns map[string]int) (secret.WrappedKey, error)
	name    string
}

// peek returns the first key of wrapperID in the table file of k.
func (k keyTable) peek(
	ctx context.Context, open backup.PortableOpener, file backup.PortableFile, wrapperID string,
) (secret.WrappedKey, error) {
	name, convert := k.name, k.convert

	var none secret.WrappedKey

	table, ok := portableTableSpec(name)
	if !ok {
		return none, fmt.Errorf("portable spec holds no %s table: %w", name, backup.ErrCoverage)
	}

	columns := make(map[string]int, len(table.Columns))
	for i, column := range table.Columns {
		columns[column.Name] = i
	}

	var (
		found bool
		key   secret.WrappedKey
	)

	err := backup.ReadPortableTable(ctx, open, file, table, func(values []any) error {
		if found || values[columns["wrapper_id"]] != wrapperID {
			return nil
		}

		converted, err := convert(values, columns)
		if err != nil {
			return err
		}

		key = converted
		found = true

		return nil
	})
	if err != nil {
		return none, fmt.Errorf("read backup data key: %w", err)
	}

	if !found {
		return none, fmt.Errorf("%s of the backup holds no data key of wrapper %q: %w",
			name, wrapperID, backup.ErrNoDataKey)
	}

	return key, nil
}

// portableTableSpec returns the portable description of the table name.
func portableTableSpec(name string) (backup.PortableTable, bool) {
	for _, table := range backup.PortableTables() {
		if table.Table == name {
			return table, true
		}
	}

	var none backup.PortableTable

	return none, false
}

// secretKeyFromValues converts one decoded row of secret_keys to its key. A
// value of an unexpected Go type is a corrupt file.
func secretKeyFromValues(values []any, columns map[string]int) (secret.WrappedKey, error) {
	namespaceName, nsOK := values[columns["namespace"]].(string)
	stackID, stackOK := values[columns["stack_id"]].(string)
	wrapperID, wrapOK := values[columns["wrapper_id"]].(string)
	ciphertext, cipherOK := values[columns["ciphertext"]].([]byte)
	createdAt, createdOK := values[columns["created_at_ms"]].(int64)

	if !nsOK || !stackOK || !wrapOK || !cipherOK || !createdOK {
		return secret.WrappedKey{}, fmt.Errorf("secret key row of the backup is malformed: %w", backup.ErrCorrupt)
	}

	return FromSecretKeyRow(SecretKeyRow{
		Namespace:   namespaceName,
		StackID:     stackID,
		WrapperID:   wrapperID,
		Ciphertext:  ciphertext,
		CreatedAtMs: createdAt,
	}), nil
}

// installationKeyFromValues converts one decoded row of
// installation_secret_keys to its key, with an empty Namespace. A value of an
// unexpected Go type is a corrupt file.
func installationKeyFromValues(values []any, columns map[string]int) (secret.WrappedKey, error) {
	scopeID, scopeOK := values[columns["scope_id"]].(string)
	wrapperID, wrapOK := values[columns["wrapper_id"]].(string)
	ciphertext, cipherOK := values[columns["ciphertext"]].([]byte)
	createdAt, createdOK := values[columns["created_at_ms"]].(int64)

	if !scopeOK || !wrapOK || !cipherOK || !createdOK {
		return secret.WrappedKey{}, fmt.Errorf("installation key row of the backup is malformed: %w", backup.ErrCorrupt)
	}

	return secret.WrappedKey{
		CreatedAt:  FromMillis(createdAt),
		Scope:      secret.Scope{Namespace: "", ID: scopeID},
		WrapperID:  wrapperID,
		Ciphertext: ciphertext,
	}, nil
}
