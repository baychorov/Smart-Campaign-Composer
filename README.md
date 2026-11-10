# Smart Campaign Composer

[![CI](https://github.com/baychorov/Smart-Campaign-Composer/actions/workflows/ci.yml/badge.svg)](https://github.com/baychorov/Smart-Campaign-Composer/actions/workflows/ci.yml)

Pazarlamacı ne istediğini doğal dille yazar, Claude hedef kitleyi ve SMS + email mesajlarını
önerir. Pazarlamacı düzenleyip onaylar, kampanya mock provider'a gönderilir ve canlı izlenir.

> "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara
> %30 indirim hatırlatması gönder. Samimi ama kısa olsun."

**Teknolojiler:** Go · Vue 3 + TypeScript · PostgreSQL · Claude API (`claude-sonnet-5-5`)

<br>

## Kurulum

Yalnızca Docker gerekir.

```bash
git clone git@github.com:baychorov/Smart-Campaign-Composer.git
cd Smart-Campaign-Composer
docker compose up --build
```

Arayüz: **http://localhost:3000**

Örnek veri ve mock provider otomatik kurulur. Varsayılan **mock LLM**'dir, API key gerekmez.

**Gerçek Claude ile:**

```bash
cp .env.example .env
# .env içinde: LLM_PROVIDER=anthropic ve ANTHROPIC_API_KEY=<key>
docker compose up --build
```

**Durdurma:** `make down` veriyi korur, `docker compose down -v` veriyi siler.

<br>

## Mimari

```mermaid
flowchart LR
    U([Pazarlamacı]) --> W["web<br/>Vue + nginx<br/>:3000"]
    W -- "/api, SSE" --> A["api<br/>Go<br/>:8080"]
    A -- "kural, mesaj" --> C[(Claude API<br/>veya mock LLM)]
    A -- "kampanya, kuyruk" --> P[(PostgreSQL)]
    K["worker<br/>Go<br/>:8081"] -- "claim / sonuç" --> P
    K -- "SMS / email" --> M["mockprovider<br/>:8090"]
```

| Servis | Görev |
|---|---|
| `web` | Arayüz, `/api` isteklerini API'ye iletir |
| `api` | Kural ve mesaj üretir, onayı alır, kuyruğu yazar, canlı durumu yayınlar |
| `worker` | Kuyruktan mesajları gönderir, hız limiti ve retry uygular |
| `mockprovider` | Sahte SMS/email servisi, rastgele hata, gecikme ve 429 döndürür |
| `postgres` | Veri ve kuyruk, ayrı bir broker yok |
| `seed` | Açılışta şemayı ve örnek veriyi yükler |

**Veri modeli**

| Tablo | Ne tutar |
|---|---|
| `users` | Ad, soyad, email, telefon, şehir, yaş, SMS ve email izni |
| `user_events` | Kayıt, ürün inceleme, sepete ekleme ve alışveriş olayları, zamanlarıyla |
| `campaigns` | Brief, kitle kuralı, mesajlar, seçilen kanallar, durum ve gönderim zamanı |
| `deliveries` | Kişi ve kanal başına bir mesaj, hem kuyruk hem gönderim kaydı |

Seed sabit bir rastgelelik tohumuyla her kurulumda aynı kullanıcıları ve olayları üretir, olay
tarihleri kurulum anına göre hesaplanır. 12 şehirde 5.021 sahte
kullanıcı ve son iki yıla yayılmış 43.334 olay var. Kullanıcıların 3.525'inin SMS, 4.017'sinin
email izni var. Testler için bilerek konmuş kenar durumları da bu veride.

<br>

## Akış

1. **Kitle:** Claude brief'ten JSON kural üretir, backend doğrular ve kişi sayısını gösterir.
2. **Mesaj:** SMS ve email ekrana akarak gelir, karakter limitleri kontrol edilir.
3. **Onay:** Pazarlamacı kanalları ve isterse gönderim zamanını seçer.
4. **Gönderim:** Worker gönderir, dashboard canlı gösterir.

<br>

## Temel güvenceler

- LLM çıktısı SQL'e girmez, değerler yalnızca parametre olur.
- İzni olmayan kişiye mesaj gitmez.
- Aynı kişiye aynı mesaj iki kez gitmez, worker çökse de.
- Kanal başına saniyede en fazla 100 mesaj gider.
- SMS en fazla 160, email konusu en fazla 80 karakterdir.
- Telefon, email ve API key loglara yazılmaz.

<br>

## Testler

| Komut | Ne test eder |
|---|---|
| `make test-unit` | Kural doğrulama, SQL üretimi, mesaj kuralları, retry, LLM yanıtları |
| `make test` | Yukarıdakiler, veritabanıyla kitle ve kuyruk testleri, uçtan uca gönderim, arayüz |
| `make lint` | Kod biçimi ve statik kontroller |
| `make smoke` | Çalışan sistemde brief'ten gönderime tüm akış |
| `make eval-dry` | Eval puanlamasının doğruluğu, API çağrısı yapmadan |
| `make test-live` | Gerçek Claude API ile bağlantı kontrolü, **ücretli** |
| `make eval` | Gerçek modelle 25 brief × 3 çalıştırma, **ücretli** |

&nbsp;

- Sistemi çalıştırmak için yalnızca Docker yeterlidir.
- Testler için ayrıca Go 1.27 ve Node 26 gerekir. Gerekli veritabanını testler kendisi başlatır.
- Yalnızca `make smoke` için sistemin önceden `docker compose up` ile çalışıyor olması gerekir.

**Model değerlendirmesi:** Son tam eval'de 75/75 doğru ([rapor](eval/reports/2026-11-09T1416-claude-sonnet-5-5.md)).
Sonradan eklenen zamanlanmış gönderim için iki brief ayrıca ölçüldü, 6/6 ([rapor](eval/reports/2026-11-09T1457-cases-2-13-claude-sonnet-5-5.md)).

<br>

## Bonus özellikler

- **Zamanlanmış gönderim:** Gönderim başlayana kadar düzenlenebilir veya iptal edilebilir.
- **CI:** Her push ve pull request'te `make lint`, `make test` ve `make smoke` çalışır.

<br>

## Sınırlar

- Kimlik doğrulama yok, tek `api` ve tek `worker` varsayılır.
- Veri modeli bilerek dar tutuldu. Harcama tutarı, ürün/kategori, cinsiyet gibi alanlar ve sıralama, "en fazla N kez" gibi kriterler desteklenmez. Gerçek bir projede bunlar eklenmeli.
- Email gövdesi düz metindir. Gerçekte Türkçe karakterli SMS 70 karakterde bölünür.
- Mock provider kayıtlarını bellekte tutar. `make seed-reset` sonrası `docker compose restart mockprovider` gerekir.
- A/B varyantları ve kullanıcının diline göre içerik yapılmadı.

<br>

## Dokümanlar

- [`DECISIONS.md`](DECISIONS.md): önemli kararların özeti
- [`docs/DECISIONS-DETAIL.md`](docs/DECISIONS-DETAIL.md): kararların ayrıntılı kaydı
- [`docs/PLAN.md`](docs/PLAN.md): uygulama adımları ve eval test seti
- [`docs/CASE.md`](docs/CASE.md): gereksinimler
- [`ai-sessions/`](ai-sessions/): Claude Code oturum kayıtları
