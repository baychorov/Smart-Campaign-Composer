package segment

import (
	"strings"
	"unicode"
)

// provinces lists Turkey's 81 provinces in canonical spelling. City values
// are matched against this list and rewritten to the canonical form, so
// "IZMIR" and "izmir" both become "İzmir". A valid province with no users
// simply yields an empty audience; a name not on the list is an error.
var provinces = []string{
	"Adana", "Adıyaman", "Afyonkarahisar", "Ağrı", "Aksaray", "Amasya", "Ankara",
	"Antalya", "Ardahan", "Artvin", "Aydın", "Balıkesir", "Bartın", "Batman",
	"Bayburt", "Bilecik", "Bingöl", "Bitlis", "Bolu", "Burdur", "Bursa",
	"Çanakkale", "Çankırı", "Çorum", "Denizli", "Diyarbakır", "Düzce", "Edirne",
	"Elazığ", "Erzincan", "Erzurum", "Eskişehir", "Gaziantep", "Giresun",
	"Gümüşhane", "Hakkari", "Hatay", "Iğdır", "Isparta", "İstanbul", "İzmir",
	"Kahramanmaraş", "Karabük", "Karaman", "Kars", "Kastamonu", "Kayseri",
	"Kilis", "Kırıkkale", "Kırklareli", "Kırşehir", "Kocaeli", "Konya", "Kütahya",
	"Malatya", "Manisa", "Mardin", "Mersin", "Muğla", "Muş", "Nevşehir", "Niğde",
	"Ordu", "Osmaniye", "Rize", "Sakarya", "Samsun", "Siirt", "Sinop", "Sivas",
	"Şanlıurfa", "Şırnak", "Tekirdağ", "Tokat", "Trabzon", "Tunceli", "Uşak",
	"Van", "Yalova", "Yozgat", "Zonguldak",
}

// provinceByKey maps foldKey(name) to the canonical name.
var provinceByKey = func() map[string]string {
	m := make(map[string]string, len(provinces))
	for _, p := range provinces {
		m[foldKey(p)] = p
	}
	return m
}()

// CanonicalCity returns the canonical spelling of a province name, ignoring
// case and Turkish diacritics. ok is false for unknown names.
func CanonicalCity(name string) (canonical string, ok bool) {
	canonical, ok = provinceByKey[foldKey(name)]
	return canonical, ok
}

// Provinces returns a copy of the canonical province list.
func Provinces() []string {
	return append([]string(nil), provinces...)
}

// foldKey reduces a string to a comparison key: lower case, Turkish letters
// mapped to ASCII, surrounding space trimmed. strings.ToLower is not enough
// for Turkish: it maps "I" to "i" (should be "ı") and "İ" to "i" plus a
// combining dot, so "IZMIR" and "İzmir" would never compare equal.
func foldKey(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch r {
		case 'İ', 'I', 'ı', 'i':
			r = 'i'
		case 'Ş', 'ş':
			r = 's'
		case 'Ğ', 'ğ':
			r = 'g'
		case 'Ü', 'ü':
			r = 'u'
		case 'Ö', 'ö':
			r = 'o'
		case 'Ç', 'ç':
			r = 'c'
		case '̇': // combining dot above, left over from "İ" in some inputs
			continue
		default:
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
