# Eval raporu: claude-sonnet-5-5

- **Tarih:** 2026-11-08 15:22 (süre 5s)
- **Sağlayıcı:** anthropic · effort `low` · commit `d8c193b+prompt`
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
| Token | 17433 input / 975 output |
| Ortalama süre | 2.4s / çalıştırma |
| Güvenlik | Derlenen bütün SQL'lerde izin koşulu var, SQL metninde kuraldan gelen değer yok |

## Kategoriye göre

| Kategori | Brief | Doğruluk | Tutarlılık |
|---|---|---|---|
| Basit | 1 | 3/3 | 1/1 |
| Kitle dışı | 1 | 3/3 | 1/1 |

## Brief'ler

| # | Kategori | Beklenen | Çalıştırma 1 | Çalıştırma 2 | Çalıştırma 3 | Doğru | Tutarlı |
|---|---|---|---|---|---|---|---|
| 2 | Basit | ok · 1201 kişi | ✅ ok · 1201 | ✅ ok · 1201 | ✅ ok · 1201 | 3/3 | ✅ |
| 13 | Kitle dışı | partial · 41 kişi | ✅ partial · 41 | ✅ partial · 41 | ✅ partial · 41 | 3/3 | ✅ |

## Hatalı çalıştırmalar

Hatalı çalıştırma yok.

## Tutarsız brief'ler

Aynı brief'in çalıştırmaları farklı sonuç verdi (doğru olsalar bile).

Yok.

## Varsayımlar

Modelin kurala yazdığı varsayımlar (pazarlamacıya onay ekranında gösterilir).

- **#13 · çalıştırma 3:** Bursalılar: şehir Bursa olarak yorumlandı.
