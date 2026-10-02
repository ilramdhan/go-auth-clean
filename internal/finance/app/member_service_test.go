package app

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"go-auth-clean/internal/finance/domain"
)

// shared menyiapkan akun owner (f.user) dengan saldo, dibagikan ke editor &
// viewer; outsider bukan anggota.
type sharedSetup struct {
	acc, acc2                *domain.Account
	editor, viewer, outsider uuid.UUID
	tx                       *domain.Transaction
	tr                       *domain.Transfer
}

func newShared(t *testing.T, f *fixture) sharedSetup {
	t.Helper()
	ctx := context.Background()
	s := sharedSetup{acc: f.account(t, "cash", "100000"), acc2: f.account(t, "bank", "0"),
		editor: uuid.New(), viewer: uuid.New(), outsider: uuid.New()}
	f.users["viewer@x.io"] = s.viewer
	if _, err := f.svc.AddMember(ctx, AddMemberInput{OwnerID: f.user, AccountID: s.acc.ID(), MemberID: &s.editor, Role: "editor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AddMember(ctx, AddMemberInput{OwnerID: f.user, AccountID: s.acc2.ID(), MemberID: &s.editor, Role: "editor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AddMember(ctx, AddMemberInput{OwnerID: f.user, AccountID: s.acc.ID(), Email: " Viewer@x.io ", Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	var err error
	s.tx, err = f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: f.user, AccountID: s.acc.ID(),
		CategoryID: f.expense.ID(), Type: "expense", Amount: "1000", Date: today})
	if err != nil {
		t.Fatal(err)
	}
	s.tr, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: f.user, FromAccountID: s.acc.ID(),
		ToAccountID: s.acc2.ID(), Amount: "500", Date: today})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSharedWalletAccessMatrix(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := newShared(t, f)

	// ---- outsider: semuanya 404 (tidak bocor keberadaan) ----
	o := s.outsider
	_, err := f.svc.GetAccount(ctx, o, s.acc.ID())
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.GetTransaction(ctx, o, s.tx.ID())
	wantErr(t, err, domain.ErrTransactionNotFound)
	_, err = f.svc.GetTransfer(ctx, o, s.tr.ID())
	wantErr(t, err, domain.ErrTransferNotFound)
	_, err = f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: o, AccountID: s.acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "1", Date: today})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: o, ID: s.tx.ID(), Note: ptr("x")})
	wantErr(t, err, domain.ErrTransactionNotFound)
	wantErr(t, f.svc.DeleteTransaction(ctx, o, s.tx.ID()), domain.ErrTransactionNotFound)
	_, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: o, FromAccountID: s.acc.ID(), ToAccountID: s.acc2.ID(), Amount: "1", Date: today})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: o, ID: s.tr.ID(), Note: ptr("x")})
	wantErr(t, err, domain.ErrTransferNotFound)
	wantErr(t, f.svc.DeleteTransfer(ctx, o, s.tr.ID()), domain.ErrTransferNotFound)
	_, err = f.svc.ListTransactions(ctx, ListTransactionsInput{UserID: o, AccountIDs: []uuid.UUID{s.acc.ID()}, Limit: 10})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.ListTransfers(ctx, ListTransfersInput{UserID: o, AccountID: ptr(s.acc.ID()), Limit: 10})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.UpdateAccount(ctx, UpdateAccountInput{UserID: o, ID: s.acc.ID(), Name: ptr("x")})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.ListMembers(ctx, o, s.acc.ID())
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.AddMember(ctx, AddMemberInput{OwnerID: o, AccountID: s.acc.ID(), MemberID: &o, Role: "editor"})
	wantErr(t, err, domain.ErrAccountNotFound)
	wantErr(t, f.svc.RemoveMember(ctx, o, s.acc.ID(), s.viewer), domain.ErrAccountNotFound)

	// ---- viewer: baca saja ----
	v := s.viewer
	if _, err := f.svc.GetAccount(ctx, v, s.acc.ID()); err != nil {
		t.Fatalf("viewer GetAccount: %v", err)
	}
	if _, err := f.svc.GetTransaction(ctx, v, s.tx.ID()); err != nil {
		t.Fatalf("viewer GetTransaction: %v", err)
	}
	if _, err := f.svc.GetTransfer(ctx, v, s.tr.ID()); err != nil {
		t.Fatalf("viewer GetTransfer: %v", err)
	}
	list, err := f.svc.ListTransactions(ctx, ListTransactionsInput{UserID: v, AccountIDs: []uuid.UUID{s.acc.ID()}, Limit: 10})
	if err != nil || len(list) != 1 {
		t.Fatalf("viewer list = %d, %v", len(list), err)
	}
	if trs, err := f.svc.ListTransfers(ctx, ListTransfersInput{UserID: v, AccountID: ptr(s.acc.ID()), Limit: 10}); err != nil || len(trs) != 1 {
		t.Fatalf("viewer transfers = %d, %v", len(trs), err)
	}
	if ms, err := f.svc.ListMembers(ctx, v, s.acc.ID()); err != nil || len(ms) != 2 {
		t.Fatalf("viewer members = %d, %v", len(ms), err)
	}
	_, err = f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: v, AccountID: s.acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "1", Date: today})
	wantErr(t, err, domain.ErrForbidden)
	_, err = f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: v, ID: s.tx.ID(), Note: ptr("x")})
	wantErr(t, err, domain.ErrForbidden)
	wantErr(t, f.svc.DeleteTransaction(ctx, v, s.tx.ID()), domain.ErrForbidden)
	// transfer acc->acc2: viewer hanya anggota acc -> tulis = 404 untuk acc2
	_, err = f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: v, ID: s.tr.ID(), Note: ptr("x")})
	wantErr(t, err, domain.ErrAccountNotFound)
	wantErr(t, f.svc.DeleteTransfer(ctx, v, s.tr.ID()), domain.ErrAccountNotFound)
	// viewer hanya anggota acc, bukan acc2 -> transfer ke acc2 = 404 (bukan 403)
	_, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: v, FromAccountID: s.acc.ID(), ToAccountID: s.acc2.ID(), Amount: "1", Date: today})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.ArchiveAccount(ctx, v, s.acc.ID())
	wantErr(t, err, domain.ErrForbidden)
	wantErr(t, f.svc.RemoveMember(ctx, v, s.acc.ID(), s.editor), domain.ErrForbidden)

	// ---- editor: tulis transaksi/transfer, tidak mengelola akun/member ----
	e := s.editor
	nt, err := f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: e, AccountID: s.acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "2000", Date: today})
	if err != nil {
		t.Fatalf("editor create: %v", err)
	}
	if nt.UserID() != f.user {
		t.Fatalf("transaksi member harus disimpan atas nama owner")
	}
	if _, err := f.svc.UpdateTransaction(ctx, UpdateTransactionInput{UserID: e, ID: nt.ID(), Amount: ptr("3000")}); err != nil {
		t.Fatalf("editor update: %v", err)
	}
	if err := f.svc.DeleteTransaction(ctx, e, nt.ID()); err != nil {
		t.Fatalf("editor delete: %v", err)
	}
	ntr, err := f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: e, FromAccountID: s.acc.ID(), ToAccountID: s.acc2.ID(), Amount: "100", Date: today})
	if err != nil {
		t.Fatalf("editor transfer: %v", err)
	}
	if _, err := f.svc.UpdateTransfer(ctx, UpdateTransferInput{UserID: e, ID: ntr.ID(), Amount: ptr("200")}); err != nil {
		t.Fatalf("editor update transfer: %v", err)
	}
	if err := f.svc.DeleteTransfer(ctx, e, ntr.ID()); err != nil {
		t.Fatalf("editor delete transfer: %v", err)
	}
	// tag milik owner tidak boleh dipakai member
	_, err = f.svc.CreateTransaction(ctx, CreateTransactionInput{UserID: e, AccountID: s.acc.ID(), CategoryID: f.expense.ID(), Type: "expense", Amount: "1", Date: today, TagIDs: []uuid.UUID{uuid.New()}})
	wantErr(t, err, domain.ErrTagNotFound)
	// mencampur akun bersama dengan akun milik editor sendiri ditolak (owner beda)
	mine, err := f.svc.CreateAccount(ctx, CreateAccountInput{UserID: e, Name: "Mine", Type: "cash"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CreateTransfer(ctx, CreateTransferInput{UserID: e, FromAccountID: s.acc.ID(), ToAccountID: mine.ID(), Amount: "1", Date: today})
	wantErr(t, err, domain.ErrAccountNotFound)
	_, err = f.svc.UpdateAccount(ctx, UpdateAccountInput{UserID: e, ID: s.acc.ID(), Name: ptr("hack")})
	wantErr(t, err, domain.ErrForbidden)
	wantErr(t, f.svc.DeleteAccount(ctx, e, s.acc.ID()), domain.ErrForbidden)
	_, err = f.svc.AddMember(ctx, AddMemberInput{OwnerID: e, AccountID: s.acc.ID(), MemberID: &o, Role: "viewer"})
	wantErr(t, err, domain.ErrForbidden)
	_, err = f.svc.UpdateMemberRole(ctx, e, s.acc.ID(), s.viewer, "editor")
	wantErr(t, err, domain.ErrForbidden)

	// saldo owner konsisten: 100000 - 1000 - 500 (editor create+delete = net 0)
	if got := f.balance(t, s.acc.ID()); got != 98500 {
		t.Fatalf("balance = %d", got)
	}

	// audit: actor editor tercatat dengan user_id owner
	var byEditor int
	for _, a := range f.db.p2.audit {
		if a.ActorID == e && a.Entity != EntityAccount {
			if a.UserID != f.user {
				t.Fatalf("audit user_id harus owner")
			}
			byEditor++
		}
	}
	if byEditor != 6 {
		t.Fatalf("audit entries by editor = %d, want 6", byEditor)
	}

	// shared listing
	sh, err := f.svc.ListSharedAccounts(ctx, e)
	if err != nil || len(sh) != 2 {
		t.Fatalf("shared = %d, %v", len(sh), err)
	}
}

func TestMemberManagement(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := newShared(t, f)

	_, err := f.svc.AddMember(ctx, AddMemberInput{OwnerID: f.user, AccountID: s.acc.ID(), MemberID: &s.editor, Role: "viewer"})
	wantErr(t, err, domain.ErrMemberExists)
	_, err = f.svc.AddMember(ctx, AddMemberInput{OwnerID: f.user, AccountID: s.acc.ID(), MemberID: &f.user, Role: "viewer"})
	wantErr(t, err, &domain.ValidationError{Field: "member_id"})
	_, err = f.svc.AddMember(ctx, AddMemberInput{OwnerID: f.user, AccountID: s.acc.ID(), MemberID: &s.outsider, Role: "owner"})
	wantErr(t, err, domain.ErrInvalidRole)
	_, err = f.svc.AddMember(ctx, AddMemberInput{OwnerID: f.user, AccountID: s.acc.ID(), Email: "nobody@x.io", Role: "viewer"})
	wantErr(t, err, domain.ErrUserNotFound)
	_, err = f.svc.AddMember(ctx, AddMemberInput{OwnerID: f.user, AccountID: s.acc.ID(), Email: "boom@x.io", Role: "viewer"})
	wantErr(t, err, errBoom)
	_, err = f.svc.AddMember(ctx, AddMemberInput{OwnerID: f.user, AccountID: s.acc.ID(), Role: "viewer"})
	wantErr(t, err, &domain.ValidationError{Field: "member_id"})
	_, err = f.svc.AddMember(ctx, AddMemberInput{OwnerID: f.user, AccountID: s.acc.ID(), MemberID: &s.outsider, Email: "a@b.c", Role: "viewer"})
	wantErr(t, err, &domain.ValidationError{Field: "member_id"})
	f.db.failOn["members.add"] = errBoom
	_, err = f.svc.AddMember(ctx, AddMemberInput{OwnerID: f.user, AccountID: s.acc.ID(), MemberID: &s.outsider, Role: "viewer"})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "members.add")

	m, err := f.svc.UpdateMemberRole(ctx, f.user, s.acc.ID(), s.viewer, "editor")
	if err != nil || m.Role != domain.RoleEditor {
		t.Fatalf("update role: %v", err)
	}
	if _, err := f.svc.UpdateMemberRole(ctx, f.user, s.acc.ID(), s.viewer, "editor"); err != nil {
		t.Fatalf("idempotent update: %v", err)
	}
	_, err = f.svc.UpdateMemberRole(ctx, f.user, s.acc.ID(), s.outsider, "editor")
	wantErr(t, err, domain.ErrMemberNotFound)
	_, err = f.svc.UpdateMemberRole(ctx, f.user, s.acc.ID(), s.viewer, "boss")
	wantErr(t, err, domain.ErrInvalidRole)
	f.db.failOn["members.update"] = errBoom
	_, err = f.svc.UpdateMemberRole(ctx, f.user, s.acc.ID(), s.viewer, "viewer")
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "members.update")

	// member keluar sendiri
	if err := f.svc.RemoveMember(ctx, s.viewer, s.acc.ID(), s.viewer); err != nil {
		t.Fatalf("leave: %v", err)
	}
	_, err = f.svc.GetAccount(ctx, s.viewer, s.acc.ID())
	wantErr(t, err, domain.ErrAccountNotFound)
	// owner mencabut editor
	if err := f.svc.RemoveMember(ctx, f.user, s.acc.ID(), s.editor); err != nil {
		t.Fatalf("remove: %v", err)
	}
	wantErr(t, f.svc.RemoveMember(ctx, f.user, s.acc.ID(), s.editor), domain.ErrMemberNotFound)
	_, err = f.svc.GetTransaction(ctx, s.editor, s.tx.ID())
	wantErr(t, err, domain.ErrTransactionNotFound)
	f.db.failOn["members.remove"] = errBoom
	wantErr(t, f.svc.RemoveMember(ctx, s.editor, s.acc2.ID(), s.editor), errBoom)
	delete(f.db.failOn, "members.remove")
	f.db.failOn["members.get"] = errBoom
	_, err = f.svc.GetAccount(ctx, s.editor, s.acc.ID())
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "members.get")

	// audit gagal -> seluruh operasi rollback
	f.db.failOn["audit.create"] = errBoom
	_, err = f.svc.AddMember(ctx, AddMemberInput{OwnerID: f.user, AccountID: s.acc.ID(), MemberID: &s.outsider, Role: "viewer"})
	wantErr(t, err, errBoom)
	delete(f.db.failOn, "audit.create")
	if ms, _ := f.svc.ListMembers(ctx, f.user, s.acc.ID()); len(ms) != 0 {
		t.Fatalf("rollback gagal: %d members", len(ms))
	}
	logs, err := f.svc.ListAuditLogs(ctx, f.user, domain.AuditFilter{Entity: EntityMember})
	if err != nil || len(logs) == 0 {
		t.Fatalf("audit logs = %d, %v", len(logs), err)
	}
}

func TestFeaturesDisabled(t *testing.T) {
	ctx := context.Background()
	svc := NewService(Deps{})
	id := uuid.New()
	_, err := svc.AddMember(ctx, AddMemberInput{})
	wantErr(t, err, &domain.ValidationError{Field: "account_id"})
	_, err = svc.ListMembers(ctx, id, id)
	wantErr(t, err, &domain.ValidationError{Field: "account_id"})
	_, err = svc.UpdateMemberRole(ctx, id, id, id, "viewer")
	wantErr(t, err, &domain.ValidationError{Field: "account_id"})
	wantErr(t, svc.RemoveMember(ctx, id, id, id), &domain.ValidationError{Field: "account_id"})
	if sh, err := svc.ListSharedAccounts(ctx, id); err != nil || len(sh) != 0 {
		t.Fatal("shared")
	}
	if l, err := svc.ListAuditLogs(ctx, id, domain.AuditFilter{}); err != nil || len(l) != 0 {
		t.Fatal("audit")
	}
	_, err = svc.CreateRate(ctx, CreateRateInput{})
	wantErr(t, err, &domain.ValidationError{Field: "rate"})
	_, err = svc.GetRate(ctx, id, id)
	wantErr(t, err, domain.ErrRateNotFound)
	_, err = svc.UpdateRate(ctx, id, id, "1")
	wantErr(t, err, domain.ErrRateNotFound)
	wantErr(t, svc.DeleteRate(ctx, id, id), domain.ErrRateNotFound)
	if l, err := svc.ListRates(ctx, id); err != nil || len(l) != 0 {
		t.Fatal("rates")
	}
	_, err = svc.CreateGoal(ctx, CreateGoalInput{})
	wantErr(t, err, &domain.ValidationError{Field: "goal"})
	_, err = svc.GetGoal(ctx, id, id)
	wantErr(t, err, domain.ErrGoalNotFound)
	_, err = svc.UpdateGoal(ctx, UpdateGoalInput{})
	wantErr(t, err, domain.ErrGoalNotFound)
	wantErr(t, svc.DeleteGoal(ctx, id, id), domain.ErrGoalNotFound)
	_, _, err = svc.Contribute(ctx, ContributeInput{})
	wantErr(t, err, domain.ErrGoalNotFound)
	_, err = svc.ListContributions(ctx, id, id)
	wantErr(t, err, domain.ErrGoalNotFound)
	wantErr(t, svc.DeleteContribution(ctx, id, id, id), domain.ErrGoalNotFound)
	if l, err := svc.ListGoals(ctx, id); err != nil || len(l) != 0 {
		t.Fatal("goals")
	}
	_, err = svc.CreateDebt(ctx, CreateDebtInput{})
	wantErr(t, err, &domain.ValidationError{Field: "debt"})
	_, err = svc.GetDebt(ctx, id, id)
	wantErr(t, err, domain.ErrDebtNotFound)
	_, err = svc.UpdateDebt(ctx, UpdateDebtInput{})
	wantErr(t, err, domain.ErrDebtNotFound)
	wantErr(t, svc.DeleteDebt(ctx, id, id), domain.ErrDebtNotFound)
	_, _, err = svc.PayDebt(ctx, PayDebtInput{})
	wantErr(t, err, domain.ErrDebtNotFound)
	_, err = svc.ListDebtPayments(ctx, id, id)
	wantErr(t, err, domain.ErrDebtNotFound)
	wantErr(t, svc.DeleteDebtPayment(ctx, id, id, id), domain.ErrDebtNotFound)
	if l, err := svc.ListDebts(ctx, id, ""); err != nil || len(l) != 0 {
		t.Fatal("debts")
	}
	_, err = svc.CreateBill(ctx, CreateBillInput{})
	wantErr(t, err, &domain.ValidationError{Field: "bill"})
	_, err = svc.GetBill(ctx, id, id)
	wantErr(t, err, domain.ErrBillNotFound)
	_, err = svc.UpdateBill(ctx, UpdateBillInput{})
	wantErr(t, err, domain.ErrBillNotFound)
	wantErr(t, svc.DeleteBill(ctx, id, id), domain.ErrBillNotFound)
	_, _, err = svc.PayBill(ctx, PayBillInput{})
	wantErr(t, err, domain.ErrBillNotFound)
	if l, err := svc.ListBills(ctx, id); err != nil || len(l) != 0 {
		t.Fatal("bills")
	}
	if n, err := svc.ProcessBills(ctx, testNow, 0); err != nil || n != 0 {
		t.Fatal("process")
	}
}
