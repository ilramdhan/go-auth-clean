# go-auth-clean

REST API **Auth + Personal Finance** ditulis dengan Go 1.26, PostgreSQL 17, dan
arsitektur *modular monolith* (package-by-bounded-context, Clean Architecture/DDD).
Proyek belajar dengan target akhir: gRPC, DDD, observability.

---

## Daftar Isi

1. [Overview](#overview)
2. [Arsitektur](#arsitektur)
3. [Prasyarat](#prasyarat)
4. [Quick Start](#quick-start)
5. [Target Makefile](#target-makefile)
6. [Testing](#testing)
7. [Swagger / OpenAPI (handoff ke Frontend)](#swagger--openapi-handoff-ke-frontend)
8. [Environment Variable Utama](#environment-variable-utama)
9. [Menjadikan User Admin](#menjadikan-user-admin)
10. [Catatan Deployment](#catatan-deployment)
11. [Roadmap ke gRPC](#roadmap-ke-grpc)
12. [Dokumen Terkait](#dokumen-terkait)

---

## Overview

| Modul       | Fitur utama |
|-------------|-------------|
| **Auth**    | Register + verifikasi email (OTP 6 digit), login, JWT access token + refresh token dengan *rotation* & *reuse detection*, logout / logout-all, lupa & reset password, 2FA TOTP + recovery code, Google OAuth (opsional), API key, manajemen sesi/device, security events, admin (suspend, role) |
| **Finance** | Account (cash/bank/ewallet/credit card), kategori (seed sistem + custom), transaksi income/expense, transfer antar akun (+ fee), budget bulanan + progress, recurring rule, tag, multi-currency + kurs, savings goal, hutang/piutang, tagihan, shared account (member), import/export CSV, laporan (summary bulanan, cashflow, kategori, tahunan, rekonsiliasi), audit log |

Base path API: `/api/v1`. Health check: `GET /healthz` (proses hidup), `GET /readyz` (DB bisa di-ping).

---

## Arsitektur

```
cmd/
  api/            # main.go + router.go: wiring manual semua dependency (tanpa DI framework)
  promote/        # CLI: jadikan user admin
internal/
  platform/       # infrastruktur lintas modul, TANPA logika bisnis
    authctx/      #   identitas user di context.Context (dipakai auth & finance)
    clock/        #   abstraksi waktu (mudah di-mock di test)
    config/       #   load + validasi env
    crypto/       #   AES-256-GCM (secretbox)
    database/     #   pgxpool, helper transaksi, dbtest (testcontainers)
    httpx/        #   envelope response & error JSON
    logger/       #   slog
    mailer/       #   SMTP
    middleware/   #   request id, access log, recover, CORS, security header, client IP, rate limit
    requestid/
    validator/
  auth/           # bounded context Auth
    domain/       #   entity, value object, aturan bisnis murni (tanpa HTTP/DB)
    app/          #   use case + port (interface) yang dibutuhkan use case
    adapter/
      http/       #   handler, DTO, mapping error -> status code
      postgres/   #   implementasi repository
      security/   #   bcrypt, JWT, TOTP, OIDC, OTP
      email/      #   template + pengiriman email notifikasi
  finance/        # bounded context Finance
    domain/       #   Account, Transaction, Transfer, Budget, Money, ...
    app/          #   use case + port, idempotency, worker recurring
    adapter/
      http/
      postgres/
    module.go     #   satu pintu: merakit modul finance & mendaftarkan route
  shared/         # tipe kecil yang murni & stabil, boleh dipakai semua layer
    money/        #   uang sebagai integer minor unit + parsing string
    pagination/   #   cursor keyset (opaque, base64)
migrations/       # SQL golang-migrate (up/down)
docs/swagger/     # hasil generate swaggo (JSON, YAML, Go)
scripts/          # smoke test, dll.
notes/            # PRD & catatan belajar
```

### Aturan dependency (The Dependency Rule)

Panah = "boleh import". Dependency selalu mengarah **ke dalam** (ke `domain`):

```
cmd/api  ──►  adapter/*  ──►  app  ──►  domain
                 │            │           ▲
                 └──► platform │           │
                              └──► shared ─┘ (shared juga boleh dipakai domain)
```

- `domain` **tidak boleh** import `app`, `adapter`, `platform`, `net/http`, atau `pgx`.
- `app` hanya import `domain`, `shared`, dan stdlib. Interface (port) dideklarasikan di `app` (sisi *consumer*).
- `adapter/*` boleh import `app`, `domain`, dan `platform`.
- `cmd/api` adalah satu-satunya tempat yang "tahu semuanya".

### Kenapa `finance` tidak import `auth`?

Finance hanya butuh satu hal dari auth: **siapa user yang sedang login** (`user_id`).
Itu disediakan oleh `internal/platform/authctx` (middleware auth menaruh
`Identity{UserID, SessionID}` di `context.Context`, finance membacanya).
Router menyuntikkan middleware auth ke `finance.RegisterRoutes(mux, authn.User)`.

Keuntungannya:

- **Batas bounded context jelas**: perubahan internal auth (tabel users, JWT, 2FA) tidak memaksa finance ikut berubah.
- **Tidak ada import cycle**, dan tiap modul bisa di-test sendiri.
- **Siap dipecah** menjadi service terpisah: cukup ganti cara `user_id` masuk ke context (mis. dari metadata gRPC) tanpa menyentuh domain finance.

---

## Prasyarat

- Go **1.26+**
- Docker + Docker Compose (PostgreSQL, Mailpit, dan test integrasi)
- `migrate` CLI dan `golangci-lint`: `make tools`
- `curl` + `jq` (untuk `make smoke`)
- `swag` sudah terdaftar sebagai Go tool (`go tool swag`), tidak perlu install terpisah.

---

## Quick Start

```bash
cp .env.example .env       # sesuaikan bila perlu (JWT_SECRET & ENCRYPTION_KEY wajib diganti di luar dev)
make db-up                 # PostgreSQL di localhost:5432
make mail-up               # Mailpit: SMTP :1025, web UI http://localhost:8025
make migrate-up            # jalankan semua migration
make run                   # API di http://localhost:8080
```

Cek:

```bash
curl -i http://localhost:8080/healthz
open http://localhost:8080/docs/            # Swagger UI
make smoke                                  # smoke test end-to-end (server harus jalan)
```

Email (OTP verifikasi, reset password) tidak benar-benar terkirim di dev: semuanya
ditangkap Mailpit dan bisa dilihat di http://localhost:8025.

> Port bentrok? Ubah `DB_PORT`, `MAILPIT_SMTP_PORT`, `MAILPIT_UI_PORT` di `.env`.

---

## Target Makefile

Jalankan `make help` untuk daftar terbaru.

| Target | Fungsi |
|--------|--------|
| `make help` | Tampilkan daftar target |
| `make run` | Jalankan API lokal (`go run ./cmd/api`) |
| `make build` | Build binary statis ke `bin/api` |
| `make test` | Unit test saja (`-short`, tanpa Docker) |
| `make test-integration` | Semua test termasuk integrasi (butuh Docker) |
| `make cover` | Test + ringkasan coverage (`coverage.out`) |
| `make lint` | golangci-lint v2 |
| `make fmt` | `gofmt` + `swag fmt` |
| `make tidy` | `go mod tidy` |
| `make swagger` | Generate OpenAPI ke `docs/swagger` dari anotasi |
| `make swagger-check` | Gagal jika `docs/swagger` belum di-regenerate |
| `make tools` | Install `golangci-lint` dan `migrate` |
| `make db-up` / `make db-down` | Start PostgreSQL / stop semua container compose |
| `make mail-up` / `make mail-ui` | Start Mailpit / tampilkan URL web UI |
| `make up` | Start semua service dev (postgres + mailpit) |
| `make migrate-up` / `make migrate-down` | Apply semua migration / rollback 1 langkah |
| `make migrate-create name=create_xxx` | Buat pasangan file migration baru |
| `make docker-build` | Build image produksi (`IMAGE=go-auth-clean:local`) |
| `make promote EMAIL=...` | Jadikan user admin |
| `make smoke` | Smoke test E2E ke server yang berjalan (`BASE_URL`, `MAILPIT_URL`) |

---

## Testing

| Jenis | Perintah | Butuh Docker? | Keterangan |
|-------|----------|---------------|------------|
| Unit | `make test` | Tidak | `go test -short -race`. Domain & use case diuji dengan *fake* repository (in-memory). |
| Integrasi | `make test-integration` | Ya | Repository Postgres & handler diuji ke **PostgreSQL sungguhan** via [testcontainers-go](https://golang.testcontainers.org/) (`internal/platform/database/dbtest`). Container dibuat otomatis lalu migration dijalankan. Test ini otomatis di-skip saat `-short`. |
| Coverage | `make cover` | Ya | Menulis `coverage.out` + ringkasan total. Detail HTML: `go tool cover -html=coverage.out`. |
| Smoke E2E | `make smoke` | Server + DB + Mailpit | `scripts/smoke.sh`: register, verifikasi OTP via Mailpit, login, akun, transaksi, transfer, budget, CSV, laporan, rotasi refresh token, IDOR, logout. Mencetak `PASS`/`FAIL` per langkah, exit non-zero bila gagal. |

Contoh menjalankan satu test: `go test -run TestTransfer ./internal/finance/...`

CI (`.github/workflows/ci.yml`) menjalankan lint, test (unit + integrasi), cek drift swagger, dan build.

---

## Swagger / OpenAPI (handoff ke Frontend)

### Cara kerja anotasi (swaggo)

Dokumentasi ditulis sebagai **komentar di atas handler** dan dibaca oleh
[swaggo/swag](https://github.com/swaggo/swag):

- Info global (`@title`, `@version`, `@BasePath /api/v1`, `@securityDefinitions`) ada di `cmd/api/main.go`.
- Tiap handler punya blok seperti ini:

```go
// Create godoc
//
//	@Summary		Create budget
//	@Description	Monthly budget for an expense category. Requires Idempotency-Key.
//	@Tags			budgets
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			Idempotency-Key	header		string					true	"Unique key per logical request (8-128 chars)"
//	@Param			body			body		CreateBudgetRequest		true	"Budget"
//	@Success		201				{object}	BudgetEnvelope
//	@Failure		409				{object}	httpx.ErrorResponse	"BUDGET_EXISTS"
//	@Router			/budgets [post]
```

- Struct DTO memakai tag `example:"..."`, `enums:"..."`, `binding/validate` agar contoh & skema di spec akurat.

### Generate & lihat

```bash
make swagger    # -> docs/swagger/swagger.json, swagger.yaml, docs.go
```

- **Swagger UI**: http://localhost:8080/docs/ (aktif bila `SWAGGER_ENABLED=true`; default `true` kecuali `APP_ENV=production`).
- **Spec mentah (JSON)**: http://localhost:8080/docs/doc.json
- **CI**: `make swagger-check` me-regenerate lalu gagal (`git diff --exit-code`) jika spec yang di-commit tidak sama dengan anotasi. Jadi setiap mengubah handler/DTO: jalankan `make swagger` dan commit hasilnya.

### Yang diserahkan ke tim FE

Pilih salah satu:

1. File **`docs/swagger/swagger.yaml`** atau **`docs/swagger/swagger.json`** (paling stabil, ikut versi git).
2. URL **`<BASE_URL>/docs/doc.json`** dari environment dev/staging (selalu sinkron dengan server yang berjalan).

Spec berformat **Swagger 2.0 (OpenAPI v2)**. Sebagian tool hanya menerima OpenAPI 3; konversi bila perlu:

```bash
npx swagger2openapi docs/swagger/swagger.yaml -o openapi3.yaml
```

### Import ke Postman / Insomnia

- **Postman**: *Import* → pilih file `swagger.yaml`/`swagger.json` atau tempel URL `/docs/doc.json` → *Generate collection*. Set variable `baseUrl = http://localhost:8080/api/v1`, lalu di tab *Authorization* collection pilih *Bearer Token* = `{{accessToken}}`.
- **Insomnia**: *Create* → *Import* → *From File / From URL* → pilih spec. Insomnia membuat request per endpoint; isi environment `base_url` dan token.

### Generate typed client

```bash
# 1) openapi-typescript: hanya tipe TS (ringan), dipakai bersama fetch/openapi-fetch
npx swagger2openapi docs/swagger/swagger.yaml -o openapi3.yaml
npx openapi-typescript openapi3.yaml -o src/api/schema.d.ts

# 2) orval: tipe + hook React Query / SWR / axios
npx orval --input docs/swagger/swagger.yaml --output src/api/client.ts
#    atau pakai orval.config.ts: { api: { input: './swagger.yaml', output: { client: 'react-query', target: 'src/api' } } }

# 3) openapi-generator: banyak bahasa (typescript-fetch, typescript-axios, dart, kotlin, swift, ...)
npx @openapitools/openapi-generator-cli generate \
  -i docs/swagger/swagger.yaml -g typescript-fetch -o src/api/generated
```

### Konvensi yang WAJIB diketahui FE

**1. Envelope response sukses**

```json
{ "data": { ... } }                                        // objek tunggal
{ "data": [ ... ], "meta": { "next_cursor": "eyJ0Ijo...", "has_more": true } }   // list
```

`204 No Content` tidak punya body (mis. logout, delete).

**2. Format error**

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "input tidak valid",
    "details": [{ "field": "email", "message": "harus berupa email yang valid" }]
  },
  "request_id": "0192f0c1-7b3a-7c4e-9d2a-1f2e3d4c5b6a"
}
```

- Logika FE sebaiknya bercabang berdasarkan **`error.code`** (stabil), bukan `message` (teks Indonesia, bisa berubah).
- `details` hanya ada untuk error validasi. Sertakan `request_id` (juga di header `X-Request-ID`) saat lapor bug.
- Kode umum: `VALIDATION_FAILED` (422), `INVALID_JSON` (400), `UNAUTHENTICATED` (401), `FORBIDDEN` (403), `NOT_FOUND` / `*_NOT_FOUND` (404), `EMAIL_TAKEN`, `DUPLICATE_NAME`, `VERSION_CONFLICT`, `IDEMPOTENCY_IN_PROGRESS` (409), `INSUFFICIENT_BALANCE` (422), `RATE_LIMITED` (429), `INTERNAL` (500). Auth: `INVALID_CREDENTIALS`, `EMAIL_NOT_VERIFIED`, `INVALID_OTP`, `OTP_EXPIRED`, `TOKEN_REUSED`, `SESSION_INVALID`, `MFA_TOKEN_INVALID`, `INVALID_MFA_CODE`. Daftar lengkap per endpoint ada di deskripsi response tiap endpoint di Swagger.

**3. Uang = string dalam major unit**

- Semua nominal dikirim & diterima sebagai **string desimal** di *major unit*: `"1500000"` (Rp1.500.000), `"31.25"` (USD 31,25).
- **Jangan** pakai `number` JS untuk menghitung uang (presisi float). Tampilkan apa adanya atau pakai library desimal (`decimal.js`, `big.js`, `dinero.js`).
- Mata uang dalam kode ISO 4217 (`IDR`, `USD`, ...). Daftar: `GET /currencies`.

**4. Autentikasi**

```
Authorization: Bearer <access_token>
```

- `POST /auth/login` → `access_token` (JWT, umur `ACCESS_TOKEN_TTL`, default 15 menit), `refresh_token`, `expires_in`, `session_id`.
- Beberapa endpoint juga menerima `X-API-Key` (lihat `ApiKeyAuth` di Swagger).

**5. Refresh token rotation**

- `POST /auth/refresh` dengan `{"refresh_token": "..."}` → **access token baru DAN refresh token baru**. Simpan yang baru, buang yang lama.
- Refresh token lama yang dipakai lagi → `401 TOKEN_REUSED`, dan **seluruh sesi itu dicabut** (deteksi pencurian token). User harus login ulang.
- Hati-hati race di FE: bila beberapa request gagal 401 bersamaan, lakukan **satu** refresh saja (pakai lock/promise bersama), lalu ulangi request lain dengan token baru.

**6. Idempotency-Key**

- Wajib untuk POST yang menggerakkan uang: `POST /transactions`, `/transfers`, `/budgets`, pembayaran tagihan/hutang, kontribusi goal, dll. (lihat parameter header di Swagger).
- Isi dengan UUID baru **per aksi user** (8–128 karakter ASCII). Kirim ulang dengan key yang **sama** bila retry karena timeout/jaringan: server mengembalikan hasil yang sama tanpa membuat data ganda (response replay membawa header `Idempotent-Replayed: true`).
- Key yang sama dengan body berbeda → `422 IDEMPOTENCY_KEY_REUSED`; request yang masih diproses → `409 IDEMPOTENCY_IN_PROGRESS`.

**7. ETag / If-Match (optimistic locking)**

- `GET` resource yang bisa diubah (account, budget, transaksi, dll.) mengirim header `ETag: "3"` (= field `version`).
- Saat `PATCH`/`PUT`, kirim `If-Match: "3"`. Bila data sudah diubah orang/tab lain → `409 VERSION_CONFLICT`: muat ulang data, tampilkan ke user, lalu coba lagi.
- Tanpa `If-Match`, server tidak mengecek versi (last write wins).

**8. Pagination keyset (cursor)**

- Query: `?limit=20&cursor=<meta.next_cursor>`.
- Halaman berikutnya: kirim `meta.next_cursor` dari response sebelumnya selama `meta.has_more == true`.
- Cursor itu **opaque**: jangan di-parse atau dibuat sendiri. Cursor terikat ke filter; mengubah filter (tanggal, akun, dll.) = mulai lagi tanpa cursor (cursor dari filter lain → `400 INVALID_CURSOR`).
- Tidak ada "lompat ke halaman N"; cocok untuk infinite scroll.

**9. Alur login dengan 2FA**

```
POST /auth/login {email, password}
  ├─ 200 { data: { access_token, refresh_token, ... } }          -> selesai
  └─ 200 { data: { mfa_required: true, mfa_token: "..." } }     -> tampilkan input kode
        POST /auth/login/2fa { mfa_token, code }   // code = 6 digit TOTP atau recovery code
          └─ 200 { data: { access_token, refresh_token, ... } }
```

Cek `data.mfa_required` dulu sebelum mencari `access_token`. `mfa_token` berumur pendek; bila kedaluwarsa (`MFA_TOKEN_INVALID`) ulangi dari login.

**10. Rate limit**

- Melebihi limit → `429 RATE_LIMITED` dengan header **`Retry-After: <detik>`**. Tunggu sebanyak itu sebelum mencoba lagi; jangan retry agresif.
- Endpoint auth publik (`POST /auth/*`: login, register, verify, forgot, dll.) punya limit lebih ketat (default 10/menit/IP, burst 5) dibanding limit global (300/menit/IP).

---

## Environment Variable Utama

Daftar lengkap + default ada di `.env.example`.

| Variable | Contoh / default | Keterangan |
|----------|------------------|------------|
| `APP_ENV` | `development` | `production` mengaktifkan cookie secure, HSTS, dan mematikan Swagger default |
| `HTTP_ADDR` | `:8080` | Alamat listen |
| `LOG_LEVEL` | `debug` / `info` | Level slog |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSL_MODE` | `localhost`, `5432`, ... | Koneksi PostgreSQL |
| `JWT_SECRET` | min. 32 karakter | `openssl rand -base64 48`. **Wajib diganti** di luar dev |
| `ACCESS_TOKEN_TTL`, `REFRESH_TOKEN_TTL` | `15m`, `168h` | Umur token |
| `ENCRYPTION_KEY` | 32 byte base64 | AES-256-GCM (secret TOTP, dll.). `openssl rand -base64 32` |
| `APP_BASE_URL`, `FRONTEND_URL` | `http://localhost:8080`, `:3000` | Dipakai untuk link di email & redirect OAuth |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:3000,...` | Origin eksplisit, tidak boleh `*` |
| `TRUSTED_PROXIES` | kosong | CIDR proxy/LB tepercaya; kosong = `X-Forwarded-For` diabaikan |
| `RATE_LIMIT_*` | lihat `.env.example` | Limit global & auth (token bucket in-memory) |
| `SMTP_*` | `localhost:1025` | Dev: Mailpit |
| `OTP_TTL`, `OTP_MAX_ATTEMPTS`, `PASSWORD_RESET_TTL` | `10m`, `5`, `30m` | OTP & reset |
| `GOOGLE_OAUTH_*` | kosong | Isi semua untuk mengaktifkan login Google |
| `SWAGGER_ENABLED` | `true` | UI di `/docs/` |
| `DEFAULT_CURRENCY`, `DEFAULT_TIMEZONE` | `IDR`, `Asia/Jakarta` | Default setting user finance |
| `RECURRING_WORKER_INTERVAL` | `5m` | Interval worker recurring transaction |

> Jangan commit `.env`. Secret produksi disimpan di secret manager / env platform deploy.

---

## Menjadikan User Admin

User baru selalu berperan `user`. Untuk memberi peran admin (akses `/api/v1/admin/*`):

```bash
make promote EMAIL=budi@example.com
```

Perintah ini menjalankan `cmd/promote` langsung ke database (memakai env DB dari `.env`).
Setelah itu admin bisa memberi/mencabut role user lain lewat `POST /admin/users/{id}/roles`.
User perlu login ulang (atau refresh token) agar role baru terbaca di token.

---

## Catatan Deployment

- **Docker image**: `make docker-build` (multi-stage, binary statis, runtime `distroless/static:nonroot`, tanpa shell). Image juga membawa folder `/app/migrations`.
- **Migration**: jalankan sebagai langkah/job terpisah **sebelum** instance baru menerima traffic, mis.:
  ```bash
  migrate -path migrations -database "$DB_URL" up
  ```
  Jangan jalankan migration dari setiap instance aplikasi secara bersamaan.
- **Health check**: liveness → `/healthz`, readiness → `/readyz`.
- **Banyak instance**: rate limiter disimpan **in-memory per instance**. Dengan N instance di belakang load balancer, limit efektif ≈ N × limit konfigurasi. Untuk limit global yang akurat, ganti implementasi `middleware.RateLimiter` dengan backend bersama (mis. Redis) atau pasang rate limit di API gateway/LB.
- **Di belakang proxy**: isi `TRUSTED_PROXIES` agar IP client (untuk rate limit & audit) diambil dengan benar dari `X-Forwarded-For`.
- **Produksi**: set `APP_ENV=production`, ganti `JWT_SECRET` & `ENCRYPTION_KEY`, `DB_SSL_MODE=require`, SMTP sungguhan dengan `SMTP_REQUIRE_TLS=true`, dan `SWAGGER_ENABLED=false` bila spec tidak boleh publik.
- **Graceful shutdown**: server menunggu request aktif selesai hingga `HTTP_SHUTDOWN_TIMEOUT`.

---

## Roadmap ke gRPC

Arsitektur sudah disiapkan agar gRPC hanya menambah **adapter baru**, bukan menulis ulang:

1. **Definisi proto**: `proto/auth/v1/auth.proto`, `proto/finance/v1/finance.proto` (sketsa mapping REST ↔ RPC ada di PRD). Kelola dengan `buf` (lint + breaking check).
2. **Adapter** `internal/auth/adapter/grpc` dan `internal/finance/adapter/grpc` di sebelah `adapter/http`, memanggil use case `app` yang **sama**.
3. **Mapping error**: error domain → `codes.*` (`NotFound`, `InvalidArgument`, `Unauthenticated`, `AlreadyExists`, `FailedPrecondition`, `ResourceExhausted`), sejajar dengan mapping HTTP yang sudah ada.
4. **Interceptor**: auth (baca JWT dari metadata → `authctx`), logging, recover, request id, rate limit: padanan middleware HTTP saat ini.
5. **Gateway** (opsional): `grpc-gateway` atau Connect agar REST & gRPC dilayani dari satu definisi.
6. **Pecah service** (opsional): finance cukup memvalidasi JWT/memanggil auth via gRPC karena sudah tidak meng-import package auth.

---

## Dokumen Terkait

- [PRD-01 Auth](notes/prd-01-auth.md): fitur auth, aturan dependency, kontrak API + mapping gRPC.
- [PRD-02 Finance](notes/prd-02-finance.md): domain finance, desain data, transaksi DB & concurrency, idempotency, reporting.
- [Cara membuat migration](notes/how-to-make-migrations.md)
