package domain

import (
	"github.com/google/uuid"

	"go-auth-clean/internal/shared/money"
)

// BalanceChanges mengumpulkan perubahan saldo per akun lalu menerapkannya
// sekaligus. Saat edit (revert lama + apply baru) yang diperiksa hanya saldo
// akhir, bukan state antara yang bisa negatif sementara.
type BalanceChanges struct {
	deltas map[uuid.UUID]money.Money
	order  []uuid.UUID
}

// NewBalanceChanges membuat kumpulan perubahan kosong.
func NewBalanceChanges() *BalanceChanges {
	return &BalanceChanges{deltas: map[uuid.UUID]money.Money{}}
}

func (b *BalanceChanges) add(accountID uuid.UUID, delta money.Money) error {
	cur, ok := b.deltas[accountID]
	if !ok {
		b.deltas[accountID] = delta
		b.order = append(b.order, accountID)
		return nil
	}
	sum, err := cur.Add(delta)
	if err != nil {
		return err
	}
	b.deltas[accountID] = sum
	return nil
}

// AddTransaction mencatat efek transaksi baru.
func (b *BalanceChanges) AddTransaction(t *Transaction) error {
	d, err := t.SignedAmount()
	if err != nil {
		return err
	}
	return b.add(t.AccountID(), d)
}

// RevertTransaction mencatat pembatalan efek transaksi.
func (b *BalanceChanges) RevertTransaction(t *Transaction) error {
	d, err := t.SignedAmount()
	if err != nil {
		return err
	}
	n, err := d.Neg()
	if err != nil {
		return err
	}
	return b.add(t.AccountID(), n)
}

// AddTransfer mencatat dua leg transfer (biaya dicatat lewat transaksi fee).
func (b *BalanceChanges) AddTransfer(t *Transfer) error {
	neg, err := t.Amount().Neg()
	if err != nil {
		return err
	}
	if err := b.add(t.FromAccountID(), neg); err != nil {
		return err
	}
	return b.add(t.ToAccountID(), t.ToAmount())
}

// RevertTransfer mencatat pembatalan dua leg transfer.
func (b *BalanceChanges) RevertTransfer(t *Transfer) error {
	neg, err := t.ToAmount().Neg()
	if err != nil {
		return err
	}
	if err := b.add(t.ToAccountID(), neg); err != nil {
		return err
	}
	return b.add(t.FromAccountID(), t.Amount())
}

// Delta mengembalikan perubahan bersih untuk satu akun.
func (b *BalanceChanges) Delta(accountID uuid.UUID) (money.Money, bool) {
	d, ok := b.deltas[accountID]
	return d, ok
}

// ApplyTo menerapkan perubahan bersih ke akun-akun (harus sudah dikunci).
// Mengembalikan akun yang saldonya benar-benar berubah (perlu disimpan).
func (b *BalanceChanges) ApplyTo(accounts map[uuid.UUID]*Account) ([]*Account, error) {
	changed := make([]*Account, 0, len(b.order))
	for _, id := range b.order {
		delta := b.deltas[id]
		if delta.IsZero() {
			continue
		}
		acc, ok := accounts[id]
		if !ok {
			return nil, ErrAccountNotFound
		}
		if err := acc.adjust(delta); err != nil {
			return nil, err
		}
		changed = append(changed, acc)
	}
	return changed, nil
}
