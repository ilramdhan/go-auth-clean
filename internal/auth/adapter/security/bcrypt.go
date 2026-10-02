// Package security berisi implementasi port kriptografi (hashing & token).
package security

import "golang.org/x/crypto/bcrypt"

type BcryptHasher struct {
	cost      int
	dummyHash []byte
}

func NewBcryptHasher(cost int) (*BcryptHasher, error) {
	// dummyHash dipakai saat user tidak ditemukan agar waktu compare tetap sama.
	dummy, err := bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing"), cost)
	if err != nil {
		return nil, err
	}
	return &BcryptHasher{cost: cost, dummyHash: dummy}, nil
}

func (h *BcryptHasher) Hash(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	return string(b), err
}

func (h *BcryptHasher) Compare(hash, password string) error {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(h.dummyHash, []byte(password))
		return bcrypt.ErrMismatchedHashAndPassword
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
