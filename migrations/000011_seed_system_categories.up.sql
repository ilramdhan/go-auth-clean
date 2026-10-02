-- Kategori system (user_id NULL) terlihat oleh semua user dan read-only.
-- UUID tetap agar idempotent dan bisa direferensikan kode/test
-- (01920000-0000-7000-8000-000000000108 = Biaya Admin untuk biaya transfer).
INSERT INTO categories (id, user_id, type, name, icon) VALUES
    ('01920000-0000-7000-8000-000000000001', NULL, 'income', 'Gaji', 'briefcase'),
    ('01920000-0000-7000-8000-000000000002', NULL, 'income', 'Bonus', 'gift'),
    ('01920000-0000-7000-8000-000000000003', NULL, 'income', 'Lainnya', 'plus'),
    ('01920000-0000-7000-8000-000000000101', NULL, 'expense', 'Makan & Minum', 'utensils'),
    ('01920000-0000-7000-8000-000000000102', NULL, 'expense', 'Transportasi', 'car'),
    ('01920000-0000-7000-8000-000000000103', NULL, 'expense', 'Belanja', 'cart'),
    ('01920000-0000-7000-8000-000000000104', NULL, 'expense', 'Tagihan', 'receipt'),
    ('01920000-0000-7000-8000-000000000105', NULL, 'expense', 'Hiburan', 'film'),
    ('01920000-0000-7000-8000-000000000106', NULL, 'expense', 'Kesehatan', 'heart'),
    ('01920000-0000-7000-8000-000000000107', NULL, 'expense', 'Pendidikan', 'book'),
    ('01920000-0000-7000-8000-000000000108', NULL, 'expense', 'Biaya Admin', 'bank'),
    ('01920000-0000-7000-8000-000000000199', NULL, 'expense', 'Lainnya', 'dots')
ON CONFLICT (id) DO NOTHING;
