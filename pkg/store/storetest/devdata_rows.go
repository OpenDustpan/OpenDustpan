package storetest

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// AddPortableRows returns a copy of a portable export whose file of table has
// the rows appended. Each row is a JSON object with a value for every column
// of the table, in the portable form: text as a string, a time as
// milliseconds or null, a flag as a boolean. The descriptor and the row count
// of the summary match the new file, so only the checks of the stage can
// refuse the rows. No port writes a dev principal, so the dev data cases edit
// the export by hand.
//
//nolint:revive // The test, the three parts of an export, the table and the rows travel together.
func AddPortableRows(
	t *testing.T,
	files map[string][]byte,
	descs []backup.PortableFile,
	sum backup.Summary,
	table string,
	rows ...map[string]any,
) (map[string][]byte, []backup.PortableFile, backup.Summary) {
	t.Helper()

	path := backup.PortablePath(table)

	data, ok := files[path]
	if !ok {
		t.Fatalf("the export holds no file for table %q", table)
	}

	var lines []string

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open gzip of %q: %v", path, err)
	}

	scanner := bufio.NewScanner(zr)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<26)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if scanErr := scanner.Err(); scanErr != nil {
		t.Fatalf("read %q: %v", path, scanErr)
	}

	for _, row := range rows {
		raw, marshalErr := json.Marshal(row)
		if marshalErr != nil {
			t.Fatalf("encode a row of %q: %v", table, marshalErr)
		}

		lines = append(lines, string(raw))
	}

	var out bytes.Buffer

	zw := gzip.NewWriter(&out)

	for _, line := range lines {
		if _, writeErr := zw.Write([]byte(line + "\n")); writeErr != nil {
			t.Fatalf("write %q: %v", path, writeErr)
		}
	}

	if closeErr := zw.Close(); closeErr != nil {
		t.Fatalf("finish %q: %v", path, closeErr)
	}

	digest := sha256.Sum256(out.Bytes())

	newFiles := maps.Clone(files)
	newFiles[path] = out.Bytes()

	newDescs := make([]backup.PortableFile, 0, len(descs))

	for _, d := range descs {
		if d.Table == table {
			d.Rows = int64(len(lines))
			d.Size = int64(out.Len())
			d.SHA256 = hex.EncodeToString(digest[:])
		}

		newDescs = append(newDescs, d)
	}

	newSum := sum
	newSum.RowCounts = maps.Clone(sum.RowCounts)
	newSum.RowCounts[table] = int64(len(lines))

	return newFiles, newDescs, newSum
}

// The row builders below return one row of the table in the portable form,
// with a value for every column. A caller may change a value of the returned
// map before it adds the row.

// DevPrincipalRow returns a principals row.
func DevPrincipalRow(id, subject string) map[string]any {
	return map[string]any{
		"id": id, "subject": subject, "display_name": "", "kind": "person", "source": "local",
		"created_at_ms": 1, "updated_at_ms": 1, "deleted_at_ms": nil, "disabled_at_ms": nil,
		"last_active_at_ms": 0, "last_sign_in_at_ms": nil, "namespace": "", "break_glass": false,
	}
}

// DevGrantRow returns a grants row of a principal. The role differs for each
// ID, so the unique index of current grants holds.
func DevGrantRow(id, principalID string) map[string]any {
	return map[string]any{
		"id": id, "subject": "principal:" + principalID, "subject_kind": "principal",
		"subject_id": principalID, "role": "role-" + id, "object": "installation", "target": "",
		"object_kind": "installation", "namespace": "", "condition": nil, "created_at_ms": 1,
		"created_by": "", "revoked_at_ms": nil, "revoked_by": "",
	}
}

// DevSessionRow returns a sessions row. The digest differs for each ID.
func DevSessionRow(id, principalID, provider string) map[string]any {
	return map[string]any{
		"id": id, "principal_id": principalID, "digest": "digest-" + id, "provider": provider,
		"created_at_ms": 1, "last_seen_at_ms": 1, "auth_time_ms": 1, "ended_at_ms": nil,
		"end_reason": "", "source_ip": "", "user_agent": "", "generation": 1,
	}
}

// DevTokenRow returns an access_tokens row. The digest differs for each ID.
func DevTokenRow(id, principalID string) map[string]any {
	return map[string]any{
		"id": id, "principal_id": principalID, "name": "token", "kind": "service",
		"digest": "digest-" + id, "created_at_ms": 1, "expires_at_ms": 4_000_000_000_000,
		"last_used_at_ms": nil, "revoked_at_ms": nil, "replaced_by": nil, "created_by": "",
		"disabled_at_ms": nil,
	}
}

// DevPreferencesRow returns a person_preferences row.
func DevPreferencesRow(principalID string) map[string]any {
	return map[string]any{
		"principal_id": principalID, "document": `{"language":"ar"}`, "updated_at_ms": 1,
	}
}

// DevForgeLinkRow returns a forge_account_links row.
func DevForgeLinkRow(principalID string) map[string]any {
	return map[string]any{
		"kind": "github", "host": "github.com", "account_id": "42", "login": "dev", "principal_id": principalID,
		"linked_at_ms": 1,
	}
}

// DevForgeLinkRequestRow returns a forge_link_requests row.
func DevForgeLinkRequestRow(principalID string) map[string]any {
	return map[string]any{
		"token_sha256": "0000000000000000000000000000000000000000000000000000000000000000", "principal_id": principalID,
		"created_at_ms": 1, "expires_at_ms": 300001, "proved_kind": "", "proved_host": "", "proved_account_id": "",
		"proved_login": "", "proved_delivery_id": "", "proved_at_ms": 0, "used_at_ms": 0,
	}
}

// DevLocalUserRow returns a local_users row with a password hash.
func DevLocalUserRow(id, principalID string) map[string]any {
	return map[string]any{
		"id": id, "principal_id": principalID, "user_name": "demo",
		"password_hash": "$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$" +
			"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"password_set_at_ms": 1, "change_required": false, "demo": true, "created_by": "system", "created_at_ms": 1,
	}
}

// DevSessionGenerationRow returns a principal_session_generations row.
func DevSessionGenerationRow(principalID string) map[string]any {
	return map[string]any{"principal_id": principalID, "generation": 3}
}

// DevPasskeyRow returns a passkeys row.
func DevPasskeyRow(id, principalID string) map[string]any {
	return map[string]any{
		"id": id, "principal_id": principalID, "name": "laptop", "credential_id": []byte("credential-" + id),
		"public_key": []byte{0xa5, 0x01, 0x02}, "transports": "internal", "aaguid": make([]byte, 16),
		"sign_count": 0, "backup_eligible": true, "created_at_ms": 1, "last_used_at_ms": 0, "suspended_at_ms": 0,
	}
}

// DevGroupRow returns a principal_groups row.
func DevGroupRow(id string) map[string]any {
	return map[string]any{
		"id": id, "name": id, "name_key": id, "description": "", "source": "local",
		"created_at_ms": 1, "updated_at_ms": 1, "deleted_at_ms": nil, "closed_to_identity_provider": false,
	}
}

// DevMemberRow returns a group_members row.
func DevMemberRow(groupID, principalID string) map[string]any {
	return map[string]any{
		"group_id": groupID, "principal_id": principalID, "added_at_ms": 1, "removed_at_ms": nil,
		"source": "local", "source_id": "", "state": "active", "suspended_at_ms": 0, "pinned": false,
	}
}

// DevSignInRequestRow returns a sign_in_requests row.
func DevSignInRequestRow(digest, provider string) map[string]any {
	return map[string]any{
		"digest": digest, "provider": provider, "nonce": "n", "verifier": "v", "return_path": "/",
		"created_at_ms": 1, "expires_at_ms": 4_000_000_000_000, "max_age_ms": nil,
	}
}

// DevTempAdminRow returns a temp_admins row.
func DevTempAdminRow(principalID, subject string) map[string]any {
	return map[string]any{
		"principal_id": principalID, "subject": subject, "expires_at_ms": 4_000_000_000_000,
		"created_at_ms": 1, "created_by": "host", "reason": "recovery", "ended_at_ms": nil,
		"review_opened_at_ms": nil,
	}
}

// DevProviderRow returns a sign_in_providers row.
func DevProviderRow(id string) map[string]any {
	return map[string]any{
		"provider_id": id, "issuer": "https://issuer.example/" + id, "display_name": id,
		"client_id": "client", "client_secret": "", "scopes": "openid", "auto_create": false,
		"enabled": false, "created_at_ms": 1, "updated_at_ms": 1, "version": 1, "mode": "oidc",
		"authorization_url": "", "token_url": "", "userinfo_url": "", "subject_claim": "",
		"name_claim": "", "username_claim": "", "email_claim": "",
	}
}

// DevIssuerRow returns an oidc_providers row.
func DevIssuerRow(id string) map[string]any {
	return map[string]any{"provider_id": id, "issuer": "https://issuer.example/" + id, "created_at_ms": 1}
}

// DevGateObjectRow returns a gate_objects row of the installation scope.
func DevGateObjectRow(kind, id, body string) map[string]any {
	return map[string]any{
		"namespace_id": "", "scope_key": "installation", "kind": kind, "id": id, "level": "installation",
		"unit_id": "", "version": 1, "body": body, "updated_at_ms": 1,
	}
}
