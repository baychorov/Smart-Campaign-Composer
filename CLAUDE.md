# Smart Campaign Composer

Pazarlamacının doğal dille yazdığı brief'i Claude yardımıyla bir hedef kitleye
(segment kuralı) ve SMS + email mesajlarına çeviren sistem. Son kararı insan verir.
Gönderim kuyruk üzerinden mock provider'a yapılır ve canlı izlenir.

Kaynaklar (içeriklerini buraya kopyalama, oradan oku):
- `docs/CASE.md`: gereksinimler (R-AUD-1, R-SEND-3 gibi ID'ler buradan)
- `DECISIONS.md`: kararlar ve gerekçeleri
- `docs/PLAN.md`: uygulama sırası, adım durumları, eval test seti

Teknoloji: Go (backend), Vue 3 + TypeScript + Vite (frontend), PostgreSQL
(veritabanı + kuyruk), Claude API (`claude-sonnet-5-5`, `LLM_MODEL` ile değiştirilebilir).

## Klasör yapısı

```
backend/       Go modülü: cmd/{api,worker,mockprovider,seed}, internal/, migrations/, Dockerfile
               Go kuralları: backend/CLAUDE.md
frontend/      Vue 3 + TypeScript uygulaması (Vite, Vitest); Dockerfile + nginx.conf
docs/          CASE.md, PLAN.md
scripts/       smoke.sh (make smoke)
eval/reports/  make eval raporları (Markdown + JSON, repoda tutulur)
ai-sessions/   Claude session export'ları (D-6)
DECISIONS.md   Karar kayıtları (D-5)
```

## Değişmez kurallar

Bu kurallar hiçbir değişiklikte gevşetilmez:

1. **LLM çıktısı asla SQL metnine girmez.** Kolon adları koddaki sabit bir tablodan,
   değerler yalnızca `$n` parametresi olarak gelir (R-AUD-1).
2. **İzin filtresi her zaman SQL derleyicide eklenir.** Kuralın bir parçası değildir;
   LLM veya kullanıcı onu kaldıramaz. Gruplar parantez içinde derlenir (R-AUD-2).
3. **Karakter sayımı rune ile yapılır**, `len()` ile değil. Limitler render sonrası
   en kötü durum için doğrulanır (R-CNT-1, R-CNT-2).
4. **`.env` okunmaz, API key loglanmaz.** Kişisel veriler (telefon, email) loglara yazılmaz.
5. LLM çıktısı onay öncesi her zaman backend'de doğrulanır; `refusal` ve `max_tokens`
   ile biten yanıtlar parse edilmez (R-AI-1).
6. Testler gerçek API key olmadan çalışır (R-AI-2).

## Komutlar

| Komut | Amaç | Durum |
|---|---|---|
| `docker compose up --build` | Tüm sistemi seed ve mock provider ile başlatır; arayüz http://localhost:3000 | var |
| `make up` / `make down` | `docker compose up --build` / `docker compose down` kısayolları | var |
| `make smoke` | Çalışan sisteme uçtan uca duman testi (yalnızca mock LLM ile çalışır, ücretsiz) | var |
| `make test` | Test katmanları 1–4 ve frontend testleri (Docker gerekir, API key gerekmez) | var |
| `make frontend-dev` | Vite dev sunucusu (:5173); `/api` istekleri Go API'ye (:8080) yönlenir | var |
| `make test-unit` | Katman 1 (birim) ve 3 (LLM, fake ve sahte SSE ile). Docker ve key gerekmez | var |
| `make test-live` | Gerçek API ile adaptör kontrat kontrolü, en fazla 2 çağrı (key gerekir, ücretli) | var |
| `make eval` | Gerçek API ile 20 brief × 3 eval'i; rapor `eval/reports/`'a yazılır (key gerekir, ücretli). Küçük deneme: `make eval RUNS=1 CASES=1,4,7` | var |
| `make eval-dry` | Aynı eval, API yerine ideal yanıtlarla (ücretsiz, rapor dosyası yazmaz) | var |
| `make lint` | `gofmt`, `go vet`, frontend lint | var |

Test katmanları, build tag'leri ve test ilkesi: `DECISIONS.md` D-TEST.
Ücretli komutlar (`test-live`, `eval`) yalnızca kullanıcı onayıyla çalıştırılır.

## Dil kuralı

- Dokümantasyon (DECISIONS.md, PLAN.md, CLAUDE.md, README) **Türkçe**.
- Kod, kod yorumları, değişken isimleri ve commit mesajları **İngilizce**.

## Çalışma şekli

- Bir değişikliğe başlamadan önce kısa bir plan yaz: ne değişecek, hangi dosyalar, nasıl test edilecek.
- Her özellik testiyle birlikte gelir; test eklenmeden adım tamamlandı sayılmaz.
- Go veya Vue'ya özgü bir kavram ya da yöntem kullandığında kısaca açıkla
  (kullanıcı Python bilgisine sahip; Go ve Vue'da deneyimi az).
- Bir adım bitince `docs/PLAN.md`'de durumunu güncelle ve kısa bir not düş.
  Bir karar değişirse `DECISIONS.md`'ye tarihli bir güncelleme notu ekle.
- Gereksiz soyutlama ekleme. Interface yalnızca değiştirilebilir sınırlarda
  tanımlanır (bkz. DECISIONS D-ARCH).
- Claude API ayrıntılarında (parametreler, model davranışı) hafızaya güvenme,
  dokümantasyondan doğrula.

## Commit formatı

Conventional Commits, İngilizce:

```
<type>(<scope>): <summary>

feat(segment): compile DNF groups to parameterized SQL
fix(delivery): respect Retry-After on 429
test(content): cover rune-based SMS length with Turkish chars
docs: update PLAN step 4 status
```

Tipler: `feat`, `fix`, `test`, `refactor`, `docs`, `chore`, `build`, `ci`.
Scope'lar paket adlarıyla eşleşir: `segment`, `content`, `llm`, `campaign`, `delivery`,
`mockprovider`, `httpapi`, `frontend`.
