package backup_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/agent/agenttest"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// agentBackupKEK is the key-encryption key of both stores of the round trip.
// The restored installation keys open only under the same key.
func agentBackupKEK() []byte { return bytes.Repeat([]byte{7}, 32) }

// agentPlainSecret returns the plain value of the seeded secret item. It is
// joined at run time, so that no credential-shaped text sits in the source.
// A backup must never hold it.
func agentPlainSecret() string {
	return strings.Join([]string{"agent", "model", "key", "9d3b"}, "-")
}

// openAgentBackupStore opens a fresh SQLite store and the sealer of its
// secret items.
func openAgentBackupStore(t *testing.T) (*sqlite.Store, *secret.InstallationSecrets) {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	wrapper, err := secret.NewBuiltinWrapper(agentBackupKEK())
	if err != nil {
		t.Fatalf("key-encryption key: %v", err)
	}

	secrets, err := secret.NewInstallationSecrets(secret.InstallationConfig{Keys: st, Wrapper: wrapper, Now: nil})
	if err != nil {
		t.Fatalf("installation secrets: %v", err)
	}

	return st, secrets
}

// agentSecretItemColumns are the columns that the backup of a secret item
// holds. The value is in the sealed column ciphertext only.
func agentSecretItemColumns() []string {
	return []string{"changed_at_ms", "ciphertext", "created_at_ms", "last_used_at_ms", "name", "ref"}
}

// TestBackupConformance_NoPlainSecret proves that a backup of a stored secret
// item holds the sealed value and never the plain value, and that a restore
// brings back the sealed value, which opens to the plain value under the
// restored installation keys only. It reads every row of every file of the
// export, also the values that decode from base64, which is how a byte column
// is written.
func TestBackupConformance_NoPlainSecret(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	src, srcSecrets := openAgentBackupStore(t)
	plain := agentPlainSecret()

	item, err := src.AgentSecretItems(srcSecrets).Create(
		ctx, "model key", []byte(plain), agenttest.TestEntry(audit.ServerScope, "alice", "agent-secret:create"),
	)
	if err != nil {
		t.Fatalf("create secret item: %v", err)
	}

	allowlist := egress.Allowlist{
		Hosts: []string{"api.example.com"},
		CIDRs: []egress.CIDRRule{{CIDR: netip.MustParsePrefix("192.0.2.0/24"), Port: 443, HTTP: true}},
	}

	entry := agenttest.TestEntry(audit.ServerScope, "alice", "agent:allowlist")
	err = src.AgentEgressAllowlist().Set(ctx, allowlist, entry)
	if err != nil {
		t.Fatalf("set allowlist: %v", err)
	}

	files, sum, descs := storetest.PortableFiles(t, src)

	rowsOf := scanAgentBackupRows(t, files, plain)

	items := rowsOf[backup.PortablePath("agent_secret_items")]
	if len(items) != 1 {
		t.Fatalf("the secret item file holds %d rows, want 1", len(items))
	}

	requireSealedItem(t, items[0], string(item.Ref), plain)

	dst, dstSecrets := openAgentBackupStore(t)

	opener := storetest.PortableOpener(files)

	if _, stageErr := dst.StagePortable(ctx, opener, sum, descs, storetest.StageAs(t, dst)); stageErr != nil {
		t.Fatalf("stage portable backup: %v", stageErr)
	}

	t.Cleanup(func() {
		if discardErr := dst.DiscardStaging(context.WithoutCancel(ctx)); discardErr != nil {
			t.Errorf("discard staging: %v", discardErr)
		}
	})

	if applyErr := dst.ApplyStaging(ctx); applyErr != nil {
		t.Fatalf("apply staged backup: %v", applyErr)
	}

	opened, err := dst.AgentSecretItems(dstSecrets).Open(ctx, item.Ref)
	if err != nil {
		t.Fatalf("open the restored secret item: %v", err)
	}

	if string(opened) != plain {
		t.Fatal("the restored secret item does not open to the value that the source store sealed")
	}

	restored, err := dst.AgentEgressAllowlist().Get(ctx)
	if err != nil {
		t.Fatalf("get the restored allowlist: %v", err)
	}

	if !slices.Equal(restored.Hosts, allowlist.Hosts) || !slices.Equal(restored.CIDRs, allowlist.CIDRs) {
		t.Fatalf("restored allowlist = %+v, want %+v", restored, allowlist)
	}
}

// requireSealedItem fails unless row is the backup row of the secret item ref
// with exactly the columns of agentSecretItemColumns, and its ciphertext is a
// sealed value that does not hold plain.
func requireSealedItem(t *testing.T, row map[string]any, ref, plain string) {
	t.Helper()

	columns := make([]string, 0, len(row))
	for column := range row {
		columns = append(columns, column)
	}

	slices.Sort(columns)

	if !slices.Equal(columns, agentSecretItemColumns()) {
		t.Fatalf("the secret item row holds the columns %v, want %v", columns, agentSecretItemColumns())
	}

	if row["ref"] != ref {
		t.Fatalf("the secret item row has the ref %v, want %q", row["ref"], ref)
	}

	text, ok := row["ciphertext"].(string)
	if !ok {
		t.Fatalf("the ciphertext of the secret item row is %T, want a base64 string", row["ciphertext"])
	}

	sealed, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		t.Fatalf("decode the ciphertext of the secret item row: %v", err)
	}

	if len(sealed) <= len(plain) || bytes.Contains(sealed, []byte(plain)) {
		t.Fatalf("the ciphertext of the secret item row is not a sealed value: %d bytes", len(sealed))
	}
}

// scanAgentBackupRows reads every table file of the export and fails if a
// file holds plain, as text or in a value that decodes from base64. It returns
// the rows of each file by path.
func scanAgentBackupRows(t *testing.T, files map[string][]byte, plain string) map[string][]map[string]any {
	t.Helper()

	rowsOf := make(map[string][]map[string]any, len(files))

	for path, data := range files {
		if !bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
			if bytes.Contains(data, []byte(plain)) {
				t.Fatalf("the file %q holds the plain secret", path)
			}

			continue
		}

		text := gunzipAgentFile(t, path, data)
		if bytes.Contains(text, []byte(plain)) {
			t.Fatalf("the file %q holds the plain secret", path)
		}

		rowsOf[path] = decodeAgentRows(t, path, text, plain)
	}

	return rowsOf
}

// gunzipAgentFile returns the bytes of one gzip table file.
func gunzipAgentFile(t *testing.T, path string, data []byte) []byte {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open gzip of %q: %v", path, err)
	}

	text, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip of %q: %v", path, err)
	}

	if err := zr.Close(); err != nil {
		t.Fatalf("close gzip of %q: %v", path, err)
	}

	return text
}

// decodeAgentRows returns the JSON rows of one table file and fails if a text
// value decodes from base64 to bytes that hold plain.
func decodeAgentRows(t *testing.T, path string, text []byte, plain string) []map[string]any {
	t.Helper()

	var rows []map[string]any

	scanner := bufio.NewScanner(bytes.NewReader(text))
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<26)

	for scanner.Scan() {
		row := map[string]any{}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatalf("decode a row of %q: %v", path, err)
		}

		for column, value := range row {
			s, ok := value.(string)
			if !ok {
				continue
			}

			decoded, err := base64.StdEncoding.DecodeString(s)
			if err == nil && bytes.Contains(decoded, []byte(plain)) {
				t.Fatalf("the column %q of %q holds the plain secret in base64", column, path)
			}
		}

		rows = append(rows, row)
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("read %q: %v", path, err)
	}

	return rows
}
