package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) CreateProviderOAuthCredential(ctx context.Context, actor, tenantSlug string, input domain.ProviderOAuthCredentialInput) (domain.ProviderOAuthCredential, error) {
	if input.Provider != domain.ProviderGitLab || !validOAuthCredentialRef(input.CredentialRef) || len(input.AccessTokenCiphertext) < 32 || len(input.RefreshTokenCiphertext) > 16<<10 {
		return domain.ProviderOAuthCredential{}, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderOAuthCredential{}, fmt.Errorf("begin provider OAuth credential: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT t.id FROM tenants t JOIN memberships m ON m.tenant_id=t.id WHERE t.slug=$1 AND m.subject=$2 AND m.active=TRUE AND m.role IN ('owner','admin')`, tenantSlug, actor).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderOAuthCredential{}, ErrForbidden
	}
	if err != nil {
		return domain.ProviderOAuthCredential{}, fmt.Errorf("authorize provider OAuth credential: %w", err)
	}
	credential := domain.ProviderOAuthCredential{CredentialRef: input.CredentialRef, TenantID: tenantID, Provider: input.Provider, AccessTokenCiphertext: input.AccessTokenCiphertext, RefreshTokenCiphertext: input.RefreshTokenCiphertext, ExpiresAt: input.ExpiresAt}
	err = tx.QueryRow(ctx, `
		INSERT INTO provider_oauth_credentials (credential_ref,tenant_id,provider,access_token_ciphertext,refresh_token_ciphertext,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (credential_ref) DO UPDATE SET access_token_ciphertext=EXCLUDED.access_token_ciphertext,refresh_token_ciphertext=EXCLUDED.refresh_token_ciphertext,expires_at=EXCLUDED.expires_at,revoked_at=NULL,updated_at=now()
		WHERE provider_oauth_credentials.tenant_id=EXCLUDED.tenant_id AND provider_oauth_credentials.provider=EXCLUDED.provider
		RETURNING revoked_at`, credential.CredentialRef, credential.TenantID, credential.Provider, credential.AccessTokenCiphertext, nullableBytes(credential.RefreshTokenCiphertext), credential.ExpiresAt).Scan(&credential.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderOAuthCredential{}, ErrConflict
	}
	if err != nil {
		return domain.ProviderOAuthCredential{}, fmt.Errorf("store provider OAuth credential: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'provider_oauth_credential.stored',$3,jsonb_build_object('provider',$4::text,'expires_at',$5::timestamptz))`, tenantID, actor, credential.CredentialRef, credential.Provider, credential.ExpiresAt); err != nil {
		return domain.ProviderOAuthCredential{}, fmt.Errorf("audit provider OAuth credential: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ProviderOAuthCredential{}, fmt.Errorf("commit provider OAuth credential: %w", err)
	}
	return credential, nil
}

func (s *PostgresStore) LoadProviderOAuthCredential(ctx context.Context, tenantID uuid.UUID, credentialRef string) (domain.ProviderOAuthCredential, error) {
	if !validOAuthCredentialRef(credentialRef) {
		return domain.ProviderOAuthCredential{}, ErrNotFound
	}
	var credential domain.ProviderOAuthCredential
	err := s.pool.QueryRow(ctx, `SELECT credential_ref,tenant_id,provider,access_token_ciphertext,refresh_token_ciphertext,expires_at,revoked_at FROM provider_oauth_credentials WHERE credential_ref=$1 AND tenant_id=$2`, credentialRef, tenantID).Scan(&credential.CredentialRef, &credential.TenantID, &credential.Provider, &credential.AccessTokenCiphertext, &credential.RefreshTokenCiphertext, &credential.ExpiresAt, &credential.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderOAuthCredential{}, ErrNotFound
	}
	if err != nil {
		return domain.ProviderOAuthCredential{}, fmt.Errorf("load provider OAuth credential: %w", err)
	}
	return credential, nil
}

// RefreshProviderOAuthCredential atomically replaces an expired OAuth access
// token only when it is still the exact encrypted value the caller refreshed.
// A concurrent worker that won the race causes a false result; callers reload
// the row and use that winner instead of retrying a now-rotated refresh token.
func (s *PostgresStore) RefreshProviderOAuthCredential(ctx context.Context, tenantID uuid.UUID, credentialRef string, refresh domain.ProviderOAuthCredentialRefresh) (bool, error) {
	if tenantID == uuid.Nil || !validOAuthCredentialRef(credentialRef) || len(refresh.ExpectedAccessTokenCiphertext) < 32 || len(refresh.AccessTokenCiphertext) < 32 || refresh.ExpiresAt.IsZero() {
		return false, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin provider OAuth credential refresh: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE provider_oauth_credentials
		SET access_token_ciphertext=$4,
			refresh_token_ciphertext=CASE WHEN octet_length($5::bytea) > 0 THEN $5 ELSE refresh_token_ciphertext END,
			expires_at=$6,
			updated_at=now()
		WHERE credential_ref=$1
			AND tenant_id=$2
			AND provider='gitlab'
			AND revoked_at IS NULL
			AND access_token_ciphertext=$3`, credentialRef, tenantID, refresh.ExpectedAccessTokenCiphertext, refresh.AccessTokenCiphertext, nullableBytes(refresh.RefreshTokenCiphertext), refresh.ExpiresAt.UTC())
	if err != nil {
		return false, fmt.Errorf("refresh provider OAuth credential: %w", err)
	}
	if result.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,'system:provider-credential-refresh','provider_oauth_credential.refreshed',$2,jsonb_build_object('provider','gitlab','expires_at',$3::timestamptz))`, tenantID, credentialRef, refresh.ExpiresAt.UTC()); err != nil {
		return false, fmt.Errorf("audit refreshed provider OAuth credential: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit provider OAuth credential refresh: %w", err)
	}
	return true, nil
}

// RevokeOrphanedProviderOAuthCredentials closes OAuth attempts that never
// became installations. It does not call GitLab's remote revocation endpoint:
// this is the deployment's encrypted local capability only. The caller uses a
// cutoff later than the signed setup receipt's lifetime and a bounded batch.
func (s *PostgresStore) RevokeOrphanedProviderOAuthCredentials(ctx context.Context, before time.Time, limit int) (int, error) {
	if before.IsZero() || limit < 1 || limit > 500 {
		return 0, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin orphaned provider credential cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT credential.credential_ref, credential.tenant_id
		FROM provider_oauth_credentials credential
		WHERE credential.provider='gitlab' AND credential.revoked_at IS NULL
		  AND credential.created_at < $1
		  AND NOT EXISTS (
			SELECT 1 FROM provider_installations installation
			WHERE installation.tenant_id=credential.tenant_id
			  AND installation.active=TRUE
			  AND installation.credential_ref=credential.credential_ref
		  )
		ORDER BY credential.created_at, credential.credential_ref
		LIMIT $2 FOR UPDATE OF credential SKIP LOCKED`, before.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("select orphaned provider credentials: %w", err)
	}
	type orphan struct {
		ref      string
		tenantID uuid.UUID
	}
	var candidates []orphan
	for rows.Next() {
		var item orphan
		if err := rows.Scan(&item.ref, &item.tenantID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan orphaned provider credential: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate orphaned provider credentials: %w", err)
	}
	rows.Close()
	count := 0
	for _, item := range candidates {
		// Re-read after locking the credential. A concurrent installation can
		// commit while the candidate query waits for its FOR SHARE lock; the
		// candidate query's original statement snapshot is not authoritative.
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM provider_installations
			WHERE tenant_id=$1 AND active=TRUE AND credential_ref=$2
		)`, item.tenantID, item.ref).Scan(&active); err != nil {
			return 0, fmt.Errorf("recheck orphaned provider credential: %w", err)
		}
		if active {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE provider_oauth_credentials
			SET revoked_at=now(), updated_at=now()
			WHERE credential_ref=$1 AND tenant_id=$2 AND revoked_at IS NULL`, item.ref, item.tenantID); err != nil {
			return 0, fmt.Errorf("revoke orphaned provider credential: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events
			(tenant_id,actor_subject,action,target,metadata)
			VALUES ($1,'system:provider-credential-reaper','provider_oauth_credential.revoked',$2,
				jsonb_build_object('provider','gitlab','reason','orphaned_authorization'))`,
			item.tenantID, item.ref); err != nil {
			return 0, fmt.Errorf("audit orphaned provider credential cleanup: %w", err)
		}
		count++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit orphaned provider credential cleanup: %w", err)
	}
	return count, nil
}

func validOAuthCredentialRef(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "secret://provider/gitlab-oauth/") {
		return false
	}
	_, err := uuid.Parse(strings.TrimPrefix(value, "secret://provider/gitlab-oauth/"))
	return err == nil
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
