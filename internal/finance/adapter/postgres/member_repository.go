package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/finance/domain"
)

var _ domain.MemberRepository = (*MemberRepository)(nil)

type MemberRepository struct{ base }

func NewMemberRepository(db *pgxpool.Pool) *MemberRepository { return &MemberRepository{base{db}} }

const memberCols = `account_id, owner_id, member_id, role, created_at, updated_at`

func scanMember(row pgx.Row) (*domain.AccountMember, error) {
	var (
		m    domain.AccountMember
		role string
	)
	err := row.Scan(&m.AccountID, &m.OwnerID, &m.MemberID, &role, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrMemberNotFound
	}
	if err != nil {
		return nil, err
	}
	m.Role = domain.MemberRole(role)
	return &m, nil
}

func (r *MemberRepository) Add(ctx context.Context, m *domain.AccountMember) error {
	q := `INSERT INTO account_members (` + memberCols + `) VALUES ($1,$2,$3,$4,$5,$6)`
	_, err := r.conn(ctx).Exec(ctx, q, m.AccountID, m.OwnerID, m.MemberID, string(m.Role), m.CreatedAt, m.UpdatedAt)
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrMemberExists
	}
	if err != nil {
		return fmt.Errorf("memberRepo.Add: %w", err)
	}
	return nil
}

// Get hanya mengembalikan keanggotaan pada akun yang belum dihapus.
func (r *MemberRepository) Get(ctx context.Context, accountID, memberID uuid.UUID) (*domain.AccountMember, error) {
	q := `SELECT m.account_id, m.owner_id, m.member_id, m.role, m.created_at, m.updated_at
		FROM account_members m JOIN accounts a ON a.id = m.account_id AND a.user_id = m.owner_id
		WHERE m.account_id = $1 AND m.member_id = $2 AND a.deleted_at IS NULL`
	m, err := scanMember(r.conn(ctx).QueryRow(ctx, q, accountID, memberID))
	if err != nil && !errors.Is(err, domain.ErrMemberNotFound) {
		return nil, fmt.Errorf("memberRepo.Get: %w", err)
	}
	return m, err
}

func (r *MemberRepository) UpdateRole(ctx context.Context, m *domain.AccountMember) error {
	tag, err := r.conn(ctx).Exec(ctx, `UPDATE account_members SET role = $3, updated_at = $4
		WHERE account_id = $1 AND member_id = $2`, m.AccountID, m.MemberID, string(m.Role), m.UpdatedAt)
	if err != nil {
		return fmt.Errorf("memberRepo.UpdateRole: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrMemberNotFound
	}
	return nil
}

func (r *MemberRepository) Remove(ctx context.Context, accountID, memberID uuid.UUID) error {
	tag, err := r.conn(ctx).Exec(ctx, `DELETE FROM account_members WHERE account_id = $1 AND member_id = $2`, accountID, memberID)
	if err != nil {
		return fmt.Errorf("memberRepo.Remove: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrMemberNotFound
	}
	return nil
}

func (r *MemberRepository) List(ctx context.Context, ownerID, accountID uuid.UUID) ([]*domain.AccountMember, error) {
	rows, err := r.conn(ctx).Query(ctx, `SELECT `+memberCols+` FROM account_members
		WHERE owner_id = $1 AND account_id = $2 ORDER BY created_at, member_id`, ownerID, accountID)
	if err != nil {
		return nil, fmt.Errorf("memberRepo.List: %w", err)
	}
	defer rows.Close()
	out := []*domain.AccountMember{}
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, fmt.Errorf("memberRepo.List scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *MemberRepository) ListShared(ctx context.Context, memberID uuid.UUID) ([]domain.SharedAccount, error) {
	cols := "a." + strings.ReplaceAll(strings.Join(strings.Fields(accountCols), " "), ", ", ", a.")
	q := `SELECT ` + cols + `, m.role FROM account_members m
		JOIN accounts a ON a.id = m.account_id AND a.user_id = m.owner_id
		WHERE m.member_id = $1 AND a.deleted_at IS NULL ORDER BY a.name, a.id`
	rows, err := r.conn(ctx).Query(ctx, q, memberID)
	if err != nil {
		return nil, fmt.Errorf("memberRepo.ListShared: %w", err)
	}
	defer rows.Close()
	out := []domain.SharedAccount{}
	for rows.Next() {
		var role string
		a, err := scanAccount(extraRow{row: rows, extra: []any{&role}})
		if err != nil {
			return nil, fmt.Errorf("memberRepo.ListShared scan: %w", err)
		}
		out = append(out, domain.SharedAccount{Account: a, Role: domain.MemberRole(role)})
	}
	return out, rows.Err()
}

func (r *MemberRepository) TransactionOwner(ctx context.Context, memberID, txID uuid.UUID) (uuid.UUID, error) {
	const q = `SELECT t.user_id FROM transactions t
		JOIN account_members m ON m.account_id = t.account_id AND m.owner_id = t.user_id
		WHERE t.id = $1 AND m.member_id = $2 AND t.deleted_at IS NULL`
	var owner uuid.UUID
	err := r.conn(ctx).QueryRow(ctx, q, txID, memberID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, domain.ErrTransactionNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("memberRepo.TransactionOwner: %w", err)
	}
	return owner, nil
}

func (r *MemberRepository) TransferOwner(ctx context.Context, memberID, transferID uuid.UUID) (uuid.UUID, error) {
	const q = `SELECT t.user_id FROM transfers t
		WHERE t.id = $1 AND t.deleted_at IS NULL AND EXISTS (SELECT 1 FROM account_members m
			WHERE m.owner_id = t.user_id AND m.member_id = $2 AND m.account_id IN (t.from_account_id, t.to_account_id))`
	var owner uuid.UUID
	err := r.conn(ctx).QueryRow(ctx, q, transferID, memberID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, domain.ErrTransferNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("memberRepo.TransferOwner: %w", err)
	}
	return owner, nil
}
