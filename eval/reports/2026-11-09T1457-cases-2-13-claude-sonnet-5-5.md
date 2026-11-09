# Eval raporu: claude-sonnet-5-5

- **Tarih:** 2026-11-09 14:57 (süre 8s)
- **Sağlayıcı:** anthropic · effort `low` · commit `b410b43`
- **Kapsam:** 2 brief × 3 çalıştırma = 6 çalıştırma, 6 API çağrısı (onarımlar dahil)
- **Veri:** sabit seed (RNG 42, 5000 rastgele kullanıcı + test kohortları), "bugün" = 2026-11-01
- **Puanlama:** Bir çalıştırma, status'ü kabul edilenlerden biriyse, kuralının seçtiği kişiler beklenen kişilerle birebir aynıysa ve beklenen `unsupported` sebepleri yazılmışsa doğrudur. Bir brief'in bütün çalıştırmaları aynı status'ü ve aynı kişileri verdiyse brief tutarlıdır (D-TEST).

## Özet

| | Sonuç |
|---|---|
| Doğruluk | 6/6 çalıştırma (%100,0) |
| Tutarlılık | 2/2 brief (%100,0) |
| Onarım | 0 çalıştırmada toplam 0 onarım |
| API hatası | 0 |
| Token | 25719 input / 1151 output |
| Ortalama süre | 3.5s / çalıştırma |
| Güvenlik | Derlenen bütün SQL'lerde izin koşulu var, SQL metninde kuraldan gelen değer yok |

## Kategoriye göre

| Kategori | Brief | Doğruluk | Tutarlılık |
|---|---|---|---|
| Basit | 1 | 3/3 | 1/1 |
| Gönderim zamanı | 1 | 3/3 | 1/1 |

## Brief'ler

| # | Kategori | Beklenen | Çalıştırma 1 | Çalıştırma 2 | Çalıştırma 3 | Doğru | Tutarlı |
|---|---|---|---|---|---|---|---|
| 2 | Basit | ok · 1204 kişi | ✅ ok · 1204 | ✅ ok · 1204 | ✅ ok · 1204 | 3/3 | ✅ |
| 13 | Gönderim zamanı | ok · 41 kişi | ✅ ok · 41 | ✅ ok · 41 | ✅ ok · 41 | 3/3 | ✅ |

## Hatalı çalıştırmalar

Hatalı çalıştırma yok.

## Tutarsız brief'ler

Aynı brief'in çalıştırmaları farklı sonuç verdi (doğru olsalar bile).

Yok.

## Varsayımlar

Modelin kurala yazdığı varsayımlar (pazarlamacıya onay ekranında gösterilir).

- **#13 · çalıştırma 1:** Bursalılar: şehir Bursa olarak yorumlandı. · Gönderim zamanı yarın 2026-11-02 saat 10:00 (Türkiye saati) olarak ayarlandı.
- **#13 · çalıştırma 2:** Bursalılar, şehri Bursa olan kullanıcılar olarak yorumlandı. · Gönderim zamanı yarın 2026-11-02 saat 10:00 (Türkiye saati) olarak alındı.
- **#13 · çalıştırma 3:** Bursalılar: şehri Bursa olan kullanıcılar olarak yorumlandı. · Yarın sabah 10:00, 2026-11-02 10:00 (Türkiye saati) olarak alındı.
