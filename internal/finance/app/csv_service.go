package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
	"go-auth-clean/internal/shared/money"
)

const (
	// MaxImportBytes & MaxImportRows membatasi file import (PRD F10).
	MaxImportBytes = 5 << 20
	MaxImportRows  = 10_000
	// maxRowErrors membatasi panjang laporan error agar response tetap kecil.
	maxRowErrors = 500
)

var (
	ErrImportTooLarge   = errors.New("import file exceeds 5 MB")
	ErrImportTooManyRow = errors.New("import file exceeds 10000 rows")
	ErrImportBadHeader  = errors.New("invalid CSV header")
)

// ExportHeader adalah kolom CSV export, juga format yang diterima import.
var ExportHeader = []string{"date", "type", "amount", "currency", "account", "category", "note", "tags"}

// ExportTransactions menulis transaksi (sesuai filter, tanpa paging) sebagai
// CSV ke w secara streaming: baris dibaca dari DB satu per satu dan langsung
// di-flush, sehingga memori konstan berapa pun jumlah barisnya.
func (s *Service) ExportTransactions(ctx context.Context, in ListTransactionsInput, w io.Writer) error {
	f, err := s.transactionFilter(ctx, in)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(ExportHeader); err != nil {
		return err
	}
	n := 0
	err = s.transactions.Export(ctx, in.UserID, f, func(r domain.ExportRow) error {
		t := r.Transaction
		rec := []string{
			t.Date().Format(time.DateOnly), string(t.Type()), t.Amount().String(), t.Amount().Currency().Code(),
			r.AccountName, r.CategoryName, t.Note(), strings.Join(r.Tags, ";"),
		}
		for i := range rec {
			rec[i] = SanitizeCSVCell(rec[i])
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
		if n++; n%500 == 0 {
			cw.Flush()
			return cw.Error()
		}
		return nil
	})
	if err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// SanitizeCSVCell mencegah CSV/formula injection: sel yang diawali = + - @
// (atau tab/CR) diberi prefix ' supaya spreadsheet tidak mengeksekusinya.
func SanitizeCSVCell(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + v
	}
	return v
}

// unsanitizeCSVCell membalik SanitizeCSVCell (round-trip export -> import).
func unsanitizeCSVCell(v string) string {
	if len(v) >= 2 && v[0] == '\'' && strings.ContainsRune("=+-@\t\r", rune(v[1])) {
		return v[1:]
	}
	return v
}

type ImportInput struct {
	UserID uuid.UUID
	File   io.Reader
	DryRun bool
}

// ImportRowError adalah error validasi satu baris (Row 1-based, header = baris 1).
type ImportRowError struct {
	Row     int
	Field   string
	Message string
}

// ImportResult: Imported = baris valid (dry-run: yang akan di-import),
// Skipped = kemungkinan duplikat (hash sudah ada atau berulang di file).
type ImportResult struct {
	DryRun     bool
	Imported   int
	Skipped    int
	Duplicates []int
	Errors     []ImportRowError
}

func (r *ImportResult) addError(row int, field string, err error) {
	if len(r.Errors) < maxRowErrors {
		r.Errors = append(r.Errors, ImportRowError{Row: row, Field: field, Message: err.Error()})
	}
}

// importRow adalah baris yang lolos validasi format.
type importRow struct {
	line     int
	date     time.Time
	typ      domain.TxType
	amount   money.Money
	account  *domain.Account
	category *domain.Category
	note     string
	tagIDs   []uuid.UUID
	hash     []byte
}

// ImportTransactions membaca CSV (format = ExportHeader; kolom currency
// opsional), memvalidasi per baris, men-skip kemungkinan duplikat (hash
// date|amount|note|account) lalu menyimpan semua baris valid dalam satu DB
// transaction. DryRun = hanya validasi, tidak ada yang ditulis.
func (s *Service) ImportTransactions(ctx context.Context, in ImportInput) (*ImportResult, error) {
	data, err := io.ReadAll(io.LimitReader(in.File, MaxImportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxImportBytes {
		return nil, ErrImportTooLarge
	}
	records, err := readCSV(data)
	if err != nil {
		return nil, err
	}
	loc, err := s.userLocation(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	res := &ImportResult{DryRun: in.DryRun, Duplicates: []int{}, Errors: []ImportRowError{}}
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		lk, err := s.newImportLookup(ctx, in.UserID)
		if err != nil {
			return err
		}
		rows := make([]importRow, 0, len(records.rows))
		for i, rec := range records.rows {
			if r, ok := s.parseImportRow(rec, records.col, i+2, lk, loc, res); ok {
				rows = append(rows, r)
			}
		}
		rows, err = s.dedupeImport(ctx, in.UserID, rows, res)
		if err != nil {
			return err
		}
		return s.commitImport(ctx, in, rows, loc, res)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

type csvRecords struct {
	col  map[string]int
	rows [][]string
}

func readCSV(data []byte) (*csvRecords, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // BOM dari Excel
	if !utf8.Valid(data) {
		return nil, &domain.ValidationError{Field: "file", Reason: "must be UTF-8"}
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.ReuseRecord = false
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrImportBadHeader, err)
	}
	col := make(map[string]int, len(header))
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, req := range []string{"date", "type", "amount", "account", "category"} {
		if _, ok := col[req]; !ok {
			return nil, fmt.Errorf("%w: missing column %q", ErrImportBadHeader, req)
		}
	}
	out := &csvRecords{col: col}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, &domain.ValidationError{Field: "file", Reason: err.Error()}
		}
		if len(out.rows) == MaxImportRows {
			return nil, ErrImportTooManyRow
		}
		out.rows = append(out.rows, rec)
	}
	return out, nil
}

// importLookup memetakan nama/ID akun, kategori dan tag milik user (satu query masing-masing).
type importLookup struct {
	accounts   map[string]*domain.Account
	categories map[string][]*domain.Category
	tags       map[string]uuid.UUID
}

func (s *Service) newImportLookup(ctx context.Context, userID uuid.UUID) (*importLookup, error) {
	accs, err := s.accounts.List(ctx, userID, false)
	if err != nil {
		return nil, err
	}
	cats, err := s.categories.List(ctx, userID, nil)
	if err != nil {
		return nil, err
	}
	tags, err := s.tags.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	lk := &importLookup{
		accounts:   make(map[string]*domain.Account, 2*len(accs)),
		categories: make(map[string][]*domain.Category, 2*len(cats)),
		tags:       make(map[string]uuid.UUID, len(tags)),
	}
	for _, a := range accs {
		lk.accounts[a.ID().String()] = a
		lk.accounts[strings.ToLower(a.Name())] = a
	}
	for _, c := range cats {
		lk.categories[c.ID().String()] = append(lk.categories[c.ID().String()], c)
		k := strings.ToLower(c.Name())
		lk.categories[k] = append(lk.categories[k], c)
	}
	for _, t := range tags {
		lk.tags[strings.ToLower(t.Name())] = t.ID()
	}
	return lk, nil
}

func (lk *importLookup) category(key string, typ domain.TxType) *domain.Category {
	var fallback *domain.Category
	for _, c := range lk.categories[strings.ToLower(key)] {
		if c.Type() == typ {
			return c
		}
		fallback = c
	}
	return fallback // tipe beda -> ErrCategoryTypeMismatch dari domain
}

func cell(rec []string, col map[string]int, name string) string {
	i, ok := col[name]
	if !ok || i >= len(rec) {
		return ""
	}
	return unsanitizeCSVCell(strings.TrimSpace(rec[i]))
}

func (s *Service) parseImportRow(rec []string, col map[string]int, line int, lk *importLookup,
	loc *time.Location, res *ImportResult,
) (importRow, bool) {
	fail := func(field string, err error) (importRow, bool) {
		res.addError(line, field, err)
		return importRow{}, false
	}
	date, err := time.Parse(time.DateOnly, cell(rec, col, "date"))
	if err != nil {
		return fail("date", errors.New("must be YYYY-MM-DD"))
	}
	typ, err := domain.ParseTxType(strings.ToLower(cell(rec, col, "type")))
	if err != nil {
		return fail("type", err)
	}
	acc := lk.accounts[strings.ToLower(cell(rec, col, "account"))]
	if acc == nil {
		return fail("account", domain.ErrAccountNotFound)
	}
	if cur := cell(rec, col, "currency"); cur != "" && !strings.EqualFold(cur, acc.Currency().Code()) {
		return fail("currency", money.ErrCurrencyMismatch)
	}
	amount, err := money.Parse(cell(rec, col, "amount"), acc.Currency())
	if err != nil {
		return fail("amount", err)
	}
	cat := lk.category(cell(rec, col, "category"), typ)
	if cat == nil {
		return fail("category", domain.ErrCategoryNotFound)
	}
	var tagIDs []uuid.UUID
	for name := range strings.SplitSeq(cell(rec, col, "tags"), ";") {
		if name = strings.TrimPrefix(strings.TrimSpace(name), "#"); name == "" {
			continue
		}
		id, ok := lk.tags[strings.ToLower(name)]
		if !ok {
			return fail("tags", fmt.Errorf("%w: %s", domain.ErrTagNotFound, name))
		}
		tagIDs = append(tagIDs, id)
	}
	if tagIDs, err = domain.NormalizeTagIDs(tagIDs); err != nil {
		return fail("tags", err)
	}
	note := cell(rec, col, "note")
	// Validasi penuh (tanggal, note, tipe kategori) lewat aggregate.
	if _, err := domain.NewTransaction(domain.NewTransactionParams{
		ID: uuid.Nil, UserID: acc.UserID(), Account: acc, Category: cat, Type: typ, Amount: amount,
		Date: date, Note: note, Now: s.now(), Location: loc,
	}); err != nil {
		field := "row"
		if ve, ok := errors.AsType[*domain.ValidationError](err); ok {
			field = ve.Field
		}
		return fail(field, err)
	}
	return importRow{
		line: line, date: date, typ: typ, amount: amount, account: acc, category: cat,
		note: note, tagIDs: tagIDs, hash: ImportHash(date, amount, note, acc.ID()),
	}, true
}

// ImportHash adalah sidik dedupe: sha256(date|amount minor|currency|note|account_id).
func ImportHash(date time.Time, amount money.Money, note string, accountID uuid.UUID) []byte {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s", date.Format(time.DateOnly), strconv.FormatInt(amount.Amount(), 10),
		amount.Currency().Code(), strings.TrimSpace(note), accountID)
	return h.Sum(nil)
}

// dedupeImport membuang baris yang hash-nya sudah ada di DB atau berulang di file.
func (s *Service) dedupeImport(ctx context.Context, userID uuid.UUID, rows []importRow, res *ImportResult) ([]importRow, error) {
	hashes := make([][]byte, len(rows))
	for i := range rows {
		hashes[i] = rows[i].hash
	}
	existing, err := s.transactions.ExistingImportHashes(ctx, userID, hashes)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(rows))
	out := rows[:0]
	for i := range rows {
		k := hex.EncodeToString(rows[i].hash)
		if existing[k] || seen[k] {
			res.Skipped++
			res.Duplicates = append(res.Duplicates, rows[i].line)
			continue
		}
		seen[k] = true
		out = append(out, rows[i])
	}
	return out, nil
}

// commitImport menerapkan baris ke saldo akun (dikunci urut ID) dan menyimpan
// transaksi. Baris yang membuat saldo minus (akun tanpa allow_negative)
// dilaporkan sebagai error baris. DryRun = saldo dihitung di memori saja.
func (s *Service) commitImport(ctx context.Context, in ImportInput, rows []importRow, loc *time.Location, res *ImportResult) error {
	ids := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].account.ID())
	}
	accs, err := s.lockAccounts(ctx, in.UserID, ids...)
	if err != nil {
		return err
	}
	now := s.now()
	touched := map[uuid.UUID]*domain.Account{}
	for i := range rows {
		r := &rows[i]
		acc := accs[r.account.ID()]
		id, err := s.newID()
		if err != nil {
			return err
		}
		t, err := domain.NewTransaction(domain.NewTransactionParams{
			ID: id, UserID: in.UserID, Account: acc, Category: r.category, Type: r.typ, Amount: r.amount,
			Date: r.date, Note: r.note, Source: domain.SourceImport, ImportHash: r.hash, Now: now, Location: loc,
		})
		if err == nil {
			err = acc.Apply(t)
		}
		if err != nil {
			res.addError(r.line, "amount", err)
			continue
		}
		res.Imported++
		touched[acc.ID()] = acc
		if in.DryRun {
			continue
		}
		if err := s.transactions.Create(ctx, t); err != nil {
			return err
		}
		if len(r.tagIDs) > 0 {
			if err := s.tags.SetForTransaction(ctx, t.ID(), r.tagIDs); err != nil {
				return err
			}
		}
	}
	if in.DryRun {
		return nil
	}
	changed := make([]*domain.Account, 0, len(touched))
	for _, id := range ids {
		if a, ok := touched[id]; ok {
			changed = append(changed, a)
			delete(touched, id)
		}
	}
	return s.saveAccounts(ctx, changed, now)
}
