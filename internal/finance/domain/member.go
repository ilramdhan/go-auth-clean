package domain

import (
	"time"

	"github.com/google/uuid"
)

// MemberRole adalah hak akses anggota shared wallet. Owner = pemilik akun
// (tidak disimpan sebagai member).
type MemberRole string

const (
	RoleOwner  MemberRole = "owner"
	RoleEditor MemberRole = "editor"
	RoleViewer MemberRole = "viewer"
)

// ParseMemberRole hanya menerima role yang bisa diberikan (editor/viewer).
func ParseMemberRole(s string) (MemberRole, error) {
	switch r := MemberRole(s); r {
	case RoleEditor, RoleViewer:
		return r, nil
	default:
		return "", ErrInvalidRole
	}
}

// CanRead: semua role boleh membaca.
func (r MemberRole) CanRead() bool { return r == RoleOwner || r == RoleEditor || r == RoleViewer }

// CanWrite: owner & editor boleh mencatat transaksi.
func (r MemberRole) CanWrite() bool { return r == RoleOwner || r == RoleEditor }

// CanManage: hanya owner yang boleh mengatur member.
func (r MemberRole) CanManage() bool { return r == RoleOwner }

// AccountMember adalah akses user lain ke akun milik owner.
type AccountMember struct {
	AccountID uuid.UUID
	OwnerID   uuid.UUID
	MemberID  uuid.UUID
	Role      MemberRole
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewAccountMember memastikan hanya owner yang membagikan dan tidak ke diri sendiri.
func NewAccountMember(acc *Account, ownerID, memberID uuid.UUID, role string, now time.Time) (*AccountMember, error) {
	if acc == nil || acc.UserID() != ownerID {
		return nil, ErrAccountNotFound
	}
	if memberID == uuid.Nil || memberID == ownerID {
		return nil, &ValidationError{Field: "member_id", Reason: "must be another user"}
	}
	r, err := ParseMemberRole(role)
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	return &AccountMember{AccountID: acc.ID(), OwnerID: ownerID, MemberID: memberID, Role: r, CreatedAt: now, UpdatedAt: now}, nil
}

// SharedAccount adalah akun milik user lain yang bisa diakses user ini.
type SharedAccount struct {
	Account *Account
	Role    MemberRole
}
