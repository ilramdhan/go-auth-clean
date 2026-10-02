package main

import (
	"context"
	"errors"

	"github.com/google/uuid"

	authdomain "go-auth-clean/internal/auth/domain"
	finapp "go-auth-clean/internal/finance/app"
)

// userDirectory menjembatani finance -> auth di composition root, supaya
// finance tidak meng-import package auth (bounded context tetap terpisah).
type userDirectory struct {
	users userByEmailFinder
}

// userByEmailFinder adalah port minimal yang dibutuhkan userDirectory
// (dipenuhi *authpg.UserRepository; interface supaya bisa dites tanpa DB).
type userByEmailFinder interface {
	FindByEmail(ctx context.Context, email string) (*authdomain.User, error)
}

var _ finapp.UserDirectory = userDirectory{}

func (d userDirectory) LookupByEmail(ctx context.Context, email string) (uuid.UUID, error) {
	u, err := d.users.FindByEmail(ctx, email)
	if errors.Is(err, authdomain.ErrUserNotFound) {
		return uuid.Nil, finapp.ErrUserNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	return u.ID, nil
}
