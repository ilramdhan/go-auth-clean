# Frontend Handover: Prompt untuk AI Coding Agent (Repo Frontend Baru)

> Dokumen ini adalah **prompt lengkap dan mandiri**. Tempel seluruh isinya ke AI coding agent (Claude Code)
> yang bekerja di **repo frontend baru yang terpisah**. Agent frontend **tidak punya akses** ke repo backend;
> satu-satunya sumber lain adalah file `swagger.yaml` (Swagger 2.0) yang akan disalin ke repo frontend.
> Semua fakta API di bawah diambil dari backend `go-auth-clean` (spec: 82 path, 122 operasi).

---

## 0. Cara pakai prompt ini

### 0.1 Untuk pemilik proyek (manusia)

1. Buat repo kosong, mis. `go-auth-clean-web`.
2. Salin `docs/swagger/swagger.yaml` dari repo backend ke `openapi/swagger.yaml` di repo frontend.
3. Buka Claude Code di repo frontend, tempel seluruh dokumen ini sebagai pesan pertama.
4. Jalankan backend lokal (lihat Bagian 12) supaya agent bisa menguji ke API sungguhan.

### 0.2 Instruksi wajib untuk AI agent frontend

Kamu adalah senior frontend engineer. Bangun aplikasi web untuk API yang dijelaskan dokumen ini.
Aturan kerja:

- **Kerja SUB-AGENT DRIVEN / pakai workflow untuk menghemat token dan context.**
  - Pecah pekerjaan per fase (Bagian 13). Di tiap fase, delegasikan tiap fitur/concern ke **sub-agent dengan scope sempit**
    (mis. "implementasikan `features/budgets` sesuai Bagian 7.10 + 8.13, sertakan test").
  - Beri sub-agent hanya potongan dokumen yang relevan (nomor bagian + aturan global Bagian 3–5), bukan seluruh dokumen.
  - Minta sub-agent mengembalikan **laporan singkat** (≤ 15 baris): file yang diubah, keputusan, test yang ditambah, hal yang belum selesai.
  - Context utama (orchestrator) tetap kecil: simpan hanya rencana, status fase, dan keputusan arsitektur. Jangan membaca ulang file besar di context utama bila sub-agent bisa melakukannya.
  - Sub-agent yang independen (mis. `features/debts` dan `features/bills`) boleh jalan paralel; yang menyentuh `shared/` dijalankan berurutan.
- **Tanya dulu sebelum keputusan ambigu** (mis. desain visual brand, nama domain produksi, apakah Google OAuth aktif). Jangan menebak kontrak API: bila spec dan dokumen ini berbeda, **spec `swagger.yaml` menang**, lalu laporkan perbedaannya.
- **Commit per concern** dengan Conventional Commits: `feat(transactions): infinite list with filters`, `fix(auth): single-flight refresh`, `test(budgets): msw progress states`, `chore(ci): add playwright job`, `docs: ...`, `refactor(shared): ...`. Satu commit = satu concern yang lolos lint + test.
- Sebelum menutup fase: jalankan `pnpm lint && pnpm typecheck && pnpm test --coverage && pnpm build`, dan e2e bila fase menyentuh alur utama.
- Jangan pernah menaruh secret di `NEXT_PUBLIC_*`, jangan menyimpan token di `localStorage`/`sessionStorage`.
- Bahasa UI default **Bahasa Indonesia**; kode, identifier, nama file, commit: bahasa Inggris.

---

## 1. Ringkasan produk dan persona

**Produk**: aplikasi web *Personal Finance* dengan akun pengguna yang aman. Backend menyediakan:

| Modul | Kemampuan |
|---|---|
| Auth | Register + verifikasi email (OTP 6 digit), login, JWT access token (default 15 menit) + refresh token rotasi dengan *reuse detection*, logout / logout semua perangkat, lupa & reset password, 2FA TOTP + 10 recovery code, Google OAuth (opsional, bisa nonaktif), API key, daftar sesi/perangkat, riwayat security event, hapus akun |
| Finance | Akun (cash/bank/ewallet/credit_card), kategori (sistem + custom, 1 level sub), transaksi income/expense, tag, transfer antar akun (+ fee, lintas mata uang), budget bulanan + progress, recurring rule, multi-currency + kurs manual, savings goal, hutang/piutang, tagihan (bill reminder), shared wallet (member viewer/editor), import/export CSV, laporan (summary bulanan, cashflow, kategori, tahunan, rekonsiliasi), audit log |
| Admin | Daftar user, detail, suspend/aktifkan, beri/cabut role admin, security event per user |

**Persona**

| Persona | Kebutuhan | Area UI |
|---|---|---|
| **User** (role `user`, semua orang) | Mencatat pemasukan/pengeluaran cepat (mobile), melihat saldo & sisa budget, laporan bulanan, berbagi dompet dengan pasangan/keluarga, keamanan akun (2FA, sesi) | `/dashboard`, `/transactions`, `/accounts`, `/budgets`, `/reports`, `/settings/*` |
| **Anggota shared wallet** (user yang diundang) | `viewer`: hanya lihat; `editor`: boleh mencatat transaksi/transfer di akun itu | `/shared`, `/accounts/[id]` dengan UI read-only untuk viewer |
| **Admin** (role `admin` di `user.roles`) | Moderasi: cari user, suspend/aktifkan, kelola role, investigasi security event | `/admin/users`, `/admin/users/[id]` |

Admin pertama dibuat dari backend (`make promote EMAIL=...`); role baru baru terbaca setelah token di-refresh / login ulang.

---

## 2. Tech stack (sudah diputuskan)

| Area | Pilihan | Catatan |
|---|---|---|
| Framework | **Next.js (App Router, versi stabil terbaru, 16.x per Oktober 2026)** + React 19 + **TypeScript `strict`** (+ `noUncheckedIndexedAccess`) | Output `standalone`. Di Next 16 file `middleware.ts` bernama **`proxy.ts`**; cek dokumentasi versi yang terpasang. |
| Package manager | **pnpm** | `packageManager` dipin di `package.json`, Node 22 LTS/24 LTS |
| UI | **shadcn/ui** + **Tailwind CSS v4** + `lucide-react` | Komponen disalin ke `src/shared/ui` |
| Server state | **TanStack Query v5** | Query key factory per feature, `useInfiniteQuery` untuk list cursor |
| Form | **React Hook Form** + **Zod** (v4) + `@hookform/resolvers` | Schema per feature di `schemas/` |
| Tipe API | **openapi-typescript + openapi-fetch** (keputusan, lihat 2.1) | Spec Swagger 2.0 dikonversi dulu dengan `swagger2openapi` |
| i18n | **next-intl** | `id` default (tanpa prefix), `en` opsional |
| Tanggal | **date-fns v4** + **`@date-fns/tz`** (`TZDate`) | Timezone dari `GET /settings`, bukan dari browser |
| Uang | **big.js** untuk aritmetika, `Intl.NumberFormat` untuk tampilan | Tidak pernah `parseFloat` untuk hitung |
| Chart | **Recharts** via komponen **shadcn charts** | |
| Session BFF | `jose` (enkripsi/sign cookie) + **Redis** (`ioredis`) sebagai session store produksi | In-memory store untuk dev/test |
| Unit/komponen test | **Vitest** + **Testing Library** + **MSW v2** | Handler MSW di-generate dari spec (`msw-auto-mock`) + typed override (`openapi-msw`) |
| E2E | **Playwright** | Ke backend sungguhan via docker compose, OTP dibaca dari Mailpit API |
| Kualitas | **ESLint** (flat config, `next/core-web-vitals`, `typescript-eslint` strict, `eslint-plugin-boundaries` atau `import/no-restricted-paths`), **Prettier** (+ `prettier-plugin-tailwindcss`) | |
| Git hooks | **Husky** + **lint-staged** + commitlint (`@commitlint/config-conventional`) | |
| Container | **Dockerfile** multi-stage (`output: 'standalone'`, user non-root) + **docker-compose** | |
| CI | **GitHub Actions**: install → lint → typecheck → unit (coverage) → build → e2e (compose) | |
| Coverage | Vitest v8 coverage, threshold **≥ 80% lines** untuk `src/features/**` dan `src/shared/**` | |

### 2.1 Keputusan generator tipe: openapi-typescript + openapi-fetch

Dipilih **openapi-typescript + openapi-fetch** (bukan orval) karena:

1. **Arsitektur BFF** (Bagian 4): request dari browser ke Next server, lalu Next server ke backend. Client yang tipis dan isomorphic (jalan di route handler server *dan* di browser) lebih cocok daripada hook hasil generate yang mengasumsikan satu base URL.
2. **Kontrol penuh atas header lintas-cutting**: `Idempotency-Key` per submit, `If-Match` dari `version`, retry 429 dengan `Retry-After`, refresh 401 single-flight. Dengan openapi-fetch ini cukup satu *middleware* client; dengan orval harus lewat custom mutator dan hasil generate sulit disesuaikan per endpoint.
3. **Spec tidak sempurna untuk generator hook**: `POST /auth/login` dan `POST /auth/oauth/exchange` bisa mengembalikan *dua bentuk* `200` (token **atau** `mfa_required`), tetapi spec hanya mendeklarasikan `auth.LoginEnvelope`. Multipart import, CSV download, dan redirect OAuth juga perlu penanganan manual. Hook TanStack Query ditulis tangan (tipis) di tiap `features/<x>/api` dengan tipe dari `paths`.
4. Bundle kecil (~6 kB), tanpa runtime codegen, regenerasi cepat (`pnpm gen:api`).

Langkah generate (dipakai script `gen:api`, Bagian 12):

```bash
pnpm dlx swagger2openapi openapi/swagger.yaml -o openapi/openapi3.yaml   # Swagger 2.0 -> OpenAPI 3
pnpm openapi-typescript openapi/openapi3.yaml -o src/shared/api/schema.d.ts
pnpm msw-auto-mock openapi/openapi3.yaml -o src/test/msw/generated --typescript   # baseline handler mock
```

Commit `openapi3.yaml`, `schema.d.ts`, dan mock hasil generate; CI memastikan tidak ada drift (`git diff --exit-code` setelah `pnpm gen:api`).

---

## 3. Arsitektur dan struktur folder

Pendekatan **feature-sliced (vertical slice)**: kode dikelompokkan per domain bisnis, dengan lapisan bersama yang generik.
Ini pola yang dipakai banyak perusahaan teknologi untuk codebase React yang tumbuh: mudah dicari, mudah dihapus, DRY tanpa coupling silang.

### 3.1 Struktur lengkap

```text
.
├── openapi/
│   ├── swagger.yaml              # salinan dari backend (sumber kebenaran)
│   └── openapi3.yaml             # hasil swagger2openapi (generated)
├── e2e/                          # Playwright
│   ├── fixtures/                 # login helper, mailpit client, seed via API
│   ├── auth.spec.ts
│   ├── transactions.spec.ts
│   └── ...
├── public/
├── messages/                     # next-intl
│   ├── id.json
│   └── en.json
├── src/
│   ├── app/                                  # ROUTING SAJA: tipis, tanpa logika bisnis
│   │   ├── [locale]/
│   │   │   ├── (public)/                     # layout tanpa sidebar
│   │   │   │   ├── login/page.tsx
│   │   │   │   ├── login/2fa/page.tsx
│   │   │   │   ├── register/page.tsx
│   │   │   │   ├── verify-email/page.tsx
│   │   │   │   ├── forgot-password/page.tsx
│   │   │   │   ├── reset-password/page.tsx
│   │   │   │   └── auth/oauth/callback/page.tsx
│   │   │   ├── (app)/                        # layout ber-sidebar, wajib login
│   │   │   │   ├── layout.tsx
│   │   │   │   ├── onboarding/page.tsx
│   │   │   │   ├── dashboard/page.tsx
│   │   │   │   ├── transactions/page.tsx
│   │   │   │   ├── transactions/import/page.tsx
│   │   │   │   ├── transfers/page.tsx
│   │   │   │   ├── accounts/page.tsx
│   │   │   │   ├── accounts/[id]/page.tsx
│   │   │   │   ├── accounts/[id]/members/page.tsx
│   │   │   │   ├── shared/page.tsx
│   │   │   │   ├── categories/page.tsx
│   │   │   │   ├── tags/page.tsx
│   │   │   │   ├── budgets/page.tsx
│   │   │   │   ├── recurring/page.tsx
│   │   │   │   ├── goals/page.tsx
│   │   │   │   ├── goals/[id]/page.tsx
│   │   │   │   ├── debts/page.tsx
│   │   │   │   ├── debts/[id]/page.tsx
│   │   │   │   ├── bills/page.tsx
│   │   │   │   ├── exchange-rates/page.tsx
│   │   │   │   ├── reports/page.tsx
│   │   │   │   ├── reports/yearly/page.tsx
│   │   │   │   ├── reports/reconciliation/page.tsx
│   │   │   │   ├── audit-log/page.tsx
│   │   │   │   └── settings/
│   │   │   │       ├── profile/page.tsx
│   │   │   │       ├── preferences/page.tsx
│   │   │   │       ├── security/page.tsx
│   │   │   │       ├── sessions/page.tsx
│   │   │   │       ├── api-keys/page.tsx
│   │   │   │       └── account/page.tsx      # danger zone: hapus akun
│   │   │   ├── (admin)/admin/                # wajib role admin
│   │   │   │   ├── layout.tsx
│   │   │   │   ├── users/page.tsx
│   │   │   │   └── users/[id]/page.tsx
│   │   │   ├── layout.tsx                    # providers, theme, intl
│   │   │   ├── not-found.tsx
│   │   │   └── error.tsx
│   │   ├── bff/                              # Backend-for-Frontend (route handlers)
│   │   │   ├── auth/login/route.ts
│   │   │   ├── auth/login-2fa/route.ts
│   │   │   ├── auth/oauth-exchange/route.ts
│   │   │   ├── auth/logout/route.ts
│   │   │   ├── auth/session/route.ts         # GET: status sesi + user (tanpa token)
│   │   │   └── api/[...path]/route.ts        # proxy generik ke /api/v1/*
│   │   └── healthz/route.ts
│   ├── features/                             # VERTICAL SLICE per domain
│   │   ├── auth/            { api, components, hooks, schemas, lib }
│   │   ├── profile/
│   │   ├── security/        (2fa, sessions, identities, security-events, password)
│   │   ├── api-keys/
│   │   ├── settings/        (finance preferences, currencies)
│   │   ├── onboarding/
│   │   ├── dashboard/
│   │   ├── accounts/
│   │   ├── members/         (shared wallets)
│   │   ├── categories/
│   │   ├── tags/
│   │   ├── transactions/    (list, form, import, export)
│   │   ├── transfers/
│   │   ├── budgets/
│   │   ├── recurring/
│   │   ├── goals/
│   │   ├── debts/
│   │   ├── bills/
│   │   ├── exchange-rates/
│   │   ├── reports/
│   │   ├── audit-log/
│   │   └── admin/
│   │       # setiap feature:
│   │       #   api/         query keys, queryOptions, hook useXxx / useCreateXxx (pakai shared/api client)
│   │       #   components/  komponen UI khusus feature
│   │       #   hooks/       hook UI (bukan fetch), mis. useBudgetAlert
│   │       #   schemas/     Zod schema form + mapping form -> request body
│   │       #   lib/         helper murni khusus feature
│   │       #   index.ts     PUBLIC API feature (satu-satunya yang boleh di-import app/)
│   ├── server/                               # kode khusus server (import 'server-only')
│   │   ├── session/         store.ts (redis|memory), cookie.ts, csrf.ts
│   │   ├── backend/         client.ts (fetch ke BACKEND_URL), refresh.ts (single-flight)
│   │   └── env.ts           validasi env server dengan Zod
│   ├── shared/                               # GENERIK, tanpa pengetahuan domain
│   │   ├── ui/              komponen shadcn + komponen generik (MoneyInput, DataTable, EmptyState, ErrorState, ConfirmDialog)
│   │   ├── api/             schema.d.ts (generated), client.ts (openapi-fetch ke /bff/api), errors.ts (ApiError, kode), idempotency.ts, etag.ts, pagination.ts
│   │   ├── lib/             money.ts, date.ts, csv-download.ts, cn.ts, uuid.ts
│   │   ├── hooks/           useDebounce, useMediaQuery, useRetryAfter
│   │   ├── config/          routes.ts, nav.ts, env.client.ts
│   │   └── i18n/            request.ts, routing.ts
│   ├── test/
│   │   ├── msw/             generated/ (msw-auto-mock), handlers.ts (override), server.ts, browser.ts
│   │   ├── factories/       builder data uji
│   │   └── setup.ts
│   ├── proxy.ts                              # (middleware) proteksi route + nonce CSP
│   └── instrumentation.ts
├── .env.example
├── Dockerfile
├── docker-compose.yml
├── next.config.ts
├── vitest.config.ts
├── playwright.config.ts
├── eslint.config.mjs
└── .github/workflows/ci.yml
```

### 3.2 Aturan layering (ditegakkan lint)

```text
app/  ──►  features/<x>/index.ts  ──►  shared/*
  │                                     ▲
  └──────────────► shared/* ────────────┘
server/  ──►  shared/api (tipe saja), shared/lib
```

1. **`app/` tipis**: page hanya merangkai komponen dari `features/*` dan membaca `params`/`searchParams`. Tidak ada `fetch`, tidak ada logika bisnis.
2. **Feature tidak boleh import feature lain secara langsung.** Bila `transactions` butuh pemilih akun, pilihan: (a) komponen `AccountSelect` yang generik dipindah ke `shared/ui` dan menerima data via props, atau (b) page di `app/` merangkai kedua feature. Komunikasi lintas feature lewat props, URL, atau invalidasi query key (key factory diekspor di `index.ts`, *boleh* dipakai feature lain hanya untuk `invalidateQueries` lewat `shared/api/query-keys.ts` terpusat).
3. **`shared/` tidak tahu domain**: tidak boleh import dari `features/` atau `app/`.
4. **`server/` hanya untuk server**: setiap file diawali `import 'server-only'`. Token backend hanya ada di sini.
5. Impor ke feature lewat `features/<x>` (index), bukan path dalam (`features/x/components/...`).
6. Satu sumber tipe API: `shared/api/schema.d.ts`. Tipe domain UI dibentuk dengan `components['schemas']['finance.AccountResponse']` dsb.

Konfigurasi `eslint-plugin-boundaries` (contoh element types: `app`, `feature`, `shared`, `server`) dengan rule `feature -> feature` = disallow.

### 3.3 Pola DRY yang wajib

- **Query key factory** per feature: `accountKeys.all`, `accountKeys.list(filters)`, `accountKeys.detail(id)`.
- **Satu `apiClient`** (openapi-fetch) dengan middleware: CSRF header, `X-Request-ID`, normalisasi error ke `ApiError`.
- **Satu `useApiMutation`** wrapper yang: membuat & menyimpan `Idempotency-Key` per percobaan submit, memetakan `ApiError.details` ke field RHF (`setError`), toast error dengan `request_id`.
- **Satu `InfiniteList`/`DataTable`** generik untuk semua list ber-cursor.
- **Satu `MoneyText` dan `MoneyInput`** untuk semua nominal.

---

## 4. Arsitektur keamanan (kritis)

### 4.1 Keputusan: pola Backend-for-Frontend (BFF)

**Direkomendasikan dan dipakai**: browser **tidak pernah** memegang `access_token` maupun `refresh_token`.

```mermaid
sequenceDiagram
  autonumber
  participant B as Browser
  participant N as Next.js server (BFF)
  participant S as Session store (Redis)
  participant A as Backend API /api/v1
  B->>N: POST /bff/auth/login {email,password} + X-CSRF-Token
  N->>A: POST /auth/login (X-Forwarded-For, User-Agent diteruskan)
  A-->>N: 200 {access_token, refresh_token, session_id, user, expires_in}
  N->>S: simpan {access, refresh, accessExp, backendSessionId, user}
  N-->>B: Set-Cookie: __Host-sid=<opaque>; HttpOnly; Secure; SameSite=Lax; Path=/
  B->>N: GET /bff/api/accounts (cookie sid)
  N->>S: ambil token
  N->>A: GET /accounts  Authorization: Bearer <access>
  A-->>N: 200 {data}
  N-->>B: 200 {data} (pass-through, + X-Request-ID)
```

Detail:

- Cookie **`__Host-sid`**: nilai acak 256-bit (opaque), `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, tanpa `Domain`. Umur = `refresh_token_expires_at`. Di dev (http://localhost) `Secure` boleh dimatikan; jangan pakai prefix `__Host-` tanpa Secure.
- **Session store** server-side (Redis di produksi, Map in-memory di dev/test) menyimpan `{ accessToken, accessExpiresAt, refreshToken, refreshExpiresAt, backendSessionId, user }`. Alternatif tanpa Redis: cookie terenkripsi JWE (`jose`, kunci `SESSION_SECRET` 32 byte), tetapi race rotasi lebih sulit, jadi Redis tetap pilihan produksi.
- **Access token** hanya di memori server/store, dipakai oleh `server/backend/client.ts`. Tidak pernah dikirim ke browser, tidak pernah di-log.
- **Proxy generik** `app/bff/api/[...path]/route.ts`:
  - Allowlist method + prefix path (hanya path yang ada di spec, tolak `..`).
  - Teruskan header: `Content-Type`, `Idempotency-Key`, `If-Match`, `Accept`, `X-Request-ID`; balikan header: `X-Request-ID`, `Retry-After`, `ETag`, `Idempotent-Replayed`, `Content-Disposition`, `Content-Type`, `RateLimit-*`.
  - Teruskan **IP asli klien** dalam `X-Forwarded-For` dan `User-Agent` asli. **Wajib**: set `TRUSTED_PROXIES` di backend ke IP/CIDR server Next, kalau tidak semua user berbagi satu bucket rate limit (IP server Next) dan daftar sesi menampilkan IP/UA server.
  - Stream body (untuk CSV export dan multipart import) tanpa buffering penuh.
- Endpoint auth (`login`, `login-2fa`, `oauth-exchange`, `logout`) punya route handler khusus karena harus menyimpan/menghapus token di store. Response ke browser hanya `{ data: { user } }` atau `{ data: { mfa_required: true, expires_at } }` (`mfa_token` disimpan di cookie sementara `__Host-mfa` HttpOnly, umur 5 menit, bukan dikirim ke JS).
- `GET /bff/auth/session` mengembalikan `{ authenticated, user }` untuk hydrate UI (role, nama, `mfa_enabled`, `has_password`).

### 4.2 Perbandingan dengan SPA murni (tidak dipilih)

| Aspek | SPA murni (token di JS) | BFF (dipilih) |
|---|---|---|
| Penyimpanan refresh token | `localStorage` / memori JS: **bisa dicuri XSS** dan dipakai dari mesin penyerang selama 7 hari | Hanya di server; XSS paling jauh bisa memakai sesi selama tab terbuka, tidak bisa mengekstrak token |
| CORS | Wajib, browser memanggil backend lintas origin | Tidak perlu untuk alur normal (server-to-server) |
| Rotasi refresh antar tab | Banyak tab = race rotasi = `TOKEN_REUSED` = logout paksa | Terpusat di server, single-flight per sesi |
| CSRF | Tidak relevan (Bearer header) | Perlu proteksi CSRF (cookie) — lihat 4.3 |
| Kompleksitas | Lebih sederhana | Butuh route handler + session store |

Backend sendiri (`/auth/refresh`) menerima refresh token di body, bukan cookie, jadi hanya BFF yang membuat token tidak terjangkau JS.

### 4.3 CSRF untuk BFF

Karena autentikasi BFF berbasis cookie, setiap request **mutasi** (`POST/PUT/PATCH/DELETE`) ke `/bff/*` wajib lolos:

1. **Cek `Origin`** (fallback `Referer`) harus sama dengan `APP_ORIGIN`. Tolak 403 bila beda/absen.
2. **Double-submit token**: cookie `__Host-csrf` (bukan HttpOnly, acak 32 byte, dibuat saat sesi dibuat) + header `X-CSRF-Token` yang sama, dibandingkan constant-time di server. `apiClient` menambahkan header otomatis.
3. `SameSite=Lax` pada cookie sesi sebagai lapisan tambahan.
4. Server Actions (bila dipakai) sudah punya cek origin bawaan Next; tetap jangan pakai Server Action untuk operasi yang butuh `Idempotency-Key` kecuali key ikut dikirim dari client.

### 4.4 Refresh token: rotasi, single-flight, TOKEN_REUSED

Fakta backend:
- `POST /auth/refresh {refresh_token}` → `200 {data: TokenResponse}` berisi **access token baru DAN refresh token baru**. Refresh lama langsung tidak berlaku.
- Memakai refresh lama lagi → **`401 TOKEN_REUSED`** dan **seluruh sesi perangkat dicabut**.
- `401 SESSION_INVALID` = sesi dicabut/kedaluwarsa (logout, revoke dari perangkat lain, reset password, suspend).
- Access token: `expires_in` detik (default 900).

Implementasi di `server/backend/refresh.ts`:

```ts
// Single-flight per sesi: N request paralel yang butuh refresh -> tepat 1 panggilan /auth/refresh.
const inflight = new Map<string, Promise<SessionTokens>>();

export async function getFreshTokens(sid: string): Promise<SessionTokens> {
  const s = await store.get(sid);
  if (!s) throw new SessionExpiredError();
  if (s.accessExpiresAt - Date.now() > 30_000) return s;          // refresh proaktif 30 dtk sebelum habis
  let p = inflight.get(sid);
  if (!p) {
    p = (async () => {
      // Multi-instance: ambil lock Redis `lock:refresh:<sid>` (SET NX PX 10000).
      // Setelah dapat lock, BACA ULANG store: instance lain mungkin sudah merotasi.
      const latest = await withRedisLock(sid, () => store.get(sid));
      if (latest && latest.accessExpiresAt - Date.now() > 30_000) return latest;
      const res = await backend.post('/auth/refresh', { refresh_token: latest!.refreshToken });
      if (!res.ok) {
        await store.delete(sid);                                  // TOKEN_REUSED / SESSION_INVALID -> logout
        throw new SessionExpiredError(res.error.code);
      }
      const next = toTokens(res.data);
      await store.set(sid, next);
      return next;
    })().finally(() => inflight.delete(sid));
    inflight.set(sid, p);
  }
  return p;
}
```

Aturan:
- Bila backend membalas `401 UNAUTHENTICATED` pada request biasa (token kedaluwarsa lebih cepat dari perkiraan), BFF refresh **sekali** lalu ulangi request **sekali**. Jangan pernah loop.
- `TOKEN_REUSED` atau `SESSION_INVALID` dari refresh → hapus sesi, hapus cookie, balas `401 {error:{code:"SESSION_EXPIRED"}}` ke browser. Client (`apiClient`) menangkap 401 dari BFF, membersihkan cache TanStack Query (`queryClient.clear()`), dan redirect ke `/login?reason=session_expired&next=<path>`. Untuk `TOKEN_REUSED` tampilkan pesan keamanan: "Sesi dihentikan karena terdeteksi penggunaan token ganda. Silakan login lagi dan periksa perangkat aktif Anda."
- Jangan pernah meretry request mutasi setelah refresh bila request awal **sudah sampai backend** dan hasilnya bukan 401 (misal timeout): gunakan `Idempotency-Key` yang sama.

### 4.5 Proteksi route (proxy/middleware) dan role

- `src/proxy.ts` (Next 16; `middleware.ts` di versi lama): untuk path di grup `(app)` dan `(admin)`, bila cookie `__Host-sid` tidak ada → redirect `/login?next=...`. Ini hanya cek cepat (tanpa I/O); validasi sebenarnya dilakukan di layout server `(app)/layout.tsx` yang memanggil session store (`getSession()`), lalu `redirect('/login')` bila tidak valid.
- Halaman publik auth (`/login`, `/register`) redirect ke `/dashboard` bila sesi valid.
- **Admin**: `(admin)/admin/layout.tsx` memeriksa `session.user.roles.includes('admin')` di server; bila tidak → `notFound()` (jangan membocorkan keberadaan halaman). Menu admin di sidebar hanya muncul untuk admin. Backend tetap otoritas akhir (`403 FORBIDDEN`).
- Role berubah setelah refresh token berikutnya; setelah admin memberi role, target perlu login ulang/refresh. BFF memperbarui `user` di store dengan `GET /users/me` setiap refresh.

### 4.6 Header keamanan dan CSP

Di `proxy.ts` buat nonce per request dan set header (atau via `next.config.ts` `headers()` untuk yang statis):

```text
Content-Security-Policy:
  default-src 'self';
  script-src 'self' 'nonce-{NONCE}' 'strict-dynamic';
  style-src 'self' 'unsafe-inline';
  img-src 'self' data: blob:;
  font-src 'self';
  connect-src 'self';
  frame-ancestors 'none';
  form-action 'self' https://accounts.google.com;
  base-uri 'self';
  object-src 'none';
  upgrade-insecure-requests
Strict-Transport-Security: max-age=63072000; includeSubDomains; preload   (produksi)
X-Content-Type-Options: nosniff
Referrer-Policy: strict-origin-when-cross-origin
Permissions-Policy: camera=(), microphone=(), geolocation=()
Cross-Origin-Opener-Policy: same-origin
```

`connect-src 'self'` cukup karena browser hanya bicara ke BFF. QR code 2FA dirender lokal (lib `qrcode` ke canvas/SVG data URL), **jangan** kirim `otpauth_uri` ke layanan QR pihak ketiga (itu membocorkan secret TOTP).

### 4.7 Env dan secret

- Hanya nilai publik di `NEXT_PUBLIC_*` (mis. `NEXT_PUBLIC_APP_NAME`, `NEXT_PUBLIC_GOOGLE_OAUTH_ENABLED`). `BACKEND_URL`, `SESSION_SECRET`, `REDIS_URL` **tanpa** prefix dan divalidasi Zod di `server/env.ts` (fail fast saat start).
- Jangan log body request auth, token, `mfa_token`, recovery code, atau API key penuh.

### 4.8 Rendering aman (XSS)

- Semua data user (note transaksi, nama kategori, nama akun, `counterparty`, `user_agent`, `metadata` security event, `before/after` audit log) dirender sebagai teks React biasa. **Dilarang** `dangerouslySetInnerHTML`. JSON audit log ditampilkan di `<pre>` via `JSON.stringify(v, null, 2)`.
- `color` kategori/tag (format `#RRGGBB`) divalidasi regex sebelum dipakai di `style`.
- `auth_url` dari `POST /users/me/identities/google/link` divalidasi: harus `https://accounts.google.com/` sebelum `window.location.assign`.
- Parameter `next=` pada redirect login hanya menerima path relatif internal (`/^\/(?!\/)/`), cegah open redirect.

### 4.9 Download CSV yang aman

- Export: link ke `/bff/api/transactions/export?...` dengan `fetch` + `blob()` + `URL.createObjectURL` + `<a download>`; nama file dari header `Content-Disposition` (default `transactions.csv`), lalu `URL.revokeObjectURL`. Backend sudah mencegah formula injection (sel diawali `= + - @` diberi prefix `'`); jangan menghapus prefix itu.
- Import: validasi di client sebelum upload: ekstensi `.csv`, MIME `text/csv`/`application/vnd.ms-excel`, ukuran ≤ 5 MB. Preview baris dirender sebagai teks.
- Recovery codes 2FA dan API key penuh: tampilkan sekali, sediakan tombol "Salin" dan "Unduh .txt" (blob lokal), jangan simpan di state global/cache query setelah dialog ditutup.

---

## 5. Konvensi API

### 5.1 Base URL dan transport

- Base path backend: **`/api/v1`** (mis. `http://localhost:8080/api/v1`). Health: `GET /healthz` (liveness, 200 tanpa body), `GET /readyz` (readiness; `503 SHUTTING_DOWN` / `503 DB_UNAVAILABLE`). Swagger UI dev: `http://localhost:8080/docs/`.
- Browser memanggil **`/bff/api/<path>`** (BFF meneruskan ke `${BACKEND_URL}/api/v1/<path>`). Di dokumen ini path endpoint ditulis relatif terhadap `/api/v1`.
- Body request: `Content-Type: application/json` (kecuali import CSV: `multipart/form-data`). Backend **menolak field tak dikenal** (`400 INVALID_JSON`), body kosong, lebih dari satu objek JSON, dan Content-Type lain (`415 UNSUPPORTED_MEDIA_TYPE`). Batas body: endpoint finance 64 KiB, umum 1 MiB, import CSV 5 MB; lebih → `413 BODY_TOO_LARGE`. Jadi **jangan kirim field ekstra** dari state form; petakan form → body secara eksplisit di `schemas/`.
- PATCH = partial: kirim **hanya field yang berubah**. Untuk beberapa field, string kosong `""` berarti "hapus nilai" (didokumentasikan per endpoint).

### 5.2 Envelope

```jsonc
// objek tunggal
{ "data": { "id": "0192...", "name": "BCA" } }
// list ber-cursor
{ "data": [ { } ], "meta": { "next_cursor": "eyJ0Ijo...", "has_more": true, "limit": 20 } }
// list tanpa paging (accounts, categories, tags, budgets, debts, bills, goals, rates, sessions, api-keys, ...)
{ "data": [ { } ] }
// 204 No Content: tanpa body (logout, delete, suspend, ...)
```

List yang kosong selalu `[]`, bukan `null`. `next_cursor` tidak ada bila `has_more=false`.

### 5.3 Format error

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

- **Bercabang berdasarkan `error.code`**, bukan `message` (pesan berbahasa Indonesia dan bisa berubah). Teks UI diambil dari katalog i18n `errors.<CODE>`, fallback ke `message` server.
- `details[]` (opsional) berisi error per field. `field` memakai nama JSON (`email`, `amount`, `status`, ...). Petakan ke `form.setError(field, ...)`. Detail yang tidak cocok dengan field mana pun → tampilkan di atas form.
- `request_id` juga ada di header `X-Request-ID` (header ini di-expose CORS). Tampilkan di toast error: "Kode referensi: 0192f0c1…" + tombol salin.
- Implementasi: `shared/api/errors.ts` berisi `class ApiError extends Error { status; code; details; requestId; retryAfter? }` dan union type `ApiErrorCode` dari tabel Bagian 6.

### 5.4 Uang (money)

Fakta backend:
- Semua nominal adalah **string desimal di major unit**: `"1500000"` = Rp1.500.000, `"12.34"` = USD 12,34. Contoh juga ada nilai negatif (`"-0.05"`, `remaining` negatif = overspent).
- Mata uang didukung (registry backend): **IDR (0 desimal), USD (2), SGD (2), JPY (0), EUR (2)**. Ambil dari `GET /currencies` (`code`, `minor_unit`, `name`, `symbol`), cache `staleTime: Infinity`.
- Input ditolak bila: notasi ilmiah, pemisah ribuan, tanda `+`, atau jumlah desimal melebihi `minor_unit` currency → `422 INVALID_AMOUNT`. Terlalu besar → `422 AMOUNT_TOO_LARGE`. Nominal transaksi harus > 0.
- `maxLength` field amount = 32 karakter.

Strategi frontend:

```ts
// shared/lib/money.ts
import Big from 'big.js';

export type Money = { amount: string; currency: string };

/** Tampilan: JANGAN parseFloat untuk hitung. Intl menerima string desimal (ES2023 "Intl.NumberFormat v3"). */
export function formatMoney(amount: string, currency: string, locale = 'id-ID', minorUnit?: number) {
  return new Intl.NumberFormat(locale, {
    style: 'currency',
    currency,
    minimumFractionDigits: minorUnit,         // dari GET /currencies (IDR 0, USD 2)
    maximumFractionDigits: minorUnit,
  }).format(amount as unknown as number);      // string diterima Intl tanpa kehilangan presisi
}
// formatMoney('1500000','IDR')      -> "Rp 1.500.000"
// formatMoney('12.5','USD','id-ID',2) -> "US$12,50"
// formatMoney('12.5','USD','en-US',2) -> "$12.50"

/** Aritmetika (sum, selisih, persen) selalu pakai Big. */
export const add = (a: string, b: string) => new Big(a).plus(b).toString();
export const cmp = (a: string, b: string) => new Big(a).cmp(b);

/** Normalisasi input user ("1.500.000" / "12,5") -> string major unit yang diterima backend. */
export function toApiAmount(input: string, minorUnit: number, locale = 'id-ID'): string {
  const group = locale.startsWith('id') ? '.' : ',';
  const decimal = locale.startsWith('id') ? ',' : '.';
  const raw = input.replaceAll(group, '').replace(decimal, '.').trim();
  if (!/^\d+(\.\d+)?$/.test(raw)) throw new Error('INVALID_AMOUNT');
  const [, frac = ''] = raw.split('.');
  if (frac.length > minorUnit) throw new Error('TOO_MANY_DECIMALS');
  return new Big(raw).toFixed(minorUnit === 0 ? 0 : Math.min(frac.length, minorUnit)).replace(/\.?0+$/, (m) => (m.startsWith('.') ? '' : m));
}
```

Aturan: simpan nilai form sebagai **string**, validasi Zod dengan regex per currency (`/^\d{1,30}$/` untuk 0 desimal, `/^\d{1,30}(\.\d{1,2})?$/` untuk 2 desimal), kirim string. Jumlah total lintas currency **tidak boleh** dijumlahkan di client; pakai angka dari laporan backend (`total_balance[]` per currency, `yearly` sudah dikonversi ke base currency).

### 5.5 Tanggal dan timezone

- **Tanggal kalender** (`transaction_date`, `transfer_date`, `start_date`, `due_date`, `as_of`, `date`, `target_date`, `next_due_date`, `period`, `next_run_date`, `upcoming[]`): string **`YYYY-MM-DD`** tanpa zona. Jangan ubah ke `Date` lalu `toISOString()` (bisa bergeser sehari). Tampilkan dengan `parseISO` + `format` date-fns, pilih dengan date picker yang menghasilkan string.
- **Bulan**: `YYYY-MM` (`month` pada budgets/summary, `period_month`).
- **Timestamp** (`created_at`, `updated_at`, `last_login_at`, `occurred_at`, `expires_at`, ...): RFC 3339 UTC (`2026-09-30T10:00:00Z`). Tampilkan dalam **timezone user dari `GET /settings`** (`timezone`, IANA, default `Asia/Jakarta`) memakai `TZDate` dari `@date-fns/tz`, bukan timezone browser.
- "Hari ini" dan batas bulan mengikuti timezone user (backend memakai timezone setting untuk default `month`, laporan, dan `DATE_IN_FUTURE`). Hitung default date picker dengan `TZDate.tz(settings.timezone)`.
- `week_start` (0 = Minggu … 6 = Sabtu) dipakai untuk kalender dan grafik mingguan.
- Validasi client: `transaction_date` tidak boleh di masa depan (`DATE_IN_FUTURE`) dan minimal `1970-01-01` (`DATE_TOO_OLD`).

### 5.6 Pagination keyset (cursor)

- Query: `limit` (1–100, default 20) dan `cursor` (= `meta.next_cursor` sebelumnya). Endpoint ber-cursor: `GET /transactions`, `/transfers`, `/recurring-rules`, `/audit-logs`, `/users/me/security-events`, `/admin/users`, `/admin/users/{id}/security-events`.
- Cursor **opaque** (jangan di-parse) dan **terikat filter**: ganti filter = mulai tanpa cursor. Cursor dari filter lain → `400 INVALID_CURSOR` (tangani dengan reset list).
- Tidak ada "halaman N"; gunakan infinite scroll / tombol "Muat lagi".

```ts
export const transactionsInfinite = (filters: TxFilters) =>
  infiniteQueryOptions({
    queryKey: txKeys.list(filters),                        // filter ada di key -> ganti filter = list baru
    queryFn: ({ pageParam, signal }) =>
      api.GET('/transactions', { params: { query: { ...filters, limit: 20, cursor: pageParam } }, signal })
        .then(unwrap),                                     // unwrap -> {data, meta} atau throw ApiError
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => (last.meta.has_more ? last.meta.next_cursor : undefined),
  });
```

Array query (`account_id`, `category_id`) dikirim berulang: `?account_id=a&account_id=b` (openapi-fetch: `querySerializer: { array: { style: 'form', explode: true } }`).

### 5.7 Idempotency-Key

- **Wajib** (header) pada: `POST /transactions`, `POST /transfers`, `POST /budgets`, `POST /recurring-rules`, `POST /bills/{id}/pay`, `POST /debts/{id}/payments`. Tanpa header → `400 IDEMPOTENCY_KEY_REQUIRED`; format salah → `400 INVALID_IDEMPOTENCY_KEY` (harus 8–128 karakter ASCII tanpa spasi).
- Buat **UUID v4/v7 baru per percobaan submit logis** (`crypto.randomUUID()`), simpan di `useRef` form. **Pakai ulang key yang sama** saat retry (timeout, network error, 5xx, 409 `IDEMPOTENCY_IN_PROGRESS`). Reset key setelah sukses atau setelah user **mengubah isi form** (body berbeda dengan key lama → `422 IDEMPOTENCY_KEY_REUSED`).
- Respons replay membawa header `Idempotent-Replayed: true`; perlakukan sebagai sukses biasa (jangan tampilkan toast ganda).
- `409 IDEMPOTENCY_IN_PROGRESS`: tunggu 1–2 detik lalu retry dengan key sama (maks 3x).
- Endpoint create lain (accounts, categories, tags, goals, debts, bills, rates, contributions) tidak memakai key; cegah double-submit dengan men-disable tombol saat `isPending`.

### 5.8 ETag / If-Match (optimistic locking)

- Resource dengan `version`: account, transaction, transfer, budget, recurring rule, savings goal, debt, bill. `GET` detail mengirim header `ETag: "3"` (= `version`).
- Saat `PATCH`, kirim `If-Match: "<version>"` dari data yang sedang diedit (format diterima: `"3"`, `3`, atau `W/"3"`; format lain → `400 INVALID_IF_MATCH`). Tanpa header = tidak dicek (last write wins) — **selalu kirim** dari UI.
- Konflik → **`409 VERSION_CONFLICT`** (backend ini memakai 409, **bukan 412**). UX: dialog "Data ini sudah diubah di perangkat/tab lain" dengan dua pilihan: **Muat ulang** (refetch, form diisi data terbaru, perubahan user ditampilkan sebagai diff/ditimpa sesuai pilihan) atau **Batal**. Jangan auto-retry dengan version baru tanpa persetujuan user.
- `DELETE /transactions/{id}` dan `DELETE /transfers/{id}` juga bisa `409 VERSION_CONFLICT` (perubahan bersamaan) → refetch lalu tanya ulang.

### 5.9 Rate limit (429)

- Global: 300 req/menit/IP (burst 50). `POST /auth/*` publik (kecuali logout & refresh): 10/menit/IP (burst 5). API key: 600/menit/key. Percobaan 2FA: maks 5 per `mfa_token`.
- `429 RATE_LIMITED` membawa header **`Retry-After: <detik>`**. UX: disable tombol submit dengan countdown "Coba lagi dalam 23 detik" (`useRetryAfter`), jangan auto-retry mutasi. Query GET boleh auto-retry setelah `Retry-After` (TanStack Query `retryDelay` membaca `error.retryAfter`, maks 2x).
- `429 ACCOUNT_LOCKED` (login/akun gagal berulang) dan `429 OTP_TOO_MANY_ATTEMPTS` adalah kasus khusus, lihat Bagian 6.
- Karena BFF, pastikan backend `TRUSTED_PROXIES` berisi IP server Next agar limit dihitung per IP user asli.

### 5.10 X-Request-ID

- Backend selalu mengirim `X-Request-ID` (dan menerima dari client bila disediakan). BFF membuat `X-Request-ID` (UUID) per request bila belum ada, meneruskannya, dan mengembalikannya.
- Toast error menampilkan `request_id` + tombol salin. Log server BFF menyertakan `request_id` yang sama untuk korelasi.

### 5.11 Konvensi lain

- Resource milik user lain selalu **404** (bukan 403) agar ID tidak bocor; anggota shared wallet tanpa hak menulis mendapat **403 FORBIDDEN**.
- `GET /users/me` → `user.roles` selalu memuat `"user"`, plus `"admin"` bila admin. `user.has_password=false` untuk akun OAuth-only (sembunyikan form ganti password / hapus akun dengan password; tampilkan arahan "set password via Lupa Password").
- Header `Authorization: Bearer <token>` hanya dipasang oleh BFF. API key (`X-API-Key` atau `Authorization: ApiKey <key>`) hanya berlaku untuk endpoint ber-`ApiKeyAuth` di spec (saat ini `GET /auth/whoami`); UI web tidak memakai API key untuk dirinya sendiri.

---

## 6. Tabel lengkap kode error

Sumber: mapping error backend (`auth` + `finance` + `httpx` + router). Kolom "UI" adalah perilaku yang direkomendasikan.

### 6.1 Umum / platform

| Code | HTTP | Kapan | UI yang direkomendasikan |
|---|---|---|---|
| `INVALID_JSON` | 400 | Body rusak, kosong, tipe field salah, field tak dikenal | Bug client: toast generik + `request_id`, log ke Sentry. `details` bisa berisi field bertipe salah. |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | Content-Type bukan JSON | Bug client (toast generik). |
| `BODY_TOO_LARGE` | 413 | Body > batas | "Data terlalu besar". |
| `VALIDATION_FAILED` | 422 | Validasi field | Petakan `details[]` ke field form; fokus ke field pertama yang error. |
| `INVALID_PARAMETER` | 400 | Query/path param tidak valid (`details[].field` = nama param) | Reset filter yang salah, toast. |
| `INVALID_CURSOR` | 400 | Cursor rusak/beda filter | Reset infinite query (hapus cursor), muat ulang dari awal. |
| `NOT_FOUND` | 404 | Path tidak ada, atau fitur OAuth nonaktif | Halaman 404 / sembunyikan tombol Google. |
| `UNAUTHENTICATED` | 401 | Token tidak ada/invalid/kedaluwarsa | BFF refresh sekali lalu retry; gagal → redirect login. |
| `SESSION_INVALID` | 401 | Sesi dicabut/kedaluwarsa (logout, revoke, reset password, suspend) | Hapus sesi BFF, redirect `/login?reason=session_expired`. |
| `FORBIDDEN` | 403 | Bukan admin; atau role shared wallet tidak cukup | Toast "Anda tidak punya izin"; admin page → 404. Shared wallet viewer: tombol tulis seharusnya sudah tersembunyi. |
| `RATE_LIMITED` | 429 | Melebihi limit (+ `Retry-After`) | Countdown, disable submit. |
| `INTERNAL` | 500 | Error tak terduga | Toast "Terjadi kesalahan" + `request_id`; GET boleh retry. |
| `SHUTTING_DOWN` | 503 | `/readyz` saat shutdown | Hanya untuk health check BFF. |
| `DB_UNAVAILABLE` | 503 | `/readyz` DB down | Halaman status "Layanan sedang gangguan". |

### 6.2 Auth

| Code | HTTP | Kapan | UI |
|---|---|---|---|
| `EMAIL_TAKEN` | 409 | Register email terdaftar | Error di field email + link "Masuk" / "Lupa password". |
| `INVALID_EMAIL` | 422 | Format email | Error field email. |
| `WEAK_PASSWORD` | 422 | < 8 karakter, > 72 byte, atau sama dengan email | Error field password; tampilkan `message` server (berisi alasan). |
| `PASSWORD_REUSED` | 422 | Password baru = password lama | Error field `new_password`. |
| `INVALID_FULL_NAME` | 422 | Nama kosong / > 100 karakter | Error field `full_name`. |
| `NO_FIELDS_TO_UPDATE` | 400 | PATCH profil tanpa perubahan | Disable tombol simpan bila form tidak dirty (seharusnya tidak terjadi). |
| `INVALID_CREDENTIALS` | 401 | Email/password salah; password salah saat ganti password/hapus akun/disable 2FA | Pesan generik di form ("Email atau password salah"), jangan sebut field mana. |
| `ACCOUNT_LOCKED` | 429 | Terlalu banyak gagal login | Banner "Akun terkunci sementara" + countdown `Retry-After` bila ada; link lupa password. |
| `EMAIL_NOT_VERIFIED` | 403 | Login akun pending | Redirect ke `/verify-email?email=...` + tombol kirim ulang kode. |
| `ACCOUNT_SUSPENDED` | 403 | Akun di-suspend admin | Halaman/banner "Akun ditangguhkan, hubungi dukungan". |
| `ACCOUNT_INACTIVE` | 403 | Akun tidak aktif (mis. dihapus) | Pesan "Akun tidak aktif". |
| `INVALID_OTP` | 400 | Kode OTP salah/tidak ada | Error field kode, kosongkan input. |
| `OTP_EXPIRED` | 400 | OTP kedaluwarsa | "Kode kedaluwarsa" + tombol kirim ulang. |
| `OTP_TOO_MANY_ATTEMPTS` | 429 | Terlalu banyak percobaan OTP | Wajib minta kode baru (verify: resend; reset: forgot lagi). |
| `TOKEN_REUSED` | 401 | Refresh token lama dipakai lagi | Logout paksa + pesan keamanan (lihat 4.4). |
| `SESSION_NOT_FOUND` | 404 | Revoke sesi yang tidak ada | Refetch daftar sesi. |
| `USER_NOT_FOUND` | 404 | Profil/admin user tidak ada | 404 page / toast. |
| `MFA_NOT_ENABLED` | 409 | Disable/regenerate saat 2FA mati | Refetch status 2FA. |
| `MFA_ALREADY_ENABLED` | 409 | Setup/enable saat 2FA aktif | Refetch status 2FA. |
| `MFA_SETUP_REQUIRED` | 409 | Enable tanpa setup | Kembali ke langkah setup. |
| `INVALID_MFA_CODE` | 401 | Kode TOTP/recovery salah (atau replay) | Error field kode. Di login 2FA: tampilkan sisa percobaan (maks 5). |
| `MFA_TOKEN_INVALID` | 401 | `mfa_token` invalid/kedaluwarsa (default 5 menit) | Kembali ke `/login` dengan pesan "Sesi verifikasi berakhir". |
| `MFA_CHALLENGE_EXPIRED` | 401 | > 5 percobaan pada satu `mfa_token` | Kembali ke `/login`. |
| `INVALID_ROLE` | 422 | Role admin tidak valid (`user`/`admin`) | Error form (seharusnya tidak terjadi dari select). |
| `CANNOT_MODIFY_SELF` | 409 | Admin suspend/cabut admin dirinya | Disable aksi untuk baris diri sendiri. |
| `INVALID_STATUS_TRANSITION` | 409 | Suspend user suspended / activate user aktif/pending | Refetch user; sembunyikan tombol yang tidak sesuai status. |
| `OAUTH_EMAIL_NOT_VERIFIED` | 403 | Email Google belum terverifikasi (via `?error=`) | Pesan di halaman callback. |
| `OAUTH_ACCOUNT_EXISTS` | 409 | Email Google sudah terdaftar dengan password (via `?error=`) | "Email ini sudah terdaftar. Masuk dengan password lalu hubungkan Google di Pengaturan > Keamanan." |
| `IDENTITY_TAKEN` | 409 | Akun Google sudah terhubung ke user lain (via `?error=` saat link) | Pesan di halaman keamanan. |
| `OAUTH_FAILED` | 400 | Gagal tukar code/validasi id_token (via `?error=`) | "Login Google gagal, coba lagi". |
| `OAUTH_STATE_INVALID` | – (redirect `?error=`) | Cookie state hilang/beda/kedaluwarsa (10 menit) | "Sesi login Google kedaluwarsa, coba lagi". |
| `OAUTH_CANCELED` | – (redirect `?error=`) | User membatalkan consent | Kembali ke login tanpa error mencolok. |
| `LOGIN_CODE_INVALID` | 400 | Code exchange OAuth invalid/kedaluwarsa (~60 detik) / sudah dipakai | Kembali ke login. |
| `API_KEY_NOT_FOUND` | 404 | Revoke key yang tidak ada | Refetch list. |
| `API_KEY_INVALID` | 401 | API key salah/kedaluwarsa/dicabut | (Hanya untuk klien API, bukan UI web.) |
| `INSUFFICIENT_SCOPE` | 403 | API key tanpa scope yang dibutuhkan | (Klien API.) |
| `API_KEY_LIMIT` | 409 | Sudah 10 key aktif | "Maksimal 10 API key aktif. Cabut salah satu." |
| `INVALID_API_KEY_NAME` | 422 | Nama kosong/> 100 | Error field name. |
| `INVALID_SCOPE` | 422 | Scope bukan `read`/`write` | Error field scopes. |
| `INVALID_EXPIRY` | 422 | `expires_in_days` di luar 1–365 | Error field. |

### 6.3 Finance

| Code | HTTP | Kapan | UI |
|---|---|---|---|
| `ACCOUNT_NOT_FOUND` | 404 | Akun tidak ada / bukan milik/anggota | Detail: 404 page. Form: error field akun + refetch daftar akun. |
| `CATEGORY_NOT_FOUND` | 404 | Kategori tidak ada | Error field kategori + refetch kategori. |
| `TRANSACTION_NOT_FOUND` | 404 | | Hapus dari cache list, toast. |
| `TRANSFER_NOT_FOUND` | 404 | | Idem. |
| `BUDGET_NOT_FOUND` | 404 | | Idem. |
| `TAG_NOT_FOUND` | 404 | Tag dihapus | Refetch tag, hapus dari pilihan. |
| `RECURRING_RULE_NOT_FOUND` | 404 | | Idem. |
| `RATE_NOT_FOUND` | 404 | | Idem. |
| `GOAL_NOT_FOUND` | 404 | | Idem. |
| `CONTRIBUTION_NOT_FOUND` | 404 | | Idem. |
| `DEBT_NOT_FOUND` | 404 | | Idem. |
| `BILL_NOT_FOUND` | 404 | | Idem. |
| `MEMBER_NOT_FOUND` | 404 | | Refetch member. |
| `USER_NOT_FOUND` | 404 | Undang member dengan email/ID tidak terdaftar | Error field email: "Pengguna tidak ditemukan". |
| `VERSION_CONFLICT` | 409 | If-Match tidak cocok | Dialog konflik (5.8). |
| `DUPLICATE_NAME` | 409 | Nama akun/kategori/tag/goal sudah ada | Error field `name`. |
| `ACCOUNT_HAS_TRANSACTIONS` | 409 | Hapus akun yang punya riwayat | Tawarkan "Arsipkan saja". |
| `CATEGORY_IN_USE` | 409 | Hapus kategori yang dipakai | Dialog pilih kategori pengganti → ulangi dengan `reassign_to`. |
| `CATEGORY_HAS_CHILDREN` | 409 | Hapus kategori induk | "Hapus/pindahkan sub kategori dulu". |
| `BUDGET_EXISTS` | 409 | Budget kategori+bulan sudah ada | "Budget sudah ada" + link edit budget tsb. |
| `MEMBER_EXISTS` | 409 | User sudah member | Error field email. |
| `RATE_EXISTS` | 409 | Kurs pasangan+tanggal sudah ada | Tawarkan edit kurs yang ada. |
| `DUPLICATE_LINK` | 409 | Transfer/transaksi sudah ditautkan ke goal/debt | Error field transfer/transaksi. |
| `DUPLICATE_IMPORT` | 409 | Import duplikat | Toast. |
| `IDEMPOTENCY_IN_PROGRESS` | 409 | Request dengan key sama masih diproses | Retry otomatis dengan key sama (5.7). |
| `IDEMPOTENCY_KEY_REQUIRED` | 400 | Header hilang | Bug client. |
| `INVALID_IDEMPOTENCY_KEY` | 400 | Key bukan 8–128 ASCII | Bug client. |
| `IDEMPOTENCY_KEY_REUSED` | 422 | Key sama, body beda | Bug client: buat key baru saat form berubah. |
| `INVALID_IF_MATCH` | 400 | Format If-Match | Bug client. |
| `INSUFFICIENT_BALANCE` | 422 | Saldo kurang dan akun `allow_negative=false` | Error field amount: "Saldo tidak mencukupi" + tampilkan saldo akun. |
| `ACCOUNT_ARCHIVED` | 422 | Transaksi/transfer ke akun arsip | Sembunyikan akun arsip dari pilihan; tawarkan "Aktifkan kembali". |
| `INVALID_ACCOUNT_TYPE` | 422 | Tipe akun | Error field type. |
| `INVALID_AMOUNT` | 422 | Nominal ≤ 0, format salah, desimal kebanyakan | Error field amount. |
| `AMOUNT_TOO_LARGE` | 422 | Overflow | Error field amount. |
| `INVALID_TYPE` | 422 | Bukan income/expense | Error field type. |
| `DATE_IN_FUTURE` | 422 | Tanggal transaksi > hari ini (timezone user) | Error field tanggal; date picker membatasi max = hari ini. |
| `DATE_TOO_OLD` | 422 | < 1970-01-01 | Error field tanggal. |
| `CATEGORY_TYPE_MISMATCH` | 422 | Kategori expense dipakai untuk income, dsb.; budget untuk kategori income | Filter pilihan kategori berdasarkan type. |
| `CATEGORY_READ_ONLY` | 422 | Edit/hapus kategori sistem | Sembunyikan aksi untuk `is_system=true`. |
| `CATEGORY_NESTING_TOO_DEEP` | 422 | Parent bukan kategori root | Pilihan parent hanya kategori root. |
| `INVALID_REASSIGN_TARGET` | 422 | `reassign_to` beda tipe/tidak valid | Error di dialog. |
| `SAME_ACCOUNT_TRANSFER` | 422 | Akun asal = tujuan | Validasi client (Zod refine). |
| `MANAGED_BY_TRANSFER` | 422 | Edit/hapus transaksi `source=transfer_fee` | Sembunyikan aksi; link "Edit di transfer". |
| `TOO_MANY_TAGS` | 422 | > 10 tag | Batasi multi-select di 10. |
| `RULE_ENDED` | 422 | Ubah/pause/resume rule `ended` | Sembunyikan aksi untuk status ended. |
| `INVALID_FREQUENCY` | 422 | Frekuensi tidak valid | Error field frequency. |
| `IMPORT_TOO_LARGE` | 413 | File > 5 MB | Validasi client sebelum upload. |
| `IMPORT_TOO_MANY_ROWS` | 422 | > 10.000 baris | "Pecah file menjadi beberapa bagian". |
| `INVALID_CSV_HEADER` | 400 | Header CSV wajib: `date,type,amount,account,category` | Tampilkan contoh header + tombol unduh template. |
| `FORBIDDEN` | 403 | Role shared wallet tidak mengizinkan (viewer menulis, editor kelola member) | Toast + refetch role. |
| `RATE_UNAVAILABLE` | 422 | Konversi tanpa kurs | "Kurs belum tersedia" + link ke halaman kurs. |
| `GOAL_ARCHIVED` | 422 | Setor ke goal arsip | Sembunyikan aksi. |
| `DEBT_SETTLED` | 422 | Bayar hutang lunas | Sembunyikan tombol bayar. |
| `OVERPAYMENT` | 422 | Bayar > sisa, atau principal < paid | Error field amount + tampilkan `remaining`. |
| `BILL_DONE` | 422 | Ubah/bayar bill `done` | Sembunyikan aksi. |
| `INVALID_ROLE` | 422 | Role member bukan viewer/editor | Error field role. |
| `INVALID_TIMEZONE` | 400 | Timezone IANA tidak valid | Error field timezone (pakai combobox dari `Intl.supportedValuesOf('timeZone')`). |
| `INVALID_WEEK_START` | 422 | Bukan 0–6 | Error field. |
| `INVALID_RANGE` | 400 | `from > to` atau rentang terlalu panjang (cashflow maks 366 hari / 120 bulan) | Error di filter tanggal. |
| `CURRENCY_MISMATCH` | 422 | Currency berbeda (mis. transfer lintas currency tanpa `to_amount`) | Tampilkan field `to_amount` saat currency akun beda. |
| `UNSUPPORTED_CURRENCY` | 422 | Currency di luar registry | Hanya pilih dari `GET /currencies`. |

---

## 7. Katalog endpoint (82 path / 122 operasi)

Notasi:
- **Auth**: `public` = tanpa token; `Bearer` = access token (dipasang BFF); `Bearer+admin` = butuh role `admin` (selain itu `403 FORBIDDEN`); `Bearer|ApiKey` = boleh API key.
- **Semua endpoint Bearer** dapat mengembalikan `401 UNAUTHENTICATED` / `SESSION_INVALID`, dan semua endpoint dapat `429 RATE_LIMITED` / `500 INTERNAL`. Itu tidak diulang per endpoint.
- `*` = wajib. Validasi = batas dari spec. Tipe `uuid`, `date` = `YYYY-MM-DD`, `ts` = RFC3339, `money` = string desimal (5.4).
- "Halaman" merujuk ke sitemap Bagian 9.
- Contoh JSON di bawah adalah isi `data` (envelope `{data}` / `{data, meta}` tidak ditulis ulang).

### 7.0 Skema objek inti (dipakai berulang)

```ts
type User = {
  id: uuid; email: string; full_name: string;
  status: 'pending_verification' | 'active' | 'suspended';
  roles: string[];              // selalu memuat 'user'; 'admin' bila admin
  mfa_enabled: boolean; has_password: boolean;
  email_verified_at?: ts; last_login_at?: ts; created_at: ts; updated_at: ts;
};
type LoginResult = {            // POST /auth/login, /auth/login/2fa, /auth/oauth/exchange
  access_token: string; token_type: 'Bearer'; expires_in: number /* detik, 900 */;
  refresh_token: string; refresh_token_expires_at: ts; session_id: uuid;
  mfa_required: false; user: User;
};
type MFAChallenge = { mfa_required: true; mfa_token: string; expires_at: ts }; // TIDAK ada di spec, lihat 7.1
type PageMeta = { next_cursor?: string; has_more: boolean; limit: number };

type Account = {
  id: uuid; name: string; type: 'cash'|'bank'|'ewallet'|'credit_card'; currency: string;
  initial_balance: money; balance: money; allow_negative: boolean;
  archived: boolean; archived_at?: ts; version: number; created_at: ts; updated_at: ts;
};
type Category = {
  id: uuid; name: string; type: 'income'|'expense'; parent_id?: uuid;
  icon?: string; color?: string /* #RRGGBB */; is_system: boolean;
};
type CategoryNode = Category & { children: Category[] };
type Tag = { id: uuid; name: string; color?: string; created_at: ts; updated_at: ts };
type Transaction = {
  id: uuid; account_id: uuid; category_id: uuid; type: 'income'|'expense';
  amount: money; currency: string; transaction_date: date; note?: string;
  source: 'manual'|'transfer_fee'|'recurring'|'import';   // transfer_fee = read-only
  tags: Tag[]; version: number; created_at: ts; updated_at: ts;
};
type Transfer = {
  id: uuid; from_account_id: uuid; to_account_id: uuid;
  amount: money; currency: string; to_amount: money; to_currency: string;
  fee?: money; fee_transaction_id?: uuid; transfer_date: date; note?: string;
  version: number; created_at: ts; updated_at: ts;
};
```

Field opsional (`?`) bisa **tidak ada** di JSON (omitempty); jangan asumsikan `null`.

### 7.1 Tag `auth` (publik + sesi)

**POST `/auth/register`** · public · Halaman `/register`
- Body: `email*` (string ≤254, email), `password*` (8–72, ≠ email), `full_name*` (1–100).
- 201 → `User` (`status: "pending_verification"`). Kode OTP 6 digit dikirim via email (berlaku 10 menit).
- Error: 409 `EMAIL_TAKEN`; 422 `VALIDATION_FAILED`/`INVALID_EMAIL`/`WEAK_PASSWORD`/`INVALID_FULL_NAME`.
- Setelah sukses: redirect `/verify-email?email=<email>`.

**POST `/auth/verify-email`** · public · `/verify-email`
- Body: `email*`, `code*` (6 digit).
- 200 → `{ "verified": true, "already_verified": false }` (idempoten: akun aktif → `already_verified: true`).
- Error: 400 `INVALID_OTP`/`OTP_EXPIRED`; 429 `OTP_TOO_MANY_ATTEMPTS`.
- Sukses → redirect `/login?verified=1&email=...` (backend **tidak** memberi token di sini).

**POST `/auth/verify-email/resend`** · public · `/verify-email`
- Body: `email*`. 202 → `{ "message": "jika email terdaftar, kode telah dikirim" }`.
- Selalu 202 (tidak membocorkan email). Ada cooldown + kuota harian di server. UI: tombol "Kirim ulang" dengan cooldown lokal 60 detik.

**POST `/auth/login`** · public · `/login`
- Body: `email*` (≤254), `password*` (≤72).
- 200 → **salah satu dari dua bentuk** (spec hanya mendeklarasikan yang pertama, BFF harus menangani keduanya):
  ```json
  { "access_token": "eyJ...", "token_type": "Bearer", "expires_in": 900, "refresh_token": "...",
    "refresh_token_expires_at": "2026-10-10T10:00:00Z", "session_id": "0192...", "mfa_required": false,
    "user": { "id": "0192...", "email": "budi@example.com", "roles": ["user"], "...": "..." } }
  ```
  ```json
  { "mfa_required": true, "mfa_token": "eyJ...", "expires_at": "2026-10-03T10:05:00Z" }
  ```
  Bedakan dengan `data.mfa_required === true`. Tambahkan tipe manual `MFAChallenge` di `shared/api/extra-types.ts`.
- Error: 401 `INVALID_CREDENTIALS`; 403 `EMAIL_NOT_VERIFIED` (hanya setelah password benar)/`ACCOUNT_SUSPENDED`/`ACCOUNT_INACTIVE`; 429 `ACCOUNT_LOCKED`/`RATE_LIMITED`.
- BFF: route `POST /bff/auth/login` → simpan token di session store, set `__Host-sid`, kembalikan **hanya** `{ user }` atau `{ mfa_required: true, expires_at }` (mfa_token disimpan di cookie `__Host-mfa`, Bagian 4).

**POST `/auth/login/2fa`** · public · `/login/2fa`
- Body: `mfa_token*` (≤2048), `code*` (6–32; TOTP 6 digit **atau** recovery code `abcde-fghij`).
- 200 → `LoginResult`.
- Error: 401 `MFA_TOKEN_INVALID`/`INVALID_MFA_CODE`/`MFA_CHALLENGE_EXPIRED` (maks 5 percobaan per token, token berlaku 5 menit); 403 `ACCOUNT_SUSPENDED`/`ACCOUNT_INACTIVE`; 429 `ACCOUNT_LOCKED`.

**POST `/auth/refresh`** · public (dipanggil hanya oleh BFF) · –
- Body: `refresh_token*` (≤512). 200 → `{ access_token, token_type, expires_in, refresh_token, refresh_token_expires_at, session_id }`.
- **Refresh token dirotasi** setiap kali; simpan yang baru secara atomik. Error: 401 `SESSION_INVALID`/`TOKEN_REUSED` (pakai token lama → seluruh sesi device dicabut). Tidak kena limiter auth 10/menit.

**POST `/auth/logout`** · Bearer · menu user → "Keluar"
- Tanpa body. 204. BFF lalu hapus session store + cookie.

**POST `/auth/logout-all`** · Bearer · `/settings/security` (Sesi)
- Body opsional: `{ "include_current": false }`. 204. Default mencabut semua sesi **kecuali** sesi sekarang; `true` → ikut keluar (BFF hapus sesi lalu redirect `/login`).

**POST `/auth/password/forgot`** · public · `/forgot-password`
- Body: `email*`. 202 selalu. Kode reset 6 digit dikirim ke akun aktif (berlaku 30 menit). Redirect ke `/reset-password?email=...`.

**POST `/auth/password/reset`** · public · `/reset-password`
- Body: `email*`, `code*`, `new_password*` (8–72). 204. **Semua sesi user dicabut** → arahkan ke `/login?reset=1`.
- Error: 400 `INVALID_OTP`/`OTP_EXPIRED`; 422 `WEAK_PASSWORD`; 429 `OTP_TOO_MANY_ATTEMPTS`.
- Juga dipakai user OAuth-only (`has_password=false`) untuk **membuat** password.

**GET `/auth/whoami`** · Bearer|ApiKey (scope `read`) · `/settings/api-keys` (tombol "Uji key", opsional)
- 200 → `{ "user_id": "...", "auth_method": "access_token"|"api_key", "roles": ["user"], "scopes": ["read"], "session_id": "...", "api_key_id": "..." }`.
- Error: 401 `API_KEY_INVALID`; 403 `INSUFFICIENT_SCOPE`. Untuk uji key, BFF mengirim `X-API-Key` dari input user **tanpa menyimpannya**.

### 7.2 Tag `oauth` (Google, opsional)

Bila backend tidak mengonfigurasi Google OAuth, endpoint ini membalas `404 NOT_FOUND`. Frontend memakai flag `NEXT_PUBLIC_GOOGLE_OAUTH_ENABLED` untuk menyembunyikan tombol.

**GET `/auth/oauth/google/start`** · public · tombol "Masuk dengan Google" di `/login` & `/register`
- **Navigasi browser** (`<a href>` / `window.location`), bukan fetch. 302 ke Google; set cookie flow `gac_oauth` (HttpOnly, SameSite=Lax, path `/api/v1/auth/oauth/google`, 10 menit).
- Karena cookie dipasang di origin yang melayani `/api/v1`, path `/api/v1/auth/oauth/google/*` **harus dilayani dari origin yang sama** dengan callback. Rekomendasi: Next `rewrites()` `/api/v1/auth/oauth/google/:path*` → `${BACKEND_URL}/api/v1/auth/oauth/google/:path*` dan `GOOGLE_OAUTH_REDIRECT_URL=https://<app>/api/v1/auth/oauth/google/callback`. Tombol mengarah ke `/api/v1/auth/oauth/google/start`.

**GET `/auth/oauth/google/callback`** · public · (dipanggil Google)
- Query: `state*`, `code`, `error`. Selalu 302 ke `${FRONTEND_URL}/auth/oauth/callback`:
  - login sukses → `#code=<one-time code>` (di **fragment**, tidak sampai ke server),
  - link sukses → `?linked=google`,
  - gagal → `?error=<CODE>` (`OAUTH_STATE_INVALID`, `OAUTH_CANCELED`, `OAUTH_EMAIL_NOT_VERIFIED`, `OAUTH_ACCOUNT_EXISTS`, `IDENTITY_TAKEN`, `OAUTH_FAILED`, `INTERNAL`, ...).
- Halaman `/auth/oauth/callback` (client component): baca `location.hash`, hapus hash dengan `history.replaceState`, POST ke `/bff/auth/oauth/exchange`.

**POST `/auth/oauth/exchange`** · public · `/auth/oauth/callback`
- Body: `code*` (≤128; berlaku ~60 detik, sekali pakai). 200 → `LoginResult` **atau** `MFAChallenge` (sama seperti login).
- Error: 400 `LOGIN_CODE_INVALID`; 403 `ACCOUNT_SUSPENDED`/`ACCOUNT_INACTIVE`; 404 bila OAuth nonaktif.

**GET `/users/me/identities`** · Bearer · `/settings/security` (Akun tertaut)
- 200 → `[{ "provider": "google", "email": "budi@gmail.com", "created_at": "..." }]`.

**POST `/users/me/identities/google/link`** · Bearer · `/settings/security`
- Tanpa body. 200 → `{ "auth_url": "https://accounts.google.com/o/oauth2/v2/auth?..." }` **dan** Set-Cookie flow `gac_oauth`.
- BFF meneruskan request ini lewat path same-origin `/api/v1/...` atau meneruskan `Set-Cookie` apa adanya (path `/api/v1/auth/oauth/google`) agar cookie tersedia saat callback. Validasi `auth_url` diawali `https://accounts.google.com/` lalu `window.location.assign(auth_url)`.
- Error: 404 bila OAuth nonaktif. Hasil akhir lewat callback `?linked=google` atau `?error=IDENTITY_TAKEN`.

### 7.3 Tag `users` (profil, password, sesi, security events)

**GET `/users/me`** · Bearer · layout `(app)` (dipanggil server-side), `/settings/profile`
- 200 → `User`. Error: 404 `USER_NOT_FOUND`. Cache: `queryKey ['me']`, `staleTime` 5 menit.

**PATCH `/users/me`** · Bearer · `/settings/profile`
- Body: `full_name` (1–100). Email tidak bisa diubah. 200 → `User`.
- Error: 400 `NO_FIELDS_TO_UPDATE`; 422 `INVALID_FULL_NAME`.

**DELETE `/users/me`** · Bearer · `/settings/account` (Zona bahaya)
- Body: `password*` (≤72). 204. Data pribadi dianonimkan, semua sesi dicabut. BFF hapus sesi → redirect `/goodbye`.
- Error: 401 `INVALID_CREDENTIALS`; 429 `ACCOUNT_LOCKED`. User `has_password=false` harus set password dulu via lupa password.

**PUT `/users/me/password`** (juga POST) · Bearer · `/settings/security`
- Body: `old_password*` (≤72), `new_password*` (8–72). 204. Sesi lain dicabut; sesi ini tetap login.
- Error: 401 `INVALID_CREDENTIALS` (password lama salah → tampilkan di field `old_password`); 422 `WEAK_PASSWORD`/`PASSWORD_REUSED`; 429 `ACCOUNT_LOCKED`.

**GET `/users/me/sessions`** · Bearer · `/settings/sessions`
- 200 → `[{ "id": "...", "user_agent": "Mozilla/5.0", "client_ip": "203.0.113.10", "created_at": "...", "last_used_at": "...", "expires_at": "...", "current": true }]` (terbaru dulu).
- Parse `user_agent` (mis. `ua-parser-js`) menjadi "Chrome di macOS". Tandai `current` dengan badge "Perangkat ini".

**DELETE `/users/me/sessions/{id}`** · Bearer · `/settings/sessions`
- 204. Error: 404 `SESSION_NOT_FOUND`. Mencabut sesi `current` = logout (gunakan tombol logout saja untuk baris current).

**GET `/users/me/security-events`** · Bearer · `/settings/security-events`
- Query: `limit`, `cursor`. 200 → list `{ id, event_type, outcome: 'success'|'failure', client_ip, user_agent, metadata, occurred_at }` + `meta`.
- `event_type` contoh: `login_succeeded`; terjemahkan via i18n `securityEvents.<event_type>` dengan fallback teks mentah (daftar tipe tidak ditetapkan di spec).

### 7.4 Tag `2fa`

**GET `/users/me/2fa`** · Bearer · `/settings/security` (kartu 2FA)
- 200 → `{ "enabled": true, "pending": false, "enabled_at": "...", "recovery_codes_remaining": 10 }`. Peringatkan bila `recovery_codes_remaining <= 3`.

**POST `/users/me/2fa/setup`** · Bearer · `/settings/security/2fa`
- Tanpa body. 200 → `{ "secret": "JBSWY3DP...", "otpauth_uri": "otpauth://totp/go-auth-clean:budi@example.com?secret=...&issuer=go-auth-clean" }`.
- Render QR **di client** (`qrcode` lib) dari `otpauth_uri`; tampilkan `secret` untuk input manual. Jangan simpan di cache Query yang persisten (`gcTime: 0`). Memanggil ulang mengganti secret pending.
- Error: 409 `MFA_ALREADY_ENABLED`.

**POST `/users/me/2fa/enable`** · Bearer · `/settings/security/2fa`
- Body: `code*` (6 digit dari app). 200 → `{ "recovery_codes": ["abcde-fghij", "..."] }` (10 kode, **ditampilkan sekali**). Sesi lain dicabut.
- Error: 401 `INVALID_MFA_CODE`; 409 `MFA_SETUP_REQUIRED`/`MFA_ALREADY_ENABLED`.

**POST `/users/me/2fa/disable`** · Bearer · `/settings/security`
- Body: `password*` (≤72), `code*` (6–32, TOTP atau recovery). 204. Sesi lain dicabut.
- Error: 401 `INVALID_CREDENTIALS`/`INVALID_MFA_CODE`; 409 `MFA_NOT_ENABLED`.

**POST `/users/me/2fa/recovery-codes`** · Bearer · `/settings/security`
- Body: `code*` (TOTP sekarang). 200 → `{ recovery_codes: string[10] }` (kode lama hangus, tampil sekali).
- Error: 401 `INVALID_MFA_CODE`; 409 `MFA_NOT_ENABLED`.

### 7.5 Tag `api-keys`

**GET `/users/me/api-keys`** · Bearer · `/settings/api-keys`
- 200 → `[{ "id": "...", "name": "CI pipeline", "prefix": "ab12cd34", "scopes": ["read"], "active": true, "created_at": "...", "expires_at": "...", "last_used_at": "...", "revoked_at": "..." }]`. Secret tidak pernah dikembalikan.

**POST `/users/me/api-keys`** · Bearer · `/settings/api-keys`
- Body: `name*` (1–100), `scopes*` (array ⊆ `["read","write"]`, maks 2, minimal 1), `expires_in_days` (1–365, kosong = 90).
- 201 → objek di atas + `"key": "gac_live_ab12cd34_0123456789abcdefghijABCDEFGHIJ01"` (**hanya di respons ini**). Tampilkan di dialog sekali-lihat dengan tombol salin + checkbox "Saya sudah menyimpannya".
- Error: 409 `API_KEY_LIMIT` (maks 10 aktif); 422 `INVALID_API_KEY_NAME`/`INVALID_SCOPE`/`INVALID_EXPIRY`.

**DELETE `/users/me/api-keys/{id}`** · Bearer · `/settings/api-keys`
- 204. Error: 404 `API_KEY_NOT_FOUND`. Konfirmasi dialog sebelum mencabut.

### 7.6 Tag `admin` (semua `Bearer+admin`)

Non-admin → `403 FORBIDDEN`. Frontend: area `/admin` dirender hanya bila `me.roles.includes('admin')`, selain itu `notFound()`.

**GET `/admin/users`** · `/admin/users`
- Query: `q` (cari email/nama, substring), `status` (`pending_verification`|`active`|`suspended`), `limit`, `cursor`. 200 → `User[]` + `meta` (terbaru dulu).
- Search di-debounce 300 ms; filter disimpan di URL search params.

**GET `/admin/users/{id}`** · `/admin/users/[id]`
- 200 → `User`. Error: 404 `USER_NOT_FOUND`.

**POST `/admin/users/{id}/suspend`** · `/admin/users/[id]`
- 204. Status → `suspended`, semua sesi & API key user dicabut. Error: 409 `CANNOT_MODIFY_SELF`/`INVALID_STATUS_TRANSITION`. Konfirmasi dialog wajib.

**POST `/admin/users/{id}/activate`** · `/admin/users/[id]`
- 204. Untuk user `suspended` (unlock). Error: 409 `CANNOT_MODIFY_SELF`/`INVALID_STATUS_TRANSITION`.

**POST `/admin/users/{id}/roles`** · `/admin/users/[id]`
- Body: `role*` (`user`|`admin`). 204, idempoten. Role baru berlaku setelah token target di-refresh (beri catatan di UI).
- Error: 422 `INVALID_ROLE`.

**DELETE `/admin/users/{id}/roles/{role}`** · `/admin/users/[id]`
- Path `role`: hanya `admin`. 204, idempoten. Error: 409 `CANNOT_MODIFY_SELF` (tidak bisa mencabut admin diri sendiri); 422 `INVALID_ROLE`.

**GET `/admin/users/{id}/security-events`** · `/admin/users/[id]` (tab Security events)
- Query: `limit`, `cursor`. 200 → list security event (bentuk sama dengan 7.3) + `meta`.

### 7.7 Tag `settings`

**GET `/settings`** · Bearer · layout `(app)` (prefetch server), `/settings/preferences`, `/onboarding`
- 200 → `{ "base_currency": "IDR", "timezone": "Asia/Jakarta", "week_start": 1, "updated_at": "..." }`. Dibuat otomatis dengan default saat pertama diakses.

**PUT `/settings`** · Bearer · `/settings/preferences`, `/onboarding`
- Body (semua wajib, replace penuh): `base_currency*` (kode dari `/currencies`), `timezone*` (IANA ≤64), `week_start*` (0–6, 0=Minggu).
- 200 → settings. Error: 400 `INVALID_TIMEZONE`; 422 `UNSUPPORTED_CURRENCY`/`INVALID_WEEK_START`.
- Setelah sukses invalidate **semua** query finance (batas bulan/"hari ini" berubah).

**GET `/currencies`** · Bearer · semua form uang (select currency)
- 200 → `[{ "code": "IDR", "minor_unit": 0, "name": "Indonesian Rupiah", "symbol": "Rp" }, ...]` (IDR, USD, SGD, JPY, EUR).

### 7.8 Tag `accounts`

**GET `/accounts`** · Bearer · `/accounts`, `/dashboard`, semua select akun
- Query: `include_archived` (bool, default false). 200 → `Account[]` (saldo = `balance`, cache server).
  ```json
  [{ "id": "0192...", "name": "BCA", "type": "bank", "currency": "IDR", "initial_balance": "1500000",
     "balance": "1465000", "allow_negative": true, "archived": false, "version": 3,
     "created_at": "2026-09-30T10:00:00Z", "updated_at": "2026-09-30T10:00:00Z" }]
  ```
- Catatan: hanya akun **milik sendiri**; akun yang dibagikan orang lain ada di `GET /shared-accounts`.

**POST `/accounts`** · Bearer · `/accounts/new` (dialog), `/onboarding`
- Body: `name*` (≤50), `type*` (`cash`|`bank`|`ewallet`|`credit_card`), `currency` (kosong = base currency), `initial_balance` (money ≤32, boleh negatif untuk kartu kredit bila allow_negative), `allow_negative` (kosong = default per tipe: `true` untuk bank & credit_card).
- 201 → `Account`. Error: 409 `DUPLICATE_NAME`; 422 `INVALID_AMOUNT`/`UNSUPPORTED_CURRENCY`/`INSUFFICIENT_BALANCE`.

**GET `/accounts/{id}`** · Bearer · `/accounts/[id]`
- 200 → `Account` + header `ETag`. Bisa diakses member shared wallet (owner/editor/viewer). Error: 404 `ACCOUNT_NOT_FOUND`.

**PATCH `/accounts/{id}`** · Bearer (owner) · `/accounts/[id]` (edit)
- Header `If-Match`. Body: `name` (≤50), `initial_balance` (mengubah saldo dengan delta yang sama), `allow_negative`. Currency & type tidak bisa diubah.
- 200 → `Account`. Error: 400 `INVALID_IF_MATCH`; 409 `VERSION_CONFLICT`/`DUPLICATE_NAME`; 422 `INSUFFICIENT_BALANCE`/`ACCOUNT_ARCHIVED`; 403 `FORBIDDEN` (bukan owner).

**DELETE `/accounts/{id}`** · Bearer (owner) · `/accounts/[id]`
- 204 (soft delete). Hanya bila belum punya transaksi/transfer; selain itu 409 `ACCOUNT_HAS_TRANSACTIONS` → tawarkan arsip.

**POST `/accounts/{id}/archive`** · Bearer (owner) · `/accounts/[id]`
- 200 → `Account` (`archived: true`). Idempoten. Akun arsip tidak menerima transaksi/transfer baru.

**POST `/accounts/{id}/unarchive`** · Bearer (owner) · `/accounts` (filter "Arsip")
- 200 → `Account`.

### 7.9 Tag `account-members` (shared wallet)

**GET `/shared-accounts`** · Bearer · `/shared`, select akun di form transaksi/transfer (untuk role `editor`)
- 200 → `[{ "account": Account, "owner_id": "...", "role": "viewer"|"editor" }]`.

**GET `/accounts/{id}/members`** · Bearer (member mana pun) · `/accounts/[id]/members`
- 200 → `[{ "account_id": "...", "user_id": "...", "owner_id": "...", "role": "editor", "created_at": "...", "updated_at": "..." }]`. Non-member → 404.
- Catatan: respons tidak memuat email/nama member; tampilkan `user_id` singkat atau email yang diketik saat undang (simpan lokal). **Tanyakan ke pemilik proyek** bila perlu nama (butuh perubahan backend).

**POST `/accounts/{id}/members`** · Bearer (owner) · `/accounts/[id]/members` (dialog undang)
- Body: `role*` (`viewer`|`editor`) + salah satu `email` (≤254) **atau** `user_id`.
- 201 → member. Error: 403 `FORBIDDEN`; 404 `USER_NOT_FOUND`; 409 `MEMBER_EXISTS`; 422 `INVALID_ROLE`.

**PATCH `/accounts/{id}/members/{member_id}`** · Bearer (owner) · `/accounts/[id]/members`
- `member_id` = **user_id** member. Body: `role*`. 200 → member. Error: 403/404 `MEMBER_NOT_FOUND`.

**DELETE `/accounts/{id}/members/{member_id}`** · Bearer (owner, atau member untuk dirinya = "Keluar dari dompet") · `/accounts/[id]/members`, `/shared`
- 204. Error: 403 `FORBIDDEN`; 404 `MEMBER_NOT_FOUND`.

### 7.10 Tag `categories`

**GET `/categories`** · Bearer · `/categories`, semua select kategori
- Query: `type` (`income`|`expense`). 200 → pohon `CategoryNode[]`:
  ```json
  [{ "id": "01920000-0000-7000-8000-000000000101", "name": "Makan & Minum", "type": "expense",
     "icon": "utensils", "color": "#FF8800", "is_system": true,
     "children": [{ "id": "...", "name": "Kopi", "parent_id": "01920000-...-0101", "is_system": false, "type": "expense" }] }]
  ```
- `staleTime` 10 menit. Komponen `CategoryPicker` (combobox bertingkat, ikon `lucide` berdasarkan `icon`, fallback ikon generik bila nama tidak dikenal).

**POST `/categories`** · Bearer · `/categories`
- Body: `name*` (≤50), `type*`, `parent_id` (harus kategori **root** dengan type sama), `icon` (≤30), `color` (`#RRGGBB`, ≤7).
- 201 → `Category`. Error: 404 `CATEGORY_NOT_FOUND` (parent); 409 `DUPLICATE_NAME`; 422 `CATEGORY_NESTING_TOO_DEEP`/`CATEGORY_TYPE_MISMATCH`.

**GET `/categories/{id}`** · Bearer · (detail jarang dipakai; ambil dari cache tree)

**PATCH `/categories/{id}`** · Bearer · `/categories`
- Body: `name`, `icon`, `color`. Kategori sistem → 422 `CATEGORY_READ_ONLY`. Error: 409 `DUPLICATE_NAME`.

**DELETE `/categories/{id}`** · Bearer · `/categories`
- Query: `reassign_to` (uuid kategori tipe sama). 204.
- Error: 409 `CATEGORY_IN_USE` (→ dialog pilih pengganti lalu ulangi dengan `reassign_to`), 409 `CATEGORY_HAS_CHILDREN`; 422 `CATEGORY_READ_ONLY`/`INVALID_REASSIGN_TARGET`.

### 7.11 Tag `tags`

**GET `/tags`** · Bearer · `/tags`, `TagMultiSelect`
- 200 → `Tag[]` (`{ id, name, color, created_at, updated_at }`).

**POST `/tags`** · Bearer · `/tags`, buat inline dari `TagMultiSelect`
- Body: `name*` (≤40 di spec; server membatasi 30 karakter setelah `#` di depan dibuang, unik case-insensitive), `color` (`#RRGGBB`). 201 → `Tag`. Error: 409 `DUPLICATE_NAME`.

**PATCH `/tags/{id}`** · Bearer · `/tags`
- Body: `name`, `color`. Error: 404 `TAG_NOT_FOUND`; 409 `DUPLICATE_NAME`.

**DELETE `/tags/{id}`** · Bearer · `/tags`
- 204; tag dilepas dari semua transaksi → invalidate `transactions`.

### 7.12 Tag `transactions`

**GET `/transactions`** · Bearer · `/transactions`, `/accounts/[id]` (tab Transaksi), `/dashboard` (5 terbaru, `limit=5`)
- Query: `from`, `to` (date, inklusif), `type`, `account_id[]`, `category_id[]`, `include_children` (bool), `min_amount`, `max_amount` (money), `currency` (untuk filter nominal), `q` (cari di note), `tag` (nama tag), `limit`, `cursor`.
- 200 → `Transaction[]` + `meta`. Urutan `transaction_date, id DESC`.
  ```json
  [{ "id": "0192...6b", "account_id": "0192...6a", "category_id": "01920000-...-0101", "type": "expense",
     "amount": "35000", "currency": "IDR", "transaction_date": "2026-09-30", "note": "Makan siang",
     "source": "manual", "tags": [{ "id": "...", "name": "liburan", "color": "#FF8800" }], "version": 1,
     "created_at": "2026-09-30T10:00:00Z", "updated_at": "2026-09-30T10:00:00Z" }]
  ```
- Error: 400 `INVALID_PARAMETER`/`INVALID_CURSOR`/`INVALID_RANGE`; 422 `INVALID_AMOUNT`.
- Filter disinkronkan ke URL (`nuqs` atau `useSearchParams`) agar bisa dibagikan/back-button.

**POST `/transactions`** · Bearer · dialog "Tambah transaksi" (FAB global), `/transactions`
- Header **`Idempotency-Key*`**. Body: `account_id*`, `category_id*` (type harus sama), `type*`, `amount*` (> 0), `transaction_date*` (≤ hari ini di timezone user), `note` (≤1024), `tag_ids` (≤10).
- 201 → `Transaction`. Error: 404 `ACCOUNT_NOT_FOUND`/`CATEGORY_NOT_FOUND`; 409 `IDEMPOTENCY_IN_PROGRESS`; 422 `INSUFFICIENT_BALANCE`/`CURRENCY_MISMATCH`/`CATEGORY_TYPE_MISMATCH`/`ACCOUNT_ARCHIVED`/`DATE_IN_FUTURE`/`IDEMPOTENCY_KEY_REUSED`.
- Invalidate: `transactions`, `accounts`, `budgets`, `reports`.

**GET `/transactions/{id}`** · Bearer · `/transactions/[id]` (sheet detail)
- 200 → `Transaction` + `ETag`.

**PATCH `/transactions/{id}`** · Bearer · `/transactions/[id]`
- Header `If-Match`. Body: `account_id`, `category_id`, `type`, `amount`, `transaction_date`, `note` (semua opsional). Tag diubah lewat endpoint terpisah.
- 200 → `Transaction`. Error: 409 `VERSION_CONFLICT`; 422 `INSUFFICIENT_BALANCE`/`MANAGED_BY_TRANSFER`/`ACCOUNT_ARCHIVED`.

**DELETE `/transactions/{id}`** · Bearer · `/transactions` (aksi baris), `/transactions/[id]`
- 204 (soft delete, saldo dikembalikan). Error: 409 `VERSION_CONFLICT`; 422 `INSUFFICIENT_BALANCE`/`MANAGED_BY_TRANSFER`/`ACCOUNT_ARCHIVED`. Tawarkan toast "Dibatalkan?" **tidak** mungkin (tidak ada undelete) → pakai dialog konfirmasi.

**PUT `/transactions/{id}/tags`** · Bearer · `/transactions/[id]`
- Body: `tag_ids` (≤10, `[]` = hapus semua). 200 → `Tag[]`. Error: 404 `TAG_NOT_FOUND`; 422 `TOO_MANY_TAGS`.
- Saat create, kirim `tag_ids` langsung di body POST; saat edit, panggil PATCH lalu PUT tags (dua mutasi, tampilkan satu status).

**GET `/transactions/export`** · Bearer · `/transactions` (tombol "Ekspor CSV")
- Query: filter sama dengan list (`from`, `to`, `type`, `account_id[]`, `category_id[]`, `tag`, `q`) + `format=csv`. 200 → **file CSV** (stream, terlama dulu). Kolom: `date,type,amount,currency,account,category,note,tags` (tags dipisah `;`). Sel diawali `= + - @` sudah diberi prefix `'` oleh server.
- BFF route `GET /bff/export/transactions` mem-pipe stream dan meneruskan `Content-Type`/`Content-Disposition`; frontend memicu download via `<a href download>` (bukan fetch → blob untuk file besar).

**POST `/transactions/import`** · Bearer · `/transactions/import`
- `multipart/form-data`, field `file*` (CSV ≤ 5 MB, ≤ 10.000 baris, format sama dengan export; `account`/`category` boleh nama atau ID; `currency` & `tags` opsional, tag harus sudah ada). Query: `dry_run` (bool).
- 200 (dry run) / 201 (import) → `{ "dry_run": true, "imported": 120, "skipped": 2, "duplicate_rows": [4, 9], "errors": [{ "row": 3, "field": "amount", "message": "invalid amount" }] }`.
- Semua baris valid di-commit dalam satu DB transaction; baris kemungkinan duplikat dilewati.
- Error: 400 `INVALID_CSV_HEADER`; 413 `IMPORT_TOO_LARGE`; 422 `IMPORT_TOO_MANY_ROWS`.
- BFF meneruskan body multipart apa adanya (stream, jangan parse) dengan batas 5 MB + overhead.

### 7.13 Tag `transfers`

**GET `/transfers`** · Bearer · `/transfers`, `/accounts/[id]` (tab Transfer)
- Query: `from`, `to`, `account_id` (asal **atau** tujuan, satu nilai), `limit`, `cursor`. 200 → `Transfer[]` + `meta`.
  ```json
  [{ "id": "0192...6d", "from_account_id": "0192...6a", "to_account_id": "0192...6c", "amount": "500000",
     "currency": "IDR", "to_amount": "500000", "to_currency": "IDR", "fee": "6500",
     "fee_transaction_id": "...", "transfer_date": "2026-09-30", "note": "Top up GoPay", "version": 1 }]
  ```

**POST `/transfers`** · Bearer · dialog "Transfer", `/transfers`
- Header **`Idempotency-Key*`**. Body: `from_account_id*`, `to_account_id*` (≠ from), `amount*`, `transfer_date*`, `fee` (dicatat sebagai expense "Biaya Admin" di akun asal), `to_amount` (**wajib bila currency akun tujuan beda**, nominal yang diterima), `note` (≤1024).
- 201 → `Transfer`. Error: 422 `SAME_ACCOUNT_TRANSFER`/`INSUFFICIENT_BALANCE`/`CURRENCY_MISMATCH`/`ACCOUNT_ARCHIVED`/`IDEMPOTENCY_KEY_REUSED`; 409 `IDEMPOTENCY_IN_PROGRESS`.
- UI: saat currency berbeda tampilkan field `to_amount` + hint kurs dari `GET /exchange-rates/convert` (prefill, user boleh ubah).

**GET `/transfers/{id}`** · Bearer · `/transfers/[id]` → 200 `Transfer` + `ETag`.

**PATCH `/transfers/{id}`** · Bearer · `/transfers/[id]`
- Header `If-Match`. Body opsional: `from_account_id`, `to_account_id`, `amount`, `to_amount`, `fee` (`"0"` menghapus fee), `transfer_date`, `note`. 200 → `Transfer`.
- Error: 409 `VERSION_CONFLICT`; 422 `SAME_ACCOUNT_TRANSFER`/`INSUFFICIENT_BALANCE`/`CURRENCY_MISMATCH`.

**DELETE `/transfers/{id}`** · Bearer · `/transfers/[id]`
- 204 (fee transaction ikut dihapus). Error: 409 `VERSION_CONFLICT`; 422 `INSUFFICIENT_BALANCE`/`ACCOUNT_ARCHIVED`.

### 7.14 Tag `budgets`

**GET `/budgets`** · Bearer · `/budgets`, `/dashboard` (ringkasan)
- Query: `month` (`YYYY-MM`, default bulan berjalan di timezone user). 200 → `Budget[]`:
  ```json
  [{ "id": "...", "category_id": "01920000-...-0101", "period_month": "2026-10", "amount": "2000000",
     "currency": "IDR", "alert_threshold_pct": 80, "spent": "1700000", "remaining": "300000",
     "progress": "85.00", "status": "warning", "overspent": false, "version": 1 }]
  ```
- `spent` = expense kategori + sub kategori (dihitung saat dibaca). `status`: `ok` | `warning` (≥ threshold) | `exceeded`. Warna progress bar: hijau / kuning / merah; `remaining` negatif = lebih.

**POST `/budgets`** · Bearer · `/budgets` (dialog)
- Header **`Idempotency-Key*`**. Body: `category_id*` (kategori **expense**), `amount*`, `currency` (kosong = base), `period_month` (kosong = bulan ini), `alert_threshold_pct` (1–100).
- 201 → `Budget`. Error: 404 `CATEGORY_NOT_FOUND`; 409 `BUDGET_EXISTS`; 422 `CATEGORY_TYPE_MISMATCH`/`INVALID_AMOUNT`.

**GET `/budgets/{id}`** · Bearer · `/budgets/[id]` → 200 `Budget` + `ETag`.

**PATCH `/budgets/{id}`** · Bearer · `/budgets/[id]`
- Header `If-Match`. Body: `amount`, `alert_threshold_pct`. Error: 409 `VERSION_CONFLICT`.

**DELETE `/budgets/{id}`** · Bearer · `/budgets` → 204.

### 7.15 Tag `recurring-rules`

**GET `/recurring-rules`** · Bearer · `/recurring`
- Query: `limit`, `cursor`. 200 → list + `meta`:
  ```json
  [{ "id": "...", "type": "expense", "account_id": "...", "category_id": "...", "amount": "186000",
     "currency": "IDR", "note": "Netflix", "frequency": "monthly", "interval": 1, "by_month_day": 3,
     "start_date": "2026-10-03", "end_date": "2027-10-03", "next_run_date": "2026-11-03",
     "last_run_date": "2026-10-03", "upcoming": ["2026-11-03", "2026-12-03"],
     "status": "active", "pause_reason": "", "version": 1 }]
  ```

**POST `/recurring-rules`** · Bearer · `/recurring/new`
- Header **`Idempotency-Key*`**. Body: `type*`, `account_id*`, `category_id*`, `amount*`, `frequency*` (`daily`|`weekly`|`monthly`|`yearly`), `start_date*`, `interval` (1–365, default 1), `end_date` **atau** `count` (1–1000) — tidak boleh keduanya, `note` (≤255).
- Bulanan/tahunan berlabuh di tanggal `start_date` (di-clamp ke akhir bulan). Transaksi dibuat worker (interval default 5 menit).
- 201 → rule. Error: 404 `ACCOUNT_NOT_FOUND`/`CATEGORY_NOT_FOUND`; 422 `INVALID_FREQUENCY`/`CATEGORY_TYPE_MISMATCH`/`ACCOUNT_ARCHIVED`.

**GET `/recurring-rules/{id}`** · Bearer · `/recurring/[id]` → 200 + `ETag`.

**PATCH `/recurring-rules/{id}`** · Bearer · `/recurring/[id]`
- Header `If-Match`. Body opsional: `type`, `account_id`, `category_id`, `amount`, `frequency`, `interval`, `start_date`, `end_date` (`""` = hapus), `count`, `note`. Jadwal berubah → `next_run_date` dihitung ulang.
- Error: 409 `VERSION_CONFLICT`; 422 `RULE_ENDED`/`INVALID_FREQUENCY`.

**DELETE `/recurring-rules/{id}`** · Bearer · `/recurring/[id]` → 204 (transaksi yang sudah dibuat tetap ada).

**POST `/recurring-rules/{id}/pause`** · Bearer · `/recurring`
- Body: `reason` (≤255, opsional). 200 → rule (`status: "paused"`). Error: 422 `RULE_ENDED`.

**POST `/recurring-rules/{id}/resume`** · Bearer · `/recurring`
- Tanpa body. 200 → rule. Lanjut dari hari ini; jadwal yang terlewat saat pause **dilewati** (beri tahu user).

### 7.16 Tag `exchange-rates`

**GET `/exchange-rates`** · Bearer · `/settings/exchange-rates`
- 200 → `[{ "id": "...", "base": "USD", "quote": "IDR", "rate": "16250.5", "as_of": "2026-10-01", "created_at": "..." }]`.

**POST `/exchange-rates`** · Bearer · `/settings/exchange-rates`
- Body: `base*`, `quote*`, `rate*` (money string >0, ≤32), `as_of*` (date). Artinya 1 base = rate quote, berlaku mulai `as_of`; pasangan kebalikan diturunkan otomatis.
- 201 → rate. Error: 409 `RATE_EXISTS`; 422 `UNSUPPORTED_CURRENCY`.

**GET `/exchange-rates/{id}`** · Bearer → 200 rate.

**PATCH `/exchange-rates/{id}`** · Bearer · `/settings/exchange-rates`
- Body: `rate*`. 200 → rate.

**DELETE `/exchange-rates/{id}`** · Bearer → 204.

**GET `/exchange-rates/convert`** · Bearer · form transfer lintas currency, widget konversi
- Query: `amount*`, `from*`, `to` (default base), `date` (default hari ini). 200 → `{ "from": { "amount": "100", "currency": "USD" }, "to": { "amount": "1625050", "currency": "IDR" } }`.
- Error: 400 `INVALID_PARAMETER`; 422 `RATE_UNAVAILABLE`.

### 7.17 Tag `savings-goals`

**GET `/savings-goals`** · Bearer · `/goals`, `/dashboard`
- 200 → `[{ "id": "...", "name": "Dana darurat", "target": "30000000", "saved": "12000000", "remaining": "18000000", "progress": "40.00", "monthly_needed": "2000000", "currency": "IDR", "target_date": "2027-06-30", "account_id": "...", "status": "active"|"achieved"|"archived", "version": 1 }]`.

**POST `/savings-goals`** · Bearer · `/goals` (dialog)
- Body: `name*` (≤100), `target*` (money), `target_date`, `account_id` (akun tabungan tertaut), `currency` (kosong = currency akun / base). 201 → goal. Error: 409 `DUPLICATE_NAME`; 404 `ACCOUNT_NOT_FOUND`.

**GET `/savings-goals/{id}`** · Bearer · `/goals/[id]` → 200 + `ETag`.

**PATCH `/savings-goals/{id}`** · Bearer · `/goals/[id]`
- Header `If-Match`. Body: `name`, `target`, `target_date` (`""` = hapus), `account_id` (`""` = lepas), `archived` (bool). Error: 409 `VERSION_CONFLICT`/`DUPLICATE_NAME`.

**DELETE `/savings-goals/{id}`** · Bearer → 204.

**GET `/savings-goals/{id}/contributions`** · Bearer · `/goals/[id]` (riwayat)
- 200 → `[{ "id": "...", "goal_id": "...", "amount": "500000", "currency": "IDR", "date": "2026-10-01", "note": "", "transfer_id": "...", "created_at": "..." }]` (penarikan = amount negatif).

**POST `/savings-goals/{id}/contributions`** · Bearer · `/goals/[id]` (dialog "Setor")
- Body: **salah satu** `transfer_id` (tautkan transfer ke akun goal; nominal diambil dari transfer) **atau** `amount` + `date` (setoran mandiri); `note` (≤500). **Tidak** memakai Idempotency-Key.
- 201 → `{ "contribution": {...}, "goal": {...} }` (goal terbaru, langsung `setQueryData`). Status → `achieved` saat `saved >= target`.
- Error: 404 `TRANSFER_NOT_FOUND`; 409 `DUPLICATE_LINK`; 422 `GOAL_ARCHIVED`.

**POST `/savings-goals/{id}/withdrawals`** · Bearer · `/goals/[id]` (dialog "Tarik")
- Body sama (`ContributeRequest`); dicatat sebagai kontribusi negatif. 201 → `{ contribution, goal }`.

**DELETE `/savings-goals/{id}/contributions/{cid}`** · Bearer · `/goals/[id]` → 204. Error: 404 `CONTRIBUTION_NOT_FOUND`.

### 7.18 Tag `debts`

**GET `/debts`** · Bearer · `/debts`
- Query: `status` (`open`|`settled`). 200 → `[{ "id": "...", "direction": "payable"|"receivable", "counterparty": "Budi", "principal": "1500000", "paid": "500000", "remaining": "1000000", "currency": "IDR", "start_date": "2026-10-01", "due_date": "2026-12-31", "note": "", "status": "open", "version": 1 }]`.
- `payable` = hutang saya; `receivable` = piutang (orang lain berhutang ke saya). Tab terpisah.

**POST `/debts`** · Bearer · `/debts` (dialog)
- Body: `direction*`, `counterparty*` (≤100), `principal*`, `start_date*`, `due_date`, `currency`, `note` (≤500). 201 → debt.

**GET `/debts/{id}`** · Bearer · `/debts/[id]` → 200 + `ETag`.

**PATCH `/debts/{id}`** · Bearer · `/debts/[id]`
- Header `If-Match`. Body: `counterparty`, `principal` (status dievaluasi ulang; < paid → 422 `OVERPAYMENT`), `due_date` (`""` = hapus), `note`. Error: 409 `VERSION_CONFLICT`.

**DELETE `/debts/{id}`** · Bearer → 204.

**GET `/debts/{id}/payments`** · Bearer · `/debts/[id]` → 200 `[{ id, debt_id, amount, currency, date, note, transaction_id, created_at }]`.

**POST `/debts/{id}/payments`** · Bearer · `/debts/[id]` (dialog "Catat pembayaran")
- Header **`Idempotency-Key*`**. Body: `amount`, `date`, `note` (≤500), dan opsi pencatatan kas: `transaction_id` (tautkan transaksi yang ada) **atau** `account_id` (+ `category_id`) untuk membuat transaksi baru (expense untuk payable, income untuk receivable). Pembayaran parsial boleh.
- 201 → `{ "payment": {...}, "debt": {...} }`. Status → `settled` saat paid = principal.
- Error: 404 `ACCOUNT_NOT_FOUND`/`TRANSACTION_NOT_FOUND`; 409 `DUPLICATE_LINK`; 422 `OVERPAYMENT`/`DEBT_SETTLED`.

**DELETE `/debts/{id}/payments/{pid}`** · Bearer · `/debts/[id]` → 204. Transaksi tertaut **tidak** ikut terhapus (beri tahu user + link ke transaksi).

### 7.19 Tag `bills`

**GET `/bills`** · Bearer · `/bills`, `/dashboard` (jatuh tempo terdekat)
- 200 → `[{ "id": "...", "name": "Listrik PLN", "amount": "450000", "currency": "IDR", "frequency": "monthly", "next_due_date": "2026-10-20", "remind_days_before": 3, "overdue": false, "status": "active"|"paused"|"done", "account_id": "...", "category_id": "...", "version": 1 }]`.
- Urutkan client-side berdasarkan `next_due_date`; badge "Terlambat" bila `overdue`, "Segera" bila dalam `remind_days_before` hari.

**POST `/bills`** · Bearer · `/bills` (dialog)
- Body: `name*` (≤100), `amount*`, `due_date*`, `frequency*` (`once`|`weekly`|`monthly`|`yearly`), `currency`, `remind_days_before` (0–30, default 3), `account_id`/`category_id` (default saat dibayar). 201 → bill.
- Error: 404 `ACCOUNT_NOT_FOUND`/`CATEGORY_NOT_FOUND`; 422 `INVALID_FREQUENCY`.

**GET `/bills/{id}`** · Bearer · `/bills/[id]` → 200 + `ETag`.

**PATCH `/bills/{id}`** · Bearer · `/bills/[id]`
- Header `If-Match`. Body: `name`, `amount`, `due_date`, `frequency`, `remind_days_before`, `account_id`/`category_id` (`""` = hapus default), `paused` (bool). Error: 409 `VERSION_CONFLICT`; 422 `BILL_DONE`.

**DELETE `/bills/{id}`** · Bearer → 204.

**POST `/bills/{id}/pay`** · Bearer · `/bills` (tombol "Tandai lunas")
- Header **`Idempotency-Key*`**. Body: `create_transaction` (bool), `account_id`, `category_id`, `amount` (kosong = nominal bill), `date`, `note` (≤500).
- 200 → `{ "bill": {...}, "transaction": {...} }` (`transaction` hanya bila dibuat). `next_due_date` maju sesuai frekuensi; `once` → `done`.
- Error: 404 `ACCOUNT_NOT_FOUND`; 422 `BILL_DONE`/`INSUFFICIENT_BALANCE`.

### 7.20 Tag `reports`

**GET `/reports/summary`** · Bearer · `/dashboard`, `/reports` (tab Bulanan)
- Query: `month` (`YYYY-MM`). 200 →
  ```json
  { "month": "2026-09", "currency": "IDR",
    "total_balance": [{ "amount": "1465000", "currency": "IDR" }],
    "totals": [{ "currency": "IDR", "income": "10000000", "expense": "3500000", "net": "6500000" }],
    "by_category": [{ "category_id": "...", "name": "Makan & Minum", "total": "1250000", "percent": "35.71" }],
    "cashflow": [{ "period": "2026-09-01", "income": "0", "expense": "35000", "net": "-35000" }] }
  ```
- Transfer tidak dihitung; fee transfer dihitung expense.

**GET `/reports/cashflow`** · Bearer · `/reports` (tab Arus kas)
- Query: `from`, `to`, `granularity` (`day`|`month`, default month), `currency`. Default: bulan ini (day) / 12 bulan terakhir (month). Maks 366 hari / 120 bulan.
- 200 → `{ "currency": "IDR", "granularity": "month", "points": [{ "period": "2026-09-01", "income": "...", "expense": "...", "net": "..." }] }` (periode kosong ikut).
- Error: 400 `INVALID_RANGE`; 422 `UNSUPPORTED_CURRENCY`.

**GET `/reports/categories`** · Bearer · `/reports` (tab Kategori)
- Query: `from`, `to`, `type` (default expense), `currency`. 200 → `{ "currency": "IDR", "type": "expense", "items": [{ "category_id", "name", "total", "percent" }] }` (sub kategori digabung ke root).

**GET `/reports/yearly`** · Bearer · `/reports` (tab Tahunan)
- Query: `year` (int). 200 → `{ "year": 2026, "currency": "IDR", "income": "...", "expense": "...", "net": "...", "savings_rate": "40.00", "months": [{ "month": "2026-01", "income", "expense", "net" }], "unconverted": [CurrencyTotals] }`.
- Bila `unconverted` tidak kosong tampilkan banner "Sebagian mata uang belum punya kurs" + link ke kurs.

**GET `/reports/reconciliation`** · Bearer · `/settings/data` (alat "Cek konsistensi saldo")
- 200 → `{ "ok": true, "drifts": [{ "account_id", "currency", "cached_balance", "expected_balance" }] }`. Read-only; tampilkan selisih (tidak ada aksi perbaikan dari frontend).

### 7.21 Tag `audit`

**GET `/audit-logs`** · Bearer · `/activity`, `/accounts/[id]` (tab Aktivitas)
- Query: `entity` (`account`|`transaction`|`transfer`|`account_member`), `entity_id`, `limit`, `cursor`. 200 → list + `meta`:
  ```json
  [{ "id": "...", "actor_id": "...", "entity": "transaction", "entity_id": "...", "action": "update",
     "before": { "amount": "35000" }, "after": { "amount": "40000" }, "created_at": "..." }]
  ```
- Termasuk perubahan oleh member shared wallet (`actor_id` ≠ saya → label "oleh anggota"). Tampilkan diff field `before`/`after` (escape semua nilai, render sebagai teks).

---

## 8. Alur pengguna (user flows)

Semua diagram memakai notasi Mermaid. "BFF" = route handler Next (`/bff/*`), "API" = backend `/api/v1`.

### 8.1 Register + verifikasi email (OTP)

```mermaid
sequenceDiagram
  actor U as User
  participant W as Web (/register)
  participant B as BFF
  participant A as API
  participant M as Email
  U->>W: isi nama, email, password
  W->>B: POST /bff/api/auth/register
  B->>A: POST /auth/register
  A-->>M: kode OTP 6 digit (10 menit)
  A-->>B: 201 User(pending_verification)
  B-->>W: 201
  W->>U: redirect /verify-email?email=...
  U->>W: input 6 digit (auto-submit saat lengkap)
  W->>B: POST /auth/verify-email
  B->>A: POST /auth/verify-email
  alt sukses / already_verified
    A-->>W: 200 {verified:true}
    W->>U: redirect /login?verified=1&email=...
  else INVALID_OTP / OTP_EXPIRED / OTP_TOO_MANY_ATTEMPTS
    A-->>W: 400/429
    W->>U: error + tombol "Kirim ulang" (POST /auth/verify-email/resend, cooldown 60s)
  end
```

- Input OTP: 6 kotak (`inputMode="numeric"`, `autocomplete="one-time-code"`), paste penuh didukung.
- Email di query string hanya untuk prefill; user boleh mengubahnya.

### 8.2 Login (dengan/tanpa 2FA)

```mermaid
flowchart TD
  A[/login: email + password/] -->|POST /bff/auth/login| B{respons API}
  B -->|200 LoginResult| C[BFF simpan token di session store, set __Host-sid]
  C --> D{next param aman?}
  D -->|ya| E[redirect next]
  D -->|tidak| F[redirect /dashboard atau /onboarding bila belum ada akun]
  B -->|200 mfa_required| G[BFF set __Host-mfa, redirect /login/2fa]
  G --> H[/input TOTP atau recovery code/]
  H -->|POST /bff/auth/login/2fa| I{respons}
  I -->|200| C
  I -->|INVALID_MFA_CODE| H
  I -->|MFA_TOKEN_INVALID / MFA_CHALLENGE_EXPIRED| A
  B -->|EMAIL_NOT_VERIFIED| J[redirect /verify-email?email=...]
  B -->|INVALID_CREDENTIALS| A
  B -->|ACCOUNT_LOCKED / RATE_LIMITED| K[banner + countdown Retry-After]
  B -->|ACCOUNT_SUSPENDED / ACCOUNT_INACTIVE| L[pesan akun tidak aktif]
```

- Halaman 2FA: toggle "Gunakan recovery code" mengganti input 6 digit menjadi input teks `xxxxx-xxxxx`. Timer kedaluwarsa dari `expires_at` (5 menit).
- `?reason=session_expired` di `/login` → banner info "Sesi Anda berakhir, silakan masuk lagi".

### 8.3 Lupa / reset password

```mermaid
sequenceDiagram
  actor U as User
  participant W as Web
  participant A as API (via BFF)
  U->>W: /forgot-password, isi email
  W->>A: POST /auth/password/forgot
  A-->>W: 202 (selalu)
  W->>U: "Jika email terdaftar, kode telah dikirim" + redirect /reset-password?email=...
  U->>W: kode 6 digit + password baru (2x)
  W->>A: POST /auth/password/reset
  alt 204
    A-->>W: semua sesi dicabut
    W->>U: redirect /login?reset=1
  else INVALID_OTP / OTP_EXPIRED / OTP_TOO_MANY_ATTEMPTS / WEAK_PASSWORD
    W->>U: error di field; OTP_TOO_MANY_ATTEMPTS -> link "minta kode baru"
  end
```

Email "password changed" berisi link ke `/forgot-password?email=...` (untuk user yang tidak merasa mengganti password); halaman harus menerima prefill tersebut.

### 8.4 Google OAuth: login dan link akun

```mermaid
sequenceDiagram
  actor U as User
  participant W as Web (same origin)
  participant A as API
  participant G as Google
  rect rgb(240,240,255)
  note over U,G: Login
  U->>W: klik "Masuk dengan Google" (href=/api/v1/auth/oauth/google/start)
  W->>A: (rewrite) GET /auth/oauth/google/start
  A-->>U: 302 Google + Set-Cookie gac_oauth
  U->>G: consent
  G-->>A: GET /auth/oauth/google/callback?code&state
  A-->>U: 302 /auth/oauth/callback#code=XYZ
  U->>W: halaman callback baca hash, replaceState
  W->>A: POST /bff/auth/oauth/exchange {code} -> POST /auth/oauth/exchange
  A-->>W: LoginResult atau mfa_required
  W->>U: /dashboard atau /login/2fa
  end
  rect rgb(240,255,240)
  note over U,G: Link (sudah login)
  U->>W: /settings/security -> "Hubungkan Google"
  W->>A: POST /users/me/identities/google/link (Set-Cookie diteruskan)
  A-->>W: {auth_url}
  W->>G: location.assign(auth_url)
  G-->>A: callback
  A-->>U: 302 /auth/oauth/callback?linked=google | ?error=IDENTITY_TAKEN
  U->>W: callback -> redirect /settings/security?linked=google (toast sukses)
  end
```

- `?error=OAUTH_ACCOUNT_EXISTS`: pesan "Email sudah terdaftar dengan password. Masuk dengan password lalu hubungkan Google di Pengaturan".
- Halaman callback membedakan konteks: ada `#code` → login; `?linked` → link; `?error` → tampilkan pesan + link kembali (ke `/login` atau `/settings/security` bila user sedang login).

### 8.5 Manajemen sesi & logout

```mermaid
flowchart LR
  S[/settings/sessions/] -->|GET /users/me/sessions| L[daftar perangkat]
  L -->|Cabut baris lain| R[DELETE /users/me/sessions/:id] --> L
  L -->|Keluar dari semua perangkat lain| O[POST /auth/logout-all {include_current:false}] --> L
  L -->|Keluar dari semua termasuk ini| X[POST /auth/logout-all {include_current:true}] --> Z[BFF hapus sesi -> /login]
  M[Menu user -> Keluar] -->|POST /bff/auth/logout| Z
```

Setelah ganti password, aktifkan/nonaktifkan 2FA: sesi lain dicabut oleh server → refetch daftar sesi dan tampilkan toast "Perangkat lain telah dikeluarkan".

### 8.6 Setup 2FA

```mermaid
stateDiagram-v2
  [*] --> Off: GET /users/me/2fa enabled=false
  Off --> Pending: POST /users/me/2fa/setup (QR + secret)
  Pending --> Pending: setup lagi (secret baru)
  Pending --> ShowCodes: POST /users/me/2fa/enable {code} 200
  Pending --> Pending: INVALID_MFA_CODE
  ShowCodes --> On: user konfirmasi sudah menyimpan 10 recovery code
  On --> ShowCodes: POST /users/me/2fa/recovery-codes {code}
  On --> Off: POST /users/me/2fa/disable {password, code}
```

Recovery code: tampilkan grid 2 kolom, tombol **Salin semua** dan **Unduh .txt** (dibuat di client via Blob). Tidak bisa menutup dialog sebelum checkbox "Saya sudah menyimpan kode ini" dicentang.

### 8.7 API keys

1. `/settings/api-keys` → `GET /users/me/api-keys` (tabel: nama, prefix `gac_…_ab12cd34`, scope, dibuat, terakhir dipakai, kedaluwarsa, status).
2. "Buat key" → form (nama, scope checkbox read/write, masa berlaku 30/90/180/365 hari) → `POST` → dialog sekali-lihat berisi `key` + contoh `curl -H "X-API-Key: <key>" .../api/v1/auth/whoami`.
3. "Cabut" → konfirmasi → `DELETE` → invalidate list. Tombol "Buat" disabled bila 10 key aktif.

### 8.8 Hapus akun

`/settings/account` → zona bahaya → dialog: ketik `HAPUS` + password → `DELETE /users/me {password}` → BFF hapus sesi → `/goodbye`. Bila `has_password=false`: dialog menjelaskan perlu set password lewat lupa password dulu (link `/forgot-password?email=<email>`).

### 8.9 Onboarding (login pertama)

```mermaid
flowchart TD
  L[Login sukses] --> Q{GET /accounts kosong?}
  Q -->|tidak| D[/dashboard/]
  Q -->|ya| S1[Langkah 1: preferensi - GET/PUT /settings: currency dasar, timezone (default dari Intl browser), awal minggu]
  S1 --> S2[Langkah 2: akun pertama - POST /accounts (nama, tipe, saldo awal)]
  S2 --> S3[Langkah 3 opsional: budget pertama - GET /categories?type=expense, POST /budgets]
  S3 --> D
```

Onboarding bisa dilewati; tandai selesai di `localStorage` (hanya kenyamanan) + kriteria utama tetap "punya ≥1 akun".

### 8.10 Dashboard

Paralel (server prefetch + `HydrationBoundary`): `GET /reports/summary?month=<bulan ini>`, `GET /accounts`, `GET /budgets`, `GET /transactions?limit=5`, `GET /bills`, `GET /savings-goals`.

Widget: total saldo per currency, income vs expense bulan ini, donut kategori, grafik cashflow harian, budget yang `warning`/`exceeded`, 5 transaksi terakhir, tagihan ≤ 7 hari / terlambat, progress goal. Selector bulan mengubah `month` untuk summary & budget.

### 8.11 Transaksi: CRUD, filter, tag

```mermaid
flowchart TD
  A[/transactions/] -->|useInfiniteQuery GET /transactions| B[list dikelompokkan per tanggal]
  A --> F[Filter bar: rentang tanggal, type, akun multi, kategori multi + sub, nominal min/max, tag, cari note]
  F -->|ubah filter: reset cursor, update URL| A
  B -->|klik baris| D[Sheet detail GET /transactions/:id]
  D -->|Edit| E[PATCH + If-Match] -->|409 VERSION_CONFLICT| C[Dialog konflik]
  D -->|Ubah tag| T[PUT /transactions/:id/tags]
  D -->|Hapus| X[konfirmasi -> DELETE]
  N[FAB Tambah] --> G[Form: type toggle, nominal, akun, kategori (filter type), tanggal, catatan, tag]
  G -->|POST + Idempotency-Key| B
```

- Transaksi `source=transfer_fee`: badge "Biaya transfer", aksi edit/hapus diganti link ke transfer. `source=recurring`: badge "Otomatis". `source=import`: badge "Impor".
- Setelah mutasi: invalidate `['transactions']`, `['accounts']`, `['budgets']`, `['reports']`. Optimistic update hanya untuk hapus (rollback on error).
- Akun shared dengan role editor ikut muncul di select akun (gabungan `/accounts` + `/shared-accounts` role editor).

### 8.12 Import (dry run) dan export CSV

```mermaid
sequenceDiagram
  actor U as User
  participant W as /transactions/import
  participant A as API (via BFF)
  U->>W: pilih file .csv (cek ekstensi, <= 5 MB di client)
  W->>A: POST /transactions/import?dry_run=true (multipart file)
  A-->>W: 200 {imported, skipped, duplicate_rows, errors[]}
  W->>U: ringkasan: N valid, M duplikat dilewati, tabel error per baris (row, field, message)
  U->>W: "Impor N baris"
  W->>A: POST /transactions/import (file sama)
  A-->>W: 201 {imported, skipped, ...}
  W->>U: toast sukses, invalidate transactions/accounts/reports
```

- Sediakan tombol "Unduh template" (CSV dibuat client: header `date,type,amount,account,category,currency,note,tags` + 1 contoh baris).
- `INVALID_CSV_HEADER` → tampilkan header wajib `date,type,amount,account,category`.
- Export: tombol di toolbar memakai filter aktif → `GET /bff/export/transactions?...` sebagai link download.

### 8.13 Transfer (termasuk lintas currency)

```mermaid
flowchart TD
  A[Form transfer] --> B[pilih akun asal & tujuan, tidak boleh sama]
  B --> C{currency sama?}
  C -->|ya| D[nominal + fee opsional]
  C -->|tidak| E[nominal asal + to_amount wajib]
  E --> R[GET /exchange-rates/convert prefill to_amount]
  R -->|RATE_UNAVAILABLE| E2[to_amount diisi manual + link tambah kurs]
  D --> S[POST /transfers + Idempotency-Key]
  E --> S
  S -->|201| L[invalidate transfers, accounts, transactions bila ada fee]
  S -->|INSUFFICIENT_BALANCE| B
```

Tampilkan ringkasan sebelum submit: "Rp500.000 dari BCA → GoPay, biaya Rp6.500, total keluar Rp506.500".

### 8.14 Budget dan alert

- `/budgets?month=YYYY-MM`: kartu per budget (nama kategori dari cache kategori, progress bar `progress`%, `spent` / `amount`, `remaining`).
- Status: `ok` (hijau), `warning` (kuning; progress ≥ `alert_threshold_pct`), `exceeded` (merah; `overspent=true`, tampilkan "Lebih Rp…" dari `remaining` absolut).
- Banner di dashboard bila ada budget `warning`/`exceeded`. Tidak ada push notification dari backend; alert dihitung saat dibaca.
- "Salin budget bulan lalu" (opsional): GET budgets bulan lalu → POST satu per satu dengan Idempotency-Key berbeda; abaikan `BUDGET_EXISTS`.

### 8.15 Recurring

```mermaid
stateDiagram-v2
  [*] --> active: POST /recurring-rules
  active --> paused: POST /:id/pause {reason}
  paused --> active: POST /:id/resume (jadwal terlewat dilewati)
  active --> ended: end_date/count tercapai (worker)
  ended --> [*]
  active --> [*]: DELETE
  paused --> [*]: DELETE
```

Form menampilkan pratinjau "Berikutnya: 3 Nov 2026, 3 Des 2026, …" dari `upcoming` setelah disimpan (untuk create, hitung pratinjau di client sebagai indikasi saja).

### 8.16 Savings goals

Daftar kartu goal (progress, sisa, `monthly_needed` "Perlu Rp2.000.000/bulan untuk tepat waktu"). Detail: riwayat kontribusi, aksi **Setor** (tab "Dari transfer" memilih transfer ke akun goal / tab "Manual" nominal + tanggal), **Tarik**, **Arsipkan** (PATCH `archived:true`). Status `achieved` → konfeti ringan (hormati `prefers-reduced-motion`).

### 8.17 Hutang / piutang

Tab "Hutang saya" (`payable`) dan "Piutang" (`receivable`) + filter status. Detail: progress `paid/principal`, riwayat pembayaran, dialog "Catat pembayaran" dengan opsi "Catat juga sebagai transaksi di akun…" (account_id + category_id) atau "Tautkan transaksi yang ada" (pilih dari `GET /transactions` difilter). Status `settled` → sembunyikan tombol bayar.

### 8.18 Tagihan (bills)

Daftar dikelompokkan: Terlambat / 7 hari ke depan / Nanti / Dijeda / Selesai. "Tandai lunas" → dialog (centang "Buat transaksi pengeluaran" default aktif bila bill punya `account_id`) → `POST /bills/:id/pay` + Idempotency-Key → toast "Jatuh tempo berikutnya: 20 Nov 2026". Pengingat dikirim worker backend (email); UI cukup menampilkan status.

### 8.19 Kurs

`/settings/exchange-rates`: tabel pasangan (base→quote, rate, berlaku mulai), tambah/ubah/hapus. Widget konversi cepat (`/exchange-rates/convert`). Jelaskan bahwa laporan tahunan memakai kurs ini.

### 8.20 Shared wallet

```mermaid
sequenceDiagram
  actor O as Owner
  actor M as Member
  participant A as API (via BFF)
  O->>A: POST /accounts/:id/members {email, role:editor}
  A-->>O: 201 member
  M->>A: GET /shared-accounts
  A-->>M: [{account, owner_id, role:editor}]
  M->>A: POST /transactions (account_id = akun shared, Idempotency-Key)
  A-->>M: 201 (data tersimpan milik owner)
  O->>A: GET /audit-logs?entity=transaction
  A-->>O: entri dengan actor_id = member
  O->>A: PATCH /accounts/:id/members/:user_id {role:viewer}
  M->>A: POST /transactions -> 403 FORBIDDEN
  M->>A: DELETE /accounts/:id/members/:self (keluar)
```

UI: badge "Dibagikan • Editor/Viewer" pada akun; viewer melihat semua tombol tulis disabled dengan tooltip "Hanya bisa melihat". Hanya owner melihat tab "Anggota" dengan aksi kelola.

### 8.21 Laporan

`/reports` dengan tab: **Bulanan** (summary), **Arus kas** (cashflow, toggle day/month, rentang tanggal), **Kategori** (donut + tabel, toggle income/expense), **Tahunan** (bar chart 12 bulan + savings rate). Semua filter di URL. Currency selector dari `/currencies` (default base). Angka chart dikonversi ke number **hanya untuk sumbu chart** (`Number(x)`), label/tooltip tetap memakai `formatMoney(string)`.

### 8.22 Audit log

`/activity`: timeline infinite (`GET /audit-logs`), filter entity, klik entri → diff before/after (tabel field: lama → baru). Dari detail akun/transaksi tersedia link "Lihat riwayat" (`entity` + `entity_id`).

### 8.23 Admin

```mermaid
flowchart TD
  A[/admin/users/] -->|GET /admin/users q,status,cursor| T[tabel user + infinite]
  T --> D[/admin/users/:id/]
  D -->|GET /admin/users/:id| P[profil, status, roles]
  D -->|GET /admin/users/:id/security-events| E[tab security events]
  P -->|Suspend konfirmasi| S[POST /suspend] --> P
  P -->|Aktifkan| AC[POST /activate] --> P
  P -->|Jadikan admin| G[POST /roles {role:admin}] --> P
  P -->|Cabut admin| R[DELETE /roles/admin] --> P
```

Tombol untuk diri sendiri disembunyikan (`CANNOT_MODIFY_SELF`). Tombol suspend hanya untuk status `active`/`pending_verification`; activate hanya untuk `suspended`.

---

## 9. Sitemap dan pemetaan halaman

### 9.1 Sitemap

```
/                              -> redirect /dashboard (login) atau /login
(public)
  /login                       /login/2fa
  /register                    /verify-email?email=
  /forgot-password?email=      /reset-password?email=
  /auth/oauth/callback         (#code | ?linked | ?error)
  /goodbye
(app)  [butuh sesi]
  /onboarding
  /dashboard
  /transactions                /transactions/[id] (sheet/intercepting route)   /transactions/import
  /transfers                   /transfers/[id]
  /accounts                    /accounts/[id]   /accounts/[id]/members
  /shared
  /categories                  /tags
  /budgets                     /budgets/[id]
  /recurring                   /recurring/new   /recurring/[id]
  /goals                       /goals/[id]
  /debts                       /debts/[id]
  /bills                       /bills/[id]
  /reports                     (?tab=monthly|cashflow|categories|yearly)
  /activity
  /settings/profile            /settings/preferences   /settings/exchange-rates
  /settings/security           /settings/security/2fa  /settings/sessions
  /settings/security-events    /settings/api-keys      /settings/data   /settings/account
(admin) [butuh role admin, selain itu 404]
  /admin/users                 /admin/users/[id]
```

### 9.2 Halaman → komponen → endpoint

| Halaman | Komponen utama (`features/*`) | Endpoint |
|---|---|---|
| `/login` | `LoginForm`, `GoogleButton` | POST /auth/login, GET /auth/oauth/google/start |
| `/login/2fa` | `MfaChallengeForm` | POST /auth/login/2fa |
| `/register` | `RegisterForm`, `PasswordStrength` | POST /auth/register |
| `/verify-email` | `OtpForm`, `ResendButton` | POST /auth/verify-email, POST /auth/verify-email/resend |
| `/forgot-password` | `ForgotPasswordForm` | POST /auth/password/forgot |
| `/reset-password` | `ResetPasswordForm` | POST /auth/password/reset |
| `/auth/oauth/callback` | `OAuthCallbackHandler` | POST /auth/oauth/exchange |
| layout `(app)` | `AppShell`, `UserMenu`, `QuickAddFab` | GET /users/me, GET /settings, GET /currencies, POST /auth/logout |
| `/onboarding` | `OnboardingWizard` | GET/PUT /settings, POST /accounts, GET /categories, POST /budgets |
| `/dashboard` | `BalanceCards`, `MonthTotals`, `CategoryDonut`, `CashflowChart`, `BudgetAlerts`, `RecentTransactions`, `UpcomingBills`, `GoalsProgress` | GET /reports/summary, /accounts, /budgets, /transactions, /bills, /savings-goals |
| `/transactions` | `TransactionFilters`, `TransactionList`, `TransactionForm`, `ExportButton` | GET/POST /transactions, GET /transactions/export, GET /accounts, /shared-accounts, /categories, /tags |
| `/transactions/[id]` | `TransactionDetail`, `TagMultiSelect`, `ConflictDialog` | GET/PATCH/DELETE /transactions/{id}, PUT /transactions/{id}/tags |
| `/transactions/import` | `CsvDropzone`, `ImportPreview` | POST /transactions/import |
| `/transfers`, `/transfers/[id]` | `TransferList`, `TransferForm` | GET/POST /transfers, GET/PATCH/DELETE /transfers/{id}, GET /exchange-rates/convert |
| `/accounts` | `AccountList`, `AccountForm` | GET/POST /accounts, POST /accounts/{id}/unarchive |
| `/accounts/[id]` | `AccountHeader`, tab transaksi/transfer/aktivitas | GET/PATCH/DELETE /accounts/{id}, POST /accounts/{id}/archive, GET /transactions?account_id, GET /transfers?account_id, GET /audit-logs?entity=account |
| `/accounts/[id]/members` | `MemberList`, `InviteMemberDialog` | GET/POST /accounts/{id}/members, PATCH/DELETE /accounts/{id}/members/{member_id} |
| `/shared` | `SharedAccountList`, `LeaveButton` | GET /shared-accounts, DELETE /accounts/{id}/members/{member_id} |
| `/categories` | `CategoryTree`, `CategoryForm`, `ReassignDialog` | GET/POST /categories, GET/PATCH/DELETE /categories/{id} |
| `/tags` | `TagList`, `TagForm` | GET/POST /tags, PATCH/DELETE /tags/{id} |
| `/budgets`, `/budgets/[id]` | `MonthPicker`, `BudgetCard`, `BudgetForm` | GET/POST /budgets, GET/PATCH/DELETE /budgets/{id} |
| `/recurring*` | `RecurringList`, `RecurringForm`, `PauseDialog` | GET/POST /recurring-rules, GET/PATCH/DELETE /recurring-rules/{id}, POST /pause, /resume |
| `/goals*` | `GoalCard`, `GoalForm`, `ContributionDialog`, `ContributionList` | GET/POST /savings-goals, GET/PATCH/DELETE /savings-goals/{id}, GET/POST /contributions, DELETE /contributions/{cid}, POST /withdrawals |
| `/debts*` | `DebtTabs`, `DebtForm`, `DebtPaymentDialog` | GET/POST /debts, GET/PATCH/DELETE /debts/{id}, GET/POST /debts/{id}/payments, DELETE /debts/{id}/payments/{pid} |
| `/bills*` | `BillGroups`, `BillForm`, `PayBillDialog` | GET/POST /bills, GET/PATCH/DELETE /bills/{id}, POST /bills/{id}/pay |
| `/reports` | `ReportTabs`, `CashflowChart`, `CategoryReport`, `YearlyChart` | GET /reports/summary, /reports/cashflow, /reports/categories, /reports/yearly |
| `/activity` | `AuditTimeline`, `DiffView` | GET /audit-logs |
| `/settings/profile` | `ProfileForm` | GET/PATCH /users/me |
| `/settings/preferences` | `PreferencesForm`, `TimezoneCombobox` | GET/PUT /settings, GET /currencies |
| `/settings/exchange-rates` | `RateTable`, `RateForm`, `ConvertWidget` | GET/POST /exchange-rates, GET/PATCH/DELETE /exchange-rates/{id}, GET /exchange-rates/convert |
| `/settings/security` | `ChangePasswordForm`, `MfaCard`, `LinkedAccounts` | PUT /users/me/password, GET /users/me/2fa, POST /2fa/disable, /2fa/recovery-codes, GET /users/me/identities, POST /users/me/identities/google/link |
| `/settings/security/2fa` | `MfaSetupWizard`, `QrCode`, `RecoveryCodesDialog` | POST /users/me/2fa/setup, POST /users/me/2fa/enable |
| `/settings/sessions` | `SessionList` | GET /users/me/sessions, DELETE /users/me/sessions/{id}, POST /auth/logout-all |
| `/settings/security-events` | `SecurityEventList` | GET /users/me/security-events |
| `/settings/api-keys` | `ApiKeyTable`, `CreateApiKeyDialog`, `ApiKeyRevealDialog` | GET/POST /users/me/api-keys, DELETE /users/me/api-keys/{id}, GET /auth/whoami |
| `/settings/data` | `ReconciliationCard`, link import/export | GET /reports/reconciliation |
| `/settings/account` | `DeleteAccountDialog` | DELETE /users/me |
| `/admin/users` | `AdminUserTable`, `UserFilters` | GET /admin/users |
| `/admin/users/[id]` | `AdminUserDetail`, `RoleManager`, `StatusActions`, `SecurityEventList` | GET /admin/users/{id}, POST /suspend, /activate, POST /roles, DELETE /roles/{role}, GET /admin/users/{id}/security-events |
| BFF internal | `server/backend` | POST /auth/refresh, GET /healthz, GET /readyz |

---

## 10. Pedoman UI/UX

- **Mobile-first**: target utama pencatatan cepat di HP. Lebar minimum 360 px. Navigasi: bottom nav (Dashboard, Transaksi, + , Budget, Lainnya) di mobile; sidebar di ≥ `lg`. Tombol `+` global membuka form transaksi sebagai **Drawer** (mobile) / **Dialog** (desktop).
- **Form cepat**: field nominal paling atas dengan `inputMode="decimal"`, format ribuan saat mengetik (tampilan saja; nilai form tetap string mentah), default akun = akun terakhir dipakai, default tanggal = hari ini (timezone user), kategori dengan pencarian + 6 kategori terakhir dipakai.
- **Warna semantik**: income hijau, expense merah, transfer netral/biru; jangan hanya mengandalkan warna (pakai ikon panah dan tanda +/−).
- **Angka**: `font-variant-numeric: tabular-nums`, rata kanan di tabel; nominal negatif pakai tanda minus, bukan kurung.
- **State lengkap** untuk setiap data view: loading (skeleton, bukan spinner layar penuh), empty (ilustrasi + CTA, mis. "Belum ada transaksi. Tambah sekarang"), error (pesan dari kode + tombol coba lagi + request_id), dan partial (infinite loading di bawah list).
- **Konfirmasi** untuk aksi destruktif (hapus, cabut, suspend, keluar dari dompet) memakai `AlertDialog` dengan nama objek; hapus akun memakai ketik-konfirmasi.
- **Toast** (sonner): sukses singkat; error memuat request_id. Jangan toast untuk error validasi field (tampilkan di field).
- **Aksesibilitas (WCAG 2.2 AA)**: semua kontrol bisa dijangkau keyboard, fokus terlihat, label eksplisit, `aria-live` untuk hasil OTP/countdown, kontras ≥ 4.5:1, chart punya tabel/ringkasan teks alternatif, hormati `prefers-reduced-motion`. Uji dengan `@axe-core/playwright`.
- **Dark mode**: `next-themes` (system default). Warna kategori dari server dipakai sebagai aksen kecil (dot/ikon) dengan kontras teks yang dihitung, bukan sebagai background penuh.
- **i18n**: semua teks lewat `next-intl`; kunci error `errors.<CODE>`; format angka/tanggal lewat helper locale-aware. Default `id`, struktur siap untuk `en`.
- **Bentuk halaman list**: filter di URL, tombol "Reset filter", jumlah hasil tidak ditampilkan (API keyset tidak memberi total).
- **Read-only shared wallet**: elemen tulis disabled + tooltip, bukan disembunyikan total, agar viewer paham alasannya.
- **Security UX**: password field dengan tombol tampil/sembunyi dan indikator kekuatan (zxcvbn-ts, hanya saran; aturan keras tetap 8–72); jangan disable paste pada field password/OTP.
- **Performa**: halaman `(app)` memakai Server Components untuk prefetch + `HydrationBoundary`; chart di-`dynamic import` (`ssr:false`); target LCP < 2.5 s di 4G, bundle JS rute awal < 200 KB gzip.

---

## 11. Strategi testing

| Level | Alat | Cakupan |
|---|---|---|
| Unit | Vitest | `shared/lib` (money parse/format, date tz, idempotency key hook, error mapper, safe redirect), `server/session` (single-flight refresh, CSRF), schema Zod |
| Komponen | Vitest + Testing Library + MSW v2 | Setiap form: render, validasi client, submit sukses, mapping `details[]` → field, kode error utama (lihat tabel Bagian 6), state loading/empty/error |
| Integrasi BFF | Vitest (node env) + MSW mem-mock backend | Route handler `/bff/auth/*` dan proxy `/bff/api/*`: header allowlist, `Authorization` disuntik, refresh 401 → retry, `TOKEN_REUSED` → sesi dihapus, `Set-Cookie` aman, CSRF ditolak |
| Kontrak | `openapi-msw` (handler bertipe dari spec) + `pnpm gen:api` di CI | Build gagal bila spec berubah dan kode tidak sesuai tipe |
| E2E | Playwright ke stack docker compose (backend sungguhan + Mailpit) | Register → baca OTP dari Mailpit API (`GET http://localhost:8025/api/v1/messages`, ambil kode 6 digit dari body) → verify → login → onboarding → tambah transaksi → budget warning → transfer → logout; 2FA setup + login 2FA (TOTP dihitung dengan `otpauth` di test); lupa password; admin suspend (user admin dibuat via `make promote EMAIL=` di backend); shared wallet dua browser context |
| Aksesibilitas | `@axe-core/playwright` | Halaman utama tanpa pelanggaran serius |
| Visual (opsional) | Playwright screenshot | Dashboard, form transaksi |

Aturan:
- **Coverage ≥ 80% lines** (juga statements) untuk `src/features/**` dan `src/shared/**` (`vitest.config.ts` → `coverage.thresholds` per glob); CI gagal bila di bawah.
- Mock di level jaringan (MSW), bukan mock modul `fetch`/hook. Factory data uji di `src/test/factories/*` (mis. `makeTransaction({ amount: '35000' })`) dengan tipe dari `openapi.d.ts`.
- Test idempotency: submit gagal network → retry memakai key yang **sama**; ubah field → key **baru**.
- Test konflik: PATCH → 409 `VERSION_CONFLICT` → dialog muncul → "Muat ulang" memanggil GET dan mengisi ulang form.
- Test 429: tombol disabled dengan countdown dari `Retry-After`.
- E2E memakai email unik per run (`e2e+<timestamp>@example.com`) dan berjalan serial untuk alur auth (limiter 10/menit/IP; jalankan backend e2e dengan rate limit yang memadai atau beri jeda).

---

## 12. Lingkungan pengembangan

### 12.1 Menjalankan backend (repo `go-auth-clean`)

```bash
cp .env.example .env            # FRONTEND_URL & CORS sudah default http://localhost:3000
make up                         # postgres:17 + mailpit (SMTP :1025, UI http://localhost:8025)
make migrate-up                 # butuh golang-migrate (make tools)
make run                        # API di http://localhost:8080, Swagger UI di /docs/
make promote EMAIL=you@example.com   # jadikan admin (login ulang setelahnya)
```

Pengaturan backend yang relevan untuk frontend:

| Env backend | Nilai dev | Catatan |
|---|---|---|
| `FRONTEND_URL` | `http://localhost:3000` | Dipakai untuk redirect OAuth callback dan link di email (`/verify-email`, `/reset-password`, `/forgot-password`) |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:3000` | Origin eksplisit, tanpa `*`. Dengan BFF, browser tidak memanggil backend langsung, tapi tetap isi untuk Swagger/dev tools |
| `TRUSTED_PROXIES` | IP/CIDR server Next (mis. `172.16.0.0/12` di docker, `127.0.0.1/32` lokal) | Kosong = `X-Forwarded-For` diabaikan → semua user terhitung satu IP di limiter |
| `GOOGLE_OAUTH_CLIENT_ID/SECRET` | kosong (nonaktif) | Isi untuk menguji OAuth |
| `GOOGLE_OAUTH_REDIRECT_URL` | `http://localhost:3000/api/v1/auth/oauth/google/callback` | Lewat rewrite same-origin Next (lihat 7.2); daftarkan di Google Console |
| `APP_ENV` | `development` | `production` mengaktifkan HSTS |
| `SWAGGER_ENABLED` | `true` di dev | |

### 12.2 Skrip `package.json` (frontend)

```jsonc
{
  "scripts": {
    "dev": "next dev --turbopack",
    "build": "next build",
    "start": "next start",
    "lint": "eslint . --max-warnings=0",
    "typecheck": "tsc --noEmit",
    "format": "prettier --write .",
    "test": "vitest run",
    "test:watch": "vitest",
    "test:coverage": "vitest run --coverage",
    "e2e": "playwright test",
    "gen:api": "swagger2openapi openapi/swagger.yaml -o openapi/openapi.yaml --patch && openapi-typescript openapi/openapi.yaml -o src/shared/api/openapi.d.ts && msw-auto-mock openapi/openapi.yaml -o src/test/msw/generated --typescript",
    "gen:api:check": "pnpm gen:api && git diff --exit-code src/shared/api/openapi.d.ts",
    "sync:spec": "cp ../go-auth-clean/docs/swagger/swagger.yaml openapi/swagger.yaml && pnpm gen:api",
    "prepare": "husky"
  }
}
```

Catatan: `swagger2openapi --patch` memperbaiki ketidaksesuaian kecil Swagger 2.0. Setelah generate, tambahkan tipe manual yang tidak ada di spec (`MFAChallenge`, union respons login) di `src/shared/api/extra-types.ts`.

### 12.3 `.env.example` (frontend)

```bash
# --- Server only (JANGAN diberi prefix NEXT_PUBLIC_) ---
BACKEND_URL=http://localhost:8080            # base tanpa /api/v1; di docker: http://api:8080
APP_ORIGIN=http://localhost:3000             # dipakai untuk cek Origin (CSRF) dan URL absolut
SESSION_SECRET=change-me-min-32-bytes-random # openssl rand -base64 48 (enkripsi/sign cookie sesi)
SESSION_STORE=memory                         # memory | redis
REDIS_URL=redis://localhost:6379
SESSION_TTL=168h                             # samakan dengan REFRESH_TOKEN_TTL backend
LOG_LEVEL=info

# --- Publik (aman diekspos ke browser) ---
NEXT_PUBLIC_APP_NAME=Go Finance
NEXT_PUBLIC_DEFAULT_LOCALE=id
NEXT_PUBLIC_GOOGLE_OAUTH_ENABLED=false

# --- E2E ---
E2E_BASE_URL=http://localhost:3000
MAILPIT_URL=http://localhost:8025
```

Validasi env saat boot di `src/server/env.ts` (Zod) — gagal cepat bila hilang.

### 12.4 docker-compose (repo frontend, full stack)

Asumsi repo backend berada di `../go-auth-clean`.

```yaml
services:
  postgres:
    image: postgres:17-alpine
    environment: { POSTGRES_USER: postgres, POSTGRES_PASSWORD: postgres, POSTGRES_DB: go_auth_db }
    healthcheck: { test: ["CMD-SHELL", "pg_isready -U postgres"], interval: 5s, retries: 10 }
    volumes: [pgdata:/var/lib/postgresql/data]

  mailpit:
    image: axllent/mailpit:latest
    ports: ["8025:8025"]              # UI + API untuk e2e

  migrate:
    image: migrate/migrate:latest
    depends_on: { postgres: { condition: service_healthy } }
    volumes: ["../go-auth-clean/migrations:/migrations:ro"]
    command: ["-path=/migrations", "-database=postgres://postgres:postgres@postgres:5432/go_auth_db?sslmode=disable", "up"]

  api:
    build: ../go-auth-clean
    depends_on:
      postgres: { condition: service_healthy }
      migrate: { condition: service_completed_successfully }
    env_file: ../go-auth-clean/.env
    environment:
      DB_HOST: postgres
      SMTP_HOST: mailpit
      SMTP_PORT: "1025"
      FRONTEND_URL: http://localhost:3000
      CORS_ALLOWED_ORIGINS: http://localhost:3000
      TRUSTED_PROXIES: 172.16.0.0/12
      APP_BASE_URL: http://localhost:8080
    ports: ["8080:8080"]
    # image backend = distroless (tanpa shell/wget) -> tidak ada healthcheck di container ini

  redis:
    image: redis:7-alpine

  web:
    build: .
    depends_on: { api: { condition: service_started }, redis: { condition: service_started } }
    environment:
      BACKEND_URL: http://api:8080
      APP_ORIGIN: http://localhost:3000
      SESSION_SECRET: dev-only-secret-change-me-0123456789abcdef
      SESSION_STORE: redis
      REDIS_URL: redis://redis:6379
    ports: ["3000:3000"]

volumes: { pgdata: {} }
```

Catatan: image backend memakai `gcr.io/distroless/static` (tanpa shell/wget), jadi readiness API dicek dari luar: `docker compose up -d` lalu tunggu `curl -fsS http://localhost:8080/readyz` (skrip `scripts/wait-api.sh`); BFF juga melakukan retry singkat saat backend belum siap.

### 12.5 Dockerfile frontend (ringkas)

```dockerfile
FROM node:22-alpine AS deps
WORKDIR /app
RUN corepack enable
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

FROM node:22-alpine AS build
WORKDIR /app
RUN corepack enable
COPY --from=deps /app/node_modules ./node_modules
COPY . .
RUN pnpm build

FROM node:22-alpine AS run
WORKDIR /app
ENV NODE_ENV=production PORT=3000 HOSTNAME=0.0.0.0
RUN addgroup -S app && adduser -S app -G app
COPY --from=build --chown=app:app /app/.next/standalone ./
COPY --from=build --chown=app:app /app/.next/static ./.next/static
COPY --from=build --chown=app:app /app/public ./public
USER app
EXPOSE 3000
CMD ["node", "server.js"]
```

### 12.6 CI (GitHub Actions, ringkas)

```yaml
name: ci
on: [push, pull_request]
jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: pnpm/action-setup@v4
      - uses: actions/setup-node@v4
        with: { node-version: 22, cache: pnpm }
      - run: pnpm install --frozen-lockfile
      - run: pnpm gen:api:check
      - run: pnpm lint
      - run: pnpm typecheck
      - run: pnpm test:coverage
      - run: pnpm build
  e2e:
    needs: verify
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/checkout@v4
        with: { repository: <org>/go-auth-clean, path: ../go-auth-clean }   # sesuaikan; tanyakan ke pemilik
      - run: cp ../go-auth-clean/.env.example ../go-auth-clean/.env && docker compose up -d --build && ./scripts/wait-api.sh
      - uses: pnpm/action-setup@v4
      - uses: actions/setup-node@v4
        with: { node-version: 22, cache: pnpm }
      - run: pnpm install --frozen-lockfile && pnpm exec playwright install --with-deps chromium
      - run: pnpm e2e
      - if: failure()
        uses: actions/upload-artifact@v4
        with: { name: playwright-report, path: playwright-report }
```

---

## 13. Rencana delivery (fase, DoD, pembagian sub-agent)

Definition of Done (berlaku di setiap fase): lint + typecheck bersih, unit/komponen test hijau dengan coverage ≥ 80% untuk kode yang disentuh, `pnpm build` sukses, tidak ada `any` baru tanpa komentar alasan, teks UI lewat i18n, a11y dasar (label, fokus), commit Conventional per concern, laporan fase singkat ke pemilik.

| Fase | Isi | Sub-agent (scope sempit, bisa paralel bila tidak menyentuh `shared/`) | DoD tambahan |
|---|---|---|---|
| **0. Fondasi** | Scaffold Next + TS strict, Tailwind v4, shadcn, ESLint boundaries, Prettier, Husky/commitlint, Vitest + MSW, Playwright, `gen:api`, `env.ts`, Dockerfile, compose, CI | (1) tooling & CI, (2) codegen + `shared/api` client + `ApiError`, (3) docker/compose | `pnpm gen:api` menghasilkan tipe; CI hijau; compose naik dan `/readyz` 200 |
| **1. BFF & sesi** | Session store (memory/redis), cookie `__Host-sid`, CSRF, proxy `/bff/api/[...path]`, refresh single-flight, `proxy.ts` route guard, CSP nonce, request id | (1) `server/session` + refresh (urutan pertama), (2) proxy + header allowlist, (3) CSP/headers | Test integrasi BFF lengkap (Bagian 11); TOKEN_REUSED → logout |
| **2. Auth publik** | Register, verify email, login, 2FA login, forgot/reset, OAuth callback, logout | (1) register+verify, (2) login+2FA, (3) forgot/reset, (4) OAuth (setelah tanya pemilik apakah aktif) | E2E register→login via Mailpit hijau |
| **3. Shell & pengaturan akun** | AppShell, navigasi, `me`, profil, password, sesi, security events, 2FA setup, API keys, identities, hapus akun, preferensi + currencies | (1) shell+nav, (2) profil/password/hapus, (3) sesi+events, (4) 2FA, (5) API keys, (6) preferensi | E2E 2FA setup + login |
| **4. Inti finance** | Akun, kategori, tag, transaksi (list infinite + filter + form + tag), transfer, onboarding | (1) `shared/lib/money` + `date` (urutan pertama), (2) accounts, (3) categories+tags, (4) transactions, (5) transfers, (6) onboarding | Idempotency & conflict dialog teruji; E2E tambah transaksi |
| **5. Perencanaan** | Budget, recurring, goals, debts, bills, kurs | satu sub-agent per feature (paralel) | Tiap feature punya test status/progress |
| **6. Laporan & dashboard** | Dashboard, reports 4 tab, reconciliation, import/export CSV, audit log | (1) dashboard, (2) reports, (3) import/export, (4) audit | Chart punya alternatif teks; import dry-run teruji |
| **7. Shared wallet & admin** | Members, shared list, role-aware UI; admin list/detail/aksi | (1) shared wallet, (2) admin | E2E dua user (owner/editor/viewer); admin suspend |
| **8. Hardening** | a11y audit, performa, error boundary, 404/500, security review (CSP, cookie, header), dokumentasi README | (1) a11y+perf, (2) security review (read-only, laporan temuan) | axe bersih; Lighthouse ≥ 90 (perf/a11y/best practices) |

Format instruksi ke sub-agent (template):

```
Tugas: implementasikan features/<name> (Bagian 7.<x>, 8.<y>, 9.2 baris <halaman>).
Patuhi: Bagian 3 (struktur & layering), 5 (konvensi API), 6 (kode error relevan: <daftar>).
Jangan ubah: src/shared/** kecuali diminta; jika perlu, laporkan usulan.
Output: kode + test (coverage ≥80%), commit Conventional per concern.
Laporan (≤15 baris): file dibuat/diubah, keputusan, test, TODO/pertanyaan.
```

---

## 14. Checklist jebakan (pitfalls)

- [ ] **Uang sebagai string**: tidak ada `parseFloat`/`Number()` untuk hitung atau kirim; hanya untuk sumbu chart. Desimal mengikuti `minor_unit` (IDR 0, USD 2). Hapus pemisah ribuan sebelum kirim.
- [ ] **Tanggal kalender** (`YYYY-MM-DD`) tidak dikonversi lewat `new Date()`/`toISOString()`; "hari ini" dihitung di timezone **settings**, bukan browser.
- [ ] **Login dua bentuk**: `mfa_required: true` tidak ada di spec; jangan crash karena `access_token` undefined.
- [ ] **Refresh token rotasi**: satu refresh in-flight per sesi (single-flight + lock Redis). Dua refresh paralel dengan token yang sama → `TOKEN_REUSED` → seluruh sesi dicabut.
- [ ] **Konflik = 409 `VERSION_CONFLICT`**, bukan 412. Selalu kirim `If-Match` dari `version` data yang sedang diedit.
- [ ] **CORS backend**: allowed headers = Authorization, Content-Type, Idempotency-Key, If-Match, X-API-Key, X-Request-ID; exposed = X-Request-ID, Retry-After, RateLimit-*, ETag, Idempotent-Replayed, Content-Disposition. Dengan BFF (server-to-server) CORS tidak berlaku; tetap set `CORS_ALLOWED_ORIGINS` ke origin frontend untuk berjaga-jaga.
- [ ] **Idempotency-Key**: wajib di 6 endpoint (transactions, transfers, budgets, recurring-rules, bills pay, debts payments); key sama untuk retry, key baru bila body berubah. Contributions **tidak** memakainya.
- [ ] **Cursor terikat filter**: ganti filter = buang cursor; tangani `INVALID_CURSOR` dengan reset.
- [ ] **Field tak dikenal ditolak** (`INVALID_JSON`): jangan spread state form ke body.
- [ ] **PATCH partial**: kirim hanya field dirty; `""` punya arti "hapus" di beberapa field (bills account/category, goals target_date/account_id, debts due_date, recurring end_date).
- [ ] **Kategori**: hanya satu level nesting; parent harus root dengan type sama; kategori sistem read-only; pilihan kategori difilter berdasarkan type transaksi; budget hanya untuk kategori expense.
- [ ] **Transaksi `transfer_fee`** read-only (`MANAGED_BY_TRANSFER`).
- [ ] **Transfer lintas currency** butuh `to_amount`; currency sama → jangan kirim `to_amount` berbeda.
- [ ] **Shared wallet**: data milik owner; non-member → 404, viewer menulis → 403. `GET /accounts` tidak memuat akun shared — gabungkan dengan `/shared-accounts` di select akun (role editor saja untuk form tulis).
- [ ] **Member API** tidak mengembalikan email/nama; `member_id` di path = `user_id`.
- [ ] **OAuth**: `#code` ada di fragment (baca di client), berlaku ~60 detik, sekali pakai (hindari double-exchange di React StrictMode — guard dengan `useRef`). Cookie flow butuh path `/api/v1/auth/oauth/google` di origin yang sama → rewrite Next. Validasi `auth_url` sebelum redirect.
- [ ] **Role baru** baru terlihat setelah refresh token; setelah admin mengubah role sendiri/orang lain, tampilkan catatan.
- [ ] **Ganti password / enable/disable 2FA / reset password** mencabut sesi lain atau semua sesi → siapkan UX logout paksa yang ramah.
- [ ] **Rate limit per IP**: tanpa `TRUSTED_PROXIES` di backend, semua user lewat BFF dianggap satu IP → 429 massal. Teruskan `X-Forwarded-For` dari BFF.
- [ ] **429 `Retry-After`** dibaca dari header (detik); jangan auto-retry mutasi.
- [ ] **Pesan server berbahasa Indonesia** dan bisa berubah: UI bercabang di `code`.
- [ ] **Secret**: tidak ada token/secret di `NEXT_PUBLIC_*`, `localStorage`, URL, atau log. Recovery code & API key hanya tampil sekali dan tidak masuk cache persisten.
- [ ] **XSS**: tidak ada `dangerouslySetInnerHTML`; nilai `color` dari server divalidasi `^#[0-9A-Fa-f]{6}$` sebelum dipakai di `style`; `next=` redirect hanya path relatif internal.
- [ ] **CSV**: export di-stream via link download; import diteruskan sebagai multipart stream (≤ 5 MB) tanpa parse di BFF; jangan render isi CSV sebagai HTML.
- [ ] **Body limit finance 64 KiB**: note panjang (≤1024) aman, tapi jangan kirim payload besar.
- [ ] **Limiter in-memory per instance backend**: hasil 429 bisa berbeda antar replika; jangan mengandalkannya untuk logika UI.
- [ ] **Recurring resume** melewati jadwal yang terlewat; **hapus debt payment** tidak menghapus transaksi tertaut — beri tahu user.
- [ ] **Admin**: sembunyikan aksi terhadap diri sendiri; suspend mencabut semua sesi & API key target.
- [ ] **Spec menang**: bila dokumen ini berbeda dengan `swagger.yaml`, ikuti spec dan laporkan.
