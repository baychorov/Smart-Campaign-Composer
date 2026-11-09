package segment

import "testing"

func TestSummarize(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
		want string
	}{
		{"all users", Rule{Status: StatusOK, AudienceScope: ScopeAll}, "Tüm kullanıcılar"},
		{
			name: "case brief",
			rule: caseRule(),
			want: "Tüm koşullar: şehri İstanbul olan; son 30 günde sepete ürün eklemiş ve sonrasında alışveriş yapmamış",
		},
		{
			name: "independent cart and purchase",
			rule: cartNoPurchaseRule(),
			want: "Tüm koşullar: şehri İstanbul olan; son 30 günde sepete ürün eklemiş; son 30 günde alışveriş yapmamış",
		},
		{
			name: "count, range, single day, sequence with occurred",
			rule: okRule([]Condition{
				countEvent(EventPurchase, 3, 90),
				dateEvent(EventProductView, true, "2026-10-01", "2026-10-15"),
				dateEvent(EventSignup, true, "2026-11-01", "2026-11-01"),
				seqEvent(EventProductView, 7, EventAddToCart, true),
				dateEvent(EventPurchase, false, "2026-12-24", "2027-01-02"),
			}),
			want: "Tüm koşullar: son 90 günde en az 3 kez alışveriş yapmış; " +
				"1 Ekim 2026 – 15 Ekim 2026 arasında ürün incelemiş; " +
				"1 Kasım 2026 tarihinde kayıt olmuş; " +
				"son 7 günde ürün incelemiş ve sonrasında sepete ürün eklemiş; " +
				"24 Aralık 2026 – 2 Ocak 2027 arasında alışveriş yapmamış",
		},
		{
			name: "brief 17",
			rule: okRule([]Condition{event(EventAddToCart, true, 30)},
				group(city(OpIn, "İstanbul"), age(OpLte, 24)),
				group(city(OpIn, "Ankara", "İzmir", "Bursa"), age(OpGte, 41))),
			want: "Tüm koşullar: son 30 günde sepete ürün eklemiş\n" +
				"Ve şu gruplardan en az biri:\n" +
				"  1. grup: şehri İstanbul olan; 24 yaş ve altı\n" +
				"  2. grup: şehri Ankara, İzmir veya Bursa olan; 41 yaş ve üzeri",
		},
		{
			name: "groups only, negations",
			rule: okRule(nil,
				group(city(OpNotIn, "İstanbul", "Ankara")),
				group(event(EventProductView, false, 7), event(EventSignup, true, 14))),
			want: "Ve şu gruplardan en az biri:\n" +
				"  1. grup: şehri İstanbul, Ankara dışında olan\n" +
				"  2. grup: son 7 günde ürün incelememiş; son 14 günde kayıt olmuş",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Summarize(tt.rule).String(); got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}
