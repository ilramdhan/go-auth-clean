# Penjelasan Struktur (Sangat Penting):

- ```cmd/api/```: Tempat file utama (main.go) berada. Ini pintu masuk aplikasi kita.
- ```internal/domain/```: Jantung aplikasi! Berisi bentuk data (Struct/Entity) dan Kontrak Perjanjian (Interface). Folder ini TIDAK BOLEH tahu soal database atau HTTP.
- ```internal/repository/```: Tukang ambil/simpan data ke Database (PostgreSQL/MySQL).
- ```internal/usecase/```: Otak aplikasi. Tempat kita menaruh logika seperti "Cek apakah email sudah ada", "Hash password", dll.
- ```internal/delivery/http/```: Pelayan restoran. Bertugas menerima request dari user (JSON) dan mengembalikan respons (JSON).
- ```pkg/```: Tempat menaruh fungsi-fungsi bantuan (helper) yang bisa dipakai di mana saja, misal fungsi pembuat Token JWT.