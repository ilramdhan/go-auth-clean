#!/usr/bin/env bash
# Smoke test end-to-end terhadap server yang SUDAH berjalan.
#
#   make smoke                          # default BASE_URL=http://localhost:8080
#   BASE_URL=http://api:8080 MAILPIT_URL=http://mail:8025 ./scripts/smoke.sh
#
# Butuh: curl, jq. OTP verifikasi email dibaca dari API Mailpit
# (MAILPIT_URL, default http://localhost:8025). Bila Mailpit tidak bisa
# dihubungi, langkah verifikasi (dan semua langkah yang butuh login) di-skip.
# Script TIDAK membaca .env dan tidak mencetak secret apa pun.
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
API="${BASE_URL%/}/api/v1"
MAILPIT_URL="${MAILPIT_URL:-http://localhost:8025}"
PASSWORD="${SMOKE_PASSWORD:-Smoke-Test-123!}"
MAX_429_RETRIES="${MAX_429_RETRIES:-6}"

for bin in curl jq; do
  command -v "$bin" >/dev/null 2>&1 || { echo "FATAL: '$bin' tidak ditemukan di PATH" >&2; exit 2; }
done

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

PASSED=0
FAILED=0
SKIPPED=0
pass() { PASSED=$((PASSED + 1)); printf 'PASS  %s\n' "$1"; }
skip() { SKIPPED=$((SKIPPED + 1)); printf 'SKIP  %s (%s)\n' "$1" "$2"; }
fail() {
  FAILED=$((FAILED + 1))
  printf 'FAIL  %s: %s\n' "$1" "$2"
  if [[ -n "${LAST_METHOD:-}" ]]; then
    printf '      request : %s %s\n' "$LAST_METHOD" "$LAST_URL"
    printf '      response: HTTP %s %s\n' "$STATUS" "$(head -c 600 "$TMP/body" 2>/dev/null | tr -d '\r')"
  fi
}
# die: gagal fatal (langkah selanjutnya bergantung pada langkah ini).
die() { fail "$1" "$2"; summary; exit 1; }
# abort: berhenti tanpa menambah FAIL (FAIL sudah dicatat oleh expect/fungsi).
abort() { echo "      (langkah selanjutnya bergantung pada langkah ini, berhenti)"; summary; exit 1; }
summary() {
  echo "----"
  echo "Hasil: ${PASSED} pass, ${FAILED} fail, ${SKIPPED} skip"
}

# req METHOD PATH [BODY] [extra curl args...]
# Menulis body ke $TMP/body, header ke $TMP/headers, status ke $STATUS.
# 429 otomatis ditunggu sesuai Retry-After lalu diulang.
STATUS=""
BODY=""
req() {
  local method="$1" path="$2" data="${3:-}"
  shift 3 2>/dev/null || shift $#
  local url="$path"
  [[ "$url" == http* ]] || url="${API}${path}"
  LAST_METHOD="$method"
  LAST_URL="$url"
  local args=(-sS -X "$method" -o "$TMP/body" -D "$TMP/headers" -w '%{http_code}' -H 'Accept: application/json')
  [[ -n "${TOKEN:-}" ]] && args+=(-H "Authorization: Bearer ${TOKEN}")
  [[ -n "$data" ]] && args+=(-H 'Content-Type: application/json' --data "$data")
  local attempt=0
  while :; do
    STATUS="$(curl "${args[@]}" "$@" "$url" || echo 000)"
    if [[ "$STATUS" == 429 && $attempt -lt $MAX_429_RETRIES ]]; then
      local wait
      wait="$(awk 'tolower($1)=="retry-after:"{print $2+0}' "$TMP/headers" | tr -d '\r')"
      wait="${wait:-5}"
      printf '      (429 rate limited, tunggu %ss lalu ulang)\n' "$wait"
      sleep "$wait"
      attempt=$((attempt + 1))
      continue
    fi
    break
  done
  BODY="$(cat "$TMP/body" 2>/dev/null || true)"
}
jqr() { jq -r "$1" <<<"$BODY"; }
expect() { # expect STEP CODE...
  local step="$1"; shift
  local c
  for c in "$@"; do [[ "$STATUS" == "$c" ]] && return 0; done
  fail "$step" "status ${STATUS}, harap $*"
  return 1
}
idem() { printf 'smoke-%s-%s' "$(date +%s%N 2>/dev/null || date +%s)" "$RANDOM$RANDOM"; }

# Ambil OTP 6 digit terbaru untuk email tertentu dari Mailpit.
fetch_otp() {
  local email="$1" i id code
  for i in $(seq 1 20); do
    id="$(curl -sf "${MAILPIT_URL%/}/api/v1/search?query=$(jq -rn --arg q "to:\"$email\"" '$q|@uri')&limit=1" \
      | jq -r '.messages[0].ID // empty' 2>/dev/null || true)"
    if [[ -n "$id" ]]; then
      code="$(curl -sf "${MAILPIT_URL%/}/api/v1/message/${id}" | jq -r '.Text' | grep -Eo '\b[0-9]{6}\b' | head -n1 || true)"
      [[ -n "$code" ]] && { echo "$code"; return 0; }
    fi
    sleep 0.5
  done
  return 1
}

# register/verify/login: helper per langkah; login mengisi TOKEN & REFRESH.
register() {
  local email="$1" name="$2" step="$3"
  TOKEN=""
  req POST /auth/register "$(jq -nc --arg e "$email" --arg n "$name" --arg p "$PASSWORD" '{email:$e,full_name:$n,password:$p}')"
  expect "$step" 201 || return 1
  [[ "$(jqr '.data.email // empty')" == "$email" ]] || { fail "$step" "email di response tidak cocok"; return 1; }
  pass "$step ($email)"
}
verify() {
  local email="$1" step="$2" code
  code="$(fetch_otp "$email")" || { fail "$step" "OTP untuk $email tidak ditemukan di Mailpit"; return 1; }
  req POST /auth/verify-email "$(jq -nc --arg e "$email" --arg c "$code" '{email:$e,code:$c}')"
  expect "$step" 200 || return 1
  pass "$step"
}
login() {
  local email="$1" step="$2"
  TOKEN=""
  req POST /auth/login "$(jq -nc --arg e "$email" --arg p "$PASSWORD" '{email:$e,password:$p}')"
  expect "$step" 200 || return 1
  if [[ "$(jqr '.data.mfa_required // false')" == "true" ]]; then
    fail "$step" "user baru seharusnya tidak butuh 2FA"; return 1
  fi
  TOKEN="$(jqr '.data.access_token // empty')"
  REFRESH="$(jqr '.data.refresh_token // empty')"
  [[ -n "$TOKEN" && -n "$REFRESH" ]] || { fail "$step" "access_token/refresh_token kosong"; return 1; }
  pass "$step"
}

echo "Smoke test -> ${BASE_URL} (mailpit: ${MAILPIT_URL})"
echo "----"

# 1. Health ---------------------------------------------------------------
req GET "${BASE_URL%/}/healthz"
if expect "healthz" 200; then pass "healthz"; else abort; fi
req GET "${BASE_URL%/}/readyz"
if expect "readyz" 200; then pass "readyz"; else abort; fi

# 2. Register + verify + login user A --------------------------------------
SUFFIX="$(date +%s)${RANDOM}"
EMAIL_A="smoke-a-${SUFFIX}@example.com"
EMAIL_B="smoke-b-${SUFFIX}@example.com"

register "$EMAIL_A" "Smoke Tester A" "register user A" || abort

if curl -sf -o /dev/null "${MAILPIT_URL%/}/api/v1/messages?limit=1"; then
  :
else
  skip "verify email A" "Mailpit tidak bisa dihubungi di ${MAILPIT_URL}; jalankan 'make mail-up' atau set MAILPIT_URL"
  echo "Tanpa verifikasi email, login tidak mungkin (akun masih pending_verification). Berhenti."
  summary
  [[ $FAILED -eq 0 ]] && exit 0 || exit 1
fi

verify "$EMAIL_A" "verify email A (OTP via Mailpit)" || abort
login "$EMAIL_A" "login user A" || abort

req GET /users/me
if expect "GET /users/me" 200; then
  [[ "$(jqr '.data.email')" == "$EMAIL_A" ]] && pass "GET /users/me" || fail "GET /users/me" "email tidak cocok"
fi

# 3. Accounts ---------------------------------------------------------------
req POST /accounts '{"name":"Smoke BCA","type":"bank","currency":"IDR","initial_balance":"1000000"}'
expect "create account BCA" 201 || abort
ACC1="$(jqr '.data.id')"
pass "create account BCA (saldo awal 1000000)"

req POST /accounts '{"name":"Smoke GoPay","type":"ewallet","currency":"IDR","initial_balance":"50000"}'
expect "create account GoPay" 201 || abort
ACC2="$(jqr '.data.id')"
pass "create account GoPay (saldo awal 50000)"

# 4. Categories + transactions ----------------------------------------------
req GET '/categories?type=income'
expect "list categories income" 200 || abort
CAT_INCOME="$(jqr '[.data[] | select(.is_system==true)][0].id // .data[0].id // empty')"
req GET '/categories?type=expense'
expect "list categories expense" 200 || abort
CAT_EXPENSE="$(jqr '[.data[] | select(.is_system==true)][0].id // .data[0].id // empty')"
[[ -n "$CAT_INCOME" && -n "$CAT_EXPENSE" ]] || die "list categories" "kategori seed income/expense tidak ditemukan"
pass "list categories (seed income & expense ada)"

TODAY="$(date +%Y-%m-%d)"
MONTH="$(date +%Y-%m)"

req POST /transactions "$(jq -nc --arg a "$ACC1" --arg c "$CAT_INCOME" --arg d "$TODAY" \
  '{account_id:$a,category_id:$c,type:"income",amount:"250000",transaction_date:$d,note:"smoke income"}')" \
  -H "Idempotency-Key: $(idem)"
expect "create income" 201 && pass "create income 250000 -> BCA" || true

TX_KEY="$(idem)"
TX_BODY="$(jq -nc --arg a "$ACC1" --arg c "$CAT_EXPENSE" --arg d "$TODAY" \
  '{account_id:$a,category_id:$c,type:"expense",amount:"75000",transaction_date:$d,note:"smoke expense"}')"
req POST /transactions "$TX_BODY" -H "Idempotency-Key: $TX_KEY"
if expect "create expense" 201; then
  TX_ID="$(jqr '.data.id')"
  pass "create expense 75000 dari BCA"
  # Replay dengan key yang sama harus mengembalikan transaksi yang sama (tidak dobel).
  req POST /transactions "$TX_BODY" -H "Idempotency-Key: $TX_KEY"
  if expect "idempotency replay" 200 201; then
    [[ "$(jqr '.data.id')" == "$TX_ID" ]] && pass "idempotency replay (id sama, tidak dobel)" \
      || fail "idempotency replay" "id berbeda: replay membuat transaksi baru"
  fi
fi

# 5. Transfer + saldo -------------------------------------------------------
req POST /transfers "$(jq -nc --arg f "$ACC1" --arg t "$ACC2" --arg d "$TODAY" \
  '{from_account_id:$f,to_account_id:$t,amount:"100000",transfer_date:$d,note:"smoke transfer"}')" \
  -H "Idempotency-Key: $(idem)"
expect "transfer BCA -> GoPay" 201 && pass "transfer 100000 BCA -> GoPay" || true

# BCA: 1000000 + 250000 - 75000 - 100000 = 1075000 ; GoPay: 50000 + 100000 = 150000
check_balance() {
  local id="$1" want="$2" label="$3" got
  req GET "/accounts/${id}"
  expect "saldo $label" 200 || return 0
  got="$(jqr '.data.balance')"
  # bandingkan secara numerik agar "1075000" == "1075000.00"
  if awk -v a="$got" -v b="$want" 'BEGIN{exit !(a+0==b+0)}'; then
    pass "saldo $label = $got"
  else
    fail "saldo $label" "dapat $got, harap $want"
  fi
}
check_balance "$ACC1" 1075000 "BCA"
check_balance "$ACC2" 150000 "GoPay"

# 6. Budget -----------------------------------------------------------------
req POST /budgets "$(jq -nc --arg c "$CAT_EXPENSE" --arg m "$MONTH" \
  '{category_id:$c,amount:"300000",currency:"IDR",period_month:$m,alert_threshold_pct:80}')" \
  -H "Idempotency-Key: $(idem)"
if expect "create budget" 201; then
  BUDGET_ID="$(jqr '.data.id')"
  pass "create budget 300000 ($MONTH)"
  req GET "/budgets/${BUDGET_ID}"
  if expect "budget progress" 200; then
    SPENT="$(jqr '.data.spent')"
    if awk -v a="$SPENT" 'BEGIN{exit !(a+0==75000)}'; then
      pass "budget progress spent=$SPENT progress=$(jqr '.data.progress')% status=$(jqr '.data.status')"
    else
      fail "budget progress" "spent=$SPENT, harap 75000"
    fi
  fi
fi

# 7. Export CSV -------------------------------------------------------------
req GET "/transactions/export?from=${TODAY}&to=${TODAY}"
if expect "export CSV" 200; then
  CT="$(awk 'tolower($1)=="content-type:"{print $2}' "$TMP/headers" | tr -d '\r;')"
  LINES="$(wc -l <"$TMP/body" | tr -d ' ')"
  if [[ "$CT" == text/csv* ]] && grep -q 'smoke expense' "$TMP/body"; then
    pass "export CSV ($CT, $LINES baris)"
  else
    fail "export CSV" "content-type=$CT atau isi tidak memuat transaksi smoke"
  fi
fi

# 8. Laporan bulanan --------------------------------------------------------
req GET "/reports/summary?month=${MONTH}"
expect "monthly report" 200 && pass "monthly report ($MONTH)" || true

# 9. Refresh token rotation -------------------------------------------------
OLD_REFRESH="$REFRESH"
SAVED_TOKEN="$TOKEN"
TOKEN=""
req POST /auth/refresh "$(jq -nc --arg r "$OLD_REFRESH" '{refresh_token:$r}')"
if expect "refresh rotation" 200; then
  NEW_ACCESS="$(jqr '.data.access_token // empty')"
  NEW_REFRESH="$(jqr '.data.refresh_token // empty')"
  if [[ -n "$NEW_REFRESH" && "$NEW_REFRESH" != "$OLD_REFRESH" ]]; then
    pass "refresh rotation (refresh token baru diterbitkan)"
  else
    fail "refresh rotation" "refresh token tidak berganti"
  fi
  req POST /auth/refresh "$(jq -nc --arg r "$OLD_REFRESH" '{refresh_token:$r}')"
  if [[ "$STATUS" == 401 ]]; then
    pass "refresh token lama ditolak (401 $(jqr '.error.code'))"
  else
    fail "refresh token lama ditolak" "status $STATUS, harap 401"
  fi
  # Reuse detection: deteksi reuse boleh mencabut seluruh family (sesi ini).
  # Pakai token terbaru bila masih berlaku, kalau tidak login ulang.
  TOKEN="$NEW_ACCESS"
  req GET /users/me
  if [[ "$STATUS" != 200 ]]; then
    echo "      (sesi dicabut setelah reuse terdeteksi; login ulang untuk logout test)"
    login "$EMAIL_A" "login ulang user A" || true
  fi
else
  TOKEN="$SAVED_TOKEN"
fi

# 10. IDOR: user B tidak boleh melihat akun milik A --------------------------
TOKEN_A="$TOKEN"
if register "$EMAIL_B" "Smoke Tester B" "register user B" \
   && verify "$EMAIL_B" "verify email B" \
   && login "$EMAIL_B" "login user B"; then
  req GET "/accounts/${ACC1}"
  if [[ "$STATUS" == 404 ]]; then
    pass "IDOR: user B GET akun A -> 404"
  else
    fail "IDOR: user B GET akun A" "status $STATUS, harap 404"
  fi
  req POST /auth/logout ""
  expect "logout user B" 204 && pass "logout user B" || true
fi
TOKEN="$TOKEN_A"

# 11. Logout ----------------------------------------------------------------
req POST /auth/logout ""
if expect "logout user A" 204; then
  pass "logout user A"
  req GET /users/me
  if [[ "$STATUS" == 401 ]]; then
    pass "access token ditolak setelah logout (401)"
  else
    fail "access token setelah logout" "status $STATUS, harap 401"
  fi
fi

summary
[[ $FAILED -eq 0 ]]
