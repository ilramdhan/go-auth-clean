package postgres

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

var _ domain.TransactionRepository = (*TransactionRepository)(nil)

type TransactionRepository struct{ base }

func NewTransactionRepository(db *pgxpool.Pool) *TransactionRepository {
	return &TransactionRepository{base{db}}
}

const txCols = `id, user_id, account_id, category_id, type, amount, currency, transaction_date,
	note, source, recurring_rule_id, occurrence_date, import_hash, version, created_at, updated_at`

func scanTransaction(row pgx.Row) (*domain.Transaction, error) {
	var (
		s                domain.TransactionState
		typ, cur, source string
		amount           int64
	)
	err := row.Scan(&s.ID, &s.UserID, &s.AccountID, &s.CategoryID, &typ, &amount, &cur, &s.Date,
		&s.Note, &source, &s.RuleID, &s.Occurrence, &s.ImportHash, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrTransactionNotFound
	}
	if err != nil {
		return nil, err
	}
	c, err := currency(cur)
	if err != nil {
		return nil, err
	}
	s.Type, s.Source, s.Amount = domain.TxType(typ), domain.TxSource(source), money.New(amount, c)
	return domain.RehydrateTransaction(s), nil
}

func (r *TransactionRepository) Create(ctx context.Context, t *domain.Transaction) error {
	_, err := r.conn(ctx).Exec(ctx, insertTxSQL, txArgs(t)...)
	if pgCode(err) == pgUniqueViolation {
		return domain.ErrDuplicateImport
	}
	if err != nil {
		return fmt.Errorf("transactionRepo.Create: %w", err)
	}
	return nil
}

const insertTxSQL = `INSERT INTO transactions (` + txCols + `)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`

func txArgs(t *domain.Transaction) []any {
	return []any{t.ID(), t.UserID(), t.AccountID(), t.CategoryID(), string(t.Type()),
		t.Amount().Amount(), t.Amount().Currency().Code(), t.Date(), t.Note(), string(t.Source()),
		t.RecurringRuleID(), t.OccurrenceDate(), t.ImportHash(), t.Version(), t.CreatedAt(), t.UpdatedAt()}
}

// CreateOccurrence menyimpan transaksi recurring secara idempotent: bila
// (rule, occurrence) sudah ada (termasuk yang sudah dihapus user) tidak ada yang dibuat.
func (r *TransactionRepository) CreateOccurrence(ctx context.Context, t *domain.Transaction) (bool, error) {
	q := insertTxSQL + ` ON CONFLICT (recurring_rule_id, occurrence_date) WHERE recurring_rule_id IS NOT NULL DO NOTHING`
	tag, err := r.conn(ctx).Exec(ctx, q, txArgs(t)...)
	if err != nil {
		return false, fmt.Errorf("transactionRepo.CreateOccurrence: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ExistingImportHashes mengembalikan hash (hex) yang sudah pernah di-import user.
func (r *TransactionRepository) ExistingImportHashes(ctx context.Context, userID uuid.UUID, hashes [][]byte) (map[string]bool, error) {
	out := make(map[string]bool)
	if len(hashes) == 0 {
		return out, nil
	}
	rows, err := r.conn(ctx).Query(ctx, `SELECT import_hash FROM transactions
		WHERE user_id = $1 AND import_hash = ANY($2) AND deleted_at IS NULL`, userID, hashes)
	if err != nil {
		return nil, fmt.Errorf("transactionRepo.ExistingImportHashes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h []byte
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out[hex.EncodeToString(h)] = true
	}
	return out, rows.Err()
}

func (r *TransactionRepository) Get(ctx context.Context, userID, id uuid.UUID) (*domain.Transaction, error) {
	q := `SELECT ` + txCols + ` FROM transactions WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`
	t, err := scanTransaction(r.conn(ctx).QueryRow(ctx, q, id, userID))
	if err != nil && !errors.Is(err, domain.ErrTransactionNotFound) {
		return nil, fmt.Errorf("transactionRepo.Get: %w", err)
	}
	return t, err
}

func (r *TransactionRepository) Update(ctx context.Context, t *domain.Transaction) error {
	const q = `UPDATE transactions SET account_id = $3, category_id = $4, type = $5, amount = $6,
		currency = $7, transaction_date = $8, note = $9, updated_at = $10, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $11 AND deleted_at IS NULL
		RETURNING version`
	var v int
	err := r.conn(ctx).QueryRow(ctx, q, t.ID(), t.UserID(), t.AccountID(), t.CategoryID(), string(t.Type()),
		t.Amount().Amount(), t.Amount().Currency().Code(), t.Date(), t.Note(), t.UpdatedAt(), t.Version()).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return r.missingOrConflict(ctx, t.UserID(), t.ID())
	}
	if err != nil {
		return fmt.Errorf("transactionRepo.Update: %w", err)
	}
	t.SyncVersion(v)
	return nil
}

func (r *TransactionRepository) SoftDelete(ctx context.Context, t *domain.Transaction, at time.Time) error {
	const q = `UPDATE transactions SET deleted_at = $3, updated_at = $3, version = version + 1
		WHERE id = $1 AND user_id = $2 AND version = $4 AND deleted_at IS NULL`
	tag, err := r.conn(ctx).Exec(ctx, q, t.ID(), t.UserID(), at, t.Version())
	if err != nil {
		return fmt.Errorf("transactionRepo.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.missingOrConflict(ctx, t.UserID(), t.ID())
	}
	return nil
}

func (r *TransactionRepository) missingOrConflict(ctx context.Context, userID, id uuid.UUID) error {
	var exists bool
	err := r.conn(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM transactions
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL)`, id, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("transactionRepo.exists: %w", err)
	}
	if exists {
		return domain.ErrVersionConflict
	}
	return domain.ErrTransactionNotFound
}

// sqlBuilder mengumpulkan klausa WHERE dengan placeholder bernomor.
type sqlBuilder struct {
	where []string
	args  []any
}

func (b *sqlBuilder) arg(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

func (b *sqlBuilder) add(format string, vals ...any) {
	ph := make([]any, len(vals))
	for i, v := range vals {
		ph[i] = b.arg(v)
	}
	b.where = append(b.where, fmt.Sprintf(format, ph...))
}

// txFilter membangun WHERE untuk List & Export. Kolom selalu dikualifikasi
// "transactions." karena Export melakukan join.
func txFilter(userID uuid.UUID, f domain.TransactionFilter) *sqlBuilder {
	b := &sqlBuilder{}
	b.add("transactions.user_id = %s", userID)
	b.where = append(b.where, "transactions.deleted_at IS NULL")
	if f.From != nil {
		b.add("transactions.transaction_date >= %s", domain.DateOf(*f.From))
	}
	if f.To != nil {
		b.add("transactions.transaction_date <= %s", domain.DateOf(*f.To))
	}
	if f.Type != nil {
		b.add("transactions.type = %s", string(*f.Type))
	}
	if len(f.AccountIDs) > 0 {
		b.add("transactions.account_id = ANY(%s)", f.AccountIDs)
	}
	if len(f.CategoryIDs) > 0 {
		if f.IncludeChildren {
			b.add(`transactions.category_id IN (SELECT id FROM categories WHERE (id = ANY(%[1]s) OR parent_id = ANY(%[1]s))
				AND (user_id = %[2]s OR user_id IS NULL))`, f.CategoryIDs, userID)
		} else {
			b.add("transactions.category_id = ANY(%s)", f.CategoryIDs)
		}
	}
	if f.Currency != nil {
		b.add("transactions.currency = %s", f.Currency.Code())
	}
	if f.MinAmount != nil {
		b.add("transactions.amount >= %s", *f.MinAmount)
	}
	if f.MaxAmount != nil {
		b.add("transactions.amount <= %s", *f.MaxAmount)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		b.add(`transactions.note ILIKE %s ESCAPE '\'`, "%"+escapeLike(q)+"%")
	}
	if name := strings.TrimSpace(f.TagName); name != "" {
		b.add(`EXISTS (SELECT 1 FROM transaction_tags tt JOIN tags g ON g.id = tt.tag_id
			WHERE tt.transaction_id = transactions.id AND g.user_id = %s AND g.name = %s)`, userID, name)
	}
	return b
}

// List memakai keyset pagination (transaction_date, id) DESC; mengembalikan Limit+1 baris.
func (r *TransactionRepository) List(ctx context.Context, userID uuid.UUID, f domain.TransactionFilter) ([]*domain.Transaction, error) {
	b := txFilter(userID, f)
	if f.After != nil {
		b.add("(transaction_date, id) < (%s, %s)", domain.DateOf(f.After.Date), f.After.ID)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT ` + txCols + ` FROM transactions WHERE ` + strings.Join(b.where, " AND ") +
		` ORDER BY transaction_date DESC, id DESC LIMIT ` + b.arg(limit+1)
	rows, err := r.conn(ctx).Query(ctx, q, b.args...)
	if err != nil {
		return nil, fmt.Errorf("transactionRepo.List: %w", err)
	}
	defer rows.Close()
	out := make([]*domain.Transaction, 0, limit+1)
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("transactionRepo.List scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Export men-stream transaksi sesuai filter (tanpa limit) urut tanggal naik,
// lengkap dengan nama akun, kategori dan tag. fn dipanggil per baris.
func (r *TransactionRepository) Export(ctx context.Context, userID uuid.UUID, f domain.TransactionFilter, fn func(domain.ExportRow) error) error {
	b := txFilter(userID, f)
	cols := qualify("transactions.", txCols)
	q := `SELECT ` + cols + `, a.name, c.name,
		COALESCE((SELECT array_agg(g.name::text ORDER BY g.name) FROM transaction_tags tt
			JOIN tags g ON g.id = tt.tag_id WHERE tt.transaction_id = transactions.id), '{}')
		FROM transactions
		JOIN accounts a ON a.id = transactions.account_id
		JOIN categories c ON c.id = transactions.category_id
		WHERE ` + strings.Join(b.where, " AND ") + ` ORDER BY transactions.transaction_date, transactions.id`
	rows, err := r.conn(ctx).Query(ctx, q, b.args...)
	if err != nil {
		return fmt.Errorf("transactionRepo.Export: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row domain.ExportRow
		t, err := scanTransaction(exportScanner{rows, &row})
		if err != nil {
			return fmt.Errorf("transactionRepo.Export scan: %w", err)
		}
		row.Transaction = t
		if err := fn(row); err != nil {
			return err
		}
	}
	return rows.Err()
}

// exportScanner menambahkan kolom join export ke scan transaksi.
type exportScanner struct {
	rows pgx.Rows
	row  *domain.ExportRow
}

func (s exportScanner) Scan(dest ...any) error {
	return s.rows.Scan(append(dest, &s.row.AccountName, &s.row.CategoryName, &s.row.Tags)...)
}

// qualify menambahkan prefix tabel ke setiap kolom pada daftar "a, b, c".
func qualify(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = prefix + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}
