-- Penanda overdue tagihan (worker bill): jatuh tempo yang sudah dinotifikasi overdue.
ALTER TABLE bills ADD COLUMN IF NOT EXISTS overdue_for DATE;
