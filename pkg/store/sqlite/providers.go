package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var (
	_ secret.InstallationKeyStore = (*Store)(nil)
	_ session.ProviderConfigStore = (*Store)(nil)
)

// CreateInstallationKey implements secret.InstallationKeyStore. It stores
// k unless a key for its scope exists, and returns the stored key either
// way.
func (s *Store) CreateInstallationKey(ctx context.Context, k secret.WrappedKey) (secret.WrappedKey, error) {
	if k.Scope.Namespace != "" {
		return secret.WrappedKey{}, fmt.Errorf("%w: installation key with the namespace %q",
			secret.ErrInvalidScope, k.Scope.Namespace)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var stored secret.WrappedKey

	err := s.inTx(ctx, func(q *db.Queries) error {
		_, err := q.CreateInstallationSecretKey(ctx, db.CreateInstallationSecretKeyParams{
			ScopeID:     k.Scope.ID,
			WrapperID:   k.WrapperID,
			Ciphertext:  k.Ciphertext,
			CreatedAtMs: codec.Millis(k.CreatedAt),
		})
		if err != nil {
			return fmt.Errorf("insert installation key: %w", err)
		}

		row, getErr := q.GetInstallationSecretKey(ctx, k.Scope.ID)
		if getErr != nil {
			return fmt.Errorf("select stored installation key: %w", getErr)
		}

		stored = installationKeyOf(row)

		return nil
	})
	if err != nil {
		return secret.WrappedKey{}, err
	}

	return stored, nil
}

// GetInstallationKey implements secret.InstallationKeyStore.
func (s *Store) GetInstallationKey(ctx context.Context, id string) (secret.WrappedKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetInstallationSecretKey(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return secret.WrappedKey{}, secret.ErrKeyNotFound
	}

	if err != nil {
		return secret.WrappedKey{}, fmt.Errorf("select installation key: %w", err)
	}

	return installationKeyOf(row), nil
}

// ListInstallationWrapperIDs implements secret.InstallationKeyStore.
func (s *Store) ListInstallationWrapperIDs(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids, err := s.queries.ListInstallationWrapperIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("select installation wrapper IDs: %w", err)
	}

	if ids == nil {
		return []string{}, nil
	}

	return ids, nil
}

// installationKeyOf maps a stored installation key to the wrapped key.
func installationKeyOf(row db.InstallationSecretKey) secret.WrappedKey {
	return secret.WrappedKey{
		CreatedAt:  codec.FromMillis(row.CreatedAtMs),
		Scope:      secret.Scope{Namespace: "", ID: row.ScopeID},
		WrapperID:  row.WrapperID,
		Ciphertext: row.Ciphertext,
	}
}

// CreateProviderConfig implements session.ProviderConfigStore.
func (s *Store) CreateProviderConfig(ctx context.Context, p session.ProviderConfig, e audit.Entry) error {
	row, err := providerRowOf(p)
	if err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inTx(ctx, func(q *db.Queries) error {
		if err := validServerEntry(e); err != nil {
			return err
		}

		n, err := q.CreateSignInProvider(ctx, db.CreateSignInProviderParams{
			ProviderID:       row.ProviderID,
			Issuer:           row.Issuer,
			DisplayName:      row.DisplayName,
			ClientID:         row.ClientID,
			ClientSecret:     row.ClientSecret,
			Scopes:           row.Scopes,
			AutoCreate:       sqliteBool(row.AutoCreate).column(),
			Enabled:          sqliteBool(row.Enabled).column(),
			CreatedAtMs:      row.CreatedAtMs,
			UpdatedAtMs:      row.UpdatedAtMs,
			Version:          row.Version,
			Mode:             row.Mode,
			AuthorizationUrl: row.OAuth.AuthorizationURL,
			TokenUrl:         row.OAuth.TokenURL,
			UserinfoUrl:      row.OAuth.UserinfoURL,
			SubjectClaim:     row.OAuth.SubjectClaim,
			NameClaim:        row.OAuth.NameClaim,
			UsernameClaim:    row.OAuth.UsernameClaim,
			EmailClaim:       row.OAuth.EmailClaim,
		})
		if err != nil {
			return fmt.Errorf("insert sign-in provider: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("%w: %s", session.ErrProviderExists, p.ID)
		}

		return appendProviderEntry(ctx, q, e)
	})
}

// GetProviderConfig implements session.ProviderConfigStore.
func (s *Store) GetProviderConfig(ctx context.Context, id string) (session.ProviderConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetSignInProvider(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return session.ProviderConfig{}, fmt.Errorf("%w: %s", session.ErrProviderNotFound, id)
	}

	if err != nil {
		return session.ProviderConfig{}, fmt.Errorf("select sign-in provider: %w", err)
	}

	return providerOf(&row)
}

// ListProviderConfigs implements session.ProviderConfigStore.
func (s *Store) ListProviderConfigs(ctx context.Context) ([]session.ProviderConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ListSignInProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("select sign-in providers: %w", err)
	}

	out := make([]session.ProviderConfig, 0, len(rows))

	for i := range rows {
		p, err := providerOf(&rows[i])
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, nil
}

// UpdateProviderConfig implements session.ProviderConfigStore.
func (s *Store) UpdateProviderConfig(ctx context.Context, p session.ProviderConfig, e audit.Entry) error {
	row, err := providerRowOf(p)
	if err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inTx(ctx, func(q *db.Queries) error {
		if err := validServerEntry(e); err != nil {
			return err
		}

		n, err := q.UpdateSignInProvider(ctx, db.UpdateSignInProviderParams{
			Issuer:           row.Issuer,
			DisplayName:      row.DisplayName,
			ClientID:         row.ClientID,
			ClientSecret:     row.ClientSecret,
			Scopes:           row.Scopes,
			AutoCreate:       sqliteBool(row.AutoCreate).column(),
			Enabled:          sqliteBool(row.Enabled).column(),
			UpdatedAtMs:      row.UpdatedAtMs,
			Version:          row.Version,
			ProviderID:       row.ProviderID,
			AuthorizationUrl: row.OAuth.AuthorizationURL,
			TokenUrl:         row.OAuth.TokenURL,
			UserinfoUrl:      row.OAuth.UserinfoURL,
			SubjectClaim:     row.OAuth.SubjectClaim,
			NameClaim:        row.OAuth.NameClaim,
			UsernameClaim:    row.OAuth.UsernameClaim,
			EmailClaim:       row.OAuth.EmailClaim,
		})
		if err != nil {
			return fmt.Errorf("update sign-in provider: %w", err)
		}

		if n == 0 {
			return missingOrChanged(ctx, q, p.ID)
		}

		if !p.Enabled {
			ended, endErr := q.EndSessionsWithSubjectPrefix(ctx, db.EndSessionsWithSubjectPrefixParams{
				AtMs: codec.Millis(e.At), EndReason: string(session.ReasonEnded), Prefix: session.SubjectPrefix(p.ID),
			})
			if endErr != nil {
				return fmt.Errorf("end sessions of sign-in provider: %w", endErr)
			}

			e = session.WithSessionsEnded(e, ended)
		}

		return appendProviderEntry(ctx, q, e)
	})
}

// DisableProviderConfig implements session.ProviderConfigStore. It runs on the
// control path, so it works in maintenance mode, and the transaction
// journals it there. It cannot enable a provider or change its issuer or
// client: it sets one flag.
func (s *Store) DisableProviderConfig(
	ctx context.Context, id string, version int64, updatedAt time.Time, e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rev := revocation{kind: maintenance.RevokedProvider, target: id, endReason: string(session.ReasonEnded)}

	return s.inRevokingTxOf(ctx, &e, rev, func(q *db.Queries) error {
		n, err := q.DisableSignInProvider(ctx, db.DisableSignInProviderParams{
			UpdatedAtMs: codec.Millis(updatedAt), Version: version, ProviderID: id,
		})
		if err != nil {
			return fmt.Errorf("disable sign-in provider: %w", err)
		}

		if n == 0 {
			return missingOrChanged(ctx, q, id)
		}

		ended, err := q.EndSessionsWithSubjectPrefix(ctx, db.EndSessionsWithSubjectPrefixParams{
			AtMs: codec.Millis(e.At), EndReason: string(session.ReasonEnded), Prefix: session.SubjectPrefix(id),
		})
		if err != nil {
			return fmt.Errorf("end sessions of sign-in provider: %w", err)
		}

		e = session.WithSessionsEnded(e, ended)

		return nil
	})
}

// DeleteProviderConfig implements session.ProviderConfigStore. It runs on the
// control path, so it works in maintenance mode, and the transaction journals
// it there as the disable of the provider: a restore applies that disable
// again to the restored provider.
func (s *Store) DeleteProviderConfig(ctx context.Context, id string, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rev := revocation{kind: maintenance.RevokedProvider, target: id, endReason: string(session.ReasonEnded)}

	return s.inRevokingTxOf(ctx, &e, rev, func(q *db.Queries) error {
		principals, err := q.CountPrincipalsWithSubjectPrefix(ctx, session.SubjectPrefix(id))
		if err != nil {
			return fmt.Errorf("count principals of sign-in provider: %w", err)
		}

		n, err := q.CountSignInProvider(ctx, id)
		if err != nil {
			return fmt.Errorf("count sign-in provider: %w", err)
		}

		switch {
		case n == 0:
			return fmt.Errorf("%w: %s", session.ErrProviderNotFound, id)
		case principals > 0:
			return fmt.Errorf("%w: %s has %d", session.ErrProviderInUse, id, principals)
		}

		if _, err := q.DeleteSignInProvider(ctx, id); err != nil {
			return fmt.Errorf("delete sign-in provider: %w", err)
		}

		return nil
	})
}

// missingOrChanged returns ErrProviderNotFound when the provider id does
// not exist, and else ErrProviderChanged.
func missingOrChanged(ctx context.Context, q *db.Queries, id string) error {
	n, err := q.CountSignInProvider(ctx, id)
	if err != nil {
		return fmt.Errorf("count sign-in provider: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("%w: %s", session.ErrProviderNotFound, id)
	}

	return fmt.Errorf("%w: %s", session.ErrProviderChanged, id)
}

// appendProviderEntry appends the entry of a provider change in the
// transaction of q.
func appendProviderEntry(ctx context.Context, q *db.Queries, e audit.Entry) error {
	if _, err := appendAuditEntry(ctx, q, e); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}

// providerRowOf validates p and returns its columns.
func providerRowOf(p session.ProviderConfig) (codec.ProviderConfigRow, error) {
	if err := p.Validate(); err != nil {
		return codec.ProviderConfigRow{}, err
	}

	row, err := codec.ToProviderConfigRow(p)
	if err != nil {
		return codec.ProviderConfigRow{}, fmt.Errorf("encode sign-in provider: %w", err)
	}

	return row, nil
}

// providerOf maps a stored provider row to the provider.
func providerOf(row *db.SignInProvider) (session.ProviderConfig, error) {
	p, err := codec.FromProviderConfigRow(codec.ProviderConfigRow{
		ProviderID: row.ProviderID, Issuer: row.Issuer, DisplayName: row.DisplayName, ClientID: row.ClientID,
		Scopes: row.Scopes, ClientSecret: row.ClientSecret, CreatedAtMs: row.CreatedAtMs,
		UpdatedAtMs: row.UpdatedAtMs, Version: row.Version, AutoCreate: row.AutoCreate != 0, Enabled: row.Enabled != 0,
		Mode: row.Mode, OAuth: session.OAuthSettings{
			AuthorizationURL: row.AuthorizationUrl, TokenURL: row.TokenUrl, UserinfoURL: row.UserinfoUrl,
			SubjectClaim: row.SubjectClaim, NameClaim: row.NameClaim, UsernameClaim: row.UsernameClaim,
			EmailClaim: row.EmailClaim,
		},
	})
	if err != nil {
		return session.ProviderConfig{}, fmt.Errorf("decode sign-in provider: %w", err)
	}

	return p, nil
}

// sqliteBool is a boolean that SQLite stores as an INTEGER column.
type sqliteBool bool

// column returns the column value of b: 1 or 0.
func (b sqliteBool) column() int64 {
	if b {
		return 1
	}

	return 0
}
