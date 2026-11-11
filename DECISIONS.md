# Kararlar

Projede verdiğim önemli kararlar ve nedenlerini bu dosyada açıkladım. Tasarımı Claude Code ile birlikte yaptım.
Geliştirme sürecinde fikir, mimari tasarım, anlık sormam gereken şeyler için Gemini ve ChatGPT kullandım.

Tasarım sürecinde Claude seçenekleri ve trade-off'larını çıkardı, kararı ben verdim ve verdiğim kararları
Claude'un proje boyunca takip edebilmesi adına bir plana dönüştürdüm. Bazı
yerlerde önerisini değiştirdim, bazı yerlerde onun eklediği bir şeyi inceleyerek kabul ettim. Bu
konuşmaların kayıtları `ai-sessions/` altında. Proje boyunca ana tasarım için alınan teknik kayıtlar ise
`docs/DECISIONS-DETAIL.md`'de.

## Claude Code kullanımı

Claude Code kullanımında her oturumun aynı kurallarla çalışması için şu dosyaları hazırladım.

- **CLAUDE.md:** Gevşetilmeyecek kurallar burada. Örneğin LLM çıktısı asla SQL'e girmiyor, her özellik testiyle geliyor.
- **backend/CLAUDE.md:** Go'ya özgü kurallar. Claude yalnızca backend'de çalışırken okuyor, kök dosya kısa kalıyor.
- **docs/PLAN.md:** Uygulamayı adımlara böldüm. Önce çekirdek akışı uçtan uca çalışır hale getirdim, bonusları ve ekstraları en sona bıraktım. Her adım bitince kısa bir not eklendi, ben kontrol ettikten sonra sonrakine geçildi.
- **docs/DECISIONS-DETAIL.md:** Teknik kararların ayrıntılı kaydı. Claude'un önceki kararlarla çelişmemesini sağladı.
- **docs/CASE.md:** Claude'un case gereksinimlerini analiz edebilmesi ve kontrolde bana yardımcı olabilmesi için bu dosyayı oluşturdum.
- **Teslim öncesi denetim:** Teslimden önce sistemi uçtan uca denetlettim, bulunan hataları testleriyle düzelttim.
- **Güvenlik ve maliyet:** `.env` dosyasının okunması engellendi. Ücretli komutlar yalnızca benim onayımla çalıştırıldı.

## 1. Kod yapısı: özellik bazlı paketler

Backend için Go'yu seçtim. Araştırmalarım sonucunda aynı anda çalışan işlerin ve uzun süre açık kalan bağlantıların çok olduğu bu projeye Go'nun daha iyi uyacağını düşündüm. Ayrıca Go ile bir proje geliştirmek de istiyordum.

Python'da clean architecture ile çalışmaya alışkınım, o nedenle ilk iş olarak Go'da bu prensibin en doğru karşılığının
ne olduğunu araştırdım. Burada Gemini'nin deep research özelliğinden faydalandım. Sonuç olarak en doğru karşılığın `domain/ports/adapters`
gibi tam bir hexagonal yapı olduğunu öğrendim. Ancak burada akışın daha lineer formda olmasından ve fazla hâkim olmadığım bir dilde işi daha sade ve modüler tutmak istememden ötürü paketlerin teknik katmana göre değil, işe göre ayrılmasını istedim: `segment`, `content`, `llm`, `campaign`,
`delivery`. Her konu tek bir yerde duruyor ve tek başına test ediliyor.

Interface'i yalnızca gerçekten değişebilecek sınırlarda kullanmanın daha doğru olacağına karar verdim:
LLM istemcisi, SMS/email provider'ı ve worker'ın kuyruk erişimi. Geri kalan her şey somut tip.

## 2. Kuyruk için ayrı bir sistem yok, sadece Postgres

Redis, RabbitMQ veya hazır bir kuyruk kütüphanesi yerine kuyruğu Postgres tablosu olarak
kendimiz yazmanın bu senaryo için en uygunu olacağını düşündüm. Bunun sebebi ise kampanya durumu ve gönderim kuyruğunun
aynı transaction'da yazılabilmesinin iki sistem arasındaki tutarsızlık riskini ortadan kaldırdığını öğrenmiş olmam. Kanal
başına saniyede 100 mesaj Postgres için oldukça hafif bir yük. Ayrıca claim, retry ve kilit mantığının
bize ait olması da güvenilirliği göstermemi kolaylaştırdı.

## 3. Hedef kitle kuralı: ortak koşullar + VEYA grupları

Claude ilk olarak düz bir VE listesi önerdi, en basit ve sade seçenek olduğundan başta bununla ilerledim. Ama "İstanbul'daki 25 yaş
altı ve Ankara'daki 40 yaş üstü kullanıcılar" gibi bir brief tasarladığımda bu liste ile yazılamıyordu.
Pazarlamacıların tek kampanyada birden fazla alt grubu hedeflemesi oldukça gerçekçi bir
senaryo olduğundan bu sınırı kabul etmedim ve yapıyı gruplara (DNF) çevirmenin daha iyi olacağına karar verdim. Grup içinde
VE, gruplar arasında VEYA.

Claude buna "ortak koşullar" eklemeyi önerdi ve mantıklı buldum. Tüm gruplara uyan bir
koşul her gruba ayrı ayrı yazılırsa, model birinde unutabilir ve o grubun kitlesi
sessizce genişler. Ortak koşullar bunu engelliyor.

Yapı iç içe olmadığından Claude'un JSON şeması garantisi
korunuyor. Ancak en fazla 5 grup var, daha fazlası hem okunmayı zorlaştırıyor hem de sorguyu büyütüyor.

**Kural asla SQL'e çevrilmiş metin olarak gelmez.** Model sadece JSON üretir, backend
doğrular, kolon adları koddaki sabit bir tablodan gelir, değerler parametre olur. İzin
filtresini kural değil derleyici ekler, model veya kullanıcı onu kaldıramaz. Bu projenin en önemli gereksinimlerinden biriydi,
ben de yapıyı bunu sıkı denetleyecek şekilde kurmaya çalıştım.

## 4. Claude'dan güvenilir JSON

Bu amaç için structured output (`output_config.format`) kullandım. Claude başta bunun şemaya uyumunun
API tarafından garanti edildiğini söyledi. Kaynağını kontrol etmesini istedim ve ek olarak Gemini ile de güncel bilgilere erişmeye çalıştım. Garanti
yalnızca yapı için olup birkaç istisnası olduğundan her yanıt backend'de ayrıca
doğrulanıyor. Hata varsa en fazla 2 kez onarım isteniyor. `refusal` veya `max_tokens`
ile biten yanıtlar hiç parse edilmiyor. Burada alternatif olarak tool use'u ele aldım. Ancak araştırmalarımda JSON almak için tool kullanmanın, tool'u `tool_choice` ile zorlamaya dayandığını ve Sonnet 5.5'in bunu desteklemediğini gördüm. Bu yüzden seçimim structured output oldu.

Model olarak Sonnet 5.5'i seçtim. Claude, Opus 5.5'i önermişti, ama bu iş kısa bir çıkarım ve metin üretimi olduğundan Sonnet daha hızlı ve daha ucuz olup yeterli geldi. Kitle çıkarımının doğruluğunu eval ile ölçtüm. Son tam eval'de 25 hazır brief × 3 çalıştırmada %100 başarı aldım.

## 5. Son kararı insan verir

- **Eksik kural onayı kilitler:** Brief'teki bir kriter ifade edilemezse (ör. "toplam
  harcaması 5000 TL'yi geçen") kural `partial` olur. Bir koşulun düşmesi kitleyi her zaman
  genişletir, bu yüzden pazarlamacı eksik kriterleri görüp açıkça onaylamadan gönderemez.
- **Kanalı model seçmez:** Onay ekranında SMS ve email için iki kutu var, ikisi de seçili
  gelir. Brief'te "sadece SMS" yazsa bile bu yalnızca bir not olarak gösterilir.
- **Kitle onay anında dondurulur:** Pazarlamacının onayladığı sayı ile gönderilen sayı
  aynı kalsın diye. Zamanlanmış gönderimde de böyle. Gönderimden hemen önce yalnızca izin
  tekrar kontrol edilir. Ancak zamanlanmış gönderim, zamanı henüz gelmediyse düzenlenebilir ve iptal edilebilir.
  Düzenlenen kampanya taslağa döner ve yeniden onaylandığında kitle o anda tekrar hesaplanır.

## 6. "Sepete ekleyip almayan" ne demek?

Case'teki örnek brief'ten yola çıkarak bu koşula özen gösterdim.
Başta bu ifadeyi iki bağımsız koşul olarak yorumladım (son 30 günde sepete ekleme var, alışveriş yok). Sistemde henüz sıralı olay desteği yoktu ve bu yorum mevcut kural yapısıyla doğrudan yazılabiliyordu. Sıralı olay desteğini ekledikten sonra sıralı yoruma geçtim. Yani sepete ekledikten sonra alışveriş yapmamış. Sepet hatırlatmasının asıl amacının bu olduğunu düşündüm. Baştaki yorum "önce alıp sonra tekrar sepete ekleyen" kişileri dışarıda bırakıyordu.

## 7. Mesaj akışı: SSE

Mesajlar ekrana akarak gelsin diye SSE kullanma kararını onayladım. Akış tek yönlü olduğu için WebSocket'in
gereksiz olacağını düşündüm. Claude'dan gelen parçalar ham JSON olduğu için backend bunları okunabilir
metne çeviriyor, arayüz yalnızca hazır metni gösteriyor. Onay düğmesi ancak mesaj
backend'de doğrulandıktan sonra açılıyor. Dashboard da her mesaj için değil, saniyede bir
özet alıyor.

## 8. Aynı kişiye aynı mesaj iki kez gitmez

"Aynı mesaj"ı kampanya + kişi + kanal olarak tanımladım. Ağ üzerinden "tam olarak bir
kez" tek bir önlemle sağlanamıyor, bu yüzden birkaç katman var: onay tek seferlik,
veritabanında unique kısıt, worker çökerse süresi dolan kilit ve provider'a her mesajla
giden idempotency anahtarı. Bunu worker'ı gönderimin ortasında öldürüp yeniden başlatarak
test ettim.

Başarısız mesajları tekrar gönderme özelliğini de sonradan ekledim. Mock provider'ın hata
oranı yükseltilince başarısız mesaj oluşuyor ve pazarlamacının bunları tekrar
deneyebilmesi gerekiyor. Mesajlar yeni kayıtla değil aynı kayıtla kuyruğa döndüğü için,
provider'da aslında teslim edilmiş bir mesaj ikinci kez gitmiyor.

## 9. Retry ve hız limiti

Retry durumunun bellekte değil veritabanında tutulmasının daha güvenli olduğunu düşündüm. Bu sayede worker yeniden başlasa da kaybolmuyor. Geçici hatalarda üstel bekleme ve rastgele sapma (jitter) var, en fazla 5 deneme yapılıyor. 429'da provider'ın söylediği kadar bekleniyor. Bu bir mesaj hatası olmadığından 5 denemeye sayılmıyor, ayrı bir sayaçla takip ediliyor.

Hız limiti kanal başına ayrı olduğundan email'deki bir sorun SMS'i durdurmuyor. Ağ gecikmesi yüzünden istekler provider'a üst üste gelebiliyor. Sınırın biraz altında kalmanın daha güvenli olacağını düşündüğümden worker saniyede 95 mesajla gönderiyor.

## 10. SMS sınırı 160 karakter

Gerçekte Türkçe karakter (ı, ş, ğ) içeren bir SMS'e yalnızca 70 karakter sığdığı konusunda Claude bilgi verdi. Daha uzun mesaj parçalara bölünüyor ve her parça ayrı SMS olarak ücretleniyor. Yani 160 karakterlik Türkçe bir SMS aslında 3 SMS ediyor.

Sınırı 70'e düşürmeyi düşündüm ama vazgeçtim, çünkü case açıkça 160 diyor ve 70 karakter pazarlama metni için çok kısa kalıyor. Gerçek bir sistemde ise sınırı karakterle değil SMS parça sayısıyla koyar ve onaydan önce maliyeti gösterirdim.

## 11. Testler API key olmadan çalışır

LLM için sahte bir istemci yazıldı, bu yüzden `make test` hiçbir zaman gerçek API'ye bağlanmıyor. Gerçek API sadece `make test-live` ve `make eval` ile kullanılıyor. Bunlar ücretli olduğu için onları yalnızca gerekli durumlarda çalıştırdım. Eval, kuralı yapısına göre değil sabit veride seçtiği kişilere göre puanlıyor. Aynı kitle farklı şekillerde yazılabildiği için asıl önemli olan modelin doğru kişileri seçmesi.

Claude'un yazdığı testlerin gerçekten hata yakaladığından emin olmak için kodu bilerek bozup testlerin kırıldığını kontrol ettim.

## 12. Frontend

Arayüzü sade ve modern tutmak istedim. Pazarlamacının yalnızca karar vermesi için gereken bilgiyi görmesini önemsedim. Karmaşık panellerden ziyade hedef odaklı bir tasarım benimsedim. Elbette gerçek bir projede eklenmesi gereken başka detaylar çıkacaktır.

- **Tek ekranda karar:** Solda hedef kitle, sağda mesajlar, altta sabit onay çubuğu var. Zamanlanmış gönderim için tarih ve saat seçimi ekledim. Onaydan sonra aynı sayfa canlı dashboard'a dönüşüyor ve anlık gönderim takibi yapılıyor.
- **Sade görünüm:** Açık ve yumuşak tonlarda tek tema kullandım, hazır bir stil kütüphanesi kullanmadım.
- **Teknik metin yok:** Arayüzde geliştiriciye yönelik açıklama yok. Onay düğmesi kapalıysa sebebi çubukta yazıyor.

## Eklediğim bonuslar

- **Zamanlanmış gönderim:** Zamanlanmış gönderimin bu case'in gerçek proje karşılığında oldukça önemli olduğunu düşündüm ve ekledim. Zamanlanmış bir kampanya, gönderim başlamadan önce düzenlenebilir veya iptal edilebilir.
- **CI pipeline:** Her push ve PR'da testleri izole ortamda çalıştıran bir CI pipeline ekledim.

## Ekstralar

- **Kampanya listesi:** Geçmiş kampanyalar listeleniyor, durum, numara ve tarihe göre filtrelenebiliyor. Taslaklar silinebiliyor.
- **Başarısızları tekrar gönderme:** Gönderim bittikten sonra başarısız kalan mesajlar tekrar gönderilebiliyor. Ayrıntısı 8. bölümde.

## Yetişmeyenler ve tercihler

- **Kimlik doğrulama ve birden fazla worker/API:** Çekirdek akışa öncelik verdim. Hız sınırı her worker'ın kendi içinde tutulduğu için birden fazla worker toplamda sınırı aşar. Mesaj üretimi de API'nin belleğinde tutulduğundan tek API varsayılıyor.
- **Kullanıcının diline göre içerik:** En başta varsayım yaparken Türkiye'yi baz aldım, mesajlar Türkçe üretiliyor.
- **Desteklenmeyen kitle kriterleri:** Harcama tutarı, ürün/kategori, sıralama ("en çok harcayan 500 kişi") ve "en fazla N kez". Model bunları sessizce atlamıyor, pazarlamacıya açıkça listeliyor. Veritabanını sade tutmak için çekirdek akışa yetecek alanlarla ilerledim. Gerçek bir üründe tutar ve ürün/kategori mutlaka eklenmesi gereken alanlar olurdu, sıralama ve "en fazla N kez" ise kural yapısına eklenmesi gereken koşullar.
- **A/B varyantları:** Projeye başlarken çekirdek akışı kurduktan sonra eklemeyi istiyordum ancak zaman kısıtından ötürü backend tarafında büyük değişiklikler gerektirdiğinden eklememe kararı aldım. Fakat özellikle pazarlama sektörü için çok faydalı ve kritik bir özellik olduğunun bilincindeyim.
- **Abonelikten çıkma metni:** Mesajlara ret/çıkış bilgisi eklenmiyor. Gerçek bir sistemde bu bilgi gönderim sırasında her mesajın sonuna otomatik eklenmeli.
- **Email formatı:** Email gövdesi düz metin olarak gönderiliyor, HTML desteği yok.
- **Mesaj üretimi kaydı:** Mesaj üretimi bitmeden sayfa kapatılırsa sonuç kayboluyor ve sayfaya dönüldüğünde yeniden üretiliyor.
