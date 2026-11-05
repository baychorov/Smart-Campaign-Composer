# Smart Campaign Composer: Case Gereksinimleri

| Katman | Teknoloji |
|---|---|
| Backend | Go |
| Frontend | Vue.js |
| LLM | Claude API |

## 1. Amaç

Pazarlamacının doğal dille yazdığı kampanya isteğini (brief), Claude yardımıyla
bir hedef kitleye ve SMS + email mesajlarına çeviren; insan onayına sunan,
gönderen ve gönderim durumunu canlı gösteren uçtan uca bir sistem.

İş bağlamı: Platformun müşterileri her gün milyonlarca kişiye SMS ve email
gönderiyor. Kampanya hazırlamak; kitleyi tanımlamayı, her kanal için metin
yazmayı ve karakter limitlerine uymayı gerektiriyor. Hedef: pazarlamacı sadece
ne istediğini yazar, gerisini Claude önerir, **son kararı insan verir**.

## 2. Örnek brief

> "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki
> kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun."

## 3. Kullanıcı akışı

1. **Kitle çıkarılır:** Claude brief'ten bir segment kuralı üretir; arayüz kaç kişiye gideceğini gösterir.
2. **Mesajlar üretilir:** SMS ve email içeriği ekrana stream edilerek akar.
3. **İnsan onaylar:** Pazarlamacı önizler, gerekirse düzenler ve onaylar.
4. **Gönderilir:** Mesajlar kuyruk üzerinden mock provider'a gönderilir.
5. **Canlı izlenir:** Dashboard gönderim durumunu gösterir.

Gerçek kimseye mesaj gitmez. Alıcılar sahte kullanıcılardır, gönderim mock provider'a yapılır.

## 4. Fonksiyonel gereksinimler

### Kitle
- **R-AUD-1:** Claude'un ürettiği segment kuralı backend'de doğrulanır. LLM çıktısı doğrudan SQL olarak çalıştırılmaz.
- **R-AUD-2:** İzni olmayan kullanıcıya o kanaldan mesaj gönderilmez.

### İçerik
- **R-CNT-1:** SMS en fazla 160 karakter.
- **R-CNT-2:** Email konusu en fazla 80 karakter.
- **R-CNT-3:** `{{first_name}}` gibi kişiselleştirme değişkenleri desteklenir.

### Gönderim
- **R-SEND-1:** Gönderim asenkron bir kuyruk üzerinden yapılır.
- **R-SEND-2:** Kanal başına saniyede en fazla 100 mesaj.
- **R-SEND-3:** Aynı kişiye aynı mesaj iki kez gitmez.
- **R-SEND-4:** Retry stratejisi aday tarafından tasarlanır.

### Takip
- **R-OBS-1:** Canlı gönderim durumu.
- **R-OBS-2:** Metrics endpoint'i.
- **R-OBS-3:** Health check endpoint'i.

## 5. AI gereksinimleri (case'in en önemli kısmı)

Claude'u çağırmak yeterli değil; çıktısının güvenilir hale getirilmesi bekleniyor.

- **R-AI-1:** Claude'un çıktısı yapılandırılmış (JSON) olmalı ve backend'de kontrol edilmeli.
- **R-AI-2:** Testler gerçek API key olmadan, mock bir LLM ile çalışmalı.

## 6. Kaynaklar ve hazırlanacaklar

- **R-ENV-1:** API key `.env` dosyasındaki `ANTHROPIC_API_KEY` değişkeninden okunur.
- **R-ENV-2:** Repoda `.env.example` bulunur.
- Geliştirme sırasında mock LLM ile çalışılabilir; gerçek model için key Claude Console'dan (https://platform.claude.com/) alınır.
- **R-SEED-1:** Birkaç bin kullanıcılık sahte veri (seed). Hangi alanların olacağına aday karar verir (örnek: ad, email, telefon, şehir, alışveriş geçmişi, kanal izinleri).
- **R-MOCK-1:** Gerçek bir servisi taklit eden mock SMS/email provider.
- **R-MOCK-2:** Mock provider, retry ve rate limit mantığını test edebilmek için zaman zaman hata, gecikme ve 429 döndürür.

## 7. Teknoloji

Frontend Vue.js, backend Go veya PHP. Veritabanı, kuyruk ve kütüphaneler adayın seçimidir.

## 8. Teslim edilecekler

- **D-1:** GitHub veya GitLab repository
- **D-2:** `docker compose up` ile tek komutla kurulum (seed verisi ve mock provider dahil)
- **D-3:** `make test` ile çalışan testler
- **D-4:** `README.md`: kurulum ve kısa mimari özeti
- **D-5:** `DECISIONS.md`: önemli kararlar ve nedenleri
- **D-6:** `ai-sessions/`: tüm Claude session export'ları

**Kapsam notu:** Her şeyi yapmak yerine çekirdek akışın sağlam olması tercih edilir. Yetişmeyenler README'de belirtilir.

## 9. Bonus (isteğe bağlı, çekirdek akış bittikten sonra)

- Zamanlanmış gönderim
- A/B varyantları
- Kullanıcının diline göre içerik
- CI pipeline

## 10. AI araçları kullanımı

- Claude Code / Cursor gibi araçların kullanımı serbest, hatta bekleniyor. Önemli olan üretilen kodu anlamak ve sahiplenmek.
- Tüm çalışma session'larının export'ları `ai-sessions/` klasörüne eklenir (Claude Code'da `/export`, claude.ai'da paylaşım linki).
- Göndermeden önce API key ve kişisel bilgiler temizlenir.
- Kayıtlar AI kullanım miktarına göre puanlanmaz. Problemin nasıl ele alındığı ve AI çıktısının nasıl doğrulandığı değerlendirilir.

## 11. Değerlendirme ağırlıkları

| Alan | Ağırlık |
|---|---|
| LLM entegrasyonu | %25 |
| Mimari ve kararlar | %20 |
| Backend güvenilirliği | %20 |
| Frontend | %20 |
| Test ve kod kalitesi | %15 |