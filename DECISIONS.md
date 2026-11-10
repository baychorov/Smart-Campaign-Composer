# Kararlar

Bu dosya projedeki önemli tasarım kararlarını ve gerekçelerini tutar (D-5).
Gereksinim ID'leri `docs/CASE.md`'ye aittir. Uygulama sırası `docs/PLAN.md`'dedir.

Her kayıt dört başlık içerir: **Karar**, **Gerekçe**, **Alternatifler**, **Sınırlar**.
Bir karar değişirse eski metin silinmez; kaydın altına tarihli bir "Güncelleme" notu eklenir.

---

## D-ARCH: Backend paket yapısı ve interface sınırları

- **Karar:** Tek Go modülü. `cmd/{api,worker,mockprovider,seed}` yalnızca bağımlılıkları kurar. İş mantığı `internal/` altında özellik bazlı paketlerde durur: `segment`, `content`, `llm`, `campaign`, `delivery`, `mockprovider`, `httpapi`, `platform`. Interface yalnızca gerçekten değiştirilebilir sınırlarda tanımlanır ve onu kullanan pakette durur: `llm.Client`, `delivery.Provider`, `delivery.Store`. Saat için interface yerine `func() time.Time` kullanılır.
- **Gerekçe:** Bir özelliğin kodu tek pakette toplanır ve kendi başına test edilir. `llm.Client` interface'i R-AI-2'nin doğrudan gereği. `delivery.Provider` ve `delivery.Store` olmadan worker'a hata enjekte edilemez (ör. "gönderdi ama kaydedemeden düştü"). `mockprovider` iş paketlerini import etmez, yalnızca HTTP üzerinden konuşur; böylece gerçek bir dış servisi taklit eder (R-MOCK-1).
- **Alternatifler:** Teknik katman yapısı (`handlers/services/repositories`): bir özellik üç klasöre dağılır. Tam hexagonal mimari: 7 günlük süre için fazla tören ve gereksiz soyutlama.
- **Sınırlar:** Kampanya deposunun interface'i yok. Postgres'e özgü davranış (`SKIP LOCKED`, unique kısıtlar) fake ile doğrulanamaz; bu kısımlar gerçek Postgres ile test edilir.

## D-DB: Veritabanı ve kuyruk

- **Karar:** Yalnızca PostgreSQL. `deliveries` tablosu hem kuyruk hem durum kaydıdır. Worker işleri `FOR UPDATE SKIP LOCKED` ile alır ve süreli kilit (lease) kullanır. Sürücü `pgx`, migration aracı `goose` veya `golang-migrate`.
- **Gerekçe:** Kampanya durumu ve kuyruk aynı transaction'da yazılır, iki sistem arası tutarlılık sorunu (dual write) olmaz. Kanal başına 100 msg/sn (R-SEND-2) Postgres için hafif bir yük. Claim, lease ve retry kodunun bize ait olması güvenilirlik tasarımını görünür kılar (R-SEND-1).
- **Alternatifler:** River (Postgres tabanlı Go kuyruğu): job satırları teslimat satırlarını ikiler, kanal limitini yine biz yazarız. Redis/Asynq: outbox gerektirir. RabbitMQ/NATS: altyapı ve öğrenme yükü. Kafka: bu ölçek için fazla.
- **Sınırlar:** Tek worker replikası varsayılır. Ölçek büyürse broker'a geçiş gerekir.
- **Güncelleme (2026-11-06, PLAN adım 3):** Migration aracı olarak `goose` (v3) seçildi. Her migration tek bir SQL dosyasında up ve down bölümleriyle durur. Dosyalar `//go:embed` ile binary'ye gömülür ve `cmd/seed` tarafından uygulanır; ayrı bir CLI gerekmez. golang-migrate her migration için iki dosya ister ve gömülü kullanımı daha zahmetlidir. Seed üreticisi "şimdi" zamanını parametre olarak alır: testler sabit bir tarih kullanır, böylece "son N gün" pencereleri kaymaz ve sayılar değişmez.
- **Güncelleme (2026-11-07, PLAN adım 9):** Claim tek bir sorgudur: `pending` ve zamanı gelmiş satırlar ile lease süresi dolmuş `sending` satırları `FOR UPDATE SKIP LOCKED` ile seçilir, `sending` yapılır ve `attempts` 1 artırılır. Aynı sorgu kullanıcının izin durumunu, telefon/email'ini ve kampanya metnini de döner. Kampanyanın ilk satırı alındığında kampanya aynı transaction'da `approved → sending` olur. Açık satırı kalmayan kampanyalar, worker boşta kaldığında `completed` yapılır.

## D-SEG: Segment kuralı = ortak koşullar + DNF grupları

- **Karar:** Kitle = **izin ∧ common ∧ (g₁ ∨ … ∨ gₙ)**. Her grup kendi içinde VE ile bağlı koşullardan oluşur, gruplar VEYA ile bağlanır. Grup içine grup konmaz.
  - **Koşul tipleri:**
    - `attribute`: `city` için `in`/`not_in`, `age` için `gte`/`lte`.
    - `event`: `signup`, `product_view`, `add_to_cart`, `purchase` olaylarından biri; olay için var/yok bilgisi ve "son N gün" penceresi.
  - **Şema alanları:**
    - `status`: `ok` | `partial` | `unsupported` | `rejected`
    - `audience_scope`: `filtered` | `all`
    - `common`, `groups[].conditions`, `assumptions`, `unsupported[{text, reason, group}]`, `notes`
  - **Kanonik biçim:** `groups` ya boş olur ya da en az 2 grup içerir; tek grup `common` olarak yazılır. Aynı alandaki VEYA ayrı grup değil `in` ile yazılır. Boş kural yalnızca `audience_scope: all` ile kabul edilir.
  - **Sınırlar:** En fazla 5 grup, grup başına en fazla 6 koşul, `common` içinde en fazla 6 koşul, toplam en fazla 20 koşul. `in` için en fazla 20 değer. `within_days` 1–365, `age` 13–100. Sınır aşılırsa sonuç `unsupported/too_complex` olur.
  - **Doğrulama (R-AUD-1, R-AI-1):**
    1. API şeması yapıyı garanti eder.
    2. Go tarafında `DisallowUnknownFields` ile tipli struct'a çevrilir.
    3. Semantik kontroller: alan/operatör uyumu; sınırlar; Türkçe kurallarıyla enum normalizasyonu; her grubun `common` ile birleştirilerek çelişki kontrolü; birebir aynı grupların reddi; başka bir grubu kapsayan grup için uyarı; status ile listelerin tutarlılığı.
  - **SQL'e derleme:** `WHERE izin AND (common) AND ((g1) OR (g2) …)`. Kolon adları Go'daki sabit bir tablodan gelir, değerler yalnızca `$n` parametresi olarak gider. Olay koşulları `EXISTS`/`NOT EXISTS` olur. Sorgu read-only transaction ve `statement_timeout` ile çalışır. Grup başına sayılar tek sorguda `count(*) FILTER` ile alınır.
- **Gerekçe:** Düz AND listesi, birden fazla alt grubu hedefleyen gerçekçi brief'leri karşılayamıyordu (ör. "İstanbul'daki 25 yaş altı ve Ankara'daki 40 yaş üstü"). İki seviyeli yapı recursive olmadığı için Claude structured output ile garanti edilebilir; API recursive şemayı desteklemiyor. `common` listesi, ortak bir koşulu her grupta tekrar ederken birinin unutulmasını ve o grubun kitlesinin sessizce genişlemesini önler. Kuralın Türkçe özeti LLM'den değil JSON'dan deterministik üretilir; pazarlamacı tam olarak çalışacak kuralı görür.
- **Alternatifler:**
  - Düz AND listesi: basit ama ifade gücü yetersiz.
  - Saf DNF (`common` olmadan): tekrarlanan koşul unutulursa kitle sessizce genişler.
  - Recursive ağaç: şema garantisi yok.
  - LLM'den SQL üretip parse etmek: R-AUD-1'in amacına aykırı ve güvenlik yüzeyi geniş.
- **Sınırlar:**
  - Desteklenmeyen kriterler: sıra/zamansal ilişki ("sepete ekledikten sonra"), sayı ve toplam ("en az 3 sipariş"), sıralama ("en çok harcayan 500"), ürün veya kategori detayı, kesin tarih aralığı, veride olmayan alanlar.
  - Bunlar `unsupported` listesine kapalı bir sebep kodlarıyla yazılır: `sequence`, `aggregate`, `ranking`, `unknown_field`, `absolute_date`, `scheduling`, `contradiction`, `too_complex`, `off_topic`.
  - İfade edilemeyen bir koşul yalnızca kendi grubundan düşer. Bütün bir grup asla düşürülmez. Boşalan grup `unsupported` sayılır.
  - Tüm gruplar aynı mesajı alır. Gruba özel içerik A/B bonusunun kapsamındadır.
- **Güncelleme (2026-11-06, PLAN adım 4):**
  - **Şehir listesi:** Şehir değerleri koddaki 81 il listesiyle karşılaştırılır ve doğru yazıma çevrilir (`IZMIR` → `İzmir`). Karşılaştırma Türkçe harfleri ASCII'ye indiren bir anahtarla yapılır; `strings.ToLower`, `I`/`İ` yüzünden yetersizdir. Listede olmayan bir ad hatadır. Listede olup kullanıcısı olmayan bir il geçerlidir ve kitle 0 olur.
    - Alternatif: DB'den `SELECT DISTINCT city`. Reddedildi, çünkü "geçerli il ama müşteri yok" ile "böyle bir şehir yok" ayrımını yapamaz. Pazarlamacı için ilki daha doğru bir geri bildirim (kullanıcı kararı).
  - **Normalizasyon:** Anlamı değiştirmeyen düzeltmeler otomatik yapılır ve her biri bir uyarı olarak kaydedilir: enum büyük/küçük harfi, şehir yazımı, tekrarlanan değer ve koşul, başka bir grubu kapsayan gruptaki fazlalık (A ∨ (A ∧ B) = A), tek grubun `common`'a taşınması. Birebir aynı gruplar hata olarak kalır. Bu düzeltmelerin anlamı değiştirmediği, ham kural ile SQL sonucunun karşılaştırıldığı entegrasyon testiyle doğrulanır.
  - **Koşul biçimi:** Tek bir düz struct kullanılır: `type` (`attribute` | `event`); attribute için `field`, `op`, `values` (city) veya `value` (age); event için `event`, `occurred` (bool), `within_days`. Hangi alanların dolu olacağını doğrulama zorlar. `unsupported[].group` 0 tabanlıdır.
  - **Derleme:** Derleyici, kuralı kendisi `Validate`'ten geçirir ve yalnızca `ok`/`partial` kuralları derler. `rejected` veya `unsupported` bir kural asla "herkese gönder"e dönüşmez. "Son N gün" penceresi Go'da `now`'dan hesaplanıp parametre olarak verilir.
  - **Sınır:** `segment` sorguyu yalnızca üretir. Read-only transaction ve `statement_timeout` ile çalıştırma adım 7'de `campaign` paketinde yapılır.
- **Güncelleme (2026-11-07, kullanıcı kararı): Yaş ifadelerinin yorumu.** "X yaş üstü", "X yaş üzeri" ve "X yaş ve üstü" hepsi `age gte X` olarak yorumlanır, yani X dahildir. "X yaş altı" `age lte X-1` olarak yorumlanır, yani X hariçtir. Kural prompt'ta yazılıdır. Kural özeti zaten "X yaş ve üzeri" diye gösterdiği için pazarlamacı sınırın dahil olduğunu görür.
  - Gerekçe: Günlük dilde "18 yaş üstü" çoğunlukla "18 ve üstü" anlamında kullanılır. Test setinde #6 ile #16 bu konuda çelişiyordu; #16 `age≥40` olarak düzeltildi.
  - Sınır: "Altı" ile "üstü" simetrik değildir. Belirsiz bir durumda pazarlamacı, onay ekranındaki özetten sınırı görüp düzeltebilir.
- **Güncelleme (2026-11-08, PLAN adım 13, kullanıcı kararı): Zaman ifadeleri.** Teklifi veya mesajı anlatan zaman ifadeleri ("hafta sonu kargo bedava", "bu hafta sonuna kadar %20 indirim") içeriktir: `notes`'a yazılır, kural `ok` kalır. Kampanyanın gönderim zamanı ("yarın sabah 10'da gönder") her zaman `unsupported`'a `scheduling` sebebiyle yazılır ve kural `partial` olur. Gönderim zamanı kitleyi değiştirmese de pazarlamacının zamanlamanın desteklenmediğini görmesi gerekir; D-PARTIAL onayıyla kampanya yine gönderilebilir.
  - Gerekçe: İlk eval'de model "hafta sonu kargo bedava"yı üç kez de gönderim zamanı sandı ve kuralı gereksiz yere `partial` yaptı. Gönderim zamanını ise bir kez `notes`'a yazıp kuralı `ok` bıraktı. Prompt'a iki örnekli bir ayrım eklendi; aynı iki brief'in yeniden çalıştırılmasında 6/6 doğru oldu.
- **Güncelleme (2026-11-09, PLAN adım 14b, kullanıcı kararı): Sayı, kesin tarih ve sıra.** Olay koşuluna yeni alanlar eklendi; yeni koşul tipi yok, eski kurallar aynen geçerli.
  - **Alanlar:** `min_count` ("en az N kez", 2–100, yalnızca "yaptı" ile, signup hariç); `from`/`to` (`within_days` yerine takvim aralığı, iki gün dahil, en fazla 366 gün, Türkiye saati UTC+3); `then` (`{event, occurred}`: "X yaptı, sonra Y yaptı/yapmadı"; Y, X'ten sonra şimdiye kadar aranır; `min_count` ile birlikte kullanılamaz, Y ≠ X).
  - **SQL:** Sayı `(SELECT count(*) …) >= $n`, sıra `EXISTS (X … AND [NOT] EXISTS (Y, Y.zaman > X.zaman))`, tarih `occurred_at >= $başlangıç AND occurred_at < $ertesi_gün`. Değerler yine yalnızca parametredir.
  - **Doğrulama:** Pencere tam olarak bir biçimde yazılır. Koşul anahtarı yeni alanları içerir; içermeseydi "aldı" ile "3 kez aldı" tekrar sayılıp biri silinir ve kitle genişlerdi. Çelişki kontrolü takvim aralıklarını kendi aralarında karşılaştırır; son N gün ile takvim aralığını karşılaştırmaz, çünkü `Validate` saati bilmez.
  - **Yeni kural:** `unsupported` içinde `too_complex` varsa status `unsupported` olmalıdır. Model bir eval çalıştırmasında 6 gruptan birini atıp `partial` dönmüştü; sınırı aşan kural kısaltılmaz.
  - **Kapsam dışı (kullanıcı kararı):** Tutar/harcama, sıralama, ürün/kategori ve "en fazla N kez". Bunlar ve üç olaylı sıra, iki olay arası süre sınırı, 366 günden uzun veya tekrarlayan tarihler `unsupported`'a yazılmaya devam eder.
  - **Gerekçe:** Bu kriterler mevcut `user_events` verisiyle ifade edilebiliyor ve pazarlamacı için gerçek kampanyalara karşılık geliyor (sadakat, belirli dönem, sepet hatırlatma). Tek düz koşul yapısı korunduğu için şema, doğrulama ve derleyici küçük kaldı.

## D-PARTIAL: Kısmi kural onayı kilitler

- **Karar:** `status: partial` olan bir kuralda onay düğmesi kapalı kalır. Pazarlamacı uygulanmayan kriterleri görüp "bu kriterler olmadan devam et" diye açıkça onaylamalıdır.
- **Gerekçe:** Bir VE koşulunun düşmesi kitleyi her zaman genişletir, yani hedeflenenden fazla kişiye mesaj gider. Bu sessizce olmamalı.
- **Alternatifler:** Yalnızca uyarı göstermek: gözden kaçabilir. `partial` sonucu tamamen reddetmek: yaklaşık ama kabul edilebilir kuralları da engeller.
- **Sınırlar:** Onay kampanya kaydına zaman damgasıyla yazılır, ama uygulanmayan kriterlerin listesini ayrıca saklamayız.
- **Güncelleme (2026-11-07, PLAN adım 7):** "Kriterler olmadan devam et" onayı ayrı bir endpoint değildir; onay isteğinin içinde (`partial_ack: true`) gönderilir. Böylece kaydedilen onay, düğmeye basıldığı anda ekranda görünenle aynıdır. Backend, `partial` bir kuralı bu alan olmadan onaylamaz (`partial_not_acknowledged`). Zaman damgası (`partial_ack_at`) yalnızca kural gerçekten `partial` ise ve onayla aynı transaction'da yazılır.

## D-CHANNEL: Kanal seçimi LLM'e bırakılmaz

- **Karar:** Onay ekranında SMS ve email için iki checkbox bulunur, varsayılan olarak ikisi de seçilidir. Brief'te kanal belirtilmişse bu yalnızca `notes` içinde bilgi notu olarak gösterilir, onayı kilitlemez.
- **Gerekçe:** Kanal seçimi deterministik ve insan kontrolünde olmalı. LLM'in yanlış yorumlaması mesajın gönderilmemesine ya da istenmeyen bir kanaldan gitmesine yol açar.
- **Alternatifler:** Şemaya bir `channels` alanı eklemek: LLM hatasına açık.
- **Sınırlar:** İzin kontrolü (R-AUD-2) kanal seçiminden bağımsız olarak her durumda derleyicide uygulanır. Arayüz her kanal için uygun kişi sayısını ayrıca gösterir.
- **Güncelleme (2026-11-07, PLAN adım 7):** Kanal seçimi de onay isteğiyle gelir (`channels: {sms, email}`) ve onayla aynı atomik UPDATE'te kaydedilir. Seçilmeyen kanal için `deliveries` satırı yazılmaz ve o kanalın içeriği (ör. yalnızca SMS seçildiyse email konusu ve gövdesi) onayda kontrol edilmez. Hiç kanal seçilmezse onay reddedilir.

## D-CART: Örnek brief'in yorumu

- **Karar:** "Son 30 günde sepete ekleyip alışveriş yapmayan" ifadesi iki bağımsız koşul olarak yorumlanır: `add_to_cart` son 30 günde var **ve** `purchase` son 30 günde yok.
- **Gerekçe:** DNF yapısıyla doğrudan ifade edilir; sıralı olay desteği gerektirmez.
- **Alternatifler:** Sıralı yorum ("sepete ekledikten sonra satın alma yok"): iş anlamına daha yakın ama şemaya sıralı olay tipi ve daha karmaşık bir sorgu ekler.
- **Sınırlar:** Pencere içinde önce satın alıp sonra sepete ekleyen kullanıcı kitle dışında kalır. Seed'e bu durum için bir kohort konur ve testte doğrulanır.
- **Güncelleme (2026-11-09, PLAN adım 14b, kullanıcı kararı): Sıralı yoruma geçildi.** "Sepete ekleyip alışveriş yapmayan" artık `add_to_cart✓30g` ve sonrasında `purchase` yok olarak yorumlanır (`then`). Aynı kural genel olarak "X yapıp Y yapmayan / X'ten sonra Y yapan" kalıbına uygulanır; iki olaya ayrı pencere verilmişse koşullar bağımsız yazılır.
  - **Gerekçe:** Sepet hatırlatmasının iş anlamı budur; önceki yorumun dışarıda bıraktığı "önce alıp sonra sepete ekleyen" kullanıcılar da hatırlatılması gereken kişilerdir. Sıralı olay desteği adım 14b'de eklendiği için alternatifteki maliyet ortadan kalktı.
  - **Etkisi:** Eval seed'inde case brief kitlesi 146 → 173. Eski yorum testlerde ayrı bir kural olarak korunur ve iki yorumun farkı kohortlarla doğrulanır.

## D-LLM-OUT: Claude'dan yapılandırılmış çıktı

- **Karar:** Hem segment çıkarımı hem içerik üretimi için `output_config.format` (JSON schema) kullanılır. Şema elle yazılır ve koda gömülür; struct ile uyumu bir testle korunur. Go SDK'nın stabil `OutputConfig` / `JSONOutputFormatParam` yolu kullanılır.
- **Gerekçe:** API, constrained decoding ile yanıtın şemaya uymasını "çoğu durumda" garanti eder. Sonnet 5.5 zorunlu `tool_choice` (`any` / `tool`) için 400 döndürür; tool zorlama yöntemi bu modelde kullanılamaz. `auto` ise çağrıyı garanti etmez. Kaynaklar: platform.claude.com, `build-with-claude/structured-outputs` ve `agents-and-tools/tool-use/define-tools`.
- **Alternatifler:** Strict tool + `tool_choice: auto`: çağrı garanti değil. Yalnızca prompt ile "JSON döndür": garanti yok; prefill de bu modelde 400 döner.
- **Sınırlar:**
  - Garantinin üç istisnası var: `stop_reason: refusal`, `stop_reason: max_tokens` ve enum değerlerinin büyük/küçük harfi.
  - Şema `minimum/maximum` ve `minLength/maxLength` kısıtlarını desteklemez; 160 karakter (R-CNT-1), 80 karakter (R-CNT-2) ve sayısal aralıklar backend'de kontrol edilir.
  - Yeni bir şemanın ilk kullanımında derleme gecikmesi olur; derlenen şema 24 saat cache'lenir.
- **Güncelleme (2026-11-06, PLAN adım 1 doğrulaması):** `claude-sonnet-5-5` ile 2 gerçek çağrı yapıldı. Deneme kodu repoya alınmadı.
  - **SDK yolu:** `github.com/anthropics/anthropic-sdk-go/v2` v2.7.0. Stabil `client.Messages.NewStreaming` + `OutputConfigParam{Effort, Format: JSONOutputFormatParam{Schema: map[string]any}}` çalışıyor; beta namespace'i veya header'ı gerekmiyor.
  - **Şema:** `$defs` + `$ref`, `anyOf` (attribute/event koşulu), `["integer","null"]`, enum'lar ve `additionalProperties: false` içeren iki seviyeli şema 400 almadan kabul edildi.
  - **Event sırası:** `message_start` → `content_block_start(text)` → text delta'ları → `content_block_stop` → `message_delta` → `message_stop`. `effort: low` ile thinking bloğu gelmedi (adaptive thinking düşünmemeyi seçti). Kod yine de thinking bloklarını atlayabilmeli.
  - **Birleştirme:** Delta'ların art arda eklenmesi `message.Accumulate` sonucuyla birebir aynı. SDK delta'ları geçerli UTF-8 string olarak veriyor (30 delta, 6'sı Türkçe karakterli, 0 geçersiz). Artımlı JSON çıkarıcı için asıl risk byte bölünmesi değil, JSON metni olarak gelen bir `\uXXXX` kaçışının ya da `\"` dizisinin iki delta'ya bölünmesi. Bu durum D-LLM-MOCK testlerine eklenir.
  - **Doğrulama:** Case brief'inin çıktısı `DisallowUnknownFields` ile tipli struct'a hatasız çevrildi. Sonuç D-CART ile birebir aynı: `add_to_cart✓30g`, `purchase✗30g`, `city∈[İstanbul]`. Ton ve indirim `unsupported`'a değil `notes`'a yazıldı.
  - **`max_tokens`:** `max_tokens=40` ile `stop_reason: max_tokens` döndü ve yarım JSON geçersizdi (`json.Valid=false`). D-LLM-RETRY'daki "parse etme" kuralı doğrulandı.
  - **Token:** Input 1635 token (kısa system prompt + brief; büyük kısmı şemanın getirdiği ek yük), output 185 token.
  - **Gecikme:** İlk event iki çağrıda da yaklaşık 7–8,5 sn sonra geldi. `message_start` ilk text delta ile aynı anda geliyor. Aynı şemayla yapılan ikinci çağrı daha hızlı değildi, yani şema cache'inin etkisi bu örneklemde görülmedi. Sonuç: Backend `started` olayını Claude'un ilk event'ini beklemeden kendisi gönderir, arayüz bu sürede "hazırlanıyor" durumu gösterir (D-STREAM).
  - **Refusal:** Tetiklenmedi (bilerek denenmedi). `StopReasonRefusal` sabiti ve `StopDetails{Category, Explanation}` alanları derleme ile doğrulandı. Davranış fake ile test edilecek.
  - **Hata:** `*anthropic.Error` mesajı metot, URL, status ve request-id içeriyor; API key içermiyor. Ayrıntı `RawJSON()` ile alınır.
- **Güncelleme (2026-11-07, PLAN adım 5):**
  - **Şema:** Koşul `anyOf` içinde iki değil üç varyantla yazıldı: şehir (`values`), yaş (`value`) ve olay. Tip ve alan `const` ile sabitlendi. Böylece "şehir için `value`" gibi karışıklıkları şema baştan engeller ve `null` alanlara gerek kalmaz. Şema `internal/llm/segment_schema.json` dosyasında durur, `go:embed` ile binary'ye gömülür. Bir test, şemadaki alan adlarının ve enum değerlerinin `segment.Rule` ve Go sabitleriyle aynı kaldığını kontrol eder. Bu şekil gerçek API'de henüz denenmedi; ilk kontrolü adım 5b'deki `make test-live` yapar.
  - **Kimlik bilgisi:** Adaptör SDK'yı `option.WithoutEnvironmentDefaults()` ile kurar. Key ve base URL yalnızca `platform.Config`'ten gelir, SDK ortam değişkenlerini veya diskteki profilleri kendisi okumaz. SDK v2.7.0 key'i `Authorization: Bearer` başlığıyla gönderir (`x-api-key` ile değil).
  - **Base URL:** `ANTHROPIC_BASE_URL` config'e eklendi (`AnthropicBaseURL`). Boş değer production API demektir.
- **Güncelleme (2026-11-07, PLAN adım 5b, `make test-live`):** 2 gerçek çağrı yapıldı.
  - Üç varyantlı koşul şeması 400 almadan kabul edildi. Case brief'i D-CART kuralına çevrildi ve sıkı decode ile doğrulamadan uyarısız geçti.
  - Input 2742 token oldu (adım 1'de 1635). Artışın sebebi daha uzun system prompt. Output 201 token. İlk delta 2,9 sn sonra geldi (adım 1'de 7–8,5 sn); örneklem küçük olduğu için gecikme beklentisi değiştirilmedi.
  - **Alan sırası:** Model JSON anahtarlarını şemadaki sırayla değil, alfabetik sırayla üretti (`assumptions`, `audience_scope`, `common`, …). Adım 6'daki artımlı JSON çıkarıcı alan sırasına güvenmemeli; her alanı adına göre tanımalı.
  - `max_tokens=40` ile `max_tokens` döndü ve yarım metin geçersiz JSON'du. Adaptör bunu hata değil stop reason olarak döndürüyor; parse etmeme kararı `generate` içinde uygulanıyor.
- **Güncelleme (2026-11-09, PLAN adım 14b): Zorunlu ama `null` olabilen alanlar.** Olay koşulunun `within_days`, `from`, `to`, `min_count` ve `then` alanları şemada zorunludur ve `null` alabilir; isteğe bağlı değildir.
  - **Gerekçe:** API zorunlu alanları şemadaki sırayla, isteğe bağlı alanları onlardan sonra yazdırır. İsteğe bağlı sürümle yapılan eval'de model bir kez `then`'i unuttu (kitle 22 kişi genişledi) ve iki kez "son N gün"ü takvim tarihine çevirdi. Zorunlu alanlarla model her koşulda pencereye, sayıya ve sıraya açıkça karar verir; `within_days` önce gelir. Değişiklik sonrası tam eval 75/75.
  - **Sınırlar:** Structured output limitleri dokümantasyondan doğrulandı: istek başına en fazla 24 isteğe bağlı alan ve 16 union (`anyOf` veya tip dizisi) alanı. Şema bu sınırların altında; yeni nullable alanlar eklenirken union sayısı kontrol edilmeli.
  - **Bugünün tarihi:** "Ekim'de", "geçen ay" gibi ifadeler için bugünün tarihi (Türkiye saati) kullanıcı mesajına yazılır. Sistem prompt'u sabit kalır ve önbelleğe alınabilir. Eval, seed'in sabit "bugün"ünü geçer.

## D-LLM-RETRY: LLM hata ve onarım stratejisi

- **Karar:**
  - 429, 5xx ve bağlantı hatalarında SDK'nın kendi retry'ı kullanılır; üstüne ikinci bir döngü yazılmaz.
  - 400 ve 401 hataları tekrar denenmez.
  - `max_tokens` ile kesilen çıktı doğrulamaya sokulmaz, daha yüksek bir limitle bir kez tekrar denenir.
  - `refusal` parse edilmez, kullanıcıya gösterilir.
  - Doğrulama hatasında en fazla 2 onarım denemesi yapılır: önceki çıktı ve hata listesi yeni, tek turluk bir istekte gönderilir. Onarım da başarısız olursa taslak geçersiz kalır ve onay kapalıdır.
  - Stream ortasında kopma olursa deneme baştan başlar ve arayüz `reset` olayıyla temizlenir.
- **Gerekçe:** Transport hataları, model hataları ve iş kuralı hataları farklı tepki gerektirir. Tek turluk onarım isteği, önceki konuşmayı yeniden göndermeyi ve thinking bloklarının yönetimini gerektirmez.
- **Alternatifler:** Sınırsız onarım: maliyet ve gecikme. Onarım hiç yapmamak: kullanıcıyı gereksiz yere hataya bırakır.
- **Sınırlar:** Her denemenin token kullanımı ve hata nedeni loglanır ve metrik olarak sayılır (R-OBS-2).
- **Güncelleme (2026-11-07, PLAN adım 5):** Sayılar ve ayrıntılar kesinleşti (`internal/llm/segment.go`):
  - **Stream kopması:** En fazla 1 kez baştan başlatılır. İlk event'ten sonraki her hata (bağlantı kopması, stream içinde gelen `error` olayı, `message_delta` gelmeden kapanan bağlantı) "kopma" sayılır. İlk event'ten önceki API hataları `*llm.APIError` olarak döner ve tekrar denenmez, çünkü SDK 429/5xx için zaten denemiştir.
  - **`max_tokens`:** Segment çıkarımı 8000 ile başlar, kesilirse 32000 ile bir kez tekrar dener. Yine kesilirse `ErrTruncated` döner. Metin tam bir JSON gibi görünse bile parse edilmez.
  - **Onarımı tetikleyen hatalar:** Sıkı decode hatası (bilinmeyen alan, yanlış tip, JSON dışı metin; kod `invalid_json`) ve `segment.Validate` hataları. Anlamı değiştirmeyen düzeltmeler (enum harfi, şehir yazımı, tek grup) uyarıdır ve onarıma gitmez.
  - **Brief sınırı:** En fazla 2000 karakter (rune ile sayılır). Boş veya daha uzun brief API çağrısı yapılmadan `ErrInvalidBrief` ile reddedilir.
  - **Kayıt:** Her çağrı `Attempt` olarak (onarım sırası, `stop_reason`, token, hata) sonuçla birlikte döner. Loglama ve metrik işi çağıran tarafta yapılır (adım 7 ve 10).

## D-MODEL: Model ve çağrı ayarları

- **Karar:** Varsayılan model `claude-sonnet-5-5`, `LLM_MODEL` env değişkeniyle değiştirilebilir. `effort` her çağrıda açıkça verilir (`low` veya `medium` ile başlanır). `thinking` parametresi gönderilmez; bu modelde `disabled` değeri 400 döner. `temperature` ve diğer sampling parametreleri gönderilmez. `max_tokens` thinking payını kapsayacak kadar yüksek tutulur. Refusal için `fallbacks: "default"` (beta) planlanır; desteği PLAN adım 1'de doğrulanır.
- **Gerekçe:** Bu iş kısa yapılandırılmış çıkarım ve kısa metin üretimi; Sonnet 5.5 bunun için maliyet ve kalite dengesi sunar. Seçim kullanıcının kararıdır.
- **Alternatifler:** `claude-opus-5-5`: daha yetenekli ama iki kat pahalı. `claude-haiku-5-5`: çok ucuz ama daha fazla onarım denemesi riski var.
- **Sınırlar:** Kalite `make eval` ile ölçülür. Model değişirse eval tekrar çalıştırılır.
- **Güncelleme (2026-11-06):** `fallbacks: "default"` kullanılmayacak (kullanıcı kararı). Refusal nadir bir durum; olduğunda D-LLM-RETRY'daki gibi parse edilmeden kullanıcıya gösterilir. Böylece beta API yüzeyine bağımlılık kalmaz ve stabil `client.Messages` yolu yeterli olur. `thinking` gönderilmeden `effort: low` ile segment çıkarımı doğru sonuç verdi (bkz. D-LLM-OUT güncellemesi).
- **Güncelleme (2026-11-07):** `fallbacks` kararı yeniden değerlendirildi ve aynen korundu (kullanıcı kararı). Ek gerekçe: Fallback, kuralı başka bir modele ürettirir. O model karmaşık JSON şemasında aynı başarıyı göstermeyebilir ve eval yalnızca seçili model için yapılır.

## D-LLM-MOCK: Test edilebilir LLM katmanı

- **Karar:**
  - `llm.Client` interface'inin tek bir düşük seviyeli metodu var: isteği alır, her stream parçası için bir callback çağırır, sonunda metin, `stop_reason` ve token kullanımını döndürür.
  - Testlerde sırayla yanıt veren ve gelen istekleri kaydeden bir fake kullanılır.
  - Anthropic adaptörü `ANTHROPIC_BASE_URL` ile yerel bir sahte SSE sunucusuna yönlendirilerek birkaç kontrat testiyle doğrulanır.
  - Çalışma zamanında `LLM_PROVIDER=mock` ile key olmadan demo yapılabilir. Mock kullanılıyorsa arayüzde bir rozet görünür.
- **Gerekçe:** R-AI-2'nin gereği. Hata senaryoları (geçersiz JSON, `refusal`, `max_tokens`, stream ortasında kesilme, çok baytlı karakterin ortasından bölünen chunk) deterministik olarak kurulabilir.
- **Alternatifler:** Kayıt/tekrar (cassette): kayıt için key gerekir ve kayıtlar eskir. Compose içinde ayrı bir mock LLM servisi: testler için fazla ağır.
- **Sınırlar:** Gerçek model davranışı yalnızca `make eval` ile ölçülür; `make test` gerçek API'ye hiç bağımlı değildir.
- **Güncelleme (2026-11-06):** Kontrat testlerinin gerçek API ile çapraz kontrolü için `make test-live` eklendi (bkz. D-TEST güncellemesi). `make test` yine gerçek API'ye bağımlı değildir.

## D-STREAM: Frontend'e akış

- **Karar:** İki akış için de SSE kullanılır. Akış `POST` ile başlatılır ve bir id döner; olaylar `GET .../events` ile dinlenir.
  - **İçerik akışı:** Backend'de artımlı bir JSON string çıkarıcı, Claude'dan gelen ham JSON parçalarından `sms.delta`, `email.subject.delta` ve `email.body.delta` olaylarını üretir. Olay sırası: `started` → `delta`'lar → `validated` veya `invalid`. Onarım denemesinde `reset` gönderilir. Onay düğmesi yalnızca `validated` olayından sonra açılır.
  - **Dashboard (R-OBS-1):** Mesaj başına olay yerine saniyede bir DB'den alınan özet gönderilir.
- **Gerekçe:** Akış tek yönlü ve SSE düz HTTP üzerinde çalışır. Akan metin yalnızca bir aday olarak gösterilir; doğrulanan ve gösterilen metin aynıdır. Özet gönderen bir dashboard yeniden bağlanmada kendiliğinden doğru duruma gelir, ayrı bir pub/sub gerekmez.
- **Alternatifler:** WebSocket: çift yönlü iletişime burada gerek yok. Polling: token akışı için kötü. Kısmi JSON'u frontend'de parse etmek: mantık tarayıcıya kayar ve testi zorlaşır. Düz metin akışı + ikinci bir JSON çağrısı: gösterilen ve doğrulanan içerik farklı olabilir.
- **Sınırlar:** Üretim olayları bellekte tutulur ve tek API replikası varsayılır. Go'da `http.Flusher` kullanılır, sunucunun `WriteTimeout` değeri akışı kesmeyecek şekilde ayarlanır ve nginx'te buffering kapatılır.
- **Güncelleme (2026-11-07, PLAN adım 6):**
  - **Endpoint'ler:** `POST /api/content/generations` (`{brief, rule}` → `202 {id, events_url}`) ve `GET /api/content/generations/{id}/events`. Kural istemciden geldiği için sunucuda yeniden doğrulanır; onaylanamayan kural 422 alır ve model çağrılmaz.
  - **Olay adları:** `started`, `sms.delta`, `email.subject.delta`, `email.body.delta` (`{"text": …}`), `reset`, `validated` (`{message, stats}`), `invalid` (`{issues}`) ve `failed` (`{code, message}`; refusal, zaman aşımı veya API hatası için). Son üç olaydan biri geldiğinde akış kapanır.
  - **Yeniden bağlanma:** Her olayın sıra numarası SSE `id` alanında gönderilir. Tarayıcının `EventSource`'u bağlantı koptuğunda `Last-Event-ID` başlığını otomatik gönderir; sunucu o noktadan devam eder. Üretim isteği yapan bağlantıdan bağımsız çalışır ve kendi zaman sınırı vardır (3 dk). İstemcinin kopması üretimi iptal etmez. Biten üretimler 30 dk sonra bellekten silinir.
  - **Delta sırası:** Delta'lar alanların modelin yazdığı sırayla gelir (gözlemlenen sıra alfabetik). Arayüz her olayı alan adına göre ilgili kutuya yazar, sıraya güvenmez.
  - **Akış ayarları:** `http.ResponseController` ile her olay hemen flush edilir ve bu yanıt için yazma süre sınırı kaldırılır. 15 sn'de bir `: ping` yorumu gönderilir; `X-Accel-Buffering: no` başlığı nginx tamponlamasını kapatır.
  - **Log:** Her üretim sonunda sonuç, onarım sayısı, deneme sayısı, token ve süre loglanır. Brief ve üretilen metin loglanmaz.
  - **Mock rozeti:** `GET /api/info` hangi LLM sağlayıcısının (`mock` veya `anthropic`) kullanıldığını döner; arayüz buna göre rozet gösterir (D-LLM-MOCK).
- **Güncelleme (2026-11-07, PLAN adım 7):** İçerik üretimi kampanyaya bağlandı: `POST /api/campaigns/{id}/content/generations`. Brief ve kural istemciden değil, kayıtlı taslaktan okunur. Olay akışı (`GET /api/content/generations/{id}/events`) değişmedi. `POST /api/content/generations` ve `POST /api/content/validate` kaldırıldı; içerik doğrulaması artık `PUT /api/campaigns/{id}/content` yanıtında döner.
- **Güncelleme (2026-11-07, PLAN adım 10):**
  - **Dashboard akışı:** `GET /api/campaigns/{id}/events`. Saniyede bir `progress` olayı gönderilir. İçeriği: kampanya durumu, kanal başına `total/pending/sending/sent/failed/skipped`, son 10 sn'deki gönderim hızı (tüm kanalların toplamı), en sık 5 `last_error` (gönderilmemiş satırlardan; atlanma nedeni dahil) ve `done`.
  - **Yeniden bağlanma:** Her olay tam durumu taşır; yeniden bağlanan istemci bir sonraki olayla doğru duruma gelir, replay gerekmez.
  - **Kapanış:** Kampanya `completed` olduğunda son olay `done: true` ile gönderilir ve akış kapanır.
  - **Hata durumları:** Bilinmeyen id için akış başlamadan JSON `404` döner. DB okuması başarısız olursa son durum korunur ve akış devam eder.
- **Güncelleme (2026-11-07, PLAN adım 11):** Kampanya yanıtına (`GET`, `PUT /rule`, `PUT /content`, onay) `content_validation` alanı eklendi: kayıtlı mesajın, kuralın güncel kitlesine göre doğrulaması (`valid`, `errors`, `stats`). Onaylanmış veya mesajı olmayan taslakta `null`. Böylece sayfa yenilenince ya da kural değişince arayüz eski bir doğrulama sonucunu göstermez. Hesap `PUT /content` ile aynıdır (`campaign.Service.ContentValidation`).

## D-CONTENT: İçerik kuralları

- **Karar:**
  - Karakter sayımı byte ile değil rune ile yapılır (`utf8.RuneCountInString`).
  - Kişiselleştirme değişkenleri bir beyaz listeyle sınırlıdır (`first_name`, `city`) ve boş değer için bir fallback tanımlanır; bilinmeyen değişken reddedilir (R-CNT-3).
  - 160 ve 80 karakter limitleri (R-CNT-1, R-CNT-2) render sonrası en kötü durum için doğrulanır: değişkenler kitledeki en uzun değerle doldurulur.
  - Email gövdesi düz metindir.
  - Pazarlamacının elle yaptığı düzenleme de aynı doğrulamadan geçer.
- **Gerekçe:** Go'da `len()` byte sayar; Türkçe karakterlerde bu yanlış sonuç verir. Kısa bir `{{first_name}}` şablonu render sonrası limiti aşabilir. Düz metin email XSS riski taşımaz.
- **Alternatifler:** GSM-7/UCS-2 segment hesabı: gerçek SMS maliyetini yansıtır, ama case "karakter" diyor.
- **Sınırlar:** Türkçe karakter içeren gerçek bir SMS'te tek segment limiti 70 karakterdir; bu fark not edilir, uygulanmaz.
- **Güncelleme (2026-11-07, PLAN adım 6):**
  - **Yedek değer:** Boş `first_name` yerine "Değerli Müşterimiz" yazılır (kullanıcı onayı). En kötü durum hesabında bir değişken, en uzun değer ile yedek metinden hangisi uzunsa o kadar yer kaplar.
  - **En kötü durumun kapsamı:** Şimdilik en uzun isim ve şehir bütün `users` tablosundan alınır (`campaign.LongestValues`). Kitle bu tablonun alt kümesi olduğu için sonuç hiçbir zaman olduğundan düşük çıkmaz, ama bazen gereğinden katıdır. Kitleye özel hesap, kampanya kaydıyla birlikte adım 7'de eklenir (kullanıcı onayı).
  - **Kişisel veri:** Claude'a en uzun değerlerin kendisi değil, yalnızca uzunlukları gönderilir. Doğrulama sonucu JSON'a çevrildiğinde de bu değerler çıktıya girmez (`json:"-"`), arayüz yalnızca karakter sayılarını görür.
  - **Ek kurallar:** Email konusu tek satır olmalıdır. Satır sonu ve sekme dışındaki kontrol karakterleri reddedilir. Alanların başındaki ve sonundaki boşluklar silinir ve sayılmaz. Email gövdesine 2000 karakter üst sınırı konuldu (case'te limit yok; kontrolden çıkmış bir üretimi durdurmak için).
  - **Değişken yazımı:** `{{ first_name }}` gibi süslü parantez içindeki boşluklara izin verilir. Kapatılmamış `{{`, açılışı olmayan `}}` ve büyük harfli ad (`{{FIRST_NAME}}`) hatadır. Tek süslü parantez (`{SEPET10}`) düz metindir.
- **Güncelleme (2026-11-07, PLAN adım 7):** En kötü durum artık kampanyanın kitlesine göre hesaplanır:
  - Taslakta içerik kaydedilirken ve üretilirken: kuralın SMS veya email ile ulaştığı kullanıcılar. Kanal onayda seçildiği için iki kanal birlikte sayılır. Kitle tanımlamayan bir kuralda yine tüm kullanıcılar kullanılır.
  - Onayda: yazılan snapshot'taki kullanıcılar, yani mesajı gerçekten alacak kişiler.
  - Örnek: Seed'deki 31 karakterlik isim İstanbul'da yaşıyor ama case kitlesinde değil. 130 karakterlik metin + `{{first_name}}` içeren bir SMS, İstanbul kitlesi için 161 karakter olduğundan reddedilir; case kitlesi için onaylanır.
- **Güncelleme (2026-11-10): SMS sınırı 160 karakter olarak kalır (kullanıcı kararı).**
  - **Bilinen fark:** Gerçek operatörlerde SMS, standart GSM-7 alfabesindeyse 160 karakter (daha uzunsa 153'lük parçalar), alfabe dışında tek bir karakter (ı, ş, ğ, İ, Ş, Ğ) bile varsa 70 karakterdir (daha uzunsa 67'lik parçalar). 160 karakterlik Türkçe bir SMS gerçekte 3 parçaya bölünür ve 3 SMS olarak ücretlendirilir.
  - **Neden 70 değil:** 70'e düşürmek değerlendirildi ve elendi. Case sınırı açıkça 160 karakter olarak koyar (R-CNT-1). 70 karakter pazarlama metni için fazla kısıtlayıcıdır ("Merhaba Ayşe, sepetindeki ürünler seni bekliyor! %30 indirim: SEPET30" bu sınıra ancak sığar). Dinamik bir kural (GSM-7 ise 160, değilse 70) pratikte hep 70 demektir: `{{first_name}}` için en kötü durum değeri Türkçe karakter içerir.
  - **Gerçek sistemde ne yapılırdı:** Sınır karakterle değil parça sayısıyla konur (ör. "en fazla 2 SMS"). Karakter seti, değişkenler en kötü durum değerleriyle doldurularak hesaplanır ve pazarlamacıya onaydan önce tahmini maliyet (kitle × parça sayısı) gösterilir.
  - Arayüzdeki sayaç sade kalır (`117 / 160`); parça sayısı gösterilmez (kullanıcı kararı, bkz. D-FE adım 15c).

## D-DEDUP: "Aynı kişiye aynı mesaj iki kez gitmez" (R-SEND-3)

- **Karar:** "Aynı mesaj" kampanya + kişi + kanal olarak tanımlanır. Dört katmanlı savunma:
  1. **Tek onay:** Onay `UPDATE ... WHERE status='draft'` ile atomik yapılır; çift tıklama kampanyayı ikinci kez kuyruğa yazmaz.
  2. **Unique kısıt:** `UNIQUE(campaign_id, user_id, channel)`. Kitle snapshot'ı onay anında aynı transaction'da yazılır.
  3. **Lease:** Claim edilen satıra `locked_until` yazılır; worker düşerse süre dolunca satır tekrar alınır.
  4. **Idempotency key:** Provider'a `Idempotency-Key: <delivery_id>` gönderilir. Mock provider anahtarı saklar ve tekrarlanan istekte eski sonucu döner.
  - Gönderimden hemen önce izin tekrar kontrol edilir; onaydan sonra izin geri çekildiyse satır `skipped` olur (R-AUD-2). Seed'de telefon ve email alanları da unique'tir.
- **Gerekçe:** Ağ üzerinden "tam olarak bir kez" tek başına mümkün değildir. Provider isteği kabul edip biz kaydedemeden düşersek durum belirsiz kalır. Garanti, en az bir kez denemekle alıcı taraftaki idempotency'nin birleşiminden gelir.
- **Alternatifler:**
  - Kampanyalar arası içerik hash'i: meşru tekrar kampanyalarını engeller.
  - At-most-once ("gönderildi" kaydını göndermeden önce yazmak): mesaj kaybına yol açar.
- **Sınırlar:**
  - Aynı içerikle yeni bir kampanya açılırsa mesaj tekrar gönderilir.
  - Gerçek bir provider idempotency desteklemiyorsa garanti zayıflar.
  - Doğrulama mock provider'ın kayıtlarıyla yapılır: worker gönderim ortasında öldürülüp yeniden başlatılır ve her anahtar için tek teslimat olduğu kontrol edilir.
- **Güncelleme (2026-11-07, PLAN adım 7):** Katman 1 ve 2 uygulandı (`campaign.Approve`).
  - Onay tek bir transaction'dır: önce `UPDATE campaigns ... WHERE id=$1 AND status='draft' RETURNING ...` satırı kilitler; ardından kontroller, kanal başına `INSERT INTO deliveries ... SELECT` (kitle SQL'i `segment.CompileAudience`'tan gelir, izin filtresi dahil) ve snapshot üzerinde içerik kontrolü yapılır. Herhangi bir kontrol başarısız olursa her şey geri alınır.
  - Eşzamanlı ikinci onay, kilidin açılmasını bekler, koşul artık tutmadığı için 0 satır günceller ve `409 not_draft` alır.
  - Test: aynı taslak 8 paralel istekle onaylandı; tam olarak biri başarılı oldu, snapshot bir kez yazıldı. `status='draft'` koşulu bilerek kaldırılınca test kırıldı; bu durumda bile ikinci snapshot'ı unique kısıt engelledi.
- **Güncelleme (2026-11-07, PLAN adım 9):** Katman 3 ve 4 uygulandı (`internal/delivery`).
  - Idempotency key delivery id'nin kendisidir.
  - **Fence:** Sonuç yalnızca satır hâlâ `sending` ise ve `attempts` claim'deki değerle aynıysa yazılır. Lease'i dolmuş yavaş bir worker, satırı yeniden almış başka bir worker'ın sonucunu ezemez.
  - **Sonuç yazılamazsa:** Gönderim yapılmış ama DB'ye yazılamamışsa satır `sending` kalır. Lease dolunca aynı anahtarla tekrar gönderilir ve provider ilk yanıtı döner.
  - **Test (katman 4):** Worker, bir gönderimden hemen sonra ve sonucu yazmadan önce "çöktürülür" (süreç içi simülasyon: sonraki tüm DB yazımları başarısız olur). Bu sırada 10 mesaj teslim edilmiş ama kaydedilmemiştir. İkinci worker kuyruğu bitirir. Mock'taki teslim sayısı DB'deki `sent` sayısına eşittir. Anahtar her denemede değiştirilince test kırıldı.
  - **Elle doğrulama:** Gerçek süreçlerle `kill -9` da denendi.

## D-RETRY: Gönderim retry stratejisi (R-SEND-4)

- **Karar:** Retry durumu bellekte bekleyerek değil, DB'de `next_attempt_at`, `attempts` ve `last_error` alanlarıyla tutulur.

  | Sonuç | Davranış |
  |---|---|
  | 2xx | `sent` |
  | 429 | `Retry-After` beklenir; deneme hakkı ayrı bir üst sınırla sayılır |
  | 5xx / timeout / bağlantı hatası | Üstel backoff + full jitter (başlangıç 2 sn, üst sınır 5 dk), en fazla 5 deneme |
  | Diğer 4xx | Kalıcı `failed`, tekrar denenmez |
  | Deneme hakkı bitti | `failed`; son hata dashboard'da görünür |
- **Gerekçe:** Süreç düşse de retry durumu korunur. Jitter, aynı anda başarısız olan mesajların aynı anda geri dönüp yeni bir yığılma yaratmasını önler.
- **Alternatifler:** Bellekte `Sleep` ile beklemek: süreç düşünce durum kaybolur. Broker'ın dead-letter queue'su: D-DB kararıyla elendi.
- **Sınırlar:** Manuel "tekrar gönder" butonu çekirdek kapsamda yok (adım 15e'de eklendi, aşağıdaki güncelleme).
- **Güncelleme (2026-11-07, PLAN adım 9):**
  - **429 sayacı:** `deliveries.throttles` kolonu eklendi (migration 00002). 429 bir deneme hakkı harcamaz; en fazla 20 kez kabul edilir. Retry-After en fazla 5 dk kabul edilir.
  - **Deneme sayımı:** Claim bir deneme sayılır. Böylece çökmeyle yarıda kalan bir deneme de sayıya girer.
  - **Retry edilen ek kodlar:** `408` ve `409` da retry edilir. `409`, provider'ın "bu anahtarla bir istek hâlâ işleniyor" yanıtıdır; genelde çökmeden kalan kendi isteğimizdir ve sonucu sonraki denemede idempotency ile alınır.
  - **Kapanış:** Düzgün kapanışta, alınmış ama henüz gönderilmemiş satırlar deneme hakkı harcamadan hemen `pending`'e döner. Başlamış istekler tamamlanır ve sonuçları yazılır.
  - **`last_error`:** Yalnızca durum kodu veya hata sınıfı (`http 503`, `timeout`) içerir; kişisel veri içermez.
- **Güncelleme (2026-11-10, PLAN adım 15e):** Manuel "başarısızları tekrar gönder" eklendi (kullanıcı kararı). Varsayılan hata oranlarıyla 5 denemenin hepsinin başarısız olması çok nadirdir; ama mock provider'ın hata oranı yükseltilerek test edildiğinde `failed` mesajlar oluşur ve pazarlamacının bunları yeniden deneyebilmesi gerekir.
  - **Karar:** `POST /api/campaigns/{id}/retry-failed` yalnızca `completed` bir kampanyada çalışır. Tek transaction'da kampanya `completed → sending` olur ve yalnızca `failed` satırlar `pending`'e döner. Deneme ve 429 sayaçları sıfırlanır, `last_error` silinir. `sent` ve `skipped` satırlara dokunulmaz.
  - **Aynı satır, aynı anahtar (R-SEND-3):** Satırlar kopyalanmaz, yeniden kuyruğa alınır. Delivery id ve dolayısıyla `Idempotency-Key` değişmez. Provider'ın "kabul edip 500 döndüğü" bir mesaj bizde `failed` görünse de provider'da teslim edilmiştir; tekrar denemede provider saklı başarıyı döner ve ikinci mesaj gitmez. İzin, worker satırı aldığında yeniden kontrol edilir.
  - **Eşzamanlılık:** Durum geçişi `UPDATE ... WHERE status = 'completed'` ile yapılır; çift tıklamada yalnızca biri satırları kuyruğa alır, diğeri `409 not_completed` alır. Başarısız satır yoksa transaction geri alınır ve `409 nothing_to_retry` döner.
  - **Worker ile yarış:** Worker'ın `FinishCampaigns` ifadesi, retry commit edilmeden önce başlamışsa satırları eski hâlleriyle (hepsi bitmiş) okuyup kampanyayı yeniden `completed` yapabilir. Buna karşı worker'ın claim'i `completed` bir kampanyayı da `sending`'e alır: kuyruğa alınan satırlar alındığında durum kendiliğinden düzelir. Durum makinesine `completed → sending` kenarı eklendi.
  - **Alternatifler:** Yeni satırlar açmak (yeni anahtarla): provider'da teslim edilmiş mesajları ikinci kez gönderir. Kalıcı 4xx hatalarını hariç tutmak: mock bunları üretmiyor ve tekrar denemede yine hızlıca `failed` olurlar; ayrım eklenmedi.
  - **Test:** Postgres'te yalnızca `failed` satırların aynı id ile döndüğü, eşzamanlı 8 istekte tek kazanan, reddedilme durumları ve worker yarışı. Uçtan uca testte tek denemeli bir worker yüksek hata oranlı mock'a karşı çalışır: 567 satır `failed` olur, bunların 298'i provider'da aslında teslim edilmiştir. Tekrar gönderim sonrası teslim sayısı `sent` sayısına eşittir. Satırlar yeni id'lerle kuyruğa alınınca test 298 fazla mesajla kırıldı; claim'deki düzeltme ve `completed` koşulu kaldırılınca da ilgili testler kırıldı.
  - **Arayüz:** Kampanya tamamlanmış ve başarısız mesaj varsa dashboard'da "N mesaj gönderilemedi" notu ve iki adımlı onaylı "Başarısızları tekrar gönder" düğmesi çıkar. `done` olayıyla kapanmış canlı akış (D-STREAM) yeniden açılır ve yeni gönderim izlenir.
  - **Elle doğrulama:** Compose ortamı `MOCK_ERROR_RATE=0.6` ve `MOCK_ACCEPT_THEN_FAIL_RATE=0.15` ile başlatıldı (LLM mock). 263 mesajlık kampanyada 34 mesaj `failed` oldu. İlk tekrar gönderimden sonra 2 SMS yine başarısız oldu; bunlardan biri son denemede kabul edilip 500 almıştı (provider'da 124 teslim, DB'de 123 `sent`). İkinci tekrar gönderimde bu satır provider'dan saklı başarıyı aldı: sonuçta SMS 125/125, email 138/138 ve provider teslim sayıları `sent` sayılarına eşit.
  - **Sınırlar:** Gönderim sürerken tekrar gönderim yapılamaz; kampanya bitince yapılabilir. Tekrar gönderim her seferinde bütün `failed` satırları kapsar, seçim yapılamaz.

## D-RATE: Kanal başına 100 msg/sn (R-SEND-2)

- **Karar:** Her kanal için ayrı, süreç içi bir token bucket (`golang.org/x/time/rate`) kullanılır; burst değeri 1 veya çok küçüktür. Limiter gerçek `Send` çağrısının önünde durur, retry'lar da limitten geçer. Her kanalın kendi dispatcher'ı ve worker havuzu vardır.
- **Gerekçe:** Burst değeri büyük olursa boşta biriken jetonlarla bir saniyede yaklaşık 200 mesaj gidebilir. Ayrı kanal havuzları sayesinde email'deki bir 429 SMS gönderimini durdurmaz.
- **Alternatifler:** Redis veya DB tabanlı dağıtık limiter: çoklu worker için doğru, ama şu an gereksiz altyapı. Sabit pencere sayacı: pencere sınırında limitin iki katına çıkabilir.
- **Sınırlar:** Tek worker replikası varsayılır. Doğrulama mock provider'ın kayan pencere sayacıyla yapılır: herhangi bir 1 saniyelik pencerede en fazla 100 istek.
- **Güncelleme (2026-11-07, PLAN adım 8):** Mock'un penceresi `(now-1s, now]` aralığıdır ve reddedilen istekler dahil **gelen her isteği** sayar. `/stats` içindeki `max_per_second`, istemcinin herhangi bir 1 saniyelik aralıkta gönderdiği en fazla isteği gösterir; 9. adımdaki test bunun ≤ 100 olduğunu kontrol eder. Sabit saniye sayacının sınırda 2 katına çıkma sorunu testle gösterildi: 0,9 sn'de 3, 1,1 sn'de 3 istek; kayan pencere ikinci grubu reddeder.
- **Güncelleme (2026-11-07, PLAN adım 9):** Worker'ın varsayılan hızı kanal başına **95 msg/sn**'dir (`WORKER_SEND_RATE`).
  - Limiter tam 100'e ayarlanırsa istekler arası süre tam 10 ms olur. Bu durumda ağdaki birkaç ms'lik oynama, mock tarafında 1 saniyelik bir pencereye 101 istek düşmesine yetebilir.
  - %5 pay, gereksinimi (provider tarafında saniyede en fazla 100) her koşulda sağlar.
  - Ölçüm: uçtan uca testte ve elle denemede `max_per_second` 96–97, `rate_limited` 0. Limiter kaldırılınca saniyede 952 istek görüldü ve testler kırıldı.

## D-MOCK: Mock provider (R-MOCK-1, R-MOCK-2)

- **Karar:** Ayrı bir HTTP servisidir. Hata, gecikme ve 429 oranları env ile ayarlanır; RNG seed'i sabitlenebilir. Kendi kayan penceresiyle 100 msg/sn limitini uygular. İsteği kabul edip 500 döndürme modu vardır. Idempotency anahtarlarını saklar ve test için bir istatistik endpoint'i sunar.
- **Gerekçe:** Retry, rate limit ve idempotency davranışları ancak gerçekçi hata türleriyle test edilebilir.
- **Sınırlar:** Veriler bellekte tutulur; servis yeniden başlarsa kayıtlar silinir.
- **Güncelleme (2026-11-07, PLAN adım 8):** `internal/mockprovider` ve `cmd/mockprovider` eklendi.
  - **Endpoint'ler:**
    - `POST /sms` (`{to, text}`) ve `POST /email` (`{to, subject, body}`). `Idempotency-Key` başlığı zorunludur.
    - Yanıtlar: `202 {message_id, status}`, `400`, `409`, `422`, `429` (`Retry-After` ile) ve `500`.
    - Test için `GET /stats` ve `POST /reset`.
  - **İşlem sırası:** gövde ve anahtar kontrolü → kayan pencere limiti → idempotency → rastgele hata → teslim. Limit önce gelir; gerçek bir provider tekrarlanan istekleri de sayar.
  - **Idempotency:**
    - Yalnızca teslim edilen mesajın yanıtı saklanır. `500` veya rastgele `429` sonrası anahtar serbest kalır, retry yeniden işlenir.
    - "Kabul et ama 500 dön" durumunda teslim saklanır; aynı anahtarla gelen retry ilk başarılı yanıtı alır ve ikinci mesaj oluşmaz.
    - Aynı anahtar işlenirken gelen ikinci istek `409` alır.
    - Aynı anahtar farklı bir gövdeyle gelirse `422` döner. Bu bir istemci hatasıdır. Gövde karşılaştırması boşluklardan etkilenmez.
    - Anahtarlar kanal başına ayrıdır.
  - **Hata enjeksiyonu:**
    - Tek bir rastgele sayı bantlara bölünür: `MOCK_ERROR_RATE`, `MOCK_429_RATE`, `MOCK_ACCEPT_THEN_FAIL_RATE`. Oranların toplamı en fazla 1'dir.
    - Gecikme `0..MOCK_DELAY_MAX` aralığındadır. İstemci gecikme sırasında bağlantıyı keserse mesaj gönderilmez ve anahtar serbest kalır.
    - Varsayılanlar: %5 `500`, %3 `429`, %2 kabul et-ama-500, en fazla 50 ms gecikme, seed 42. `/reset` sayaçları sıfırlar ve hata dizisini seed'den yeniden başlatır.
  - **Ayarlar:** `MOCK_*` env değişkenleri `platform.Config`'e değil `mockprovider.Config`'e okunur; yalnızca bu sahte servisi ilgilendirir.
  - **Kişisel veri:** Alıcı ve metin loglanmaz ve `/stats`'a girmez. Logda yalnızca kanal, anahtar (delivery id) ve sonuç bulunur.

## D-SNAPSHOT: Kitlenin hesaplanma zamanı

- **Karar:** Kitle onay anında hesaplanır ve `deliveries` tablosuna snapshot olarak yazılır. Gönderim sırasında yalnızca izin tekrar kontrol edilir.
- **Gerekçe:** Pazarlamacının onayladığı sayı ile gerçekte gönderilen sayı tutarlı kalır. "Son N gün" gibi göreli pencereler onay anına sabitlenir.
- **Alternatifler:** Kitleyi gönderim anında yeniden hesaplamak: onaylanan sayı değişebilir.
- **Sınırlar:** Onaydan sonra kitleye giren yeni kullanıcılar bu kampanyaya dahil edilmez.
- **Güncelleme (2026-11-07, PLAN adım 7):** Snapshot'taki "son N gün" pencereleri onay anına (`approved_at`) sabitlenir. Taslakta gösterilen sayılar kural üzerinden canlı hesaplanır; onaydan sonra `deliveries` tablosundan okunur. Kitle boşsa onay reddedilir (`empty_audience`).
  - **Sınır:** Kural doğrulamasının uyarıları (ör. kapsama uyarısı) yalnızca oluşturma ve kural düzenleme yanıtında döner, saklanmaz.
- **Güncelleme (2026-11-09, PLAN adım 15a, kullanıcı kararı): Zamanlanmış gönderimde de kitle onay anında hesaplanır.** Snapshot onayda yazılır, kuyruk satırları `next_attempt_at = gönderim zamanı` ile bekler. Pazarlamacının onayladığı kişi sayısı ve içerik kontrolü, gönderilenle aynıdır. Alternatif (kitleyi gönderim anında hesaplamak) elendi: sayı ve en kötü durum karakter kontrolü gönderim anında, kimse başında değilken değişebilirdi. İzin geri çekme gönderim anındaki kontrolle yine korunur (D-DEDUP).

## D-SCHEDULE: Zamanlanmış gönderim (Bölüm 9 bonus)

- **Karar (2026-11-09, PLAN adım 15a):**
  - **Mekanizma:** Ayrı bir zamanlayıcı yok. Onay, kuyruğu ileri tarihli yazar (`next_attempt_at`); worker'ın mevcut satır alma sorgusu zamanı gelmemiş satırları zaten almaz. Kampanya `scheduled` olur ve ilk satır alındığında `sending`'e geçer.
  - **Zamanın kaynağı:** Model brief'teki gönderim zamanını `send_at` olarak önerir (saat yoksa 10:00, varsayım olarak yazılır). Bu yalnızca bir öneridir: onay ekranındaki tarih-saat alanına dolu gelir, pazarlamacı değiştirebilir ya da boşaltıp hemen gönderebilir. Son karar D-CHANNEL'daki gibi insanındır. Gönderim zamanı kitle kriteri değildir; status'ü etkilemez ve `unsupported`'a yazılmaz (adım 13'teki "zaman ifadeleri" kararının gönderim zamanı kısmının yerini alır).
  - **Sınırlar:** En erken 5 dakika, en geç 30 gün sonrası. Türkiye saati (UTC+3).
  - **İptal ve yarış önceliği (kullanıcı kararı: öncelik net olmalı):** Kuyruk satırlarını önce kilitleyen kazanır. Worker satırları `FOR UPDATE SKIP LOCKED` ile, iptal `FOR UPDATE NOWAIT` ile kilitler. Worker bir satırı bile almışsa iptal hemen başarısız olur ve geri alınır (`409 sending_started`); gönderim kesilmez. İptal önce kilitlerse worker satırları atlar ve iptal onları siler. Hiçbir taraf beklemediği için deadlock oluşmaz.
- **Gerekçe:** Mevcut kuyruk, retry, rate limit ve idempotency hiç değişmeden zamanlanmış gönderime de uygulanır; yeni bir süreç veya altyapı gerekmez (D-DB). Gönderim başladıktan sonra iptal edilememesi yarım gönderilmiş kampanyaları önler.
- **Alternatifler:** Gönderim anında kitle hesaplayan bir zamanlayıcı döngüsü (yukarıda D-SNAPSHOT güncellemesi), harici zamanlayıcı (pg_cron, iş kuyruğu kütüphaneleri: yeni altyapı), bellekte zamanlayıcı (süreç yeniden başlarsa kaybolur). Zamanı yalnızca pazarlamacının girmesi: brief'te zaten yazılı olanı tekrar girmek gereksiz iş (kullanıcı kararı).
- **Sınırlar:** Tekrarlayan gönderim yok. Gönderim başladıktan sonra durdurma yok. Onaydan sonra kitleye giren kullanıcılar dahil edilmez. Zamanlanmış kampanyanın zamanı onaydan sonra değiştirilemez; iptal edip yeni kampanya açmak gerekir.

## D-CI: CI pipeline (Bölüm 9 bonus)

- **Karar (2026-11-09, PLAN adım 15b):** GitHub Actions (`.github/workflows/ci.yml`), `ubuntu-latest` üzerinde paralel çalışan üç job:
  - `lint`: `make lint`.
  - `test`: `make test` (katman 1–4 ve Vitest). Postgres, yereldeki gibi compose ile başlatılır; ayrı bir CI servis tanımı yoktur.
  - `smoke`: `docker compose up --build --detach --wait web worker`, ardından `make smoke`, en sonunda her durumda `docker compose down -v`. Hata olursa servis logları yazdırılır.
  - **Tetikleme (kullanıcı kararı):** `main`'e her push ve her pull request. Tag ile tetikleme ve versiyonlama yok. Yalnızca doküman değişiklikleri (`**.md`, `ai-sessions/`, `eval/reports/`) CI'ı tetiklemez.
  - **Sürümler tek kaynaktan okunur:** Go sürümü `backend/go.mod`'dan, Node sürümü kökteki `.nvmrc`'den (geliştirme ortamındaki sürüm, `nvm use` ile de kullanılır) alınır. Go modül ve npm cache'i açıktır.
  - Aynı branch'e yeni bir push gelirse devam eden çalıştırma iptal edilir (`concurrency`). Workflow'un repo izni yalnızca okumadır.
  - **Secret yok:** CI'da LLM her zaman mock modda çalışır. `make test-live` ve `make eval` CI'da çalışmaz (D-TEST); ücretli çağrı riski yoktur.
  - **Branch protection yok (kullanıcı kararı):** Şimdilik tek geliştirici doğrudan `main`'e push ediyor. Eklenirse doküman atlama kuralı gözden geçirilmeli, çünkü atlanan bir PR'da zorunlu kontrol beklemede kalır.
- **Gerekçe:** CI "bu commit sağlam mı?" sorusunu sorar. Hata, geldiği commit'te yakalandığında değerlidir; yalnızca tag'de çalışan bir pipeline hatayı birçok commit sonra, kaynağı belirsiz biçimde gösterir. Tag tetiklemesi sürüm yayınlamanın (CD) konusudur ve bu ölçekte bir release tüketicisi yoktur. PR tetiklemesi ekip çalışmasında birleştirme öncesi kontrol sağlar. Smoke job'u, değerlendiren kişinin `docker compose up` deneyimini (D-2) her değişiklikte doğrular.
- **Alternatifler:** Yalnızca tag'de çalışmak (yukarıda). Postgres'i Actions `services:` ile başlatmak: yereldeki `make test`'ten farklı bir yol olurdu. Smoke'u CI'a almamak: daha hızlı, ama imaj ve compose hatalarını yakalamaz.
- **Sınırlar:** Smoke testi zamanlamaya duyarlı kontroller içerir (SSE akışı, saniyede 100). Paylaşımlı runner'da kararsız sonuç verirse yalnızca `main` push'unda çalışacak şekilde daraltılacak.

## D-OBS: Gözlemlenebilirlik (R-OBS-1..3)

- **Karar:** `/metrics` Prometheus formatında yayınlanır (`client_golang`). Health kontrolü iki endpoint'e ayrılır: `/healthz` sürecin ayakta olup olmadığını, `/readyz` DB'ye erişilip erişilemediğini kontrol eder. Hem api hem worker için geçerlidir. Metrikler: gönderim durumları, provider gecikmesi, retry sayısı, LLM denemeleri ve doğrulama hataları, token kullanımı.
- **Gerekçe:** Prometheus formatı yaygın bir standart; liveness ve readiness kontrollerinin ayrı olması da container ortamlarında yaygın bir pratik.
- **Sınırlar:** Grafana panosu kurulmaz; canlı durum için uygulamanın kendi dashboard'u kullanılır.
- **Güncelleme (2026-11-07, PLAN adım 10):**
  - **Endpoint'ler:**
    - API `:8080`, worker `:8081` (`WORKER_ADDR`). İkisinde de `/healthz`, `/readyz` ve `/metrics` var.
    - `/readyz` DB'ye 2 sn süreli ping atar; erişemezse `503` döner. Hata ayrıntısı (host, kullanıcı) gösterilmez.
    - `/healthz` DB'den bağımsızdır. Böylece bir DB kesintisi orkestratörde yeniden başlatma döngüsüne yol açmaz.
  - **Registry:** Her binary'nin kendi registry'si var; Go çalışma zamanı ve süreç metrikleri dahil. Testler kendi temiz registry'lerini kullanır.
  - **Metrikler:**
    - HTTP: `scc_http_requests_total{route,code}`, `scc_http_request_duration_seconds{route}`. `route` ham URL değil mux kalıbıdır (`GET /api/campaigns/{id}`), böylece her kampanya ayrı bir seri açmaz. SSE yanıtlarının süresi ölçülmez.
    - Worker:
      - `scc_delivery_results_total{channel,result}` (sent, retry, throttled, failed, skipped, release).
      - `scc_provider_request_duration_seconds{channel,class}`.
      - `scc_delivery_record_errors_total`, `scc_delivery_lease_lost_total`.
      - `scc_deliveries{channel,status}`: açık satırlar; her scrape'te DB'den okunur.
    - LLM:
      - `scc_llm_requests_total{operation,outcome}`, `scc_llm_repairs_total`.
      - `scc_llm_validation_errors_total{operation,code}`: her denemenin hata kodları sayılır; bunun için `llm.Attempt`'e `IssueCodes` eklendi.
      - `scc_llm_tokens_total{operation,type}`, `scc_llm_operation_duration_seconds`, `scc_segment_rule_status_total{status}`.
  - **Etiketler:** Hiçbir etikette id, isim, telefon, email veya brief metni yoktur; testle doğrulanır.
  - **Log:** İçerik üretim logundaki `outcome` değeri metrik etiketiyle aynı olsun diye `validated` yerine `ok` yapıldı.
  - **Doğrulama:** Gönderim sırasında Postgres durdurulunca `/readyz` iki serviste de `503`, `/healthz` `200` döndü. DB geri gelince worker kendiliğinden devam etti. Kesinti sırasında 53 sonuç yazılamadı (`scc_delivery_record_errors_total`); bunlar lease sonrası aynı anahtarla tekrar gönderildi ve mock'taki teslim sayısı DB ile birebir aynı kaldı.

## D-FE: Frontend

- **Karar:** Vue 3 (Composition API), Vite ve TypeScript. SSE bağlantısı bir composable içinde (`useEventStream`) tutulur. Arayüz ve içerik dili Türkçedir.
- **Gerekçe:** TypeScript, SSE olaylarının ve kural şemasının tiplerini korur.
- **Sınırlar:** Kimlik doğrulama yok; tek kullanıcı varsayılır ve README'de belirtilir.
- **Güncelleme (2026-11-06):** Frontend testleri Vitest ile yazılır (Vite ile aynı yapılandırmayı kullanır) ve `make test`'e dahildir (bkz. D-TEST).
- **Güncelleme (2026-11-07, PLAN adım 11, kullanıcı kararları):**
  - **Yerleşim:** İki sütunlu çalışma alanı: solda kitle, sağda mesajlar; altta sabit bir onay çubuğu. Pazarlamacı onaylarken kitleyi, içeriği ve kanal seçimini aynı anda görür. Dar ekranda tek sütuna düşer. Onaydan sonra aynı URL dashboard'u gösterir.
    - Alternatifler: Adım adım sihirbaz (onay anında kitle görünmez), tek sütunlu uzun akış (onay düğmesinde kitle ekran dışında kalır). Bir HTML maketiyle karşılaştırıldı.
  - **Tema:** Yalnızca soft açık tema; koyu tema ve neon renkler yok. Renkler `src/styles/tokens.css`'te CSS değişkenleri olarak durur; bileşenler sabit renk kullanmaz, tema tek dosyadan değiştirilebilir. Stil kütüphanesi yok, bileşen içi scoped CSS. Font (IBM Plex Sans/Mono) pakete gömülüdür, dış istek yapılmaz; yalnızca Türkçe için gereken Latin alt kümeleri yüklenir.
  - **Kural düzenleme kapsamı:** Koşul silme, şehir silme, yaş ve gün sayısını değiştirme. Koşul veya grup ekleme yok. Düzenlenen kural `PUT /rule` ile gider; gösterilen kural her zaman backend'in normalize ettiği kuraldır. Son koşulun silme düğmesi gösterilmez. Mesaj üretilirken kural düzenlenemez.
  - **Üretimin başlaması:** Kural `ok` ve mesaj yoksa sayfa açılınca otomatik başlar. `partial` kuralda pazarlamacı uygulanmayan kriterleri gördükten sonra düğmeyle başlatır.
  - **Kampanya listesi yok:** Kampanya id'si URL'de durur, sayfa yenilenince aynı kampanya açılır.
  - **Onay kilidi:** Onay düğmesinin neden kapalı olduğu çubukta yazar. Kurallar backend'in kontrolleriyle aynıdır (`lib/approval.ts`); backend onayda hepsini yeniden kontrol eder.
  - **Yeniden yükleme:** Üretim id'si `sessionStorage`'da tutulur; sayfa yenilenirse yeni bir üretim başlatılmaz, sürmekte olanın olayları baştan dinlenir. Üretim kaydedilmeden biterse (`validated`), frontend mesajı `PUT /content` ile kaydeder.
  - **Sınırlar:** Önizlemede "en uzun isim" örneği yok, çünkü API kişisel veri olan bu değeri göndermez; en kötü durum yalnızca karakter sayısı olarak görünür. Kaydedilmemiş düzenlemede en kötü durum gösterilmez, kaydedince hesaplanır.
- **Güncelleme (2026-11-09, PLAN adım 14a): Kampanya listesi ve taslak silme.** `/campaigns` sayfası kampanyaları en yeniden eskiye listeler (Tümü / Taslaklar / Gönderilenler). Satırlar kapalı gelir (id, durum, tarih, kanal sayıları); açılınca brief, kitle, kanallar, gönderim sayıları ve mesajlar gösterilir. Taslaklar iki adımlı onayla silinir.
  - **API:** `GET /api/campaigns` (`before=<id>` ile sayfalama; araya yeni kampanya girse de sayfalar kaymaz) ve `DELETE /api/campaigns/{id}`. Liste iki sorgudur: kampanyalar ve sayfanın bütün gönderim sayıları tek `GROUP BY` ile.
  - **Silme:** Tek bir `DELETE … WHERE status = 'draft'`. Onay satırı ilk adımda kilitlediği için aynı anda gelen onay ve silmeden yalnızca biri başarılı olur. Onaylanmış kampanya silinemez (`409`); gönderim kayıtları korunur.
  - **Gerekçe:** Pazarlamacı gönderdiği kampanyalara geri dönebilmeli; terk edilen taslaklar listeyi kalabalıklaştırmamalı.

- **Güncelleme (2026-11-09, PLAN adım 15c): Ölçek, gezinme ve sadeleştirme (kullanıcı kararları).**
  - **Ölçek:** Arayüz ekrana orantılı yayılmalı; eski hâlinde yazılar küçük kalıyor ve içerik ortada toplanıyordu. Sayfa stilleri `rem` birimindedir; kök yazı boyutu kampanya sayfalarında %140, brief sayfasında %150'dir (`html[data-page]`, `App.vue` ayarlar). Böylece yazı, boşluk ve genişlik birlikte büyür. Yüzde değeri tarayıcının yazı boyutu ayarını korur.
    - Üst bar ve sayfa kapsayıcısının genişliği (`.container`, 1760 px) piksel olarak sabittir: üst bar her sayfada aynı boyutta ve aynı yerde durur. `scrollbar-gutter: stable` kısa ve uzun sayfalarda genişliği eşitler. 1–2 px'lik çizgiler piksel kalır (ölçeklenince bulanıklaşır).
    - Alternatifler: CSS `zoom` (yerleşimi ve medya sorgularını şaşırtır), yalnızca kapsayıcıyı genişletmek (yazılar yine küçük kalır; kullanıcı reddetti).
  - **Gezinme:** Üst barda iki bölüm vardır: "Kampanyalar" ve "Yeni kampanya". Kampanya sayfası Kampanyalar'ın alt sayfasıdır; menüde Kampanyalar aktif görünür. Kampanya sayfasının sol üstünde `← Kampanyalar` bağlantısı vardır. Listeden gelindiyse tarayıcı geçmişinde geri gidilir; liste `KeepAlive` ile bellekte tutulduğu için filtre, açık satır ve kaydırma konumu korunur, durum ve sayılar arka planda tazelenir. Brief'ten ya da bağlantıyla gelindiyse liste açılır. Liste filtresi URL'dedir (`?filtre=taslak|gonderilen`); yenilemede de korunur.
  - **Liste satırı:** Sabit sütunlu ızgara (No, Durum, Tarih, Gönderim, Kanallar) ve üstte gri sütun başlıkları; numaralar `#` olmadan, kalın.
  - **Kaldırılanlar:** Kitle kutusundaki "Kanal izni filtresi her zaman uygulanır" notu (filtre backend'de değişmeden uygulanır; kural özeti "Kanal izni olan…" diye başlar), "· hepsi sağlanmalı" ibaresi, mesaj üretimindeki olay satırı (`started → …`; geliştirme bilgisi, frontend'deki kaydı da silindi). Bekleme metni "Claude … çeviriyor" yerine "Hazırlanıyor…". Mock modu üst barda küçük bir "Demo" etiketidir.
  - **Gönderim zamanı:** `datetime-local` alanı 12/24 saati tarayıcının ve işletim sisteminin diline göre seçer; koddan ayarlanamaz. Alan, tarih alanı ile saat (00–23) ve dakika (5'er dakika; önerilen zaman ızgara dışındaysa o da listelenir) seçimlerine bölündü. Yeni seçilen tarih 10:00 ile başlar; tarihi silmek "hemen gönder" demektir. Değer biçimi (`2026-11-02T10:00`, Türkiye saati) değişmedi.
    - Alternatifler: hazır tarih seçici bileşeni (yeni bağımlılık, tasarıma uydurma işi), olduğu gibi bırakmak.
    - Sınır: Tarih alanının biçimi (`gg.aa.yyyy` ya da `mm/dd/yyyy`) yine tarayıcının diline bağlıdır.
  - **Mesaj alanları (aynı gün, ikinci tur):** Sayaç yalnızca yazılan uzunluğu gösterir (`117 / 160`); "en kötü durum" sayısı ve uzunluk barı pazarlamacı için kaldırıldı. En kötü durum kontrolü backend'de aynen sürer (R-CNT-2): sınırı aşan mesajda sayaç kırmızıdır ve onay, backend'in hata mesajıyla engellenir. Önizleme sabit bir örnek müşteriyle gösterilir; değişken listesi ve doğrulama açıklaması gibi geliştirici metinleri arayüzde yoktur.

- **Güncelleme (2026-11-10, PLAN adım 15d): "Geçersiz" taslaklar.** Onaylanabilir kitle çıkmayan (unsupported veya rejected kurallı) taslak, listede ve kampanya sayfasında "Geçersiz" etiketiyle gösterilir. Bu bir veritabanı durumu değildir: kampanya taslak kalır (silinebilir), etiket kuraldan türetilir (`approvable`). Böyle bir kuralda düzenlenecek koşul olmadığı için değer sonradan değişmez.
  - Alternatif: `campaigns.status`'a yeni bir `invalid` değeri. Migration, durum makinesi ve taslağa bağlı sorgularda değişiklik gerektirir; yaşam döngüsüne yeni bir adım eklemeden aynı bilgiyi verir.
  - **Yeniden yazma eski kaydı değiştirir (kullanıcı kararı):** Geçersiz bir taslaktan "Brief'i yeniden yaz" ile açılan form, yeni brief kaydedildikten sonra eski taslağı siler; denemeler listede birikmez. Önce oluşturulur, sonra silinir; silme başarısız olursa yeni kampanya yine açılır. Geçerli taslaklarda bu düğme zaten yoktur.

## D-COMPOSE: Tek komutla kurulum (D-2)

- **Karar (2026-11-08, PLAN adım 12):** `docker compose up --build` tüm sistemi başlatır. Servisler ve açılış sırası:
  - `postgres` → `seed` (migration ve örnek veri, sonra kapanır) → `api`, `worker` (ve bağımsız olarak `mockprovider`) → `web`.
  - Sıra `depends_on` koşullarıyla sağlanır: Postgres healthcheck'i, seed'in başarıyla bitmesi (`service_completed_successfully`), api ve worker için `/readyz`, mock provider için `/healthz`.
  - **İmajlar:** Backend için tek imaj (`backend/Dockerfile`, çok aşamalı): dört binary aynı imajda durur, her servis `command` ile birini çalıştırır. Çalışma imajı Alpine'dir (healthcheck için `wget` gerekir) ve root olmayan kullanıcıyla çalışır. Frontend imajı (`frontend/Dockerfile`) Vue uygulamasını derler ve nginx ile sunar.
  - **nginx:** `/api` isteklerini `api:8080`'e iletir; tarayıcı tek bir origin görür, CORS gerekmez. SSE için `proxy_buffering off` ve uzun `proxy_read_timeout`. İstemci tarafı rotalar (`/campaigns/:id`) `index.html`'e düşer. Hash'li dosyalar (`/assets/`) uzun süre önbelleğe alınır.
  - **Portlar:** arayüz `3000`, API `8080` (`/metrics`), worker `8081`, mock provider `8090` (`/stats`), Postgres `5432`.
  - **Config:** Container'lar birbirine servis adıyla ulaşır; adresler (`DATABASE_URL`, `PROVIDER_URL`, `*_ADDR`) compose dosyasında sabittir. `.env`'den yalnızca compose'ta `${...}` ile anılan değerler alınır: `LLM_*` ve `ANTHROPIC_*` yalnızca api'ye, `WORKER_*` worker'a, `MOCK_*` mock provider'a. Boş değer Go kodundaki varsayılan demektir. `.env` yoksa LLM mock modda çalışır.
  - **Seed:** Veri zaten varsa seed hiçbir şey yapmadan çıkar; `docker compose down -v` veriyi silip sıfırdan başlatır.
  - **`make smoke` (`scripts/smoke.sh`):** Çalışan sisteme nginx üzerinden arayüzün akışını uygular: brief → kural → içerik akışı → kayıt → onay → gönderimin bitmesi. Kontroller:
    - SSE olaylarının tek tek gelmesi (tarayıcı gibi `Accept-Encoding` ile).
    - Snapshot sayılarının gönderim sonuçlarıyla aynı olması.
    - Mock provider'daki teslim sayısının DB'deki `sent` sayısına eşit olması.
    - Saniyede 100 sınırının aşılmaması.
    - Loglarda email ve telefon olmaması.
    
    API mock LLM kullanmıyorsa script çalışmayı reddeder; gerçek API çağrısı yapmaz. Test kampanyası sonunda silinir.
- **Gerekçe:** Değerlendiren kişi yalnızca Docker ile sistemi açabilmeli (D-2). Tek backend imajı derleme süresini ve bakım yükünü azaltır. nginx, geliştirmedeki Vite proxy'siyle aynı tek origin düzenini üretimde kurar. Seed'in ayrı ve tek seferlik bir servis olması, api ile worker'ın migration'ları tamamlanmamış bir veritabanına bağlanmasını önler.
- **Alternatifler:**
  - Her binary için ayrı imaj: aynı kod dört kez derlenir.
  - Statik dosyaları Go API'nin sunması: API'ye frontend sorumluluğu ekler; nginx ile ayırmak D-STREAM'de zaten öngörülmüştü.
  - Migration'ı api'nin başlangıcında çalıştırmak: api ve worker aynı anda başladığında yarış oluşur.
  - Distroless çalışma imajı: healthcheck için içinde araç yoktur.
- **Sınırlar:**
  - Tek api ve tek worker replikası (D-DB, D-STREAM).
  - `docker compose config` komutu `.env`'deki API key'i açık metin olarak yazdırır; doğrulama için `docker compose config --quiet` kullanılmalı.
  - Bu nginx sürümünde (1.29) tamponlamayı veya gzip'i bilerek açmak olay akışını bozmadı (nginx her parçada flush ediyor). Bu yüzden smoke testindeki akış kontrolü bilerek bozularak doğrulanamadı; tarayıcı davranışını taklit eden bir güvence olarak duruyor.
  - "Son N gün" pencereleri seed'in yüklendiği zamana göre kayar; sayılar zamanla değişir (bkz. PLAN adım 7 notu).

## D-TEST: Test yaklaşımı (D-3)

- **Karar:**
  - `make test` iki tür test içerir: saf birim testler ve gerçek Postgres ile entegrasyon testleri. Docker gerektirir, gerçek API key gerektirmez.
  - `make eval` gerçek API'yi kullanır ve isteğe bağlıdır. `docs/PLAN.md`'deki 20 brief her biri 3 kez çalıştırılır. Puanlama yapısal karşılaştırmayla değil, kuralın sabit seed'de döndürdüğü kullanıcı kümesinin beklenen kümeyle karşılaştırılmasıyla yapılır. Doğruluk ve tutarlılık ayrı ayrı raporlanır.
- **Gerekçe:** Aynı kitle farklı yapılarla yazılabilir (ör. iki grup ya da tek grupta `in`). Küme karşılaştırması bu farkları doğru biçimde eşit sayar.
- **Sınırlar:** Eval sonuçları her çalıştırmada değişebilir; rapora tarih ve model yazılır.
- **Güncelleme (2026-11-06): Beş test katmanı.** Yukarıdaki iki türlü ayrımın yerini aşağıdaki yapı alır. `make eval` kararı ve küme karşılaştırmasıyla puanlama aynen geçerlidir.
  - **Katmanlar:**

    | # | Katman | Kapsam | Gerektirir |
    |---|---|---|---|
    | 1 | Birim | Saf mantık: segment doğrulama ve SQL derleme, içerik kuralları (rune sayımı, en kötü durum render, değişken beyaz listesi), retry politikası, limiter | Hiçbir şey |
    | 2 | Entegrasyon | Gerçek Postgres: seed üzerinde beklenen kişi sayıları (D-CART kohortu dahil), `UNIQUE(campaign_id, user_id, channel)`, `SKIP LOCKED` claim ve lease | Docker |
    | 3 | LLM | Fake `llm.Client` ve `httptest` ile kurulan sahte SSE sunucusu: bozuk JSON, onarım döngüsü, `max_tokens`, `refusal`, stream ortasında kopma, iki delta'ya bölünen `\uXXXX` kaçışı | Hiçbir şey |
    | 4 | Uçtan uca gönderim | Worker + mock provider + Postgres: tam bir kez teslim (worker gönderim ortasında öldürülüp yeniden başlatılır), herhangi bir 1 saniyelik pencerede kanal başına en fazla 100 istek | Docker |
    | 5 | Canlı | Gerçek API. `make test`'e dahil değildir | API key, ücretli |

  - **Make hedefleri:**
    - `make test`: katman 1–4 ve frontend testleri (Vitest). Docker gerekir, key gerekmez.
    - `make test-unit`: katman 1 ve 3. Docker ve key gerekmez; geliştirme sırasında hızlı geri bildirim için.
    - `make test-live`: PLAN adım 1'deki SDK doğrulamasının kalıcı hâli (aşağıda).
    - `make eval`: 20 brief × 3 çalıştırma (yukarıdaki karar).
  - **Ayrım Go build tag ile yapılır:** Katman 2 ve 4 dosyaları `//go:build integration`, katman 5 dosyaları `//go:build live` satırıyla başlar. Tag'siz `go test ./...` yalnızca katman 1 ve 3'ü derler ve çalıştırır. Böylece Docker'ı veya key'i olmayan biri ağır ya da ücretli bir testi yanlışlıkla çalıştıramaz.
  - **`make test-live`:** Anthropic adaptörü üzerinden en fazla 2 çağrı yapılır, SDK'nın otomatik retry'ı kapalıdır.
    1. Segment şemasıyla streaming: `end_turn` beklenir; çıktı sıkı decode ve semantik doğrulamadan geçer.
    2. Çok düşük `max_tokens`: `max_tokens` dalına girilir ve çıktı parse edilmez.
    - Key `.env`'den config yükleyicisiyle okunur ve hiçbir çıktıya yazılmaz. Key yoksa test açık bir mesajla başarısız olur; komut açıkça çağrıldığı için sessizce atlanmaz.
    - Amaç SDK, API veya model değiştiğinde adaptör kontratının bozulmadığını görmektir. Model kalitesini ölçmez; kaliteyi `make eval` ölçer.
  - **İlke:** Her metot değil, her paketin dışa açık davranışı test edilir. Öncelik riskli alanlardadır: yanlış kişiye gönderim (izin filtresi, grup parantezleri, kitle snapshot'ı), çift gönderim (D-DEDUP katmanları), SQL güvenliği (LLM'den gelen metnin SQL'e girmemesi) ve karakter limitleri (rune, en kötü durum).
  - **Gerekçe:** Katmanlar ihtiyaç duydukları altyapıya göre ayrılır; en sık çalışan katman en hızlı olanıdır. Canlı test, sahte SSE sunucusunun gerçek API'den uzaklaşıp uzaklaşmadığını kontrol eder; bunu katman 3 tek başına göremez.
  - **Sınırlar:** `refusal` canlı olarak tetiklenmez, yalnızca katman 3'te test edilir. Canlı testler ve eval CI'da çalışmaz.
- **Güncelleme (2026-11-08, PLAN adım 13, `make eval`):**
  - **Yapı:** `internal/eval` paketi ve `cmd/eval`. Beklentiler `internal/eval/cases.json` dosyasındadır (go:embed). Her brief için: kabul edilen status'ler (ilki tercih edilen), kitle türü, kabul edilen kurallar, zorunlu `unsupported` sebepleri ve varsayım gerekip gerekmediği. Dosya yüklenirken her beklenen kural `segment.Validate`'ten uyarısız geçmek zorundadır; bozuk bir beklenti eval'i başlatmaz.
  - **Veri:** Her eval geçici bir veritabanı açar, sabit RNG seed'i (42) ve sabit bir "bugün" (2026-11-01) ile seed'ler, iş bitince siler. Böylece "son N gün" pencereleri kaymaz ve beklenen kitleler her çalıştırmada aynıdır. Geçici veritabanı kodu `platform.CreateTempDB`'ye taşındı; entegrasyon testleri (`pgtest`) de bunu kullanır.
  - **Akış:** Brief, üretimdeki `llm.ExtractSegment` yolundan geçer (aynı prompt, şema, effort `low` ve onarım döngüsü). Onaylanabilir kural, üretimdeki derleyiciyle (izin koşulu dahil) iki kanal için derlenir ve kişi kümesi SMS ∪ email olarak alınır.
  - **Puanlama:** Bir çalıştırma şu üç koşulu sağlarsa doğrudur: (1) status kabul edilenlerden biri, (2) kişi kümesi kabul edilen kurallardan birinin kümesiyle birebir aynı, (3) beklenen `unsupported` sebepleri yazılmış. Kitle türleri:
    - `exact`: küme karşılaştırılır.
    - `any`: kitle varsayıma bağlıdır (#8 "uzun süredir"); yalnızca onaylanabilir bir kural ve bir varsayım beklenir.
    - `none`: onaylanabilir kural çıkmamalı.
    - Ek status'ler: `invalid` (onarımlardan sonra da geçersiz; #9'da kabul edilir, çünkü backend çelişkiyi yakalamıştır), `refusal`, `error` (API hatası).
  - **Tutarlılık:** Bir brief'in bütün çalıştırmaları aynı status'ü ve aynı kişi kümesini verdiyse brief tutarlıdır. API hatası içeren brief tutarlı sayılmaz.
  - **Güvenlik:** Her derlenen sorgu ayrıca kontrol edilir: kanalın izin koşuluyla başlamalı, metninde tırnak, `;` veya ASCII dışı karakter olmamalı. Başarısız olursa çalıştırma yanlış sayılır ve komut hata koduyla biter.
  - **Maliyet kontrolü:** SDK retry'ı kapalıdır; her istek sayılır ve `-max-calls` bütçesini (varsayılan: çalıştırma × brief × 3, yani 2 onarımlı en kötü durum) aşan istek yapılmaz, eval durur. Geçici bir API hatası tekrar denenmez, raporda "API hatası" olarak ayrıca görünür. `make eval-dry` aynı akışı ideal yanıtlarla, API çağrısı yapmadan çalıştırır.
  - **Raporlar:** `eval/reports/<tarih>-<model>.md` (okunacak rapor) ve `.json` (her çalıştırmanın ham kuralı, token ve süre bilgisi). Kullanıcı id'leri rapora girmez, yalnızca kişi sayıları girer; bunu bir test doğrular. Raporlar repoda tutulur (kullanıcı kararı).
  - **Testler:** Puanlama, tutarlılık, SQL kontrolü ve rapor birim testleri; seed'li Postgres üzerinde ideal yanıtlarla 40/40, bilerek yanlış ve farklı yazılmış yanıtlarla doğru puanlama (aynı kitlenin iki grup olarak yazılması doğru, `age≤30` ve unutulan koşul yanlış, sebep hatası, çelişkinin onarımdan sonra `invalid` olması), bütçe ve veritabanı hatası testleri.
- **Güncelleme (2026-11-09, PLAN adım 14b):** Test seti 25 brief'e çıktı (#21–25: sayı, tarih ve sıra kriterlerinin birleşimleri ve hâlâ desteklenmeyen tutar). #4, #6 ve #11'in beklentisi güncellendi. Sonuç: 75/75 doğru, 25/25 tutarlı (`eval/reports/2026-11-09T1416-claude-sonnet-5-5.md`). Ara ölçümler ve düzeltmeler PLAN adım 14b notundadır.

## D-LANG: Dil kuralı

- **Karar:** Dokümantasyon (DECISIONS.md, PLAN.md, CLAUDE.md, README) Türkçedir. Kod, kod yorumları, değişken isimleri ve commit mesajları İngilizcedir.
- **Gerekçe:** Doküman okuyucusu Türkçe konuşuyor; kod ise uluslararası standartları izler.
