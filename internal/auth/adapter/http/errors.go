package http

import (
	"errors"
	"net/http"

	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/httpx"
	"go-auth-clean/internal/shared/pagination"
)

// mapError menerjemahkan error domain ke HTTP. Error tak dikenal diteruskan
// apa adanya (httpx.WriteError menjadikannya 500 tanpa membocorkan detail).
func mapError(err error) error {
	switch {
	case errors.Is(err, domain.ErrEmailTaken):
		return &httpx.Error{Status: http.StatusConflict, Code: "EMAIL_TAKEN", Message: "email sudah terdaftar"}
	case errors.Is(err, domain.ErrInvalidEmail):
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "INVALID_EMAIL", Message: "format email tidak valid"}
	case errors.Is(err, domain.ErrWeakPassword):
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "WEAK_PASSWORD", Message: err.Error()}
	case errors.Is(err, domain.ErrPasswordReused):
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "PASSWORD_REUSED", Message: "password baru harus berbeda dari password lama"}
	case errors.Is(err, domain.ErrInvalidFullName):
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "INVALID_FULL_NAME", Message: "nama lengkap tidak valid (1-100 karakter)"}
	case errors.Is(err, domain.ErrNoFieldsToUpdate):
		return &httpx.Error{Status: http.StatusBadRequest, Code: "NO_FIELDS_TO_UPDATE", Message: "tidak ada field yang diubah"}
	case errors.Is(err, domain.ErrInvalidCredentials):
		return &httpx.Error{Status: http.StatusUnauthorized, Code: "INVALID_CREDENTIALS", Message: "email atau password salah"}
	case errors.Is(err, domain.ErrAccountLocked):
		return &httpx.Error{Status: http.StatusTooManyRequests, Code: "ACCOUNT_LOCKED", Message: "akun terkunci sementara, coba lagi nanti"}
	case errors.Is(err, domain.ErrEmailNotVerified):
		return &httpx.Error{Status: http.StatusForbidden, Code: "EMAIL_NOT_VERIFIED", Message: "email belum diverifikasi"}
	case errors.Is(err, domain.ErrAccountSuspended):
		return &httpx.Error{Status: http.StatusForbidden, Code: "ACCOUNT_SUSPENDED", Message: "akun ditangguhkan"}
	case errors.Is(err, domain.ErrAccountInactive):
		return &httpx.Error{Status: http.StatusForbidden, Code: "ACCOUNT_INACTIVE", Message: "akun tidak aktif"}
	case errors.Is(err, domain.ErrInvalidOTP), errors.Is(err, domain.ErrOTPNotFound):
		return &httpx.Error{Status: http.StatusBadRequest, Code: "INVALID_OTP", Message: "kode tidak valid"}
	case errors.Is(err, domain.ErrOTPExpired):
		return &httpx.Error{Status: http.StatusBadRequest, Code: "OTP_EXPIRED", Message: "kode sudah kedaluwarsa, minta kode baru"}
	case errors.Is(err, domain.ErrOTPTooManyAttempts):
		return &httpx.Error{Status: http.StatusTooManyRequests, Code: "OTP_TOO_MANY_ATTEMPTS", Message: "terlalu banyak percobaan, minta kode baru"}
	case errors.Is(err, domain.ErrTokenReused):
		return &httpx.Error{Status: http.StatusUnauthorized, Code: "TOKEN_REUSED", Message: "refresh token sudah dipakai; semua sesi perangkat ini dicabut"}
	case errors.Is(err, domain.ErrSessionInvalid):
		return &httpx.Error{Status: http.StatusUnauthorized, Code: "SESSION_INVALID", Message: "sesi tidak valid atau telah berakhir"}
	case errors.Is(err, domain.ErrSessionNotFound):
		return &httpx.Error{Status: http.StatusNotFound, Code: "SESSION_NOT_FOUND", Message: "sesi tidak ditemukan"}
	case errors.Is(err, domain.ErrUserNotFound):
		return &httpx.Error{Status: http.StatusNotFound, Code: "USER_NOT_FOUND", Message: "user tidak ditemukan"}
	case errors.Is(err, pagination.ErrInvalidCursor):
		return &httpx.Error{Status: http.StatusBadRequest, Code: "INVALID_CURSOR", Message: "cursor tidak valid"}
	default:
		return mapP2Error(err)
	}
}

// mapP2Error memetakan error fitur P2 (2FA, RBAC, OAuth, API key).
func mapP2Error(err error) error {
	switch {
	case errors.Is(err, domain.ErrMFANotEnabled):
		return &httpx.Error{Status: http.StatusConflict, Code: "MFA_NOT_ENABLED", Message: "2FA belum aktif"}
	case errors.Is(err, domain.ErrMFAAlreadyEnabled):
		return &httpx.Error{Status: http.StatusConflict, Code: "MFA_ALREADY_ENABLED", Message: "2FA sudah aktif"}
	case errors.Is(err, domain.ErrMFASetupRequired):
		return &httpx.Error{Status: http.StatusConflict, Code: "MFA_SETUP_REQUIRED", Message: "jalankan setup 2FA terlebih dahulu"}
	case errors.Is(err, domain.ErrInvalidMFACode), errors.Is(err, domain.ErrMFACodeReplayed):
		// Replay sengaja tidak dibedakan dari kode salah (tidak membocorkan info).
		return &httpx.Error{Status: http.StatusUnauthorized, Code: "INVALID_MFA_CODE", Message: "kode 2FA tidak valid"}
	case errors.Is(err, domain.ErrMFATokenInvalid):
		return &httpx.Error{Status: http.StatusUnauthorized, Code: "MFA_TOKEN_INVALID", Message: "mfa_token tidak valid atau kedaluwarsa, login ulang"}
	case errors.Is(err, domain.ErrForbidden):
		return errForbidden
	case errors.Is(err, domain.ErrInvalidRole):
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "INVALID_ROLE", Message: "role tidak valid"}
	case errors.Is(err, domain.ErrCannotModifySelf):
		return &httpx.Error{Status: http.StatusConflict, Code: "CANNOT_MODIFY_SELF", Message: "aksi ini tidak bisa dilakukan pada akun sendiri"}
	case errors.Is(err, domain.ErrInvalidTransition):
		return &httpx.Error{Status: http.StatusConflict, Code: "INVALID_STATUS_TRANSITION", Message: "status user tidak bisa diubah dari status saat ini"}
	case errors.Is(err, domain.ErrInvalidStatus):
		return &httpx.Error{Status: http.StatusBadRequest, Code: "INVALID_PARAMETER", Message: "status tidak valid",
			Details: []httpx.FieldDetail{{Field: "status", Message: "status tidak valid"}}}
	case errors.Is(err, domain.ErrOAuthDisabled):
		return httpx.ErrNotFound
	case errors.Is(err, domain.ErrOAuthEmailNotVerif):
		return &httpx.Error{Status: http.StatusForbidden, Code: "OAUTH_EMAIL_NOT_VERIFIED", Message: "email belum diverifikasi oleh provider"}
	case errors.Is(err, domain.ErrOAuthAccountExists):
		return &httpx.Error{Status: http.StatusConflict, Code: "OAUTH_ACCOUNT_EXISTS", Message: "email sudah terdaftar; login dengan password lalu hubungkan akun"}
	case errors.Is(err, domain.ErrIdentityTaken):
		return &httpx.Error{Status: http.StatusConflict, Code: "IDENTITY_TAKEN", Message: "akun eksternal sudah terhubung ke user lain"}
	case errors.Is(err, domain.ErrOAuthFailed):
		return &httpx.Error{Status: http.StatusBadRequest, Code: "OAUTH_FAILED", Message: "autentikasi OAuth gagal"}
	case errors.Is(err, domain.ErrLoginCodeInvalid):
		return &httpx.Error{Status: http.StatusBadRequest, Code: "LOGIN_CODE_INVALID", Message: "kode login tidak valid atau kedaluwarsa"}
	case errors.Is(err, domain.ErrAPIKeyNotFound):
		return &httpx.Error{Status: http.StatusNotFound, Code: "API_KEY_NOT_FOUND", Message: "API key tidak ditemukan"}
	case errors.Is(err, domain.ErrAPIKeyInvalid):
		return errAPIKeyInvalid
	case errors.Is(err, domain.ErrAPIKeyLimit):
		return &httpx.Error{Status: http.StatusConflict, Code: "API_KEY_LIMIT", Message: "jumlah API key aktif sudah maksimal"}
	case errors.Is(err, domain.ErrInvalidAPIKeyName):
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "INVALID_API_KEY_NAME", Message: "nama API key tidak valid (1-100 karakter)"}
	case errors.Is(err, domain.ErrInvalidScope):
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "INVALID_SCOPE", Message: "scope tidak valid (read/write)"}
	case errors.Is(err, domain.ErrInvalidExpiry):
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "INVALID_EXPIRY", Message: "masa berlaku API key tidak valid"}
	default:
		return err
	}
}

var (
	errUnauthenticated = &httpx.Error{Status: http.StatusUnauthorized, Code: "UNAUTHENTICATED", Message: "token tidak valid atau tidak ada"}
	// errSessionRevoked: access token masih valid secara kriptografi tetapi sesinya
	// sudah dicabut (logout / revoke / reset password).
	errSessionRevoked = &httpx.Error{Status: http.StatusUnauthorized, Code: "SESSION_INVALID", Message: "sesi tidak valid atau telah berakhir"}
)

func invalidParam(field, msg string) error {
	return &httpx.Error{Status: http.StatusBadRequest, Code: "INVALID_PARAMETER", Message: msg,
		Details: []httpx.FieldDetail{{Field: field, Message: msg}}}
}
