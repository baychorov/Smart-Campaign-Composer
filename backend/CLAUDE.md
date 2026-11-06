# Backend: Go kuralları

Genel kurallar kökteki `CLAUDE.md`'de. Bu dosya yalnızca Go'ya özgü kuralları içerir.
Go sürümü: 1.27.

## Paket yapısı: özellik bazlı

```
cmd/
  api/ worker/ mockprovider/ seed/   main.go yalnızca bağımlılıkları kurar, mantık içermez
internal/
  segment/      kural tipleri, doğrulama, SQL derleyici (saf mantık, I/O yok)
  content/      mesaj doğrulama, şablon render, artımlı JSON çıkarıcı
  llm/          Client interface'i, Anthropic adaptörü, fake, prompt'lar
  campaign/     durum makinesi, akışın orkestrasyonu, Postgres deposu
  delivery/     worker, limiter, retry politikası, provider istemcisi
  mockprovider/ HTTP sunucusu, hata enjeksiyonu (iş paketlerini import etmez)
  httpapi/      handler'lar, SSE
  platform/     config, DB bağlantısı, metrics
migrations/
```

- Paketler teknik katmana göre değil, özelliğe göre ayrılır. `services/` ve `repositories/` gibi klasörler yok.
- Bağımlılık yönü: `httpapi` → `campaign` → `segment`, `content`, `llm`, `delivery`. Saf paketler (`segment`, `content`) başka iş paketini import etmez.
- `internal/` altındaki paketler modül dışından import edilemez; bunu derleyici zorlar.

## Interface'ler

- Go'da interface'ler örtük olarak karşılanır (Python'daki `Protocol` gibi). Bu yüzden interface implementasyonun yanında değil, **onu kullanan pakette** tanımlanır.
- Yalnızca şu sınırlarda interface tanımlanır:
  - `llm.Client`: gerçek API ve fake arasında geçiş için (R-AI-2)
  - `delivery.Provider`: SMS ve email provider'ları; testlerde fake ile değiştirilir
  - `delivery.Store`: dar bir interface (claim, mark sent/retry/failed); worker'a hata enjekte etmek için
- Saat için interface yerine `now func() time.Time` alanı kullanılır.
- Yeni bir interface eklemeden önce iki implementasyonun gerçekten olduğunu (ya da test için şart olduğunu) kontrol et. Tek implementasyon için interface yazma, somut tipi kullan.
- Interface'leri küçük tut; genellikle 1–4 metot.

## Hata yönetimi

- Hataları bağlam ekleyerek sarmala: `fmt.Errorf("claim deliveries: %w", err)`. Mesaj küçük harfle başlar ve nokta ile bitmez.
- Hata türünü kontrol etmek için `errors.Is` ve `errors.As` kullanılır; hata mesajı string olarak karşılaştırılmaz.
- Çağıranın ayırt etmesi gereken durumlar için sentinel hata tanımlanır (`var ErrInvalidRule = errors.New(...)`) veya doğrulama hataları için alan listesi taşıyan bir tip yazılır.
- `panic` yalnızca programcı hatası için kullanılır (ör. başlangıçta geçersiz config). İstek akışında kullanılmaz.
- Hata bir kez ele alınır: ya loglanır ya da yukarı döndürülür, ikisi birden değil.
- Anthropic SDK hataları `errors.As(err, &apierr)` ile ayrıştırılır ve `StatusCode` üzerinden dallanır.

## Context

- I/O yapan ya da uzun süren her fonksiyon ilk parametre olarak `ctx context.Context` alır.
- `context` struct alanında saklanmaz.
- HTTP handler'larda `r.Context()` aşağıya iletilir; istemci bağlantıyı kopardığında (ör. SSE) iş iptal olur.
- Dış çağrılara (LLM, provider, DB) `context.WithTimeout` ile süre sınırı konur.
- Worker'ın kapanışı `signal.NotifyContext` ile yapılır; işlemdeki satırlar lease süresi dolunca başka worker tarafından geri alınır.

## Veri ve SQL

- Sürücü `pgx`. SQL'de yalnızca `$n` parametreleri kullanılır. Kolon adları `segment` paketindeki sabit bir tablodan gelir.
- LLM JSON'u `json.Decoder` + `DisallowUnknownFields()` ile decode edilir. Go eksik alanı sessizce sıfır değer yapar; zorunlu alanlar ayrıca doğrulanır.
- Metin uzunluğu `utf8.RuneCountInString` ile ölçülür. Türkçe büyük/küçük harf dönüşümü için `strings.ToLower` yeterli değildir (İ/ı sorunu); normalizasyon için ayrı bir fonksiyon kullanılır.
- Loglama `log/slog` ile yapılır. API key, telefon ve email loglanmaz.

## Test yaklaşımı

- **Tablo bazlı testler:** Girdi ve beklenen sonuç bir slice'ta tutulur; her satır `t.Run` ile alt test olarak çalışır.
- **Birim testler:** `segment` ve `content` gibi saf paketler DB'siz test edilir.
- **Entegrasyon testleri:** Postgres davranışına bağlı kısımlar (`SKIP LOCKED`, unique kısıtlar, segment sayıları) gerçek Postgres ile test edilir. Fake ile doğrulanamaz.
- **Fake'ler elle yazılır:** Mock üreten kütüphane kullanılmaz. Fake, sırayla yanıt verir ve gelen çağrıları kaydeder.
- **Zaman:** Testlerde `Sleep`'e dayanılmaz. `testing/synctest` ile sahte saat kullanılır veya `now` fonksiyonu enjekte edilir.
- **Property testleri:** İzin koşulunun her derlenen SQL'de bulunduğu, grup parantezlerinin doğru olduğu ve kullanıcıdan gelen değerin SQL metnine hiç girmediği test edilir.
- **Gerçek API:** `make test` gerçek API'ye hiç bağlanmaz. Gerçek API yalnızca `make test-live` ve `make eval` içinde kullanılır.
- **Build tag'leri:** Postgres veya mock provider gerektiren test dosyaları `//go:build integration`, gerçek API kullananlar `//go:build live` ile başlar. Katmanlar: `DECISIONS.md` D-TEST.
- **Yarış kontrolü:** Testler `-race` ile çalışır.
