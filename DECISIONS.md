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

## D-PARTIAL: Kısmi kural onayı kilitler

- **Karar:** `status: partial` olan bir kuralda onay düğmesi kapalı kalır. Pazarlamacı uygulanmayan kriterleri görüp "bu kriterler olmadan devam et" diye açıkça onaylamalıdır.
- **Gerekçe:** Bir VE koşulunun düşmesi kitleyi her zaman genişletir, yani hedeflenenden fazla kişiye mesaj gider. Bu sessizce olmamalı.
- **Alternatifler:** Yalnızca uyarı göstermek: gözden kaçabilir. `partial` sonucu tamamen reddetmek: yaklaşık ama kabul edilebilir kuralları da engeller.
- **Sınırlar:** Onay kampanya kaydına zaman damgasıyla yazılır, ama uygulanmayan kriterlerin listesini ayrıca saklamayız.

## D-CHANNEL: Kanal seçimi LLM'e bırakılmaz

- **Karar:** Onay ekranında SMS ve email için iki checkbox bulunur, varsayılan olarak ikisi de seçilidir. Brief'te kanal belirtilmişse bu yalnızca `notes` içinde bilgi notu olarak gösterilir, onayı kilitlemez.
- **Gerekçe:** Kanal seçimi deterministik ve insan kontrolünde olmalı. LLM'in yanlış yorumlaması mesajın gönderilmemesine ya da istenmeyen bir kanaldan gitmesine yol açar.
- **Alternatifler:** Şemaya bir `channels` alanı eklemek: LLM hatasına açık.
- **Sınırlar:** İzin kontrolü (R-AUD-2) kanal seçiminden bağımsız olarak her durumda derleyicide uygulanır. Arayüz her kanal için uygun kişi sayısını ayrıca gösterir.

## D-CART: Örnek brief'in yorumu

- **Karar:** "Son 30 günde sepete ekleyip alışveriş yapmayan" ifadesi iki bağımsız koşul olarak yorumlanır: `add_to_cart` son 30 günde var **ve** `purchase` son 30 günde yok.
- **Gerekçe:** DNF yapısıyla doğrudan ifade edilir; sıralı olay desteği gerektirmez.
- **Alternatifler:** Sıralı yorum ("sepete ekledikten sonra satın alma yok"): iş anlamına daha yakın ama şemaya sıralı olay tipi ve daha karmaşık bir sorgu ekler.
- **Sınırlar:** Pencere içinde önce satın alıp sonra sepete ekleyen kullanıcı kitle dışında kalır. Seed'e bu durum için bir kohort konur ve testte doğrulanır.

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

## D-MODEL: Model ve çağrı ayarları

- **Karar:** Varsayılan model `claude-sonnet-5-5`, `LLM_MODEL` env değişkeniyle değiştirilebilir. `effort` her çağrıda açıkça verilir (`low` veya `medium` ile başlanır). `thinking` parametresi gönderilmez; bu modelde `disabled` değeri 400 döner. `temperature` ve diğer sampling parametreleri gönderilmez. `max_tokens` thinking payını kapsayacak kadar yüksek tutulur. Refusal için `fallbacks: "default"` (beta) planlanır; desteği PLAN adım 1'de doğrulanır.
- **Gerekçe:** Bu iş kısa yapılandırılmış çıkarım ve kısa metin üretimi; Sonnet 5.5 bunun için maliyet ve kalite dengesi sunar. Seçim kullanıcının kararıdır.
- **Alternatifler:** `claude-opus-5-5`: daha yetenekli ama iki kat pahalı. `claude-haiku-5-5`: çok ucuz ama daha fazla onarım denemesi riski var.
- **Sınırlar:** Kalite `make eval` ile ölçülür. Model değişirse eval tekrar çalıştırılır.
- **Güncelleme (2026-11-06):** `fallbacks: "default"` kullanılmayacak (kullanıcı kararı). Refusal nadir bir durum; olduğunda D-LLM-RETRY'daki gibi parse edilmeden kullanıcıya gösterilir. Böylece beta API yüzeyine bağımlılık kalmaz ve stabil `client.Messages` yolu yeterli olur. `thinking` gönderilmeden `effort: low` ile segment çıkarımı doğru sonuç verdi (bkz. D-LLM-OUT güncellemesi).

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
- **Sınırlar:** Manuel "tekrar gönder" butonu çekirdek kapsamda yok.

## D-RATE: Kanal başına 100 msg/sn (R-SEND-2)

- **Karar:** Her kanal için ayrı, süreç içi bir token bucket (`golang.org/x/time/rate`) kullanılır; burst değeri 1 veya çok küçüktür. Limiter gerçek `Send` çağrısının önünde durur, retry'lar da limitten geçer. Her kanalın kendi dispatcher'ı ve worker havuzu vardır.
- **Gerekçe:** Burst değeri büyük olursa boşta biriken jetonlarla bir saniyede yaklaşık 200 mesaj gidebilir. Ayrı kanal havuzları sayesinde email'deki bir 429 SMS gönderimini durdurmaz.
- **Alternatifler:** Redis veya DB tabanlı dağıtık limiter: çoklu worker için doğru, ama şu an gereksiz altyapı. Sabit pencere sayacı: pencere sınırında limitin iki katına çıkabilir.
- **Sınırlar:** Tek worker replikası varsayılır. Doğrulama mock provider'ın kayan pencere sayacıyla yapılır: herhangi bir 1 saniyelik pencerede en fazla 100 istek.

## D-MOCK: Mock provider (R-MOCK-1, R-MOCK-2)

- **Karar:** Ayrı bir HTTP servisidir. Hata, gecikme ve 429 oranları env ile ayarlanır; RNG seed'i sabitlenebilir. Kendi kayan penceresiyle 100 msg/sn limitini uygular. İsteği kabul edip 500 döndürme modu vardır. Idempotency anahtarlarını saklar ve test için bir istatistik endpoint'i sunar.
- **Gerekçe:** Retry, rate limit ve idempotency davranışları ancak gerçekçi hata türleriyle test edilebilir.
- **Sınırlar:** Veriler bellekte tutulur; servis yeniden başlarsa kayıtlar silinir.

## D-SNAPSHOT: Kitlenin hesaplanma zamanı

- **Karar:** Kitle onay anında hesaplanır ve `deliveries` tablosuna snapshot olarak yazılır. Gönderim sırasında yalnızca izin tekrar kontrol edilir.
- **Gerekçe:** Pazarlamacının onayladığı sayı ile gerçekte gönderilen sayı tutarlı kalır. "Son N gün" gibi göreli pencereler onay anına sabitlenir.
- **Alternatifler:** Kitleyi gönderim anında yeniden hesaplamak: onaylanan sayı değişebilir.
- **Sınırlar:** Onaydan sonra kitleye giren yeni kullanıcılar bu kampanyaya dahil edilmez.

## D-OBS: Gözlemlenebilirlik (R-OBS-1..3)

- **Karar:** `/metrics` Prometheus formatında yayınlanır (`client_golang`). Health kontrolü iki endpoint'e ayrılır: `/healthz` sürecin ayakta olup olmadığını, `/readyz` DB'ye erişilip erişilemediğini kontrol eder. Hem api hem worker için geçerlidir. Metrikler: gönderim durumları, provider gecikmesi, retry sayısı, LLM denemeleri ve doğrulama hataları, token kullanımı.
- **Gerekçe:** Prometheus formatı yaygın bir standart; liveness ve readiness kontrollerinin ayrı olması da container ortamlarında yaygın bir pratik.
- **Sınırlar:** Grafana panosu kurulmaz; canlı durum için uygulamanın kendi dashboard'u kullanılır.

## D-FE: Frontend

- **Karar:** Vue 3 (Composition API), Vite ve TypeScript. SSE bağlantısı bir composable içinde (`useEventStream`) tutulur. Arayüz ve içerik dili Türkçedir.
- **Gerekçe:** TypeScript, SSE olaylarının ve kural şemasının tiplerini korur.
- **Sınırlar:** Kimlik doğrulama yok; tek kullanıcı varsayılır ve README'de belirtilir.
- **Güncelleme (2026-11-06):** Frontend testleri Vitest ile yazılır (Vite ile aynı yapılandırmayı kullanır) ve `make test`'e dahildir (bkz. D-TEST).

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

## D-LANG: Dil kuralı

- **Karar:** Dokümantasyon (DECISIONS.md, PLAN.md, CLAUDE.md, README) Türkçedir. Kod, kod yorumları, değişken isimleri ve commit mesajları İngilizcedir.
- **Gerekçe:** Doküman okuyucusu Türkçe konuşuyor; kod ise uluslararası standartları izler.
