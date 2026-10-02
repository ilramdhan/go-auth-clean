# PRD-01 — Auth Service (`go-auth-clean`)

> **Status:** Draft v1.0 · **Tanggal:** 2026-10-01 · **Owner:** Ilham Ramadhan
> **Tipe dokumen:** Product Requirements Document + Learning Guide
> **Target pembaca:** Go developer pemula → menuju expert (Clean Architecture, DDD, gRPC, observability)
> **Dokumen terkait:** `prd-02-finance.md` (modul personal finance — mengikuti *Shared Conventions* yang sama)

---

## Daftar Isi

0. [Shared Conventions (wajib dipatuhi)](#0-shared-conventions-wajib-dipatuhi)
1. [Tujuan, Non-Goals, Glossary](#1-tujuan-non-goals-glossary)
2. [Daftar Fitur & Spesifikasi](#2-daftar-fitur--spesifikasi)
3. [Data Model / ERD](#3-data-model--erd)
4. [API Contract (REST) + Mapping gRPC](#4-api-contract-rest--mapping-grpc)
5. [Token Design](#5-token-design)
6. [Security Checklist](#6-security-checklist-owasp-asvs-inspired)
7. [Observability](#7-observability)
8. [Testing Strategy](#8-testing-strategy)
9. [Milestones Implementasi (Sprint 0..N)](#9-milestones-implementasi-sprint-0n)
10. [Hal Penting yang Sering Dilupakan Pemula Go](#10-hal-penting-yang-sering-dilupakan-pemula-go)
11. [Tooling](#11-tooling)
12. [Lampiran: Perbaikan dari Kode Lama](#12-lampiran-perbaikan-dari-kode-lama)

---

## 0. Shared Conventions (wajib dipatuhi)

Konvensi ini **identik** dengan PRD Finance. Jika ada konflik, bagian ini yang menang.

### 0.1 Struktur repository (modular monolith, package-by-bounded-context)

```
cmd/api/main.go                  # composition root saja: load config, build deps, start server, graceful shutdown
internal/
  platform/                      # cross-cutting infra, TANPA business logic
    config/  logger/ (log/slog)  database/ (pgxpool + tx manager)  httpserver/
    middleware/ (request_id, recover, access log, cors, rate limit, auth)
    validator/  clock/  idgen/
  auth/
    domain/             # entity, value object (Email, Password policy), domain error, repository INTERFACE — tidak import infra/http
    app/                # use case, input/output struct (command/result), port: PasswordHasher, TokenIssuer, Mailer, Clock
    adapter/postgres/   # implementasi repository
    adapter/http/       # handler + DTO (request/response) + error mapping; nanti adapter/grpc/ di sebelahnya
    adapter/security/   # argon2id/bcrypt hasher, JWT issuer
  finance/              # bentuk sama
  shared/               # shared kernel kecil saja: Money, Pagination, typed ID
migrations/             # golang-migrate, nomor sekuensial
api/openapi.yaml        # sekarang  -> api/proto/ (nanti, contract-first)
pkg/                    # hanya kode reusable & project-agnostic (lebih baik kosong)
```

### 0.2 Aturan dependency (The Dependency Rule)

```mermaid
flowchart LR
    main[cmd/api/main.go] --> httpad[auth/adapter/http]
    main --> pgad[auth/adapter/postgres]
    main --> secad[auth/adapter/security]
    main --> platform[internal/platform/*]
    httpad --> app[auth/app]
    grpcad[auth/adapter/grpc - nanti] -.-> app
    pgad --> domain[auth/domain]
    secad --> app
    app --> domain
    httpad --> domain
    style domain fill:#e8f5e9
    style app fill:#e3f2fd
```

- `domain` **tidak boleh** import `app`, `adapter`, `platform`, `net/http`, `pgx`.
- `app` hanya import `domain` (+ `shared`, stdlib). Port (interface) dideklarasikan di `app` (consumer).
- `adapter/*` boleh import `app` + `domain` + `platform`.
- `cmd/api/main.go` adalah satu-satunya tempat yang "tahu semuanya" (wiring manual, tanpa DI framework).

### 0.3 Library

| Kebutuhan | Pilihan | Catatan |
|---|---|---|
| HTTP router | stdlib `net/http` `ServeMux` (pattern `"POST /api/v1/auth/login"`, Go 1.22+) | tanpa framework |
| DB | `github.com/jackc/pgx/v5` + `pgxpool` | raw SQL; `sqlc` opsional nanti |
| Logging | `log/slog` (JSON handler) | selalu sertakan `request_id` |
| Validasi | `github.com/go-playground/validator/v10` | **hanya** di adapter http |
| JWT | `github.com/golang-jwt/jwt/v5` | typed `RegisteredClaims` |
| Password hash | `golang.org/x/crypto/argon2` (argon2id) | bcrypt boleh |
| ID | `github.com/google/uuid` v7 | digenerate di app via port `IDGenerator` |
| Migration | `golang-migrate/migrate` | sequential |
| Test | stdlib `testing` + `testify` + `testcontainers-go` | |
| Lint | `golangci-lint` v2 | |

### 0.4 Konvensi error & response

- Domain mendefinisikan sentinel/typed error **tanpa HTTP code**: `ErrEmailTaken`, `ErrInvalidCredentials`, dll.
- Adapter http memetakan ke status code; nanti adapter grpc memetakan ke `codes.*`.
- Error response:

```json
{
  "error": {
    "code": "EMAIL_TAKEN",
    "message": "Email sudah terdaftar",
    "details": [{"field": "email", "message": "sudah digunakan"}]
  },
  "request_id": "01929c3e-7b5a-7cc1-9a7e-2f1d3c4b5a6d"
}
```

- Sukses: `{"data": ...}`; list: `{"data": [...], "meta": {"page":1,"page_size":20,"total":57}}`.
- Prefix API: `/api/v1`. Timestamp: `TIMESTAMPTZ`, selalu UTC, format JSON RFC 3339 (`2026-10-01T08:00:00Z`).
- `context.Context` selalu parameter pertama. Constructor return concrete struct (*accept interfaces, return structs*). Interface dideklarasikan oleh consumer.
- Config dari env (12-factor). `.env` **tidak** di-commit, `.env.example` di-commit.
- Graceful shutdown; `/healthz` (liveness, tanpa dependency) dan `/readyz` (ping DB).

---

## 1. Tujuan, Non-Goals, Glossary

### 1.1 Tujuan

1. **Produk:** menyediakan layanan autentikasi yang aman untuk aplikasi personal finance: registrasi, verifikasi email, login, manajemen sesi multi-device, reset password, dan audit keamanan.
2. **Arsitektur:** core logic (domain + app) **tidak berubah** ketika delivery layer ditambah gRPC. Bukti: test unit di `auth/app` tidak import `net/http`, dan `adapter/grpc` bisa ditambahkan tanpa mengedit satu baris pun di `domain`/`app`.
3. **Pembelajaran:** setiap sprint mengajarkan konsep Go spesifik (interface, context, errors, goroutine, generics, transaksi, middleware) dengan *Definition of Done* yang bisa diverifikasi.
4. **Kualitas production-grade:** structured logging, rate limiting, graceful shutdown, migrasi reversible, integration test dengan Postgres asli, CI hijau.

### 1.2 Non-Goals (v1)

- Bukan Identity Provider penuh (tidak ada OIDC provider, tidak menerbitkan token untuk aplikasi pihak ketiga).
- Tidak ada UI/front-end (hanya API).
- Tidak ada microservices — satu binary (modular monolith). Pemisahan service adalah opsi masa depan.
- Tidak ada multi-tenant / organisasi.
- SMS OTP tidak di-scope (hanya email).
- 2FA TOTP, OAuth Google, RBAC lanjutan, API keys → **P2** (didesain agar mudah ditambah, tidak dikerjakan di MVP).

### 1.3 Glossary

| Istilah | Arti |
|---|---|
| **Bounded Context** | Batas model domain (DDD). Di sini: `auth`, `finance`. Masing-masing punya bahasa & model sendiri. |
| **Entity** | Objek dengan identitas (ID) yang bertahan lintas perubahan, mis. `User`, `Session`. |
| **Value Object** | Objek tanpa identitas, immutable, divalidasi saat dibuat, mis. `Email`, `HashedPassword`. |
| **Use case / Application Service** | Orkestrasi satu aksi bisnis (`Register`, `Login`). Ada di `app/`. |
| **Port** | Interface yang dibutuhkan use case dari dunia luar (`PasswordHasher`, `Mailer`). |
| **Adapter** | Implementasi port atau delivery (`adapter/postgres`, `adapter/http`). |
| **Composition root** | Tempat merakit semua dependency (`cmd/api/main.go`). |
| **Access token** | JWT berumur pendek (15 menit) untuk otorisasi request API. Stateless. |
| **Refresh token** | String opaque acak 32 byte, berumur panjang, untuk mendapatkan access token baru. Disimpan di DB sebagai **hash SHA-256**. |
| **Session** | Satu login di satu device. Direpresentasikan oleh baris di tabel `sessions`; claim `sid` di JWT menunjuk ke sini. |
| **Token family** | Rantai refresh token hasil rotasi dari satu login. Disimpan sebagai `family_id`. Bila token lama dipakai ulang → seluruh family dicabut. |
| **Rotation** | Setiap refresh menghasilkan refresh token baru dan menandai yang lama `used`. |
| **Reuse detection** | Mendeteksi refresh token yang sudah dipakai/dicabut dipresentasikan lagi → indikasi pencurian. |
| **OTP** | One-Time Password 6 digit untuk verifikasi email / reset password. Disimpan sebagai hash. |
| **Lockout** | Akun dikunci sementara setelah N kali gagal login. |
| **Audit log** | Catatan append-only event keamanan (login sukses/gagal, ganti password, dll). |
| **Soft delete** | Menandai baris terhapus (`deleted_at`) tanpa menghapus fisik. |
| **request_id** | ID unik per HTTP request, dipropagasi via `context`, muncul di log & response. |
| **Sentinel error** | Variabel error global yang dibandingkan dengan `errors.Is`, mis. `var ErrNotFound = errors.New("not found")`. |
| **Typed error** | Struct yang implement `error`, diekstrak dengan `errors.As`. |

---

## 2. Daftar Fitur & Spesifikasi

### 2.1 Ringkasan prioritas

| # | Fitur | Prioritas | Endpoint utama |
|---|---|---|---|
| F01 | Register | MVP | `POST /api/v1/auth/register` |
| F02 | Email verification (OTP 6 digit) | MVP | `POST /api/v1/auth/verify-email` |
| F03 | Resend verification | MVP | `POST /api/v1/auth/verify-email/resend` |
| F04 | Login | MVP | `POST /api/v1/auth/login` |
| F05 | Refresh token (rotation + reuse detection) | MVP | `POST /api/v1/auth/refresh` |
| F06 | Logout (sesi saat ini) | MVP | `POST /api/v1/auth/logout` |
| F07 | Get profile (me) | MVP | `GET /api/v1/me` |
| F08 | Account lockout & IP rate limit | MVP | (cross-cutting) |
| F09 | Logout all devices | P1 | `POST /api/v1/auth/logout-all` |
| F10 | List active sessions / revoke satu sesi | P1 | `GET /api/v1/me/sessions`, `DELETE /api/v1/me/sessions/{id}` |
| F11 | Update profile | P1 | `PATCH /api/v1/me` |
| F12 | Change password (revoke sesi lain) | P1 | `POST /api/v1/me/password` |
| F13 | Forgot / reset password | P1 | `POST /api/v1/auth/password/forgot`, `POST /api/v1/auth/password/reset` |
| F14 | Audit log security events | P1 | `GET /api/v1/me/security-events` |
| F15 | Account deletion (soft delete) | P1 | `DELETE /api/v1/me` |
| F16 | 2FA TOTP | P2 | `/api/v1/me/2fa/*` |
| F17 | OAuth Google login | P2 | `/api/v1/auth/oauth/google/*` |
| F18 | RBAC roles (user/admin) | P2 | `/api/v1/admin/*` |
| F19 | API keys | P2 | `/api/v1/me/api-keys` |

> **Keputusan desain verifikasi email:** MVP memakai **OTP 6 digit** (lebih sederhana untuk API-only & mobile). Tabel `verification_tokens` dibuat generik (`purpose`, `channel`) sehingga *link token* (32 byte random, di URL) bisa ditambahkan tanpa migrasi baru — cukup isi `kind = 'link'`.

### 2.2 Status lifecycle user

```mermaid
stateDiagram-v2
    [*] --> pending_verification: Register
    pending_verification --> active: Verify email (OTP valid)
    active --> suspended: Admin suspend / fraud
    suspended --> active: Admin unsuspend
    active --> deleted: User hapus akun
    pending_verification --> deleted: Cleanup job (tidak verifikasi > 30 hari)
    suspended --> deleted: Admin
    deleted --> [*]
```

Catatan: **lockout karena brute-force bukan status**, melainkan kolom `locked_until` (sementara, otomatis lepas). Status `suspended` adalah tindakan administratif.

---

### F01 — Register (MVP)

**User story:** Sebagai calon pengguna, saya ingin mendaftar dengan email, nama, dan password agar bisa memakai aplikasi.

**Flow:**
1. Client `POST /api/v1/auth/register` `{email, password, full_name}`.
2. Middleware: request_id, body limit (1 MiB → untuk auth cukup 16 KiB), rate limit IP.
3. Handler decode JSON (`DisallowUnknownFields`), validasi struktural (validator v10: required, email format, panjang).
4. Handler memanggil `app.RegisterService.Register(ctx, RegisterCommand{...})`.
5. Use case: `domain.NewEmail(raw)` → normalisasi (trim, lowercase) & validasi.
6. Use case: `domain.ValidatePassword(raw)` → policy (lihat aturan).
7. `hasher.Hash(ctx, password)` (argon2id).
8. `idgen.New()` → UUIDv7, `clock.Now()` → UTC.
9. Dalam **satu transaksi** (`TxManager.WithinTx`):
   1. `users.Create(ctx, user)` — INSERT; jika Postgres error `23505` pada `users_email_key` → return `domain.ErrEmailTaken`.
   2. Invalidasi OTP lama purpose `email_verification` (tidak ada untuk user baru, tetap dipanggil untuk konsistensi).
   3. `otps.Create(ctx, otp)` dengan `code_hash`.
   4. `audit.Record(ctx, EventUserRegistered)`.
10. **Setelah commit**, kirim email OTP via `Mailer` (jangan kirim email di dalam transaksi — kalau tx rollback, email sudah terkirim).
11. Response `201 Created` `{data: {user: {...}, verification_required: true}}`. **Tidak** menerbitkan token (user harus verifikasi dulu).

**Business rules:**
- Email unik case-insensitive (`CITEXT` + UNIQUE). Disimpan juga dalam bentuk lowercase-trim.
- Password: min 12 karakter (rekomendasi NIST; min absolut 8), maks 128 karakter (argon2id) / 72 **byte** jika memakai bcrypt. Tidak ada aturan komposisi paksa (NIST 800-63B), tetapi tolak password yang sama dengan email dan (opsional) yang ada di breached list.
- `full_name` 1–100 karakter (rune, bukan byte), trim whitespace.
- Status awal: `pending_verification`.

**Edge cases:**
- Dua request register dengan email sama secara bersamaan → satu sukses, satu `409 EMAIL_TAKEN` (dijamin oleh unique index, bukan pre-check `SELECT`).
- Email terdaftar tapi `pending_verification` → tetap `409`. (Alternatif anti-enumeration: selalu `202` dan kirim email "Anda sudah punya akun" — dicatat sebagai opsi hardening, lihat security note.)
- Email milik akun `deleted` → karena soft delete, unique index harus **partial** `WHERE deleted_at IS NULL` sehingga email bisa dipakai ulang.
- Mailer gagal → registrasi tetap sukses; log `ERROR` dan user bisa `resend`. (Nanti: outbox pattern.)
- Unicode di email (IDN) → MVP: tolak non-ASCII local part; dokumentasikan.

**Security notes:**
- Register membuka celah *user enumeration* (409). Ini trade-off UX yang diterima di MVP; mitigasi dengan rate limit ketat (5/jam/IP).
- Jangan log password/OTP. Log email hanya dalam bentuk masked (`il***@gmail.com`).
- Hash password **sebelum** membuka transaksi (argon2id ~50-100 ms; jangan menahan koneksi DB selama hashing).

---

### F02 — Email Verification via OTP (MVP)

**User story:** Sebagai user baru, saya ingin memverifikasi email dengan kode 6 digit agar akun saya aktif.

**Flow:**
1. `POST /api/v1/auth/verify-email` `{email, code}`.
2. Use case cari user by email. Jika tidak ada → `ErrInvalidOTP` (generic, bukan "user not found").
3. Ambil OTP aktif: `purpose='email_verification' AND consumed_at IS NULL AND expires_at > now()` (maks satu, dijamin partial unique index).
4. Jika tidak ada / expired → `ErrInvalidOTP`.
5. Jika `attempts >= max_attempts` → `ErrOTPTooManyAttempts`.
6. **Atomic increment** `attempts = attempts + 1 RETURNING attempts` **sebelum** compare (supaya percobaan paralel tetap terhitung).
7. Hitung `sha256(code + pepper)` → `subtle.ConstantTimeCompare` dengan `code_hash`.
8. Jika tidak cocok → `ErrInvalidOTP` (audit `otp_failed`).
9. Jika cocok, dalam transaksi: set `consumed_at = now()`, `users.status='active'`, `email_verified_at=now()`, audit `email_verified`.
10. Response `200` `{data: {verified: true}}`. (Opsional: langsung login & terbitkan token — **tidak** di MVP, supaya flow login tunggal.)

**Business rules:**
- OTP: 6 digit numeric, `crypto/rand` (bukan `math/rand`), TTL 10 menit, `max_attempts = 5`.
- Hanya **satu OTP aktif per (user, purpose)**. Membuat OTP baru meng-*consume*/invalidate yang lama.
- Hash: `HMAC-SHA256(server_pepper, code)` — karena ruang OTP kecil (10^6), hash polos bisa di-brute-force offline jika DB bocor; pepper dari config mengurangi risiko itu.

**Edge cases:** user sudah `active` → `200` idempoten (`already_verified: true`) tanpa membocorkan apa pun yang sensitif; OTP benar tapi expired → `OTP_EXPIRED` boleh dibedakan (tidak membocorkan keberadaan akun karena hanya pemegang OTP yang tahu); request paralel dengan OTP benar → hanya satu yang berhasil `UPDATE ... WHERE consumed_at IS NULL` (cek `RowsAffected`).

**Security notes:** constant-time compare; rate limit per email + per IP; attempts tidak di-reset saat resend kecuali OTP baru dibuat (OTP baru = counter baru, tapi resend dibatasi cooldown & kuota harian).

---

### F03 — Resend Verification (MVP)

**User story:** Sebagai user yang belum menerima/kehilangan kode, saya ingin meminta kode baru.

**Flow:**
1. `POST /api/v1/auth/verify-email/resend` `{email}`.
2. Selalu response `202 Accepted` `{data: {message: "Jika email terdaftar dan belum terverifikasi, kode baru telah dikirim."}}` — **anti-enumeration**.
3. Di belakang: jika user ada & `pending_verification`:
   1. Cek cooldown: OTP terakhir `created_at > now() - 60s` → diam (tetap 202) atau `429 RESEND_COOLDOWN` dengan `Retry-After` (pilih salah satu; rekomendasi: 202 diam + rate limit IP).
   2. Cek kuota: maks 5 OTP / 24 jam / user.
   3. Transaksi: invalidate OTP aktif lama → buat OTP baru.
   4. Kirim email setelah commit.

**Edge cases:** user `active` → no-op; user `deleted`/`suspended` → no-op; email tidak valid format → `400 VALIDATION_ERROR` (format boleh dibocorkan).

**Security notes:** timing — jalur "user tidak ada" dan "user ada" harus kira-kira sama durasinya; kirim email secara asynchronous (goroutine dengan context terpisah + timeout, atau queue) agar latency tidak membocorkan.

---

### F04 — Login (MVP)

**User story:** Sebagai user terverifikasi, saya ingin login dengan email & password untuk mendapatkan akses.

**Flow:**
1. `POST /api/v1/auth/login` `{email, password, device_name?}`. Header `User-Agent`, IP dari `r.RemoteAddr` (atau `X-Forwarded-For` **hanya** jika proxy tepercaya dikonfigurasi).
2. Rate limit: per IP & per email (lihat tabel §6).
3. `users.FindByEmail`.
   - **Tidak ditemukan** → `hasher.Compare(ctx, dummyHash, password)` (hash dummy dibuat sekali saat startup dengan parameter yang sama) → return `ErrInvalidCredentials`. Ini mencegah **timing-based user enumeration**.
4. Jika `locked_until > now` → `ErrAccountLocked` (`423`/`429` + `Retry-After`). Tetap jalankan compare dummy agar timing seragam (opsional).
5. `hasher.Compare(hash, password)`.
   - Gagal → `users.IncrementFailedLogin(ctx, id, now, threshold, lockDuration)` dengan **satu SQL atomik**:
     ```sql
     UPDATE users
        SET failed_login_attempts = failed_login_attempts + 1,
            locked_until = CASE WHEN failed_login_attempts + 1 >= $2
                                THEN $3::timestamptz + $4::interval
                                ELSE locked_until END,
            updated_at = $3
      WHERE id = $1
     RETURNING failed_login_attempts, locked_until;
     ```
     Error dari update ini **di-log** (`WARN`), tidak diabaikan, tapi tidak mengubah response (tetap `ErrInvalidCredentials`). Audit `login_failed`.
6. Cek status: `pending_verification` → `ErrEmailNotVerified` (`403`); `suspended` → `ErrAccountSuspended` (`403`); `deleted` → perlakukan seperti tidak ditemukan.
   > Urutan penting: cek status **setelah** password benar, supaya penyerang tidak bisa memetakan status akun tanpa password.
7. `hasher.NeedsRehash(hash)` → jika parameter argon2 berubah, rehash & update (*opportunistic upgrade*).
8. Transaksi:
   1. `users.ResetFailedLogin(id)` + `last_login_at = now`.
   2. Buat `Session{id: uuidv7, user_id, family_id: uuidv7, user_agent, ip, expires_at: now+refreshTTL}`.
   3. Generate refresh token 32 byte `crypto/rand` → base64url (43 char) → simpan `sha256(token)` di `refresh_tokens` (`family_id`, `session_id`).
   4. Audit `login_succeeded`.
9. `tokenIssuer.IssueAccess(ctx, AccessClaims{UserID, SessionID, Roles})` → JWT 15 menit.
10. Response `200` `{data: {access_token, token_type:"Bearer", expires_in:900, refresh_token, refresh_expires_in, user:{...}}}` (atau refresh token di cookie — lihat §5).

**Business rules:** lockout setelah 5 gagal berturut → kunci 15 menit (eksponensial opsional: 15m, 30m, 1h). Login sukses reset counter. Maks 10 sesi aktif per user — sesi tertua dicabut (P1).

**Edge cases:** password benar saat akun locked → tetap ditolak sampai `locked_until`; dua login paralel dengan password salah → counter naik 2 (benar karena atomic); clock skew → semua waktu dari `Clock` port (UTC), DB `now()` hanya untuk default kolom.

**Security notes:** pesan generic "Email atau password salah"; jangan beri tahu "akun belum diverifikasi" sebelum password terbukti benar; jangan log password; IP disimpan untuk audit (PII — retensi 90 hari).

---

### F05 — Refresh Token: Rotation + Reuse Detection (MVP)

**User story:** Sebagai client, saya ingin memperoleh access token baru tanpa login ulang selama sesi masih valid.

**Flow:**
1. `POST /api/v1/auth/refresh` dengan refresh token (cookie `__Host-refresh_token` atau body `{refresh_token}`).
2. `hash := sha256(token)`; `rt := refreshTokens.FindByHash(hash)` (dengan `SELECT ... FOR UPDATE` di dalam transaksi).
3. Tidak ditemukan → `ErrInvalidRefreshToken` (`401`).
4. **Reuse detection:** jika `rt.used_at != nil` **atau** `rt.revoked_at != nil`:
   1. Revoke seluruh family: `UPDATE refresh_tokens SET revoked_at=now, revoked_reason='reuse_detected' WHERE family_id=$1 AND revoked_at IS NULL` dan revoke session terkait.
   2. Audit `refresh_token_reuse_detected` (level `WARN`, sertakan IP & UA).
   3. Return `ErrRefreshTokenReused` → `401` dengan code `TOKEN_REUSED` (client wajib login ulang).
5. Jika `rt.expires_at <= now` atau session revoked/expired → `ErrInvalidRefreshToken`.
6. Cek user masih `active` (bukan suspended/deleted) dan `password_changed_at` < `rt.created_at`.
7. Tandai `rt.used_at = now`, buat refresh token baru dengan `family_id` sama, `parent_id = rt.id`, `expires_at` = **min(now + refreshTTL, session.absolute_expires_at)** (sliding window dengan batas absolut, mis. 30 hari).
8. Update `sessions.last_used_at`, `ip`, `user_agent`.
9. Commit → terbitkan access token baru (sid sama).
10. Response sama dengan login (tanpa `user`).

**Business rules:** refresh TTL 7 hari (sliding), absolut 30 hari. **Grace period** opsional 10 detik untuk race jaringan (dua tab refresh bersamaan): jika token `used_at` < 10 detik lalu **dan** dari IP+UA yang sama → kembalikan `409 REFRESH_IN_PROGRESS` (bukan revoke family). MVP: tanpa grace, dokumentasikan.

**Edge cases:** dua request paralel dengan token sama → `FOR UPDATE` menyerialisasi; yang kedua melihat `used_at` → reuse → family revoked (konsekuensi tanpa grace period; client harus serialize refresh — dokumentasikan untuk tim front-end).

**Security notes:** simpan **hash saja**; bandingkan via lookup by hash (hash tidak bisa di-timing-attack secara berarti karena preimage acak 256-bit). Refresh token **tidak** berupa JWT (opaque) → bisa dicabut seketika.

---

### F06 — Logout Sesi Saat Ini (MVP)

**User story:** Sebagai user, saya ingin keluar dari device ini.

**Flow:**
1. `POST /api/v1/auth/logout` dengan `Authorization: Bearer <access>` (+ refresh token opsional).
2. Middleware auth memvalidasi JWT → `sid` di context.
3. Use case: `sessions.Revoke(ctx, sid, reason="logout")` + revoke semua refresh token di family session tersebut.
4. Audit `logout`. Response `204 No Content`; hapus cookie (`Max-Age=0`).

**Edge cases:** access token sudah expired tapi refresh valid → izinkan logout via refresh token saja (endpoint menerima salah satu). Logout dua kali → idempoten `204`.

**Security notes:** access token tetap valid sampai expired (maks 15 menit) karena stateless. Untuk revocation instan, middleware auth **opsional** mengecek `sessions.revoked_at` (cache in-memory 30 detik) — keputusan: **ON untuk endpoint sensitif** (`/me/password`, `/me` DELETE), OFF untuk endpoint biasa (P1: denylist `sid` di memori/Redis).

---

### F07 — Get Profile / Me (MVP)

**User story:** Sebagai user login, saya ingin melihat profil saya.

**Flow:** `GET /api/v1/me` → middleware auth → `users.FindByID(ctx, uid)` → `200 {data:{id,email,full_name,status,email_verified_at,created_at,updated_at}}`.

**Edge cases:** user dihapus setelah token terbit → `401 UNAUTHENTICATED` (bukan 404, agar client logout).
**Security notes:** jangan expose `password_hash`, `failed_login_attempts`, `locked_until`. DTO response terpisah dari entity (jangan `json.Marshal(entity)`).

---

### F08 — Account Lockout & IP Rate Limit (MVP)

**User story:** Sebagai pemilik sistem, saya ingin mencegah brute-force & credential stuffing.

**Mekanisme dua lapis:**
1. **Per-akun lockout** (domain/DB): kolom `failed_login_attempts`, `locked_until` — atomic SQL (lihat F04).
2. **Per-IP / per-key rate limit** (middleware): token bucket `golang.org/x/time/rate` per key, disimpan di `map[string]*limiter` dengan `sync.Mutex` + janitor goroutine untuk cleanup (MVP, single instance). P1: Redis (sliding window) agar konsisten multi-instance.

**Business rules:** lihat tabel rate limit §6.3. Response `429 RATE_LIMITED` dengan header `Retry-After`, `RateLimit-Limit`, `RateLimit-Remaining` (draft IETF).

**Edge cases:** user di belakang NAT (kantor) → limit per IP jangan terlalu ketat; kombinasikan key `ip` + `email`. Lockout bisa dipakai penyerang untuk DoS akun korban → gunakan lockout sementara (bukan permanen) dan sediakan reset via forgot password (yang juga membuka lock).

**Security notes:** jangan percaya `X-Forwarded-For` kecuali `TRUSTED_PROXIES` dikonfigurasi; ambil IP paling kanan yang bukan proxy tepercaya.

---

### F09 — Logout All Devices (P1)

**User story:** Sebagai user yang curiga akunnya dipakai orang lain, saya ingin mengeluarkan semua device.

**Flow:** `POST /api/v1/auth/logout-all` (auth) → body `{include_current: bool}` default `true` → `UPDATE sessions SET revoked_at=now, revoked_reason='logout_all' WHERE user_id=$1 AND revoked_at IS NULL [AND id <> $2]` + revoke refresh token terkait (via join/`session_id`) → audit → `204`.

**Edge cases:** tidak ada sesi lain → tetap `204`. **Security:** sertakan di email notifikasi "Semua sesi telah dikeluarkan" (P2).

---

### F10 — List Active Sessions / Revoke One Session (P1)

**User story:** Sebagai user, saya ingin melihat device mana saja yang login dan mencabut salah satunya.

**Flow list:** `GET /api/v1/me/sessions` → sesi `revoked_at IS NULL AND expires_at > now` untuk `user_id` → `{data:[{id, device_name, user_agent, ip_masked, created_at, last_used_at, current: bool}], meta:{total}}`.

**Flow revoke:** `DELETE /api/v1/me/sessions/{id}` → validasi UUID → `sessions.RevokeForUser(ctx, userID, sessionID)` (WHERE harus menyertakan `user_id` → mencegah **IDOR**) → `RowsAffected==0` → `404 SESSION_NOT_FOUND` → `204`.

**Edge cases:** revoke sesi sendiri (`current`) → sama dengan logout. **Security:** IP ditampilkan ter-mask (`103.10.xx.xx`) opsional; IDOR wajib dicegah dengan filter `user_id`.

---

### F11 — Update Profile (P1)

**User story:** Sebagai user, saya ingin mengubah nama tampilan.

**Flow:** `PATCH /api/v1/me` `{full_name?}` → hanya field yang ada (pointer di DTO: `*string`) → validasi → `UPDATE users SET full_name=$2, updated_at=$3 WHERE id=$1 AND deleted_at IS NULL RETURNING ...` → `200`.

**Business rules:** ganti email **bukan** bagian dari PATCH ini (P2: flow `change-email` dengan OTP ke email baru + notifikasi ke email lama). **Edge:** body kosong `{}` → `400 VALIDATION_ERROR` "tidak ada field yang diubah". **Security:** whitelist field (mass-assignment protection) — DTO hanya punya field yang diizinkan.

---

### F12 — Change Password (P1)

**User story:** Sebagai user, saya ingin mengganti password, dan device lain otomatis ter-logout.

**Flow:**
1. `POST /api/v1/me/password` `{current_password, new_password}`.
2. Verifikasi `current_password` (gagal → `ErrInvalidCredentials` + hitung ke lockout).
3. Validasi policy `new_password`; tolak jika sama dengan password lama (`Compare(newPassword, oldHash)`).
4. Hash baru (di luar transaksi).
5. Transaksi: update `password_hash`, `password_changed_at=now`; revoke **semua sesi lain** (`id <> current sid`) + refresh token-nya; audit `password_changed`.
6. Kirim email notifikasi "Password Anda diubah" (after commit).
7. `204`.

**Security notes:** endpoint sensitif → middleware cek sesi belum dicabut. Access token lama di device lain masih hidup ≤15 menit — mitigasi: middleware membandingkan `iat` token dengan `password_changed_at` (P1, cache).

---

### F13 — Forgot / Reset Password (P1)

**User story:** Sebagai user yang lupa password, saya ingin meresetnya via email.

**Flow forgot:**
1. `POST /api/v1/auth/password/forgot` `{email}` → **selalu `202`** pesan generic.
2. Jika user ada & status ∈ {active, pending_verification}: cooldown 60s, kuota 5/24 jam → invalidate OTP `password_reset` lama → buat OTP baru (TTL 15 menit, max_attempts 5) → kirim email (async).

**Flow reset:**
1. `POST /api/v1/auth/password/reset` `{email, code, new_password}`.
2. Verifikasi OTP (sama seperti F02, purpose `password_reset`).
3. Validasi policy → hash → transaksi: update password, `password_changed_at`, reset `failed_login_attempts=0`, `locked_until=NULL`; jika `pending_verification` → set `active` + `email_verified_at` (karena kepemilikan email terbukti); consume OTP; **revoke semua sesi**; audit `password_reset`.
4. `204`. User login ulang.

**Edge cases:** OTP benar tapi password lemah → jangan consume OTP (user bisa coba lagi password lain) — tetapi attempts tetap naik? **Keputusan:** validasi password policy **sebelum** verifikasi OTP, sehingga input invalid tidak menghabiskan attempts.

**Security notes:** jangan auto-login setelah reset; email notifikasi setelah reset; link-token alternatif (P2) harus `POST`, bukan `GET` (link prefetcher email client bisa mengonsumsi token).

---

### F14 — Audit Log Security Events (P1)

**User story:** Sebagai user, saya ingin melihat riwayat aktivitas keamanan akun saya; sebagai admin, saya ingin forensik.

**Event types (enum `audit_event_type`):** `user_registered`, `email_verified`, `otp_failed`, `login_succeeded`, `login_failed`, `account_locked`, `token_refreshed` (opsional, volume tinggi → OFF default), `refresh_token_reuse_detected`, `logout`, `logout_all`, `session_revoked`, `password_changed`, `password_reset_requested`, `password_reset`, `profile_updated`, `account_deleted`.

**Flow write:** use case memanggil port `AuditRecorder.Record(ctx, AuditEvent{...})` — **di dalam transaksi yang sama** dengan perubahan state (konsistensi), kecuali `login_failed` (tidak ada tx; insert mandiri, error di-log).

**Flow read:** `GET /api/v1/me/security-events?page=1&page_size=20` → hanya event milik user, urut `occurred_at DESC`.

**Business rules:** append-only (tidak ada UPDATE/DELETE dari aplikasi; DB role app hanya `INSERT, SELECT` pada tabel ini — P1). Retensi 1 tahun; IP/UA diretensi 90 hari lalu di-null-kan (job).

**Security notes:** `metadata JSONB` **tidak boleh** berisi password/token/OTP. `user_id` nullable (login gagal untuk email tak dikenal → simpan `email_hash` saja, bukan email mentah).

---

### F15 — Account Deletion (Soft Delete) (P1)

**User story:** Sebagai user, saya ingin menghapus akun saya.

**Flow:**
1. `DELETE /api/v1/me` `{password}` (re-auth wajib).
2. Verifikasi password.
3. Transaksi: `status='deleted'`, `deleted_at=now`; **anonymize** opsional: `email = 'deleted+'||id||'@invalid'` agar email bebas dipakai (atau gunakan partial unique index — keduanya; rekomendasi: partial index + tetap simpan email untuk 30 hari grace period); revoke semua sesi; audit `account_deleted`.
4. Publish event in-process `UserDeleted` → modul finance (nanti) bereaksi (soft delete data finance). MVP: panggil port `UserDeletionListener` yang diimplementasi finance, atau cukup dokumentasikan.
5. `204`.

**Business rules:** hard delete / purge PII setelah 30 hari oleh job (P2). Semua query user wajib `WHERE deleted_at IS NULL`.

**Edge cases:** login setelah dihapus → `ErrInvalidCredentials` (seperti tidak ada). Register ulang email sama → diperbolehkan (partial unique index).

---

### F16 — 2FA TOTP (P2)

**User story:** Sebagai user, saya ingin keamanan tambahan dengan aplikasi authenticator.

**Flow (ringkas):** `POST /me/2fa/setup` → generate secret 20 byte (`github.com/pquerna/otp`), simpan **terenkripsi** (AES-GCM, key dari config) status `pending` → return `otpauth://` URI + QR. `POST /me/2fa/enable {code}` → verifikasi → simpan 10 recovery codes (hash). Login: jika 2FA aktif → response `200 {data:{mfa_required:true, mfa_token}}` (JWT 5 menit, `aud=mfa`) → `POST /auth/login/2fa {mfa_token, code}` → token biasa.

**Rules:** toleransi ±1 step (30s); cegah replay kode yang sama dalam window (`last_used_step`). **Security:** secret dienkripsi, bukan di-hash (perlu dibaca untuk verifikasi).

### F17 — OAuth Google Login (P2)

**Flow:** Authorization Code + PKCE. `GET /auth/oauth/google/start` → redirect dengan `state` (random, disimpan di cookie signed) + `code_challenge`. Callback → tukar code → verifikasi `id_token` (iss, aud, exp, signature JWKS Google) → cari `user_identities(provider='google', subject=sub)` → link/buat user (email Google `email_verified=true` → status `active`). **Edge:** email sama dengan akun password yang sudah ada → **jangan auto-link** tanpa login password (account takeover risk); minta user login lalu link manual.

### F18 — RBAC Roles (P2)

Tabel `roles`, `user_roles` (lihat DDL opsional). Claim `roles: ["user"]` di JWT. Middleware `RequireRole("admin")`. Admin endpoint: list user, suspend/unsuspend. **Rule:** cek role dari claim untuk performa; untuk aksi destruktif, cek ulang dari DB.

### F19 — API Keys (P2)

Format `gac_live_<prefix8>_<secret32>`; simpan `prefix` (lookup) + `sha256(secret)`; scopes; `last_used_at`; expiry. Ditampilkan sekali saat dibuat. Header `Authorization: ApiKey <key>`. Rate limit per key.

---

## 3. Data Model / ERD

### 3.1 Prinsip

- **ID:** `UUID` tanpa default di kolom — digenerate di app (UUIDv7, time-ordered → index B-tree lebih ramah daripada v4). Tidak memakai extension `uuid-ossp`. (Jika butuh default DB untuk script manual, `gen_random_uuid()` built-in PG13+ boleh, tapi app tetap mengisi.)
- **Email:** `CITEXT` (extension `citext`) **dan** app menyimpan versi lowercase-trim. Unique **partial** index `WHERE deleted_at IS NULL`.
- **Waktu:** `TIMESTAMPTZ`, app mengirim UTC. `created_at`/`updated_at` diisi app (via `Clock`) agar testable; default `now()` sebagai safety net.
- **Status:** Postgres `ENUM` vs `TEXT + CHECK`? Dipilih **`TEXT + CHECK`** — menambah nilai cukup ubah constraint dalam migrasi transaksional (ALTER TYPE ADD VALUE punya batasan dalam transaksi). Konsisten dengan modul finance.
- **Secret** (refresh token, OTP, API key): hanya hash (`BYTEA`, 32 byte SHA-256 / HMAC-SHA256).
- **Migrasi:** `golang-migrate`, format `NNNNNN_nama.up.sql` / `.down.sql`. **Up membuat, Down menghapus** (kode lama tertukar — wajib diperbaiki dan dites `up → down → up` di CI).

### 3.2 DDL lengkap

`migrations/000001_init_extensions.up.sql`
```sql
CREATE EXTENSION IF NOT EXISTS citext;
```
`migrations/000001_init_extensions.down.sql`
```sql
DROP EXTENSION IF EXISTS citext;
```

`migrations/000002_create_users.up.sql`
```sql
CREATE TABLE users (
    id                     UUID         PRIMARY KEY,
    email                  CITEXT       NOT NULL,
    password_hash          TEXT         NOT NULL,            -- PHC string: $argon2id$v=19$m=65536,t=3,p=2$salt$hash
    full_name              TEXT         NOT NULL,
    status                 TEXT         NOT NULL DEFAULT 'pending_verification',
    email_verified_at      TIMESTAMPTZ,
    password_changed_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    failed_login_attempts  INTEGER      NOT NULL DEFAULT 0,
    locked_until           TIMESTAMPTZ,
    last_login_at          TIMESTAMPTZ,
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at             TIMESTAMPTZ,

    CONSTRAINT users_status_chk CHECK (status IN ('pending_verification','active','suspended','deleted')),
    CONSTRAINT users_email_len_chk CHECK (char_length(email) BETWEEN 3 AND 254),
    CONSTRAINT users_full_name_len_chk CHECK (char_length(full_name) BETWEEN 1 AND 100),
    CONSTRAINT users_failed_attempts_chk CHECK (failed_login_attempts >= 0),
    CONSTRAINT users_deleted_consistency_chk CHECK (
        (status = 'deleted') = (deleted_at IS NOT NULL)
    ),
    CONSTRAINT users_verified_consistency_chk CHECK (
        status <> 'active' OR email_verified_at IS NOT NULL
    )
);

-- Email unik hanya di antara akun yang belum dihapus
CREATE UNIQUE INDEX users_email_active_uq ON users (email) WHERE deleted_at IS NULL;
CREATE INDEX users_status_idx ON users (status) WHERE deleted_at IS NULL;
```
`migrations/000002_create_users.down.sql`
```sql
DROP TABLE IF EXISTS users;
```

`migrations/000003_create_sessions_and_refresh_tokens.up.sql`
```sql
CREATE TABLE sessions (
    id                   UUID         PRIMARY KEY,           -- = claim "sid" di JWT
    user_id              UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id            UUID         NOT NULL,              -- family refresh token untuk sesi ini (1:1)
    device_name          TEXT,
    user_agent           TEXT,
    ip_address           INET,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_used_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at           TIMESTAMPTZ  NOT NULL,              -- absolute expiry (mis. 30 hari)
    revoked_at           TIMESTAMPTZ,
    revoked_reason       TEXT,

    CONSTRAINT sessions_family_uq UNIQUE (family_id),
    CONSTRAINT sessions_expiry_chk CHECK (expires_at > created_at),
    CONSTRAINT sessions_revoked_reason_chk CHECK (
        revoked_reason IS NULL OR revoked_reason IN
        ('logout','logout_all','password_changed','password_reset','reuse_detected','admin','account_deleted','session_limit')
    ),
    CONSTRAINT sessions_user_agent_len_chk CHECK (user_agent IS NULL OR char_length(user_agent) <= 512)
);

CREATE INDEX sessions_user_active_idx ON sessions (user_id, last_used_at DESC) WHERE revoked_at IS NULL;
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE refresh_tokens (
    id            UUID         PRIMARY KEY,
    session_id    UUID         NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id     UUID         NOT NULL,
    parent_id     UUID         REFERENCES refresh_tokens(id) ON DELETE SET NULL,
    token_hash    BYTEA        NOT NULL,                     -- SHA-256(raw token), 32 byte
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ  NOT NULL,
    used_at       TIMESTAMPTZ,                               -- diisi saat dirotasi
    revoked_at    TIMESTAMPTZ,
    revoked_reason TEXT,

    CONSTRAINT refresh_tokens_hash_len_chk CHECK (octet_length(token_hash) = 32),
    CONSTRAINT refresh_tokens_expiry_chk CHECK (expires_at > created_at)
);

CREATE UNIQUE INDEX refresh_tokens_hash_uq ON refresh_tokens (token_hash);
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id) WHERE revoked_at IS NULL;
CREATE INDEX refresh_tokens_session_idx ON refresh_tokens (session_id);
CREATE INDEX refresh_tokens_expires_idx ON refresh_tokens (expires_at);
```
`migrations/000003_create_sessions_and_refresh_tokens.down.sql`
```sql
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS sessions;
```

`migrations/000004_create_verification_tokens.up.sql`
```sql
-- Generik: OTP 6 digit atau link token; purpose menentukan penggunaan.
CREATE TABLE verification_tokens (
    id            UUID         PRIMARY KEY,
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose       TEXT         NOT NULL,
    kind          TEXT         NOT NULL DEFAULT 'otp',
    channel       TEXT         NOT NULL DEFAULT 'email',
    target        TEXT         NOT NULL,                     -- alamat tujuan (email) saat token dibuat
    code_hash     BYTEA        NOT NULL,                     -- HMAC-SHA256(pepper, code)
    attempts      INTEGER      NOT NULL DEFAULT 0,
    max_attempts  INTEGER      NOT NULL DEFAULT 5,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ  NOT NULL,
    consumed_at   TIMESTAMPTZ,
    invalidated_at TIMESTAMPTZ,

    CONSTRAINT vt_purpose_chk CHECK (purpose IN ('email_verification','password_reset','email_change','login_2fa')),
    CONSTRAINT vt_kind_chk    CHECK (kind IN ('otp','link')),
    CONSTRAINT vt_channel_chk CHECK (channel IN ('email')),
    CONSTRAINT vt_attempts_chk CHECK (attempts >= 0 AND max_attempts > 0 AND attempts <= max_attempts),
    CONSTRAINT vt_hash_len_chk CHECK (octet_length(code_hash) = 32),
    CONSTRAINT vt_expiry_chk  CHECK (expires_at > created_at)
);

-- Hanya SATU token aktif per (user, purpose)
CREATE UNIQUE INDEX vt_one_active_per_purpose_uq
    ON verification_tokens (user_id, purpose)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;

-- Untuk cooldown & kuota harian
CREATE INDEX vt_user_purpose_created_idx ON verification_tokens (user_id, purpose, created_at DESC);
```
`migrations/000004_create_verification_tokens.down.sql`
```sql
DROP TABLE IF EXISTS verification_tokens;
```

> Catatan: partial unique index memakai kolom, bukan `expires_at > now()` (fungsi `now()` tidak boleh di predicate index karena tidak IMMUTABLE). Karena itu saat membuat OTP baru, app **harus** meng-`invalidated_at` OTP lama (termasuk yang sudah expired) dalam transaksi yang sama.

`migrations/000005_create_audit_logs.up.sql`
```sql
CREATE TABLE audit_logs (
    id            UUID         PRIMARY KEY,
    user_id       UUID         REFERENCES users(id) ON DELETE SET NULL,
    event_type    TEXT         NOT NULL,
    outcome       TEXT         NOT NULL DEFAULT 'success',
    email_hash    BYTEA,                                     -- untuk event tanpa user_id (login gagal email tak dikenal)
    session_id    UUID,
    ip_address    INET,
    user_agent    TEXT,
    request_id    TEXT,
    metadata      JSONB        NOT NULL DEFAULT '{}'::jsonb,
    occurred_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT audit_event_type_chk CHECK (event_type IN (
        'user_registered','email_verified','otp_failed',
        'login_succeeded','login_failed','account_locked',
        'token_refreshed','refresh_token_reuse_detected',
        'logout','logout_all','session_revoked',
        'password_changed','password_reset_requested','password_reset',
        'profile_updated','account_deleted'
    )),
    CONSTRAINT audit_outcome_chk CHECK (outcome IN ('success','failure')),
    CONSTRAINT audit_metadata_obj_chk CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE INDEX audit_logs_user_time_idx ON audit_logs (user_id, occurred_at DESC);
CREATE INDEX audit_logs_event_time_idx ON audit_logs (event_type, occurred_at DESC);
CREATE INDEX audit_logs_ip_time_idx ON audit_logs (ip_address, occurred_at DESC) WHERE outcome = 'failure';
```
`migrations/000005_create_audit_logs.down.sql`
```sql
DROP TABLE IF EXISTS audit_logs;
```

`migrations/000006_create_roles.up.sql` *(opsional, P2)*
```sql
CREATE TABLE roles (
    id          SMALLINT     PRIMARY KEY,
    name        TEXT         NOT NULL UNIQUE,
    description TEXT,
    CONSTRAINT roles_name_chk CHECK (name ~ '^[a-z][a-z0-9_]{1,31}$')
);

INSERT INTO roles (id, name, description) VALUES
    (1, 'user',  'Pengguna biasa'),
    (2, 'admin', 'Administrator');

CREATE TABLE user_roles (
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id    SMALLINT    NOT NULL REFERENCES roles(id) ON DELETE RESTRICT,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by UUID        REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (user_id, role_id)
);
CREATE INDEX user_roles_role_idx ON user_roles (role_id);
```
`migrations/000006_create_roles.down.sql`
```sql
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS roles;
```

### 3.3 Query kunci (contoh)

```sql
-- Revoke satu family (reuse detection)
UPDATE refresh_tokens
   SET revoked_at = $2, revoked_reason = 'reuse_detected'
 WHERE family_id = $1 AND revoked_at IS NULL;

UPDATE sessions
   SET revoked_at = $2, revoked_reason = 'reuse_detected'
 WHERE family_id = $1 AND revoked_at IS NULL;

-- Konsumsi OTP secara atomik (hanya satu request yang menang)
UPDATE verification_tokens
   SET consumed_at = $2
 WHERE id = $1 AND consumed_at IS NULL AND invalidated_at IS NULL
RETURNING id;

-- Increment attempts atomik
UPDATE verification_tokens
   SET attempts = attempts + 1
 WHERE id = $1 AND attempts < max_attempts
RETURNING attempts, max_attempts;
```

### 3.4 Notasi dbdiagram.io

```dbml
Table users {
  id uuid [pk]
  email citext [not null, note: 'unique where deleted_at is null']
  password_hash text [not null]
  full_name text [not null]
  status text [not null, default: 'pending_verification', note: 'pending_verification|active|suspended|deleted']
  email_verified_at timestamptz
  password_changed_at timestamptz [not null]
  failed_login_attempts int [not null, default: 0]
  locked_until timestamptz
  last_login_at timestamptz
  created_at timestamptz [not null]
  updated_at timestamptz [not null]
  deleted_at timestamptz
  Indexes {
    status
  }
}

Table sessions {
  id uuid [pk, note: 'JWT sid']
  user_id uuid [not null, ref: > users.id]
  family_id uuid [not null, unique]
  device_name text
  user_agent text
  ip_address inet
  created_at timestamptz [not null]
  last_used_at timestamptz [not null]
  expires_at timestamptz [not null]
  revoked_at timestamptz
  revoked_reason text
  Indexes {
    (user_id, last_used_at)
    expires_at
  }
}

Table refresh_tokens {
  id uuid [pk]
  session_id uuid [not null, ref: > sessions.id]
  user_id uuid [not null, ref: > users.id]
  family_id uuid [not null]
  parent_id uuid [ref: > refresh_tokens.id]
  token_hash bytea [not null, unique, note: 'sha256']
  created_at timestamptz [not null]
  expires_at timestamptz [not null]
  used_at timestamptz
  revoked_at timestamptz
  revoked_reason text
  Indexes {
    family_id
    session_id
  }
}

Table verification_tokens {
  id uuid [pk]
  user_id uuid [not null, ref: > users.id]
  purpose text [not null, note: 'email_verification|password_reset|email_change|login_2fa']
  kind text [not null, default: 'otp']
  channel text [not null, default: 'email']
  target text [not null]
  code_hash bytea [not null]
  attempts int [not null, default: 0]
  max_attempts int [not null, default: 5]
  created_at timestamptz [not null]
  expires_at timestamptz [not null]
  consumed_at timestamptz
  invalidated_at timestamptz
  Indexes {
    (user_id, purpose) [note: 'unique where consumed_at is null and invalidated_at is null']
    (user_id, purpose, created_at)
  }
}

Table audit_logs {
  id uuid [pk]
  user_id uuid [ref: > users.id]
  event_type text [not null]
  outcome text [not null]
  email_hash bytea
  session_id uuid
  ip_address inet
  user_agent text
  request_id text
  metadata jsonb [not null]
  occurred_at timestamptz [not null]
  Indexes {
    (user_id, occurred_at)
    (event_type, occurred_at)
  }
}

Table roles {
  id smallint [pk]
  name text [not null, unique]
  description text
}

Table user_roles {
  user_id uuid [ref: > users.id]
  role_id smallint [ref: > roles.id]
  granted_at timestamptz [not null]
  granted_by uuid [ref: > users.id]
  Indexes {
    (user_id, role_id) [pk]
  }
}
```

### 3.5 ERD (mermaid)

```mermaid
erDiagram
    users ||--o{ sessions : has
    users ||--o{ refresh_tokens : owns
    sessions ||--o{ refresh_tokens : "family (rotation chain)"
    refresh_tokens |o--o{ refresh_tokens : "parent_id"
    users ||--o{ verification_tokens : receives
    users |o--o{ audit_logs : "subject of"
    users ||--o{ user_roles : has
    roles ||--o{ user_roles : grants
```

---

## 4. API Contract (REST) + Mapping gRPC

### 4.1 Konvensi umum

- Base URL: `/api/v1`. `Content-Type: application/json; charset=utf-8`. Body maks 16 KiB untuk endpoint auth.
- Auth: `Authorization: Bearer <access_token>`. Refresh token: cookie `__Host-refresh_token` (web) **atau** body `refresh_token` (mobile/CLI) — lihat §5.
- Setiap response punya header `X-Request-ID`.
- Error code umum (berlaku semua endpoint): `VALIDATION_ERROR` (400), `MALFORMED_JSON` (400), `PAYLOAD_TOO_LARGE` (413), `UNSUPPORTED_MEDIA_TYPE` (415), `UNAUTHENTICATED` (401), `TOKEN_EXPIRED` (401), `FORBIDDEN` (403), `NOT_FOUND` (404), `METHOD_NOT_ALLOWED` (405), `RATE_LIMITED` (429), `INTERNAL` (500), `SERVICE_UNAVAILABLE` (503).

### 4.2 Tabel endpoint

| # | Method & Path | Auth | Request JSON | Response sukses | Error codes spesifik | Rate limit |
|---|---|---|---|---|---|---|
| 1 | `POST /auth/register` | – | `{"email":"a@b.com","password":"...","full_name":"Ilham"}` | `201 {"data":{"user":{"id","email","full_name","status":"pending_verification","created_at"},"verification_required":true}}` | `EMAIL_TAKEN` 409, `WEAK_PASSWORD` 422 | 5/jam/IP |
| 2 | `POST /auth/verify-email` | – | `{"email":"a@b.com","code":"123456"}` | `200 {"data":{"verified":true}}` | `INVALID_OTP` 400, `OTP_EXPIRED` 400, `OTP_TOO_MANY_ATTEMPTS` 429 | 10/15m/IP, 5/OTP |
| 3 | `POST /auth/verify-email/resend` | – | `{"email":"a@b.com"}` | `202 {"data":{"message":"..."}}` | – (selalu 202) | 3/jam/IP, cooldown 60s/email, 5/hari/email |
| 4 | `POST /auth/login` | – | `{"email","password","device_name?"}` | `200 {"data":{"access_token","token_type":"Bearer","expires_in":900,"refresh_token","refresh_expires_in":604800,"session_id","user":{...}}}` | `INVALID_CREDENTIALS` 401, `ACCOUNT_LOCKED` 423, `EMAIL_NOT_VERIFIED` 403, `ACCOUNT_SUSPENDED` 403 | 10/menit/IP, 5/15m/email |
| 5 | `POST /auth/refresh` | refresh token | `{"refresh_token?":"..."}` | `200 {"data":{"access_token","token_type","expires_in","refresh_token","refresh_expires_in"}}` | `INVALID_REFRESH_TOKEN` 401, `TOKEN_REUSED` 401 | 30/menit/IP |
| 6 | `POST /auth/logout` | Bearer atau refresh | `{"refresh_token?":"..."}` | `204` | – | 30/menit/user |
| 7 | `POST /auth/logout-all` | Bearer | `{"include_current":true}` | `204` | – | 5/menit/user |
| 8 | `GET /me` | Bearer | – | `200 {"data":{"id","email","full_name","status","email_verified_at","created_at","updated_at"}}` | – | 120/menit/user |
| 9 | `PATCH /me` | Bearer | `{"full_name":"Ilham R"}` | `200 {"data":{...user}}` | `NO_FIELDS_TO_UPDATE` 400 | 20/menit/user |
| 10 | `POST /me/password` | Bearer (+cek sesi) | `{"current_password","new_password"}` | `204` | `INVALID_CREDENTIALS` 401, `WEAK_PASSWORD` 422, `PASSWORD_REUSED` 422 | 5/15m/user |
| 11 | `DELETE /me` | Bearer (+cek sesi) | `{"password":"..."}` | `204` | `INVALID_CREDENTIALS` 401 | 3/jam/user |
| 12 | `GET /me/sessions` | Bearer | – | `200 {"data":[{"id","device_name","user_agent","ip_masked","created_at","last_used_at","current":true}],"meta":{"total":2}}` | – | 60/menit/user |
| 13 | `DELETE /me/sessions/{id}` | Bearer | – | `204` | `SESSION_NOT_FOUND` 404 | 20/menit/user |
| 14 | `GET /me/security-events?page=1&page_size=20` | Bearer | – | `200 {"data":[{"id","event_type","outcome","ip_masked","user_agent","occurred_at"}],"meta":{"page":1,"page_size":20,"total":57}}` | – | 60/menit/user |
| 15 | `POST /auth/password/forgot` | – | `{"email":"a@b.com"}` | `202 {"data":{"message":"..."}}` | – (selalu 202) | 3/jam/IP, cooldown 60s/email |
| 16 | `POST /auth/password/reset` | – | `{"email","code","new_password"}` | `204` | `INVALID_OTP` 400, `OTP_EXPIRED` 400, `OTP_TOO_MANY_ATTEMPTS` 429, `WEAK_PASSWORD` 422 | 10/15m/IP |
| 17 | `GET /healthz` | – | – | `200 {"status":"ok"}` | – | none |
| 18 | `GET /readyz` | – | – | `200 {"status":"ready","checks":{"db":"ok"}}` / `503` | `SERVICE_UNAVAILABLE` | none |
| P2 | `POST /me/2fa/setup`, `POST /me/2fa/enable`, `POST /auth/login/2fa`, `GET /auth/oauth/google/start`, `GET /auth/oauth/google/callback`, `GET/POST/DELETE /me/api-keys`, `GET /admin/users`, `POST /admin/users/{id}/suspend` | – | – | – | – | – |

> `/healthz` & `/readyz` **tanpa** prefix `/api/v1` (dipakai orchestrator/k8s, bukan client).

### 4.3 Mapping domain error → HTTP → gRPC

Mapping ini hidup di `adapter/http/errors.go` dan nanti `adapter/grpc/errors.go`. Domain **tidak tahu** angka-angka ini.

| Domain error | Code string | HTTP | gRPC `codes.*` |
|---|---|---|---|
| `domain.ErrValidation` (typed `*ValidationError`) | `VALIDATION_ERROR` | 400 | `InvalidArgument` |
| `domain.ErrWeakPassword` | `WEAK_PASSWORD` | 422 | `InvalidArgument` |
| `domain.ErrEmailTaken` | `EMAIL_TAKEN` | 409 | `AlreadyExists` |
| `domain.ErrInvalidCredentials` | `INVALID_CREDENTIALS` | 401 | `Unauthenticated` |
| `domain.ErrAccountLocked` | `ACCOUNT_LOCKED` | 423 | `ResourceExhausted` |
| `domain.ErrEmailNotVerified` | `EMAIL_NOT_VERIFIED` | 403 | `FailedPrecondition` |
| `domain.ErrAccountSuspended` | `ACCOUNT_SUSPENDED` | 403 | `PermissionDenied` |
| `domain.ErrInvalidOTP` | `INVALID_OTP` | 400 | `InvalidArgument` |
| `domain.ErrOTPExpired` | `OTP_EXPIRED` | 400 | `FailedPrecondition` |
| `domain.ErrOTPTooManyAttempts` | `OTP_TOO_MANY_ATTEMPTS` | 429 | `ResourceExhausted` |
| `domain.ErrInvalidRefreshToken` | `INVALID_REFRESH_TOKEN` | 401 | `Unauthenticated` |
| `domain.ErrRefreshTokenReused` | `TOKEN_REUSED` | 401 | `Unauthenticated` |
| `domain.ErrSessionNotFound` | `SESSION_NOT_FOUND` | 404 | `NotFound` |
| `domain.ErrUserNotFound` | `NOT_FOUND` / `UNAUTHENTICATED` (di `/me`) | 404/401 | `NotFound` |
| `domain.ErrPasswordReused` | `PASSWORD_REUSED` | 422 | `InvalidArgument` |
| (lainnya / unknown) | `INTERNAL` | 500 | `Internal` |

Contoh kode adapter (ringkas):

```go
// internal/auth/adapter/http/errors.go
package http

import (
	"errors"
	"log/slog"
	nethttp "net/http"

	"go-auth-clean/internal/auth/domain"
)

type errorSpec struct {
	status int
	code   string
	msg    string
}

var errorTable = []struct {
	target error
	spec   errorSpec
}{
	{domain.ErrEmailTaken, errorSpec{nethttp.StatusConflict, "EMAIL_TAKEN", "Email sudah terdaftar"}},
	{domain.ErrInvalidCredentials, errorSpec{nethttp.StatusUnauthorized, "INVALID_CREDENTIALS", "Email atau password salah"}},
	{domain.ErrAccountLocked, errorSpec{nethttp.StatusLocked, "ACCOUNT_LOCKED", "Akun dikunci sementara"}},
	{domain.ErrRefreshTokenReused, errorSpec{nethttp.StatusUnauthorized, "TOKEN_REUSED", "Sesi tidak valid, silakan login ulang"}},
	// ...
}

func (h *Handler) writeError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	var vErr *domain.ValidationError
	if errors.As(err, &vErr) {
		writeJSONError(w, r, nethttp.StatusBadRequest, "VALIDATION_ERROR", "Input tidak valid", vErr.Fields)
		return
	}
	for _, e := range errorTable {
		if errors.Is(err, e.target) {
			writeJSONError(w, r, e.spec.status, e.spec.code, e.spec.msg, nil)
			return
		}
	}
	// Log di edge: satu kali, dengan error chain lengkap.
	h.log.ErrorContext(r.Context(), "unhandled error", slog.Any("error", err))
	writeJSONError(w, r, nethttp.StatusInternalServerError, "INTERNAL", "Terjadi kesalahan pada server", nil)
}
```

> Penamaan package `http` bentrok dengan `net/http` → import alias `nethttp`. Alternatif: beri nama package `httpadapter` / `authhttp`. **Rekomendasi:** `package authhttp` (direktori tetap `adapter/http`) agar tidak perlu alias di mana-mana.

### 4.4 Sketsa gRPC `AuthService` (nanti, `api/proto/auth/v1/auth.proto`)

```proto
syntax = "proto3";

package auth.v1;

option go_package = "go-auth-clean/gen/auth/v1;authv1";

import "google/protobuf/timestamp.proto";
import "google/protobuf/empty.proto";

service AuthService {
  rpc Register(RegisterRequest) returns (RegisterResponse);                 // POST /auth/register
  rpc VerifyEmail(VerifyEmailRequest) returns (google.protobuf.Empty);      // POST /auth/verify-email
  rpc ResendVerification(ResendVerificationRequest) returns (google.protobuf.Empty);
  rpc Login(LoginRequest) returns (TokenPair);                              // POST /auth/login
  rpc Refresh(RefreshRequest) returns (TokenPair);                          // POST /auth/refresh
  rpc Logout(LogoutRequest) returns (google.protobuf.Empty);                // POST /auth/logout
  rpc LogoutAll(LogoutAllRequest) returns (google.protobuf.Empty);
  rpc ForgotPassword(ForgotPasswordRequest) returns (google.protobuf.Empty);
  rpc ResetPassword(ResetPasswordRequest) returns (google.protobuf.Empty);
}

service AccountService {
  rpc GetMe(google.protobuf.Empty) returns (User);                          // GET /me
  rpc UpdateMe(UpdateMeRequest) returns (User);                             // PATCH /me
  rpc ChangePassword(ChangePasswordRequest) returns (google.protobuf.Empty);
  rpc DeleteMe(DeleteMeRequest) returns (google.protobuf.Empty);
  rpc ListSessions(ListSessionsRequest) returns (ListSessionsResponse);
  rpc RevokeSession(RevokeSessionRequest) returns (google.protobuf.Empty);
  rpc ListSecurityEvents(ListSecurityEventsRequest) returns (ListSecurityEventsResponse);
}

message User {
  string id = 1;
  string email = 2;
  string full_name = 3;
  UserStatus status = 4;
  google.protobuf.Timestamp email_verified_at = 5;
  google.protobuf.Timestamp created_at = 6;
  google.protobuf.Timestamp updated_at = 7;
}

enum UserStatus {
  USER_STATUS_UNSPECIFIED = 0;
  USER_STATUS_PENDING_VERIFICATION = 1;
  USER_STATUS_ACTIVE = 2;
  USER_STATUS_SUSPENDED = 3;
  USER_STATUS_DELETED = 4;
}

message RegisterRequest { string email = 1; string password = 2; string full_name = 3; }
message RegisterResponse { User user = 1; bool verification_required = 2; }
message VerifyEmailRequest { string email = 1; string code = 2; }
message ResendVerificationRequest { string email = 1; }
message LoginRequest { string email = 1; string password = 2; string device_name = 3; }
message RefreshRequest { string refresh_token = 1; }
message LogoutRequest { string refresh_token = 1; }
message LogoutAllRequest { bool include_current = 1; }
message ForgotPasswordRequest { string email = 1; }
message ResetPasswordRequest { string email = 1; string code = 2; string new_password = 3; }
message UpdateMeRequest { optional string full_name = 1; }
message ChangePasswordRequest { string current_password = 1; string new_password = 2; }
message DeleteMeRequest { string password = 1; }
message ListSessionsRequest {}
message Session {
  string id = 1; string device_name = 2; string user_agent = 3; string ip_masked = 4;
  google.protobuf.Timestamp created_at = 5; google.protobuf.Timestamp last_used_at = 6; bool current = 7;
}
message ListSessionsResponse { repeated Session sessions = 1; }
message RevokeSessionRequest { string session_id = 1; }
message ListSecurityEventsRequest { int32 page_size = 1; string page_token = 2; }
message SecurityEvent { string id = 1; string event_type = 2; string outcome = 3; google.protobuf.Timestamp occurred_at = 4; }
message ListSecurityEventsResponse { repeated SecurityEvent events = 1; string next_page_token = 2; }

message TokenPair {
  string access_token = 1;
  string token_type = 2;
  int64 expires_in = 3;
  string refresh_token = 4;
  int64 refresh_expires_in = 5;
  string session_id = 6;
}
```

**Kenapa core tidak berubah:** handler HTTP dan gRPC sama-sama hanya melakukan: (1) terjemahkan request → `app.XxxCommand`, (2) panggil `app` service, (3) terjemahkan `app.XxxResult`/error → response. Auth middleware HTTP ↔ gRPC `UnaryServerInterceptor` memakai `TokenVerifier` port yang sama dan menaruh `auth.Principal` di `context` dengan helper yang sama (`authctx.WithPrincipal`). Error mapping punya tabel sendiri per adapter. Request ID: HTTP header `X-Request-ID` ↔ gRPC metadata `x-request-id`.

```mermaid
flowchart TB
    subgraph Delivery
      H[adapter/http handler] 
      G[adapter/grpc server]
    end
    H -- LoginCommand --> UC[app.LoginService.Login]
    G -- LoginCommand --> UC
    UC --> D[domain: User, Email, Session]
    UC --> P1[[UserRepository]]
    UC --> P2[[PasswordHasher]]
    UC --> P3[[TokenIssuer]]
    P1 -.impl.-> PG[adapter/postgres]
    P2 -.impl.-> SEC[adapter/security argon2id]
    P3 -.impl.-> JWT[adapter/security jwt]
```

---

## 5. Token Design

### 5.1 Ringkasan

| Aspek | Access token | Refresh token |
|---|---|---|
| Format | JWT (JWS compact), `HS256` di MVP | Opaque: 32 byte `crypto/rand` → base64url tanpa padding (43 char) |
| TTL | **15 menit** | **7 hari sliding**, absolut maks **30 hari** (`sessions.expires_at`) |
| Disimpan server? | Tidak (stateless) | Ya, **hanya `SHA-256(token)`** di `refresh_tokens.token_hash` |
| Bisa dicabut? | Tidak instan (kecuali cek `sid` di middleware sensitif) | Ya, instan |
| Dikirim ke | Semua endpoint ber-auth (`Authorization: Bearer`) | Hanya `POST /auth/refresh` & `/auth/logout` |

### 5.2 JWT claims

```json
{
  "iss": "go-auth-clean",
  "aud": ["go-auth-clean-api"],
  "sub": "0192a1b2-...-user-id",
  "sid": "0192a1b3-...-session-id",
  "jti": "0192a1b4-...-unique-token-id",
  "iat": 1790000000,
  "nbf": 1790000000,
  "exp": 1790000900,
  "roles": ["user"]
}
```

Aturan:
- Key dari config `JWT_SECRET` (base64), **≥ 32 byte** setelah decode; aplikasi **gagal start** jika kurang. Tidak ada default hard-coded.
- Parser **wajib** membatasi algoritma: `jwt.WithValidMethods([]string{"HS256"})` → menolak `none` dan *algorithm confusion*.
- Validasi `iss`, `aud`, `exp` wajib (`jwt.WithIssuer`, `jwt.WithAudience`, `jwt.WithExpirationRequired`), leeway 30 detik.
- Header `kid` disertakan (mis. `"k1"`) agar **key rotation** mungkin: verifier menyimpan map `kid → key`; sign dengan key terbaru, verify dengan key lama selama TTL.
- **Upgrade nanti:** `EdDSA` (Ed25519) atau `RS256` + endpoint **JWKS** (`/.well-known/jwks.json`). Berguna saat ada gRPC service lain/microservice: service lain cukup memverifikasi dengan public key tanpa memegang secret.

```go
// internal/auth/adapter/security/jwt.go
package security

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/auth/domain"
)

type accessClaims struct {
	SessionID string   `json:"sid"`
	Roles     []string `json:"roles,omitempty"`
	jwt.RegisteredClaims
}

type JWTIssuer struct {
	key      []byte
	keyID    string
	issuer   string
	audience string
	ttl      time.Duration
	clock    app.Clock
	ids      app.IDGenerator
}

// Compile-time assertion: gagal compile kalau signature tidak cocok dengan port.
var (
	_ app.TokenIssuer   = (*JWTIssuer)(nil)
	_ app.TokenVerifier = (*JWTIssuer)(nil)
)

func NewJWTIssuer(key []byte, keyID, issuer, audience string, ttl time.Duration, clock app.Clock, ids app.IDGenerator) (*JWTIssuer, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("jwt: key must be at least 32 bytes, got %d", len(key))
	}
	return &JWTIssuer{key: key, keyID: keyID, issuer: issuer, audience: audience, ttl: ttl, clock: clock, ids: ids}, nil
}

func (j *JWTIssuer) IssueAccess(_ context.Context, p app.Principal) (app.AccessToken, error) {
	now := j.clock.Now()
	exp := now.Add(j.ttl)
	claims := accessClaims{
		SessionID: p.SessionID.String(),
		Roles:     p.Roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    j.issuer,
			Subject:   p.UserID.String(),
			Audience:  jwt.ClaimStrings{j.audience},
			ExpiresAt: jwt.NewNumericDate(exp),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        j.ids.New().String(),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = j.keyID
	signed, err := tok.SignedString(j.key)
	if err != nil {
		return app.AccessToken{}, fmt.Errorf("jwt.IssueAccess: %w", err)
	}
	return app.AccessToken{Value: signed, ExpiresAt: exp}, nil
}

func (j *JWTIssuer) VerifyAccess(_ context.Context, raw string) (app.Principal, error) {
	var claims accessClaims
	_, err := jwt.ParseWithClaims(raw, &claims,
		func(t *jwt.Token) (any, error) { return j.key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(j.issuer),
		jwt.WithAudience(j.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(j.clock.Now),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return app.Principal{}, domain.ErrAccessTokenExpired
		}
		return app.Principal{}, domain.ErrInvalidAccessToken
	}
	return principalFromClaims(claims)
}
```

> `principalFromClaims` mem-parse `sub`/`sid` ke `uuid.UUID` dan mengembalikan `domain.ErrInvalidAccessToken` jika gagal.

### 5.3 Di mana client menyimpan token?

| Opsi | Kelebihan | Kekurangan |
|---|---|---|
| **A. Access di memory JS + refresh di cookie `HttpOnly; Secure; SameSite=Strict; Path=/api/v1/auth; __Host-`** | Refresh token tidak bisa dicuri XSS (JS tidak bisa baca); access token hilang saat reload → ambil ulang via refresh | Perlu CSRF consideration untuk `/auth/refresh` (mitigasi: SameSite=Strict + cek header `Origin` + wajib custom header `X-Requested-With`/`Content-Type: application/json`); butuh CORS `credentials: true` dengan origin eksplisit |
| B. Keduanya di `localStorage` + header | Sederhana, cocok SPA lintas domain | **Rentan XSS**: satu XSS = refresh token 30 hari tercuri |
| C. Keduanya di cookie | Tidak ada JS handling token | Semua endpoint perlu proteksi CSRF; access token dikirim ke semua path |
| D. Mobile/CLI: body JSON + secure storage OS (Keychain/Keystore) | Native, tidak ada cookie | Tanggung jawab client menyimpan aman |

**Rekomendasi:** dukung **A untuk web** dan **D untuk mobile/CLI/gRPC** sekaligus. Server mengirim refresh token di **cookie dan** body hanya jika client meminta (`X-Client-Type: mobile`) — atau lebih sederhana: endpoint menerima refresh dari cookie **atau** body, dan response login meng-*set cookie* untuk web serta menyertakan `refresh_token` di body hanya untuk client non-browser. Access token selalu via `Authorization: Bearer` (seragam dengan gRPC metadata `authorization`).

```go
http.SetCookie(w, &http.Cookie{
	Name:     "__Host-refresh_token", // __Host- prefix: wajib Secure, Path=/, tanpa Domain
	Value:    res.RefreshToken,
	Path:     "/",
	MaxAge:   int(res.RefreshExpiresIn.Seconds()),
	HttpOnly: true,
	Secure:   true,
	SameSite: http.SameSiteStrictMode,
})
```

> Catatan: prefix `__Host-` mensyaratkan `Path=/`. Jika ingin membatasi `Path=/api/v1/auth`, pakai nama tanpa prefix (`refresh_token`) + `Secure`.

### 5.4 Sequence: login, rotation, reuse detection

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant API as HTTP Adapter
    participant UC as app.RefreshService
    participant DB as Postgres

    C->>API: POST /auth/login (email, password)
    API->>UC: Login(cmd)
    UC->>DB: INSERT session(family_id=F) + refresh_tokens(hash(RT1), family F)
    UC-->>C: access AT1 (15m) + refresh RT1

    Note over C: 15 menit kemudian AT1 expired
    C->>API: POST /auth/refresh (RT1)
    API->>UC: Refresh(RT1)
    UC->>DB: BEGIN; SELECT ... WHERE token_hash=sha256(RT1) FOR UPDATE
    DB-->>UC: RT1 (used_at NULL, revoked_at NULL)
    UC->>DB: UPDATE RT1 SET used_at=now; INSERT RT2(parent=RT1, family F); COMMIT
    UC-->>C: AT2 + RT2

    Note over C,DB: Penyerang mencuri RT1 dan memakainya
    C->>API: POST /auth/refresh (RT1) [attacker]
    API->>UC: Refresh(RT1)
    UC->>DB: SELECT RT1 FOR UPDATE
    DB-->>UC: RT1 used_at != NULL  => REUSE
    UC->>DB: UPDATE refresh_tokens SET revoked_at WHERE family_id=F; UPDATE sessions SET revoked_at WHERE family_id=F
    UC->>DB: INSERT audit_logs(refresh_token_reuse_detected)
    UC-->>C: 401 TOKEN_REUSED

    Note over C: User sah memakai RT2
    C->>API: POST /auth/refresh (RT2)
    UC->>DB: RT2 revoked_at != NULL => REUSE (family sudah dicabut)
    UC-->>C: 401 TOKEN_REUSED -> user login ulang
```

### 5.5 Generate & hash refresh token

```go
// internal/auth/adapter/security/opaque.go
package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

type OpaqueTokenGenerator struct{}

// Generate mengembalikan token mentah (untuk client) dan hash-nya (untuk DB).
func (OpaqueTokenGenerator) Generate() (raw string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashToken(raw), nil
}

func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
```

> Kenapa SHA-256 cukup (tidak perlu argon2) untuk refresh token? Karena entropinya 256-bit acak — brute-force preimage tidak mungkin. Password perlu hash lambat karena entropinya rendah. OTP (entropi 20-bit) perlu **HMAC dengan pepper** + attempts limit.

---

## 6. Security Checklist (OWASP ASVS-inspired)

### 6.1 Autentikasi & password

- [ ] **Password policy:** min **12** karakter (absolut min 8), maks **128** karakter untuk argon2id. Jika bcrypt: tolak > **72 byte** (bukan karakter! UTF-8 multi-byte) — `golang.org/x/crypto/bcrypt` mengembalikan `ErrPasswordTooLong` sejak v0.25+, jangan diam-diam di-truncate.
- [ ] Tidak ada aturan komposisi paksa; izinkan semua karakter Unicode & spasi; jangan trim password.
- [ ] (Opsional P1) cek **breached password** via HIBP k-anonymity API (`range/{first5sha1}`) dengan timeout 2s, fail-open.
- [ ] **argon2id** parameter (OWASP 2024+): `m=19 MiB, t=2, p=1` minimum; rekomendasi `m=64 MiB, t=3, p=2` jika server mampu. Salt 16 byte `crypto/rand`, key 32 byte. Simpan dalam format PHC string agar parameter bisa di-upgrade (`NeedsRehash`).
- [ ] Batasi concurrency hashing (semaphore `chan struct{}` ukuran = `GOMAXPROCS`) agar argon2 64 MiB × 1000 request paralel tidak OOM.
- [ ] **Timing-safe:** `subtle.ConstantTimeCompare` untuk hash; **dummy hash compare** saat user tidak ada.
- [ ] **Generic error messages:** login → "Email atau password salah"; forgot/resend → selalu 202.
- [ ] Lockout sementara (5 gagal → 15 menit), counter **atomic SQL**.
- [ ] Re-auth (password) untuk aksi sensitif: change password, delete account, (P2) disable 2FA.
- [ ] Notifikasi email untuk: password changed, password reset, (P2) login dari device baru.

```go
// internal/auth/adapter/security/argon2.go (inti)
package security

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

type Argon2Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

type Argon2Hasher struct {
	p   Argon2Params
	sem chan struct{}
}

func NewArgon2Hasher(p Argon2Params, maxConcurrent int) *Argon2Hasher {
	return &Argon2Hasher{p: p, sem: make(chan struct{}, maxConcurrent)}
}

func (h *Argon2Hasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Argon2Hasher) release() { <-h.sem }

func (h *Argon2Hasher) Hash(ctx context.Context, password string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()

	salt := make([]byte, h.p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2.Hash: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.p.Time, h.p.Memory, h.p.Threads, h.p.KeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.p.Memory, h.p.Time, h.p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

var errMalformedHash = errors.New("argon2: malformed hash")

func (h *Argon2Hasher) Compare(ctx context.Context, encoded, password string) (bool, error) {
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errMalformedHash
	}
	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return false, errMalformedHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errMalformedHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, errMalformedHash
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
```

### 6.2 Session & token

- [ ] Refresh token disimpan **hash SHA-256** saja; rotation + reuse detection (family revoke).
- [ ] JWT: `WithValidMethods`, `iss`, `aud`, `exp`, `jti`, `sid`; key ≥ 32 byte dari secret manager/env; `kid` untuk rotasi.
- [ ] Logout/ganti password/reset password mencabut sesi relevan.
- [ ] Cookie: `HttpOnly`, `Secure`, `SameSite=Strict`, prefix `__Host-`.
- [ ] Batas sesi aktif per user (10).

### 6.3 Rate limits

| Scope | Key | Limit | Algoritma | Response |
|---|---|---|---|---|
| Global per IP (semua endpoint) | IP | 300/menit, burst 50 | token bucket | 429 |
| `POST /auth/login` | IP | 10/menit, burst 5 | token bucket | 429 + `Retry-After` |
| `POST /auth/login` | email (lowercase) | 5/15 menit | fixed/sliding window | 429 (selain lockout DB) |
| `POST /auth/register` | IP | 5/jam | sliding window | 429 |
| `POST /auth/verify-email`, `/auth/password/reset` | IP | 10/15 menit | sliding window | 429 |
| OTP verification | per OTP row | 5 attempts | DB counter | 429 `OTP_TOO_MANY_ATTEMPTS` |
| `/auth/verify-email/resend`, `/auth/password/forgot` | IP | 3/jam | sliding window | 429 |
| `/auth/verify-email/resend`, `/auth/password/forgot` | email | cooldown 60s, 5/24 jam | DB query `created_at` | 202 (diam) |
| `POST /auth/refresh` | IP | 30/menit | token bucket | 429 |
| Endpoint ber-auth | user_id | 120/menit | token bucket | 429 |

> MVP: in-memory (`golang.org/x/time/rate`) — **hanya benar untuk single instance**. Multi-instance → Redis (P1).

### 6.4 HTTP hardening

- [ ] **Timeouts `http.Server`** (tanpa ini → Slowloris):

```go
srv := &http.Server{
	Addr:              cfg.HTTP.Addr,
	Handler:           handler,
	ReadHeaderTimeout: 5 * time.Second,
	ReadTimeout:       10 * time.Second,
	WriteTimeout:      15 * time.Second,
	IdleTimeout:       60 * time.Second,
	MaxHeaderBytes:    1 << 20, // 1 MiB
	ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
}
```

- [ ] **Body size limit:** `r.Body = http.MaxBytesReader(w, r.Body, 16<<10)`; deteksi `*http.MaxBytesError` → 413.
- [ ] JSON decoder: `dec.DisallowUnknownFields()`; tolak trailing data (`dec.More()` / decode kedua harus `io.EOF`).
- [ ] Cek `Content-Type: application/json` → 415.
- [ ] **Security headers** (middleware):
  - `Strict-Transport-Security: max-age=63072000; includeSubDomains` (hanya di HTTPS/production)
  - `X-Content-Type-Options: nosniff`
  - `X-Frame-Options: DENY` / `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'` (API JSON)
  - `Referrer-Policy: no-referrer`
  - `Cache-Control: no-store` untuk semua response auth (token jangan di-cache)
- [ ] **CORS:** allowlist origin eksplisit dari config (`CORS_ALLOWED_ORIGINS`); **jangan** `*` bersama `Access-Control-Allow-Credentials: true`; handle preflight `OPTIONS`; `Vary: Origin`. Go 1.22+ ServeMux: daftarkan middleware CORS di luar mux agar `OPTIONS` tidak kena 405.
- [ ] `http.CrossOriginProtection` (Go 1.25+) sebagai lapisan CSRF untuk endpoint berbasis cookie (`/auth/refresh`, `/auth/logout`).
- [ ] Recover middleware: panic → 500 generic + log stack trace; jangan bocorkan detail ke client.
- [ ] TLS diterminasi di reverse proxy/load balancer; set `TRUSTED_PROXIES` untuk membaca IP klien.

### 6.5 Data & secrets

- [ ] **SQL injection:** selalu parameterized (`$1`, `$2`) dengan pgx; **tidak pernah** `fmt.Sprintf` nilai user ke SQL. Untuk `ORDER BY` dinamis → whitelist map.
- [ ] **Never log:** password, refresh/access token, OTP, `Authorization` header, cookie, `JWT_SECRET`, DSN dengan password.
- [ ] **PII masking di log:** email `il***@gmail.com`, IP penuh boleh di audit_logs (retensi 90 hari) tapi di app log cukup `/24`.
- [ ] Type `Secret string` dengan `String()`/`LogValue()` yang mengembalikan `"[REDACTED]"` untuk field config sensitif.
- [ ] **Secrets management:** env var dari secret manager (Doppler/Vault/AWS SM/GCP SM/k8s Secret). `.env` hanya dev, ada di `.gitignore`. Jalankan `gitleaks` di CI.
- [ ] DB user aplikasi bukan superuser; migrasi pakai user terpisah (P1).
- [ ] Dependency scanning: `govulncheck ./...` di CI.

```go
// internal/platform/config/secret.go
package config

import "log/slog"

type Secret string

func (Secret) String() string          { return "[REDACTED]" }
func (Secret) LogValue() slog.Value    { return slog.StringValue("[REDACTED]") }
func (s Secret) Reveal() string        { return string(s) }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
```

---

## 7. Observability

### 7.1 Prinsip

1. **Log at the edge, wrap in the middle.** Layer dalam (repo, use case) **tidak** log-lalu-return; mereka `return fmt.Errorf("userRepo.FindByEmail: %w", err)`. Error di-log **sekali** di adapter (handler/error mapper) atau access-log middleware. Menghindari log duplikat 5× untuk satu error.
2. Pengecualian yang **boleh** log di use case: kejadian bisnis yang tidak menghasilkan error ke caller tetapi penting (mis. "gagal update failed_login_attempts" yang ditelan, "mailer gagal setelah commit", "refresh token reuse detected" sebagai `WARN`).
3. Semua log JSON via `slog`, selalu pakai `*Context` variant (`logger.InfoContext(ctx, ...)`) agar handler bisa menarik `request_id` dari context.

### 7.2 Field standar

| Field | Sumber | Contoh |
|---|---|---|
| `time` | slog | `2026-10-01T08:00:00.123Z` |
| `level` | slog | `INFO` |
| `msg` | kode | `http request` |
| `service` / `version` / `env` | config (atribut logger root) | `go-auth-clean`, `v0.3.1`, `production` |
| `request_id` | middleware (header `X-Request-ID` atau UUIDv7 baru) | `0192...` |
| `user_id` | auth middleware (setelah verify) | `0192...` |
| `session_id` | auth middleware | `0192...` |
| `method`, `path` (pattern, bukan URL mentah), `status`, `duration_ms`, `bytes`, `remote_ip`, `user_agent` | access log | `POST`, `POST /api/v1/auth/login`, `401`, `87` |
| `error` | error mapper | `login: userRepo.FindByEmail: context deadline exceeded` |
| `component` | logger.With | `authhttp`, `postgres` |

> Gunakan `r.Pattern` (Go 1.23+) untuk field `route` — kardinalitas rendah dan tidak memuat ID.

### 7.3 Context-aware slog handler

```go
// internal/platform/logger/context.go
package logger

import (
	"context"
	"log/slog"
)

type ctxKey struct{}

type ctxFields struct {
	attrs []slog.Attr
}

// WithAttrs menambahkan atribut ke context; dipanggil middleware (request_id, user_id).
func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	prev, _ := ctx.Value(ctxKey{}).(ctxFields)
	merged := make([]slog.Attr, 0, len(prev.attrs)+len(attrs))
	merged = append(merged, prev.attrs...)
	merged = append(merged, attrs...)
	return context.WithValue(ctx, ctxKey{}, ctxFields{attrs: merged})
}

type ContextHandler struct{ slog.Handler }

func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if f, ok := ctx.Value(ctxKey{}).(ctxFields); ok {
		r.AddAttrs(f.attrs...)
	}
	return h.Handler.Handle(ctx, r)
}

func (h ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ContextHandler{h.Handler.WithAttrs(attrs)}
}

func (h ContextHandler) WithGroup(name string) slog.Handler {
	return ContextHandler{h.Handler.WithGroup(name)}
}
```

```go
// internal/platform/middleware/request_id.go
package middleware

import (
	"log/slog"
	"net/http"
	"regexp"

	"github.com/google/uuid"

	"go-auth-clean/internal/platform/logger"
)

var validReqID = regexp.MustCompile(`^[A-Za-z0-9\-_.]{8,64}$`)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validReqID.MatchString(id) { // jangan percaya input mentah (log injection)
			id = uuid.Must(uuid.NewV7()).String()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := WithRequestID(r.Context(), id)
		ctx = logger.WithAttrs(ctx, slog.String("request_id", id))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

### 7.4 Kebijakan level log

| Level | Kapan | Contoh |
|---|---|---|
| `DEBUG` | Detail dev, OFF di production | query plan, payload ter-mask |
| `INFO` | Lifecycle & access log | server started, request selesai (2xx/3xx/4xx biasa) |
| `WARN` | Anomali yang ditangani / perlu perhatian | refresh reuse detected, account locked, mailer retry, 429 burst, gagal update counter |
| `ERROR` | Request gagal karena bug/infra (5xx), operasi background gagal | DB down, panic recovered |
| (tidak ada FATAL) | `os.Exit(1)` hanya di `main` saat startup gagal | config invalid |

Access log: 4xx → `INFO` (atau `WARN` untuk 401/429 berulang), 5xx → `ERROR`. Level runtime bisa diubah via `slog.LevelVar` (env `LOG_LEVEL`).

### 7.5 Apa yang di-log di setiap layer

| Layer | Log? | Apa |
|---|---|---|
| `domain` | **Tidak** | Tidak punya logger. Hanya return error. |
| `app` | Jarang | Hanya event bisnis yang ditelan/penting (lihat 7.1 butir 2). Logger di-inject sebagai `*slog.Logger`. |
| `adapter/postgres` | **Tidak** | Wrap error dengan nama operasi. (Opsional: pgx tracer untuk slow query > 200ms di level WARN.) |
| `adapter/http` | Ya | Error 5xx (sekali, di error mapper). |
| `middleware` | Ya | Access log (1 baris per request), panic recover, rate limit hit. |
| `main` | Ya | Startup/shutdown, config tersanitasi. |

### 7.6 Metrics (P1 — Prometheus)

- `http_requests_total{route,method,status}`, `http_request_duration_seconds{route,method}` (histogram).
- `auth_login_total{outcome="success|invalid_credentials|locked|unverified"}`, `auth_refresh_total{outcome}`, `auth_refresh_reuse_detected_total`, `auth_otp_sent_total{purpose}`.
- `pgxpool_*` (acquired/idle/total conns), `go_*` & `process_*` bawaan `promhttp`.
- Endpoint `/metrics` di port internal terpisah (`:9090`), bukan publik.
- App mendefinisikan port `Metrics` kecil agar `app` tidak import Prometheus.

### 7.7 Tracing (P2 — OpenTelemetry, gRPC-ready)

- `otelhttp.NewHandler` untuk HTTP, `otelgrpc` stats handler untuk gRPC; propagator W3C `traceparent`.
- `pgx` tracer (`otelpgx`) untuk span query.
- Masukkan `trace_id`/`span_id` ke log (handler slog membaca `trace.SpanContextFromContext(ctx)`) → korelasi log ↔ trace.
- Karena semua fungsi menerima `ctx` sejak hari pertama, menambah tracing tidak mengubah signature.

---

## 8. Testing Strategy

### 8.1 Piramida

```mermaid
flowchart TB
    E2E["E2E smoke (sedikit) — docker compose + curl/hurl"] --> I
    I["Integration — repo + testcontainers Postgres, handler + app nyata"] --> U
    U["Unit (banyak) — domain & app dengan fakes, table-driven"]
```

| Jenis | Lokasi | Tools | Target coverage |
|---|---|---|---|
| Unit domain | `internal/auth/domain/*_test.go` | `testing`, `testify/require` | ≥ 90% |
| Unit app (use case) | `internal/auth/app/*_test.go` | fakes hand-written | ≥ 85% |
| Repository integration | `internal/auth/adapter/postgres/*_test.go` (build tag `integration`) | `testcontainers-go/modules/postgres`, golang-migrate | ≥ 70% |
| HTTP handler | `internal/auth/adapter/http/*_test.go` | `net/http/httptest`, fake app service | ≥ 75% |
| Security adapters | `adapter/security` | known vectors, tamper tests (alg none, wrong aud) | ≥ 85% |
| Total | | `go test -race -coverprofile` | ≥ 75% (gate di CI) |

### 8.2 Fakes, bukan mocks-first

Fake = implementasi sederhana dan benar dari port (mis. map in-memory). Lebih mudah dibaca, tidak rapuh terhadap urutan pemanggilan. Mocks (`mockery`/`gomock`) hanya bila perlu memverifikasi interaksi spesifik (mis. "Mailer dipanggil tepat sekali").

```go
// internal/auth/app/fakes_test.go
package app_test

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"go-auth-clean/internal/auth/domain"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakeUserRepo struct {
	mu    sync.Mutex
	byID  map[uuid.UUID]domain.User
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{byID: make(map[uuid.UUID]domain.User)}
}

func (r *fakeUserRepo) Create(_ context.Context, u domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.byID {
		if existing.Email == u.Email && existing.DeletedAt == nil {
			return domain.ErrEmailTaken
		}
	}
	r.byID[u.ID] = u
	return nil
}

func (r *fakeUserRepo) FindByEmail(_ context.Context, email domain.Email) (domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.byID {
		if u.Email == email && u.DeletedAt == nil {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrUserNotFound
}

type fakeHasher struct{}

func (fakeHasher) Hash(_ context.Context, p string) (string, error) { return "hashed:" + p, nil }
func (fakeHasher) Compare(_ context.Context, h, p string) (bool, error) {
	return h == "hashed:"+p, nil
}
```

### 8.3 Table-driven test

```go
func TestNewEmail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{name: "normal", in: "ilham@example.com", want: "ilham@example.com"},
		{name: "uppercase dan spasi", in: "  Ilham@Example.COM ", want: "ilham@example.com"},
		{name: "kosong", in: "", wantErr: domain.ErrInvalidEmail},
		{name: "tanpa @", in: "ilham.example.com", wantErr: domain.ErrInvalidEmail},
		{name: "terlalu panjang", in: strings.Repeat("a", 250) + "@x.io", wantErr: domain.ErrInvalidEmail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NewEmail(tt.in)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got.String())
		})
	}
}
```

> Sejak Go 1.22, variabel loop per-iterasi → `tt := tt` tidak diperlukan lagi.

### 8.4 Use case test yang wajib ada (Login)

- user tidak ada → `ErrInvalidCredentials` **dan** hasher.Compare tetap dipanggil (dummy) — fake hasher menghitung panggilan.
- password salah → counter naik, audit `login_failed`.
- password salah ke-5 → `locked_until` terisi.
- akun locked → `ErrAccountLocked` meski password benar.
- `pending_verification` + password benar → `ErrEmailNotVerified`.
- sukses → session dibuat, refresh hash tersimpan (≠ raw), access token berisi `sid`.
- repo increment error → tetap `ErrInvalidCredentials`, error ter-log (gunakan `slog` handler ke `bytes.Buffer`).

### 8.5 Repository integration test (testcontainers)

```go
//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("auth_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	runMigrations(t, dsn) // golang-migrate dengan source file://../../../../migrations

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}
```

Kasus wajib:
- `Create` dua kali email sama (beda case) → `domain.ErrEmailTaken` (dari kode `23505`, bukan pre-check).
- `Create` paralel 10 goroutine email sama → tepat 1 sukses.
- `IncrementFailedLogin` paralel 20 goroutine → nilai akhir tepat 20.
- Rotation: `MarkUsed` + `Insert` dalam tx; rollback tx → tidak ada perubahan.
- `RevokeFamily` → semua token family revoked.
- Partial unique OTP: insert OTP kedua aktif untuk purpose sama → error; setelah invalidate → sukses.
- Migrasi `up → down → up` sukses.

Tip performa: satu container per **package** (`TestMain`) + `TRUNCATE ... CASCADE` antar test, atau template database.

### 8.6 HTTP handler test (httptest)

```go
func TestLoginHandler_InvalidCredentials(t *testing.T) {
	t.Parallel()
	svc := &fakeLoginService{err: domain.ErrInvalidCredentials}
	h := authhttp.NewHandler(authhttp.Deps{Login: svc, Logger: slog.New(slog.DiscardHandler)})

	mux := http.NewServeMux()
	h.Register(mux)

	body := strings.NewReader(`{"email":"a@b.com","password":"wrong-password-123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"error":{"code":"INVALID_CREDENTIALS","message":"Email atau password salah"},"request_id":""}`, rec.Body.String())
}
```

Kasus wajib handler: JSON rusak → 400 `MALFORMED_JSON`; field tak dikenal → 400; body > limit → 413; content-type salah → 415; validasi gagal → 400 dengan `details[]`; error unknown → 500 tanpa bocor detail; cookie refresh ter-set dengan atribut benar.

### 8.7 Aturan menjalankan test

- `go test -race -count=1 ./...` (unit) — `-race` **wajib** di CI.
- `go test -race -tags=integration ./...` (butuh Docker).
- `t.Parallel()` di mana aman; `t.Context()` (Go 1.24+) untuk context yang otomatis cancel di akhir test.
- Tidak ada `time.Sleep` di test → pakai `fakeClock`.
- Fuzz test (`go test -fuzz`) untuk parser: `NewEmail`, decoder JSON request, parser PHC hash.
- Golden/contract test: response JSON dibandingkan dengan `api/openapi.yaml` (P1, `kin-openapi`).

---

## 9. Milestones Implementasi (Sprint 0..N)

Asumsi: 1 sprint ≈ 1 minggu paruh waktu. Setiap sprint diakhiri commit/PR kecil dengan CI hijau.

```mermaid
gantt
    title Roadmap Auth (perkiraan)
    dateFormat  YYYY-MM-DD
    section Fondasi
    S0 Setup & tooling           :s0, 2026-10-05, 7d
    S1 Platform (config, log, http, db) :s1, after s0, 7d
    section MVP
    S2 Domain + Register         :s2, after s1, 7d
    S3 Verifikasi email (OTP)    :s3, after s2, 7d
    S4 Login + JWT + Me          :s4, after s3, 7d
    S5 Refresh rotation + logout :s5, after s4, 7d
    S6 Hardening (rate limit, headers) :s6, after s5, 7d
    section P1
    S7 Sessions, change/reset password :s7, after s6, 7d
    S8 Audit log, delete, observability :s8, after s7, 7d
    section Next
    S9 gRPC adapter              :s9, after s8, 7d
```

### Sprint 0 — Setup & Tooling
**Deliverables:** `go mod init go-auth-clean`, struktur folder (§0.1), `Makefile`, `docker-compose.yml` (Postgres + Mailpit), `.env.example`, `.gitignore`, `.golangci.yml`, GitHub Actions (lint + test), `README` singkat cara run.
**DoD:** `make up && make lint && make test` sukses di mesin bersih; CI hijau; tidak ada secret di repo.
**Konsep Go:** module & package, `internal/` visibility, `go.mod`/`go.sum`, `go vet`, build tags, toolchain directive (`go 1.26`), `go tool` directive (Go 1.24+) untuk mem-pin tools seperti `migrate`/`golangci-lint`.

### Sprint 1 — Platform
**Deliverables:** `platform/config` (parse env → struct, validasi, `Secret`), `platform/logger` (slog JSON + `ContextHandler`), `platform/database` (pgxpool + `TxManager`), `platform/httpserver` (server dengan timeouts + graceful shutdown), middleware `RequestID`, `Recover`, `AccessLog`, `SecurityHeaders`, `/healthz`, `/readyz`, `platform/clock`, `platform/idgen`.
**DoD:** `SIGTERM` → server berhenti menerima request baru, request in-flight selesai ≤ 10s, pool DB ditutup, exit 0; `/readyz` 503 saat Postgres dimatikan; setiap log berisi `request_id`; panic di handler → 500 + log stack.
**Konsep Go:** `context` (cancel, timeout, `signal.NotifyContext`), goroutine & channel di graceful shutdown, `errgroup`, closures untuk middleware (`func(http.Handler) http.Handler`), middleware chaining, `http.ResponseWriter` wrapping (status recorder), `defer`/`recover`, struct embedding.

```go
// cmd/api/main.go (kerangka)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-auth-clean/internal/platform/config"
	"go-auth-clean/internal/platform/database"
	"go-auth-clean/internal/platform/logger"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(cfg.Log)

	pool, err := database.NewPool(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()

	handler := buildHTTPHandler(cfg, log, pool) // wiring semua modul di sini

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server started", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	log.Info("server stopped gracefully")
	return nil
}
```

> Tambahkan import `fmt` dan `net`. Catatan: `BaseContext` membuat context request ikut ter-cancel saat shutdown — pertimbangkan memakai `context.WithoutCancel(ctx)` bila ingin request in-flight tetap selesai.

**TxManager (pola context-carrying tx):**

```go
// internal/platform/database/tx.go
package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX dipenuhi oleh *pgxpool.Pool maupun pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type txKey struct{}

type TxManager struct{ pool *pgxpool.Pool }

func NewTxManager(pool *pgxpool.Pool) *TxManager { return &TxManager{pool: pool} }

// WithinTx menjalankan fn dalam transaksi; repository mengambil tx dari ctx via Conn().
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx) // nested: ikut tx luar
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err = fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// Conn mengembalikan tx dari context jika ada, kalau tidak pool.
func Conn(ctx context.Context, pool *pgxpool.Pool) DBTX {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}
```

Port di `app`: `type TxManager interface { WithinTx(ctx context.Context, fn func(ctx context.Context) error) error }` — app tidak tahu pgx.

### Sprint 2 — Domain + Register
**Deliverables:** `auth/domain` (`User`, `UserStatus`, `Email`, password policy, errors, `UserRepository` interface), `auth/app` (`RegisterService` + ports), `adapter/postgres/user_repo.go` (deteksi `23505`), `adapter/security/argon2.go`, `adapter/http` register handler + DTO + error mapper + JSON helpers, migrasi 000001–000002, OpenAPI untuk endpoint register.
**DoD:** register end-to-end via curl; email duplikat (beda case) → 409; unit test domain/app ≥ 85%; integration test repo dengan testcontainers; assertion `var _ app.UserRepository = (*postgres.UserRepo)(nil)` ada; migrasi up/down/up lulus.
**Konsep Go:** interface implisit & consumer-side interface, `var _ I = (*T)(nil)`, value object dengan unexported field + constructor, sentinel error, `errors.Is`/`errors.As` (`*pgconn.PgError`), `fmt.Errorf("%w")`, method value vs pointer receiver, table-driven test.

```go
// internal/auth/domain/errors.go
package domain

import "errors"

var (
	ErrUserNotFound        = errors.New("user not found")
	ErrEmailTaken          = errors.New("email already taken")
	ErrInvalidEmail        = errors.New("invalid email")
	ErrWeakPassword        = errors.New("password does not meet policy")
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrAccountLocked       = errors.New("account temporarily locked")
	ErrEmailNotVerified    = errors.New("email not verified")
	ErrAccountSuspended    = errors.New("account suspended")
	ErrInvalidOTP          = errors.New("invalid otp")
	ErrOTPExpired          = errors.New("otp expired")
	ErrOTPTooManyAttempts  = errors.New("otp too many attempts")
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
	ErrRefreshTokenReused  = errors.New("refresh token reused")
	ErrSessionNotFound     = errors.New("session not found")
	ErrInvalidAccessToken  = errors.New("invalid access token")
	ErrAccessTokenExpired  = errors.New("access token expired")
	ErrPasswordReused      = errors.New("new password equals current password")
)

// ValidationError adalah typed error untuk banyak field.
type ValidationError struct {
	Fields []FieldError
}

type FieldError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return "validation failed" }
```

```go
// internal/auth/domain/email.go
package domain

import (
	"net/mail"
	"strings"
)

type Email struct{ value string }

func NewEmail(raw string) (Email, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if len(v) < 3 || len(v) > 254 {
		return Email{}, ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(v)
	if err != nil || addr.Address != v {
		return Email{}, ErrInvalidEmail
	}
	return Email{value: v}, nil
}

func (e Email) String() string { return e.value }

// Masked untuk log: "il***@gmail.com".
func (e Email) Masked() string {
	local, domainPart, ok := strings.Cut(e.value, "@")
	if !ok || len(local) < 2 {
		return "***@" + domainPart
	}
	return local[:2] + "***@" + domainPart
}
```

```go
// internal/auth/adapter/postgres/user_repo.go (potongan)
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-auth-clean/internal/auth/app"
	"go-auth-clean/internal/auth/domain"
	"go-auth-clean/internal/platform/database"
)

const uniqueViolation = "23505"

type UserRepo struct{ pool *pgxpool.Pool }

var _ app.UserRepository = (*UserRepo)(nil)

func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

func (r *UserRepo) Create(ctx context.Context, u domain.User) error {
	const q = `
		INSERT INTO users (id, email, password_hash, full_name, status, password_changed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`
	_, err := database.Conn(ctx, r.pool).Exec(ctx, q,
		u.ID, u.Email.String(), u.PasswordHash, u.FullName, string(u.Status), u.PasswordChangedAt, u.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "users_email_active_uq" {
			return domain.ErrEmailTaken
		}
		return fmt.Errorf("userRepo.Create: %w", err)
	}
	return nil
}

func (r *UserRepo) FindByEmail(ctx context.Context, email domain.Email) (domain.User, error) {
	const q = `
		SELECT id, email, password_hash, full_name, status, email_verified_at, password_changed_at,
		       failed_login_attempts, locked_until, last_login_at, created_at, updated_at, deleted_at
		  FROM users
		 WHERE email = $1 AND deleted_at IS NULL`
	row := database.Conn(ctx, r.pool).QueryRow(ctx, q, email.String())
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("userRepo.FindByEmail: %w", err)
	}
	return u, nil
}
```

> Perhatikan: `updated_at` **ikut di-SELECT** (bug kode lama), dan scan ke `*time.Time` untuk kolom nullable.

### Sprint 3 — Verifikasi Email (OTP)
**Deliverables:** migrasi 000004, `VerificationTokenRepository`, OTP generator (`crypto/rand`, HMAC pepper), `Mailer` port + adapter SMTP (Mailpit di dev) + `LogMailer` untuk test, use case `VerifyEmail`, `ResendVerification`, kirim email setelah commit (goroutine dengan `context.WithoutCancel` + timeout).
**DoD:** register → email muncul di Mailpit UI (`http://localhost:8025`); OTP salah 5× → `OTP_TOO_MANY_ATTEMPTS`; resend dalam 60s tidak membuat OTP baru; OTP tersimpan sebagai hash (cek manual di psql); test paralel konsumsi OTP → hanya satu sukses.
**Konsep Go:** `crypto/rand` vs `math/rand/v2`, `crypto/hmac`, `crypto/subtle`, goroutine fire-and-forget yang aman (context terpisah, recover, WaitGroup untuk shutdown), `context.WithoutCancel` (Go 1.21+), `html/template`/`text/template` untuk email, `embed` untuk template.

```go
// Generate OTP 6 digit tanpa modulo bias
func GenerateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func HashOTP(pepper []byte, code string) []byte {
	m := hmac.New(sha256.New, pepper)
	m.Write([]byte(code))
	return m.Sum(nil)
}

func OTPEqual(pepper []byte, code string, stored []byte) bool {
	return hmac.Equal(HashOTP(pepper, code), stored) // constant-time
}
```

### Sprint 4 — Login + JWT + Me
**Deliverables:** migrasi 000003, `SessionRepository`, `RefreshTokenRepository`, use case `Login` (dummy hash, atomic counter, lockout, status check), `JWTIssuer`/`TokenVerifier`, middleware `Auth` (menaruh `Principal` di context), `GET /me`.
**DoD:** login sukses mengembalikan token; `GET /me` dengan token valid 200, token di-tamper/`alg:none`/aud salah → 401; benchmark menunjukkan durasi login "user tidak ada" vs "password salah" berbeda < 10%; 20 login gagal paralel → counter tepat 20 (integration test); semua implementasi punya `var _ Port = (*Impl)(nil)`.
**Konsep Go:** context values dengan unexported key type, type assertion `v, ok :=`, `time.Duration` & `time.Time` (UTC), struct tags JSON, `testing.B` benchmark, `sync.Once` (dummy hash), method set & interface satisfaction.

```go
// internal/auth/app/login.go (inti)
package app

type LoginService struct {
	users     UserRepository
	sessions  SessionRepository
	tokens    RefreshTokenRepository
	audit     AuditRecorder
	hasher    PasswordHasher
	issuer    TokenIssuer
	opaque    OpaqueTokenGenerator
	tx        TxManager
	clock     Clock
	ids       IDGenerator
	log       *slog.Logger
	dummyHash string
	policy    LoginPolicy
}

func (s *LoginService) Login(ctx context.Context, cmd LoginCommand) (LoginResult, error) {
	email, err := domain.NewEmail(cmd.Email)
	if err != nil {
		_, _ = s.hasher.Compare(ctx, s.dummyHash, cmd.Password)
		return LoginResult{}, domain.ErrInvalidCredentials
	}

	user, err := s.users.FindByEmail(ctx, email)
	switch {
	case errors.Is(err, domain.ErrUserNotFound):
		_, _ = s.hasher.Compare(ctx, s.dummyHash, cmd.Password) // samakan timing
		s.recordFailure(ctx, nil, email, cmd.Meta, "user_not_found")
		return LoginResult{}, domain.ErrInvalidCredentials
	case err != nil:
		return LoginResult{}, fmt.Errorf("login: %w", err)
	}

	now := s.clock.Now()
	if user.IsLocked(now) {
		return LoginResult{}, domain.ErrAccountLocked
	}

	ok, err := s.hasher.Compare(ctx, user.PasswordHash, cmd.Password)
	if err != nil {
		return LoginResult{}, fmt.Errorf("login: compare: %w", err)
	}
	if !ok {
		if _, incErr := s.users.IncrementFailedLogin(ctx, user.ID, now, s.policy.MaxAttempts, s.policy.LockDuration); incErr != nil {
			s.log.WarnContext(ctx, "increment failed login", slog.Any("error", incErr), slog.String("user_id", user.ID.String()))
		}
		s.recordFailure(ctx, &user.ID, email, cmd.Meta, "bad_password")
		return LoginResult{}, domain.ErrInvalidCredentials
	}

	if err := user.CanLogin(); err != nil { // ErrEmailNotVerified / ErrAccountSuspended
		return LoginResult{}, err
	}

	// ... buat session + refresh token dalam s.tx.WithinTx, lalu issue access token
	return s.startSession(ctx, user, cmd.Meta, now)
}
```

Interface yang dideklarasikan **di app** dan harus cocok persis:

```go
// internal/auth/app/ports.go
type Login interface {
	Login(ctx context.Context, cmd LoginCommand) (LoginResult, error) // 2 return value — bukan 3!
}

var _ Login = (*LoginService)(nil) // akan gagal compile jika signature berubah
```

### Sprint 5 — Refresh Rotation + Logout
**Deliverables:** use case `Refresh` (FOR UPDATE, reuse detection, family revoke), `Logout`, `LogoutAll`, cookie handling, audit untuk event terkait.
**DoD:** skenario sequence §5.4 lulus sebagai integration test; refresh token di DB berupa 32 byte hash; logout idempoten; dua refresh paralel token sama → satu sukses, family revoked (didokumentasikan).
**Konsep Go:** transaksi & row locking, `defer tx.Rollback` pattern, named return + defer, `[]byte` vs `string`, `encoding/base64` variants, `http.Cookie`.

### Sprint 6 — Hardening
**Deliverables:** middleware `RateLimit` (per IP & per key, janitor goroutine), `CORS`, `BodyLimit`, `CrossOriginProtection`, `TRUSTED_PROXIES` client IP resolver, JSON decode helper ketat, OpenAPI lengkap MVP, `govulncheck` & `gitleaks` di CI.
**DoD:** checklist §6 MVP semua ✔; test: 11 login/menit dari 1 IP → 429 + `Retry-After`; body 1 MiB → 413; tidak ada goroutine leak (`go.uber.org/goleak` di test middleware).
**Konsep Go:** `sync.Mutex` vs `sync.RWMutex` vs `sync.Map`, `time.Ticker` + `Stop`, goroutine lifecycle terikat context, generics (mis. `func decodeJSON[T any](r *http.Request) (T, error)`, `type Page[T any]`), `golang.org/x/time/rate`.

```go
// generic JSON helper di adapter http
func decodeJSON[T any](w http.ResponseWriter, r *http.Request, maxBytes int64) (T, error) {
	var dst T
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&dst); err != nil {
		return dst, err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return dst, errors.New("body must contain a single JSON object")
	}
	return dst, nil
}
```

### Sprint 7 — Sessions, Change & Reset Password, Update Profile (P1)
**Deliverables:** F09–F13 lengkap, email notifikasi, endpoint sesi dengan IDOR protection.
**DoD:** ganti password mencabut sesi lain (test); reset password membuka lockout; `DELETE /me/sessions/{id}` milik user lain → 404; PATCH dengan field kosong → 400.
**Konsep Go:** `r.PathValue("id")` (Go 1.22 ServeMux), pointer field untuk PATCH semantics (`*string`), `uuid.Parse` error handling, refactor shared logic tanpa `utils` package.

### Sprint 8 — Audit Log, Account Deletion, Observability (P1)
**Deliverables:** `AuditRecorder` (postgres), `GET /me/security-events` dengan pagination (`shared.Pagination`), `DELETE /me`, event `UserDeleted` (in-process), Prometheus `/metrics`, slow-query tracer, dokumentasi runbook singkat.
**DoD:** setiap event §F14 tercatat (test); metadata audit tidak mengandung secret (test grep); dashboard metrics lokal opsional; coverage total ≥ 75%.
**Konsep Go:** `encoding/json` + `json.RawMessage` untuk JSONB, generics `Page[T]`, observer pattern dengan interface kecil, `expvar`/Prometheus client, `slog.LogValuer`.

### Sprint 9+ — gRPC Adapter (Next)
**Deliverables:** `api/proto/auth/v1/auth.proto`, `buf` config (`buf.yaml`, `buf.gen.yaml`), generate ke `gen/`, `internal/auth/adapter/grpc` (server, error mapping ke `status.Error(codes.X, ...)`, interceptor auth/request_id/logging/recover), jalankan HTTP & gRPC berdampingan di `main` dengan `errgroup`; opsional `grpc-gateway`/`connect-go`.
**DoD:** **tidak ada diff** di `internal/auth/domain` dan `internal/auth/app`; test gRPC dengan `bufconn`; `grpcurl` login berhasil.
**Konsep Go:** code generation, `errgroup.Group` untuk multiple servers, interceptor (mirip middleware), `google.golang.org/grpc/status` & `codes`, metadata.

Sprint berikutnya: P2 (2FA TOTP, OAuth Google, RBAC, API keys), Redis rate limit, outbox pattern untuk email, JWKS/EdDSA, OpenTelemetry.

---

## 10. Hal Penting yang Sering Dilupakan Pemula Go

Setiap poin: ❌ buruk → ✅ baik.

### 10.1 Nil interface vs nil pointer
```go
// ❌ return pointer nil bertipe konkret lewat interface error => err != nil selalu true
func validate() error {
	var e *ValidationError // nil
	return e               // interface (type=*ValidationError, value=nil) != nil
}
// ✅ return nil literal
func validate() error {
	var fields []FieldError
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}
```

### 10.2 Variabel loop (pre-Go 1.22 vs sekarang)
```go
// Go 1.22+ setiap iterasi punya variabel baru, jadi ini aman:
for _, u := range users {
	go func() { process(u) }()
}
// ❌ Tetapi kebiasaan lama `u := u` tidak perlu lagi; dan masih bermasalah jika go.mod < 1.22.
// ✅ Pastikan go.mod: `go 1.26`.
```

### 10.3 `defer` di dalam loop
```go
// ❌ file baru ditutup saat fungsi selesai → kehabisan file descriptor
for _, p := range paths {
	f, _ := os.Open(p)
	defer f.Close()
}
// ✅ bungkus dalam fungsi
for _, p := range paths {
	if err := processFile(p); err != nil {
		return err
	}
}
func processFile(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	// ...
	return nil
}
```

### 10.4 Lupa `rows.Close()` dan `rows.Err()`
```go
// ❌ koneksi tidak kembali ke pool; error iterasi hilang
rows, _ := db.Query(ctx, q)
for rows.Next() { /* scan */ }
// ✅
rows, err := db.Query(ctx, q)
if err != nil {
	return nil, fmt.Errorf("sessionRepo.List: %w", err)
}
defer rows.Close()
for rows.Next() { /* scan, cek err */ }
if err := rows.Err(); err != nil {
	return nil, fmt.Errorf("sessionRepo.List rows: %w", err)
}
// ✅✅ pgx v5: pgx.CollectRows(rows, pgx.RowToStructByName[sessionRow]) sudah menutup rows.
```

### 10.5 Mengabaikan error
```go
// ❌ (bug kode lama: update counter gagal diam-diam)
s.users.IncrementFailedLogin(ctx, id)
// ✅ tangani atau log secara sadar
if _, err := s.users.IncrementFailedLogin(ctx, id, now, max, lock); err != nil {
	s.log.WarnContext(ctx, "increment failed login", slog.Any("error", err))
}
// Jika benar-benar sengaja diabaikan, tulis eksplisit: _ = tx.Rollback(ctx)
```

### 10.6 Goroutine leak
```go
// ❌ goroutine menunggu selamanya jika tidak ada penerima
func fetch() string {
	ch := make(chan string)
	go func() { ch <- slowCall() }()
	select {
	case v := <-ch:
		return v
	case <-time.After(time.Second):
		return "" // goroutine di atas stuck selamanya
	}
}
// ✅ channel buffered 1, atau gunakan context
ch := make(chan string, 1)
```

### 10.7 Tidak menghormati cancel context
```go
// ❌ loop panjang tanpa cek ctx
for _, id := range ids { work(id) }
// ✅
for _, id := range ids {
	if err := ctx.Err(); err != nil {
		return err
	}
	work(ctx, id)
}
```

### 10.8 Menyimpan `context.Context` di struct / pakai `context.Background()` di tengah request
```go
// ❌
type Service struct{ ctx context.Context }
func (s *Service) Do() { s.repo.Find(context.Background()) } // putus dari cancel & request_id
// ✅ context selalu parameter pertama, diteruskan
func (s *Service) Do(ctx context.Context) error { return s.repo.Find(ctx) }
```

### 10.9 Zona waktu
```go
// ❌ time.Now() lokal server (WIB) tersimpan campur aduk; sulit di-test
u.CreatedAt = time.Now()
// ✅ lewat Clock port, selalu UTC
type Clock interface{ Now() time.Time }
type SystemClock struct{}
func (SystemClock) Now() time.Time { return time.Now().UTC() }
// Bandingkan waktu dengan t.Equal(u), bukan ==, (monotonic clock & location berbeda).
```

### 10.10 float untuk uang (relevan modul finance)
```go
// ❌
var total float64 = 0.1 + 0.2 // 0.30000000000000004
// ✅ integer minor unit (sen/rupiah) + currency, di shared.Money
type Money struct {
	Amount   int64  // satuan terkecil
	Currency string // "IDR"
}
// DB: NUMERIC(19,4) atau BIGINT minor units.
```

### 10.11 Global state
```go
// ❌
var DB *pgxpool.Pool // diakses dari mana saja, sulit di-test
// ✅ dependency injection via constructor
func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }
```

### 10.12 `init()` yang berisi logika berat
```go
// ❌ koneksi DB di init(): tidak bisa handle error, urutan tidak jelas, test ikut konek
func init() { DB, _ = pgxpool.New(context.Background(), os.Getenv("DSN")) }
// ✅ semua inisialisasi di run() pada main, error dikembalikan
```

### 10.13 Panic untuk alur error biasa
```go
// ❌
if user == nil { panic("user not found") }
// ✅ panic hanya untuk bug tak terpulihkan / programmer error saat startup (MustXxx)
if errors.Is(err, domain.ErrUserNotFound) { return domain.ErrInvalidCredentials }
```

### 10.14 `http.Client` tanpa timeout
```go
// ❌ http.DefaultClient tidak punya timeout → goroutine menggantung selamanya
resp, err := http.Get("https://api.pwnedpasswords.com/range/ABCDE")
// ✅
client := &http.Client{Timeout: 3 * time.Second}
req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
resp, err := client.Do(req)
if err != nil { return err }
defer resp.Body.Close()
```

### 10.15 Shadowed `err`
```go
// ❌ err di dalam if adalah variabel baru; err luar tetap nil
var err error
if cond {
	user, err := repo.Find(ctx, id) // := membuat err baru
	_ = user
}
return err // selalu nil
// ✅
var user domain.User
var err error
if cond {
	user, err = repo.Find(ctx, id)
}
// Aktifkan linter `govet` shadow.
```

### 10.16 Concurrent map write
```go
// ❌ fatal error: concurrent map writes (rate limiter)
limiters[ip] = rate.NewLimiter(r, b)
// ✅
type limiterStore struct {
	mu sync.Mutex
	m  map[string]*rate.Limiter
}
func (s *limiterStore) get(ip string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.m[ip]
	if !ok {
		l = rate.NewLimiter(10, 5)
		s.m[ip] = l
	}
	return l
}
```

### 10.17 `omitempty` pada zero value
```go
// ❌ Count 0 hilang dari JSON; bool false hilang; client bingung
type Resp struct {
	Count    int  `json:"count,omitempty"`
	Verified bool `json:"verified,omitempty"`
}
// ✅ jangan omitempty untuk nilai yang bermakna saat 0/false; pakai pointer untuk "tidak ada"
type Resp struct {
	Count           int        `json:"count"`
	Verified        bool       `json:"verified"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"` // null jika belum
}
// Go 1.24+: `omitzero` menghilangkan time.Time{} zero dengan benar.
```

### 10.18 Exported vs unexported (dan JSON)
```go
// ❌ field huruf kecil tidak di-marshal oleh encoding/json
type loginRequest struct {
	email    string `json:"email"`
	password string `json:"password"`
}
// ✅ field exported, type boleh unexported
type loginRequest struct {
	Email    string `json:"email" validate:"required,email,max=254"`
	Password string `json:"password" validate:"required,min=1,max=128"`
}
```

### 10.19 Nama package: `utils`, `common`, stutter
```go
// ❌
package utils      // isinya campur aduk
user.UserService   // stutter
// ✅ nama menggambarkan isi, dibaca bersama identifier
package clock      // clock.System
auth/app.LoginService, domain.User, postgres.UserRepo
```

### 10.20 Interface terlalu besar / dideklarasikan di sisi producer
```go
// ❌ di package postgres: type UserRepository interface { 20 method }
// ✅ di package consumer (app), hanya method yang dipakai use case tsb
type userFinder interface {
	FindByEmail(ctx context.Context, email domain.Email) (domain.User, error)
}
```

### 10.21 Return interface dari constructor
```go
// ❌
func NewUserRepo(p *pgxpool.Pool) app.UserRepository { return &UserRepo{p} }
// ✅ accept interfaces, return structs
func NewUserRepo(p *pgxpool.Pool) *UserRepo { return &UserRepo{pool: p} }
```

### 10.22 Lupa compile-time assertion
```go
// ❌ (bug kode lama) Login mengembalikan 3 nilai, interface 2 — baru ketahuan di wiring/runtime
// ✅
var _ app.Login = (*app.LoginService)(nil)
```

### 10.23 Membandingkan error dengan `==` atau string
```go
// ❌
if err == pgx.ErrNoRows {}               // gagal jika error di-wrap
if strings.Contains(err.Error(), "duplicate") {}
// ✅
if errors.Is(err, pgx.ErrNoRows) {}
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) && pgErr.Code == "23505" {}
```

### 10.24 Membocorkan error internal ke client
```go
// ❌
http.Error(w, err.Error(), 500) // "pq: relation users does not exist ..."
// ✅ log detail, kirim pesan generic + request_id
h.log.ErrorContext(ctx, "unhandled", slog.Any("error", err))
writeJSONError(w, r, 500, "INTERNAL", "Terjadi kesalahan pada server", nil)
```

### 10.25 Menulis header setelah body / lupa `return` setelah error
```go
// ❌
if err != nil {
	writeError(w, r, err)
}
w.WriteHeader(http.StatusOK) // "superfluous response.WriteHeader call"
// ✅
if err != nil {
	writeError(w, r, err)
	return
}
w.Header().Set("Content-Type", "application/json") // header SEBELUM WriteHeader
w.WriteHeader(http.StatusOK)
```

### 10.26 `math/rand` untuk secret
```go
// ❌ dapat diprediksi
code := fmt.Sprintf("%06d", mathrand.IntN(1_000_000))
// ✅ crypto/rand
n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
```

### 10.27 Slice aliasing dengan `append`
```go
// ❌ dua slice berbagi backing array → saling menimpa
base := make([]string, 0, 10)
a := append(base, "x")
b := append(base, "y") // a[0] sekarang "y"
// ✅ copy eksplisit / slices.Clone
a := append(slices.Clone(base), "x")
```

### 10.28 Membaca body response tanpa batas / tanpa menutup
```go
// ❌
b, _ := io.ReadAll(resp.Body)
// ✅
defer resp.Body.Close()
b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
```

### 10.29 `time.After` di loop panjang
```go
// ❌ (sebelum Go 1.23 bocor timer; tetap boros alokasi)
for { select { case <-time.After(time.Second): tick() ; case <-ctx.Done(): return } }
// ✅
t := time.NewTicker(time.Second)
defer t.Stop()
for {
	select {
	case <-t.C:
		tick()
	case <-ctx.Done():
		return
	}
}
```

### 10.30 Value receiver yang memodifikasi state / menyalin mutex
```go
// ❌ perubahan hilang; mutex tersalin (go vet copylocks)
func (s limiterStore) Add(k string) { s.mu.Lock(); /* ... */ }
// ✅ pointer receiver untuk tipe yang punya mutex / dimodifikasi
func (s *limiterStore) Add(k string) { s.mu.Lock(); defer s.mu.Unlock() }
```

---

## 11. Tooling

### 11.1 Makefile

```makefile
SHELL := /bin/bash
.DEFAULT_GOAL := help

include .env
export

MIGRATIONS_DIR := migrations
DB_URL ?= postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=disable

.PHONY: help
help: ## Tampilkan bantuan
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: up down
up: ## Jalankan postgres + mailpit
	docker compose up -d
down: ## Matikan container
	docker compose down

.PHONY: run dev build
run: ## Jalankan API
	go run ./cmd/api
dev: ## Hot reload dengan air
	go tool air
build: ## Build binary
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$$(git describe --tags --always)" -o bin/api ./cmd/api

.PHONY: test test-integration cover
test: ## Unit test + race
	go test -race -count=1 ./...
test-integration: ## Integration test (butuh Docker)
	go test -race -count=1 -tags=integration ./...
cover: ## Coverage report
	go test -race -tags=integration -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: lint fmt vuln
lint: ## golangci-lint
	golangci-lint run ./...
fmt: ## Format kode
	gofmt -s -w . && go tool goimports -w -local go-auth-clean .
vuln: ## Cek vulnerability
	go tool govulncheck ./...

.PHONY: migrate-up migrate-down migrate-create migrate-force
migrate-up: ## Jalankan semua migrasi
	go tool migrate -path $(MIGRATIONS_DIR) -database "$(DB_URL)" up
migrate-down: ## Rollback 1 migrasi
	go tool migrate -path $(MIGRATIONS_DIR) -database "$(DB_URL)" down 1
migrate-create: ## make migrate-create name=create_users
	go tool migrate create -ext sql -dir $(MIGRATIONS_DIR) -seq $(name)
migrate-force: ## make migrate-force version=3
	go tool migrate -path $(MIGRATIONS_DIR) -database "$(DB_URL)" force $(version)

.PHONY: sqlc proto
sqlc: ## (nanti) generate kode dari SQL
	go tool sqlc generate
proto: ## (nanti) generate gRPC dari api/proto
	buf generate
```

> `include .env` gagal jika file tidak ada → gunakan `-include .env`. Tools di-pin via `go get -tool` (Go 1.24+), contoh: `go get -tool github.com/golang-migrate/migrate/v4/cmd/migrate` (butuh build tag `postgres`: lebih mudah install via `go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest` lalu panggil `migrate` langsung).

### 11.2 docker-compose.yml

```yaml
services:
  postgres:
    image: postgres:17-alpine
    container_name: auth-postgres
    environment:
      POSTGRES_USER: ${DB_USER:-auth}
      POSTGRES_PASSWORD: ${DB_PASSWORD:-auth}
      POSTGRES_DB: ${DB_NAME:-auth}
      TZ: UTC
      PGTZ: UTC
    ports:
      - "${DB_PORT:-5432}:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${DB_USER:-auth} -d ${DB_NAME:-auth}"]
      interval: 5s
      timeout: 3s
      retries: 10

  mailpit:
    image: axllent/mailpit:latest
    container_name: auth-mailpit
    ports:
      - "1025:1025"   # SMTP
      - "8025:8025"   # Web UI
    environment:
      MP_SMTP_AUTH_ACCEPT_ANY: 1
      MP_SMTP_AUTH_ALLOW_INSECURE: 1

volumes:
  pgdata:
```

### 11.3 `.env.example`

```dotenv
# --- App ---
APP_ENV=development            # development|staging|production
APP_NAME=go-auth-clean
LOG_LEVEL=debug                # debug|info|warn|error
LOG_FORMAT=json                # json|text

# --- HTTP ---
HTTP_ADDR=:8080
HTTP_READ_HEADER_TIMEOUT=5s
HTTP_READ_TIMEOUT=10s
HTTP_WRITE_TIMEOUT=15s
HTTP_IDLE_TIMEOUT=60s
HTTP_SHUTDOWN_TIMEOUT=10s
HTTP_MAX_BODY_BYTES=16384
CORS_ALLOWED_ORIGINS=http://localhost:3000
TRUSTED_PROXIES=               # CIDR dipisah koma, kosong = tidak percaya X-Forwarded-For

# --- Database ---
DB_HOST=localhost
DB_PORT=5432
DB_USER=auth
DB_PASSWORD=auth
DB_NAME=auth
DB_SSLMODE=disable
DB_MAX_CONNS=10
DB_MIN_CONNS=2
DB_MAX_CONN_LIFETIME=1h

# --- Auth / Token ---
JWT_ISSUER=go-auth-clean
JWT_AUDIENCE=go-auth-clean-api
JWT_KEY_ID=k1
JWT_SECRET=                    # base64, >= 32 byte. Generate: openssl rand -base64 48
ACCESS_TOKEN_TTL=15m
REFRESH_TOKEN_TTL=168h         # 7 hari sliding
SESSION_ABSOLUTE_TTL=720h      # 30 hari
OTP_PEPPER=                    # base64, >= 32 byte
OTP_TTL=10m
OTP_MAX_ATTEMPTS=5
OTP_RESEND_COOLDOWN=60s
LOGIN_MAX_ATTEMPTS=5
LOGIN_LOCK_DURATION=15m

# --- Argon2id ---
ARGON2_MEMORY_KIB=65536
ARGON2_TIME=3
ARGON2_THREADS=2
ARGON2_MAX_CONCURRENT=4

# --- Mail ---
SMTP_HOST=localhost
SMTP_PORT=1025
SMTP_USERNAME=
SMTP_PASSWORD=
SMTP_FROM="Go Auth <no-reply@localhost>"

# --- Rate limit ---
RATE_LIMIT_ENABLED=true
```

### 11.4 `.golangci.yml` (golangci-lint v2)

```yaml
version: "2"

run:
  timeout: 5m
  tests: true

linters:
  default: standard            # errcheck, govet, ineffassign, staticcheck, unused
  enable:
    - bodyclose
    - contextcheck
    - errorlint                # pakai errors.Is/As
    - errname                  # ErrXxx / XxxError
    - exhaustive               # switch enum lengkap
    - gosec
    - misspell
    - nilerr
    - noctx                    # http request tanpa context
    - prealloc
    - revive
    - rowserrcheck
    - sqlclosecheck
    - unconvert
    - unparam
    - wrapcheck
    - forbidigo
    - depguard
  settings:
    govet:
      enable:
        - shadow
    forbidigo:
      forbid:
        - pattern: ^fmt\.Print.*$
          msg: gunakan slog
        - pattern: ^log\.(Print|Fatal|Panic).*$
          msg: gunakan slog; exit hanya di main
    depguard:
      rules:
        domain:
          files: ["**/internal/*/domain/**"]
          deny:
            - pkg: net/http
              desc: domain tidak boleh tahu HTTP
            - pkg: github.com/jackc/pgx/v5
              desc: domain tidak boleh tahu database
            - pkg: go-auth-clean/internal/platform
              desc: domain bebas infrastruktur
        app:
          files: ["**/internal/*/app/**"]
          deny:
            - pkg: net/http
              desc: app tidak boleh tahu HTTP
            - pkg: github.com/jackc/pgx/v5
              desc: gunakan port repository
            - pkg: github.com/go-playground/validator/v10
              desc: validator hanya di adapter http
    wrapcheck:
      ignore-sigs:
        - errors.New(
        - fmt.Errorf(
        - .Errorf(
        - status.Error(
  exclusions:
    rules:
      - path: _test\.go
        linters: [gosec, wrapcheck, unparam]

formatters:
  enable:
    - gofumpt
    - goimports
  settings:
    goimports:
      local-prefixes: [go-auth-clean]
```

> `depguard` menegakkan Dependency Rule secara otomatis — pelanggaran arsitektur gagal di CI.

### 11.5 Air (opsional, hot reload) — `.air.toml`

```toml
root = "."
tmp_dir = "tmp"

[build]
  cmd = "go build -o ./tmp/api ./cmd/api"
  bin = "./tmp/api"
  include_ext = ["go", "sql", "tmpl"]
  exclude_dir = ["tmp", "bin", "vendor", "migrations"]
  delay = 500
  kill_delay = "5s"           # beri waktu graceful shutdown
  send_interrupt = true
```

### 11.6 Git hygiene

`.gitignore`:
```gitignore
# Binaries & build
/bin/
/tmp/
*.exe
*.test
*.out
coverage.*

# Env & secrets
.env
.env.*
!.env.example
*.pem
*.key

# IDE / OS
.idea/
.vscode/
.DS_Store

# Generated (opsional: commit gen/ jika tidak generate di CI)
# /gen/
```

**Conventional Commits:**
```
feat(auth): add refresh token rotation with reuse detection
fix(auth): use atomic increment for failed login attempts
refactor(platform): move http error mapping to adapter
test(auth): add testcontainers integration test for user repo
chore(ci): add govulncheck step
docs(prd): add auth PRD
```
- Branch: `feat/auth-login`, `fix/otp-hash`. PR kecil (< 400 baris), satu concern.
- Jangan commit file hasil migrate yang tertukar; review migrasi up/down berpasangan.
- (Opsional) `pre-commit`/`lefthook`: `gofumpt`, `golangci-lint --fast`, `gitleaks protect`.

### 11.7 CI (GitHub Actions) — `.github/workflows/ci.yml`

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - uses: golangci/golangci-lint-action@v8
        with:
          version: latest

  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Unit + integration tests (testcontainers pakai Docker runner)
        run: go test -race -count=1 -tags=integration -coverprofile=coverage.out ./...
      - name: Coverage gate (>= 75%)
        run: |
          total=$(go tool cover -func=coverage.out | awk '/^total:/ {print substr($3, 1, length($3)-1)}')
          echo "coverage: ${total}%"
          awk -v t="$total" 'BEGIN { exit (t < 75) }'

  migrations:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:17-alpine
        env:
          POSTGRES_USER: auth
          POSTGRES_PASSWORD: auth
          POSTGRES_DB: auth
        ports: ["5432:5432"]
        options: >-
          --health-cmd "pg_isready -U auth" --health-interval 5s --health-retries 10
    steps:
      - uses: actions/checkout@v4
      - name: Install migrate
        run: |
          curl -L https://github.com/golang-migrate/migrate/releases/latest/download/migrate.linux-amd64.tar.gz | tar xvz
          sudo mv migrate /usr/local/bin/
      - name: up -> down -> up
        env:
          DB_URL: postgres://auth:auth@localhost:5432/auth?sslmode=disable
        run: |
          migrate -path migrations -database "$DB_URL" up
          migrate -path migrations -database "$DB_URL" down -all
          migrate -path migrations -database "$DB_URL" up

  security:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go run golang.org/x/vuln/cmd/govulncheck@latest ./...
      - uses: gitleaks/gitleaks-action@v2
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}

  build:
    needs: [lint, test, migrations]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: CGO_ENABLED=0 go build -trimpath -o bin/api ./cmd/api
      # (nanti) docker build multi-stage → distroless, push ke registry
```

---

## 12. Lampiran: Perbaikan dari Kode Lama

| # | Masalah di kode lama | Requirement baru | Section |
|---|---|---|---|
| 1 | File migrasi up/down tertukar | Up = create, down = drop; CI menjalankan up→down→up | §3.1, §11.7 |
| 2 | Pakai extension `uuid-ossp` | UUIDv7 dari app via `IDGenerator`; `gen_random_uuid()` bila perlu default | §3.1 |
| 3 | Email case-sensitive | `CITEXT` + lowercase di `domain.NewEmail` + partial unique index | §3.2 |
| 4 | Refresh token plaintext | Simpan `SHA-256` saja | §5.5 |
| 5 | Tidak ada rotation/reuse detection | `family_id`, `parent_id`, `used_at`; reuse → revoke family | F05, §5.4 |
| 6 | Timing enumeration di login | Dummy hash compare | F04 |
| 7 | Race pada counter brute-force | `UPDATE ... SET x = x + 1 ... RETURNING` | F04, §3.3 |
| 8 | Error update counter diabaikan | Log `WARN` | F04, §10.5 |
| 9 | Signature `Login` tidak cocok interface (3 return) | `(LoginResult, error)` + `var _ Iface = (*Impl)(nil)` | Sprint 4, §10.22 |
| 10 | JWT secret tidak jelas | Dari env, ≥ 32 byte, gagal start jika kurang; `kid` | §5.2 |
| 11 | Claims minim | `iss`, `aud`, `sub`, `jti`, `sid`, `iat`, `nbf`, `exp` dengan `RegisteredClaims`; `WithValidMethods` | §5.2 |
| 12 | `apperror` terikat `net/http` | Domain error murni; mapping di adapter | §4.3 |
| 13 | Repo membungkus error dengan HTTP error | Return `domain.ErrXxx` atau `fmt.Errorf("userRepo.Op: %w", err)` | Sprint 2 |
| 14 | Pre-check email sebelum insert (race) | Tangkap `23505` | Sprint 2 |
| 15 | `updated_at` tidak di-SELECT | Select semua kolom yang dipetakan ke entity | Sprint 2 |
| 16 | `is_active` boolean | `status` (pending_verification, active, suspended, deleted) + `email_verified_at`, `password_changed_at`, `deleted_at` | §2.2, §3.2 |
| 17 | OTP plaintext | HMAC-SHA256 + pepper, attempts/max_attempts, satu aktif per purpose, constant-time compare, cooldown resend | F02, F03, §3.2 |

### Definition of Done global (setiap PR)

- [ ] `make lint test test-integration` hijau; `-race` tanpa temuan.
- [ ] Tidak ada import terlarang (depguard).
- [ ] Setiap implementasi port punya compile-time assertion.
- [ ] Error di-wrap dengan konteks operasi; log hanya di edge.
- [ ] Tidak ada secret/token/password/OTP di log (review + test).
- [ ] Migrasi baru punya pasangan down dan lulus up→down→up.
- [ ] OpenAPI diperbarui untuk perubahan endpoint.
- [ ] Commit mengikuti Conventional Commits.

---

*Akhir dokumen PRD-01 Auth. Modul berikutnya: PRD-02 Finance (mengikuti §0 Shared Conventions).*
