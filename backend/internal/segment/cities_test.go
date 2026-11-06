package segment

import (
	"strings"
	"testing"
)

func TestProvinceList(t *testing.T) {
	if len(provinces) != 81 {
		t.Fatalf("got %d provinces, want 81", len(provinces))
	}
	// Folding must not merge two provinces into one key.
	if len(provinceByKey) != len(provinces) {
		t.Fatalf("fold keys collide: %d keys for %d provinces", len(provinceByKey), len(provinces))
	}
}

func TestCanonicalCity(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"İstanbul", "İstanbul", true},
		{"istanbul", "İstanbul", true},
		{"ISTANBUL", "İstanbul", true},
		{"İSTANBUL", "İstanbul", true},
		{strings.ToLower("İzmir"), "İzmir", true}, // "i̇zmir" with a combining dot
		{"IZMIR", "İzmir", true},
		{"izmir", "İzmir", true},
		{"ŞANLIURFA", "Şanlıurfa", true},
		{"sanliurfa", "Şanlıurfa", true},
		{"IĞDIR", "Iğdır", true},
		{"igdir", "Iğdır", true},
		{"  Ankara ", "Ankara", true},
		{"Bolu", "Bolu", true}, // valid even though the seed has no users there
		{"Paris", "", false},
		{"", "", false},
		{"'; DROP TABLE users;--", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := CanonicalCity(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Errorf("CanonicalCity(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}
