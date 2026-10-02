package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/database"
)

var (
	_ domain.RoleRepository      = (*RoleRepository)(nil)
	_ domain.MFARepository       = (*MFARepository)(nil)
	_ domain.IdentityRepository  = (*IdentityRepository)(nil)
	_ domain.LoginCodeRepository = (*LoginCodeRepository)(nil)
	_ domain.APIKeyRepository    = (*APIKeyRepository)(nil)
)

func isUnique(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == pgUniqueViolation
}

// ---------- Roles ----------

type RoleRepository struct{ db *pgxpool.Pool }

func NewRoleRepository(db *pgxpool.Pool) *RoleRepository { return &RoleRepository{db: db} }

func (r *RoleRepository) conn(ctx context.Context) database.DBTX { return database.Conn(ctx, r.db) }

func (r *RoleRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]domain.Role, error) {
	const q = `SELECT ro.name FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
		WHERE ur.user_id = $1 AND ro.name <> 'user' ORDER BY ro.id`
	rows, err := r.conn(ctx).Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("roleRepo.ListByUser: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("roleRepo.ListByUser: %w", err)
	}
	out := []domain.Role{domain.RoleUser}
	for _, n := range names {
		if role, err := domain.ParseRole(n); err == nil {
			out = append(out, role)
		}
	}
	return out, nil
}

func (r *RoleRepository) Grant(ctx context.Context, userID uuid.UUID, role domain.Role, grantedBy uuid.UUID, at time.Time) error {
	const q = `INSERT INTO user_roles (user_id, role_id, granted_at, granted_by)
		SELECT $1, id, $3, $4 FROM roles WHERE name = $2
		ON CONFLICT (user_id, role_id) DO NOTHING`
	var by *uuid.UUID
	if grantedBy != uuid.Nil {
		by = &grantedBy
	}
	if _, err := r.conn(ctx).Exec(ctx, q, userID, string(role), at, by); err != nil {
		return fmt.Errorf("roleRepo.Grant: %w", err)
	}
	return nil
}

func (r *RoleRepository) Revoke(ctx context.Context, userID uuid.UUID, role domain.Role) error {
	const q = `DELETE FROM user_roles WHERE user_id = $1 AND role_id = (SELECT id FROM roles WHERE name = $2)`
	if _, err := r.conn(ctx).Exec(ctx, q, userID, string(role)); err != nil {
		return fmt.Errorf("roleRepo.Revoke: %w", err)
	}
	return nil
}

func (r *RoleRepository) CountUsers(ctx context.Context, role domain.Role) (int, error) {
	const q = `SELECT count(*) FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
		JOIN users u ON u.id = ur.user_id WHERE ro.name = $1 AND u.deleted_at IS NULL`
	var n int
	if err := r.conn(ctx).QueryRow(ctx, q, string(role)).Scan(&n); err != nil {
		return 0, fmt.Errorf("roleRepo.CountUsers: %w", err)
	}
	return n, nil
}

// ---------- MFA ----------

type MFARepository struct{ db *pgxpool.Pool }

func NewMFARepository(db *pgxpool.Pool) *MFARepository { return &MFARepository{db: db} }

func (r *MFARepository) conn(ctx context.Context) database.DBTX { return database.Conn(ctx, r.db) }

func (r *MFARepository) Get(ctx context.Context, userID uuid.UUID) (*domain.UserMFA, error) {
	const q = `SELECT user_id, secret_encrypted, status, last_used_step, enabled_at, created_at, updated_at
		FROM user_mfa WHERE user_id = $1`
	var m domain.UserMFA
	err := r.conn(ctx).QueryRow(ctx, q, userID).Scan(&m.UserID, &m.SecretEncrypted, &m.Status,
		&m.LastUsedStep, &m.EnabledAt, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrMFANotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mfaRepo.Get: %w", err)
	}
	return &m, nil
}

func (r *MFARepository) UpsertPending(ctx context.Context, m *domain.UserMFA) error {
	// WHERE pada DO UPDATE: baris enabled tidak tersentuh -> 0 row -> ErrMFAAlreadyEnabled.
	const q = `INSERT INTO user_mfa (user_id, secret_encrypted, status, last_used_step, created_at, updated_at)
		VALUES ($1, $2, 'pending', 0, $3, $3)
		ON CONFLICT (user_id) DO UPDATE SET secret_encrypted = EXCLUDED.secret_encrypted,
			last_used_step = 0, updated_at = EXCLUDED.updated_at
		WHERE user_mfa.status = 'pending'`
	tag, err := r.conn(ctx).Exec(ctx, q, m.UserID, m.SecretEncrypted, m.CreatedAt)
	if err != nil {
		return fmt.Errorf("mfaRepo.UpsertPending: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrMFAAlreadyEnabled
	}
	return nil
}

func (r *MFARepository) Enable(ctx context.Context, userID uuid.UUID, step int64, now time.Time) error {
	const q = `UPDATE user_mfa SET status = 'enabled', enabled_at = $3, last_used_step = $2, updated_at = $3
		WHERE user_id = $1 AND status = 'pending'`
	tag, err := r.conn(ctx).Exec(ctx, q, userID, step, now)
	if err != nil {
		return fmt.Errorf("mfaRepo.Enable: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrMFASetupRequired
	}
	return nil
}

func (r *MFARepository) AdvanceStep(ctx context.Context, userID uuid.UUID, step int64, now time.Time) error {
	const q = `UPDATE user_mfa SET last_used_step = $2, updated_at = $3
		WHERE user_id = $1 AND last_used_step < $2`
	tag, err := r.conn(ctx).Exec(ctx, q, userID, step, now)
	if err != nil {
		return fmt.Errorf("mfaRepo.AdvanceStep: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrMFACodeReplayed
	}
	return nil
}

func (r *MFARepository) Delete(ctx context.Context, userID uuid.UUID) error {
	const q = `WITH rc AS (DELETE FROM mfa_recovery_codes WHERE user_id = $1)
		DELETE FROM user_mfa WHERE user_id = $1`
	if _, err := r.conn(ctx).Exec(ctx, q, userID); err != nil {
		return fmt.Errorf("mfaRepo.Delete: %w", err)
	}
	return nil
}

func (r *MFARepository) ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, hashes [][]byte, now time.Time) error {
	ids := make([]uuid.UUID, len(hashes))
	for i := range hashes {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("mfaRepo.ReplaceRecoveryCodes: %w", err)
		}
		ids[i] = id
	}
	// Satu statement = atomic tanpa perlu tx eksplisit.
	const q = `WITH del AS (DELETE FROM mfa_recovery_codes WHERE user_id = $1)
		INSERT INTO mfa_recovery_codes (id, user_id, code_hash, created_at)
		SELECT id, $1, h, $4 FROM unnest($2::uuid[], $3::bytea[]) AS t(id, h)`
	if _, err := r.conn(ctx).Exec(ctx, q, userID, ids, hashes, now); err != nil {
		return fmt.Errorf("mfaRepo.ReplaceRecoveryCodes: %w", err)
	}
	return nil
}

func (r *MFARepository) UseRecoveryCode(ctx context.Context, userID uuid.UUID, hash []byte, now time.Time) error {
	const q = `UPDATE mfa_recovery_codes SET used_at = $3
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`
	tag, err := r.conn(ctx).Exec(ctx, q, userID, hash, now)
	if err != nil {
		return fmt.Errorf("mfaRepo.UseRecoveryCode: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrInvalidMFACode
	}
	return nil
}

func (r *MFARepository) CountRecoveryCodes(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := r.conn(ctx).QueryRow(ctx,
		`SELECT count(*) FROM mfa_recovery_codes WHERE user_id = $1 AND used_at IS NULL`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("mfaRepo.CountRecoveryCodes: %w", err)
	}
	return n, nil
}

// ---------- External identities ----------

type IdentityRepository struct{ db *pgxpool.Pool }

func NewIdentityRepository(db *pgxpool.Pool) *IdentityRepository { return &IdentityRepository{db: db} }

func (r *IdentityRepository) conn(ctx context.Context) database.DBTX {
	return database.Conn(ctx, r.db)
}

const identityColumns = `id, user_id, provider, subject, email, created_at`

func scanIdentity(row pgx.Row) (domain.ExternalIdentity, error) {
	var i domain.ExternalIdentity
	err := row.Scan(&i.ID, &i.UserID, &i.Provider, &i.Subject, &i.Email, &i.CreatedAt)
	return i, err
}

func (r *IdentityRepository) FindBySubject(ctx context.Context, provider, subject string) (*domain.ExternalIdentity, error) {
	q := `SELECT ` + identityColumns + ` FROM user_identities WHERE provider = $1 AND subject = $2`
	i, err := scanIdentity(r.conn(ctx).QueryRow(ctx, q, provider, subject))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrIdentityNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("identityRepo.FindBySubject: %w", err)
	}
	return &i, nil
}

func (r *IdentityRepository) Create(ctx context.Context, i *domain.ExternalIdentity) error {
	const q = `INSERT INTO user_identities (id, user_id, provider, subject, email, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := r.conn(ctx).Exec(ctx, q, i.ID, i.UserID, i.Provider, i.Subject, i.Email, i.CreatedAt)
	if isUnique(err) {
		return domain.ErrIdentityTaken
	}
	if err != nil {
		return fmt.Errorf("identityRepo.Create: %w", err)
	}
	return nil
}

func (r *IdentityRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]domain.ExternalIdentity, error) {
	q := `SELECT ` + identityColumns + ` FROM user_identities WHERE user_id = $1 ORDER BY created_at, id`
	rows, err := r.conn(ctx).Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("identityRepo.ListByUser: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ExternalIdentity, error) {
		return scanIdentity(row)
	})
	if err != nil {
		return nil, fmt.Errorf("identityRepo.ListByUser: %w", err)
	}
	return out, nil
}

func (r *IdentityRepository) DeleteByUser(ctx context.Context, userID uuid.UUID) error {
	if _, err := r.conn(ctx).Exec(ctx, `DELETE FROM user_identities WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("identityRepo.DeleteByUser: %w", err)
	}
	return nil
}

// ---------- OAuth login codes ----------

type LoginCodeRepository struct{ db *pgxpool.Pool }

func NewLoginCodeRepository(db *pgxpool.Pool) *LoginCodeRepository {
	return &LoginCodeRepository{db: db}
}

func (r *LoginCodeRepository) conn(ctx context.Context) database.DBTX {
	return database.Conn(ctx, r.db)
}

func (r *LoginCodeRepository) Create(ctx context.Context, c *domain.LoginCode) error {
	const q = `INSERT INTO oauth_login_codes (code_hash, user_id, provider, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)`
	if _, err := r.conn(ctx).Exec(ctx, q, c.CodeHash, c.UserID, c.Provider, c.ExpiresAt, c.CreatedAt); err != nil {
		return fmt.Errorf("loginCodeRepo.Create: %w", err)
	}
	return nil
}

func (r *LoginCodeRepository) Consume(ctx context.Context, hash []byte, now time.Time) (*domain.LoginCode, error) {
	// DELETE ... RETURNING: hanya satu pemanggil yang mendapat baris (sekali pakai).
	const q = `DELETE FROM oauth_login_codes WHERE code_hash = $1
		RETURNING code_hash, user_id, provider, expires_at, created_at`
	var c domain.LoginCode
	err := r.conn(ctx).QueryRow(ctx, q, hash).Scan(&c.CodeHash, &c.UserID, &c.Provider, &c.ExpiresAt, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrLoginCodeInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("loginCodeRepo.Consume: %w", err)
	}
	if !c.ExpiresAt.After(now) {
		return nil, domain.ErrLoginCodeInvalid
	}
	return &c, nil
}

func (r *LoginCodeRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.conn(ctx).Exec(ctx, `DELETE FROM oauth_login_codes WHERE expires_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("loginCodeRepo.DeleteExpired: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ---------- API keys ----------

type APIKeyRepository struct{ db *pgxpool.Pool }

func NewAPIKeyRepository(db *pgxpool.Pool) *APIKeyRepository { return &APIKeyRepository{db: db} }

func (r *APIKeyRepository) conn(ctx context.Context) database.DBTX { return database.Conn(ctx, r.db) }

//nolint:gosec // G101 false positive: daftar kolom SQL, bukan kredensial.
const apiKeyColumns = `id, user_id, name, prefix, secret_hash, scopes, expires_at, last_used_at, revoked_at, created_at`

func scanAPIKey(row pgx.Row) (domain.APIKey, error) {
	var (
		k      domain.APIKey
		scopes []string
	)
	err := row.Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.SecretHash, &scopes,
		&k.ExpiresAt, &k.LastUsedAt, &k.RevokedAt, &k.CreatedAt)
	for _, s := range scopes {
		k.Scopes = append(k.Scopes, domain.Scope(s))
	}
	return k, err
}

func (r *APIKeyRepository) Create(ctx context.Context, k *domain.APIKey) error {
	const q = `INSERT INTO api_keys (id, user_id, name, prefix, secret_hash, scopes, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	scopes := make([]string, len(k.Scopes))
	for i, s := range k.Scopes {
		scopes[i] = string(s)
	}
	_, err := r.conn(ctx).Exec(ctx, q, k.ID, k.UserID, k.Name, k.Prefix, k.SecretHash, scopes, k.ExpiresAt, k.CreatedAt)
	if err != nil {
		return fmt.Errorf("apiKeyRepo.Create: %w", err)
	}
	return nil
}

func (r *APIKeyRepository) FindByPrefix(ctx context.Context, prefix string) (*domain.APIKey, error) {
	q := `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE prefix = $1`
	k, err := scanAPIKey(r.conn(ctx).QueryRow(ctx, q, prefix))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrAPIKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("apiKeyRepo.FindByPrefix: %w", err)
	}
	return &k, nil
}

func (r *APIKeyRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]domain.APIKey, error) {
	q := `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC, id DESC`
	rows, err := r.conn(ctx).Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("apiKeyRepo.ListByUser: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.APIKey, error) {
		return scanAPIKey(row)
	})
	if err != nil {
		return nil, fmt.Errorf("apiKeyRepo.ListByUser: %w", err)
	}
	return out, nil
}

func (r *APIKeyRepository) CountActiveByUser(ctx context.Context, userID uuid.UUID, now time.Time) (int, error) {
	const q = `SELECT count(*) FROM api_keys WHERE user_id = $1 AND revoked_at IS NULL
		AND (expires_at IS NULL OR expires_at > $2)`
	var n int
	if err := r.conn(ctx).QueryRow(ctx, q, userID, now).Scan(&n); err != nil {
		return 0, fmt.Errorf("apiKeyRepo.CountActiveByUser: %w", err)
	}
	return n, nil
}

func (r *APIKeyRepository) Revoke(ctx context.Context, userID, id uuid.UUID, now time.Time) error {
	tag, err := r.conn(ctx).Exec(ctx,
		`UPDATE api_keys SET revoked_at = $3 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, id, userID, now)
	if err != nil {
		return fmt.Errorf("apiKeyRepo.Revoke: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAPIKeyNotFound
	}
	return nil
}

func (r *APIKeyRepository) RevokeAllByUser(ctx context.Context, userID uuid.UUID, now time.Time) error {
	_, err := r.conn(ctx).Exec(ctx,
		`UPDATE api_keys SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, now)
	if err != nil {
		return fmt.Errorf("apiKeyRepo.RevokeAllByUser: %w", err)
	}
	return nil
}

func (r *APIKeyRepository) TouchLastUsed(ctx context.Context, id uuid.UUID, now, olderThan time.Time) error {
	_, err := r.conn(ctx).Exec(ctx,
		`UPDATE api_keys SET last_used_at = $2 WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < $3)`,
		id, now, olderThan)
	if err != nil {
		return fmt.Errorf("apiKeyRepo.TouchLastUsed: %w", err)
	}
	return nil
}
