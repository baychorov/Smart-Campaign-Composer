//go:build integration

package campaign

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/platform/pgtest"
	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/seed"
)

func TestLongestValues(t *testing.T) {
	pool := pgtest.NewDB(t)

	// Empty table: no values, so only fallbacks count.
	got, err := LongestValues(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if got[content.VarFirstName] != "" || got[content.VarCity] != "" {
		t.Errorf("empty table: %v", got)
	}

	ds := seed.Generate(seed.DefaultSeed, 2000, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	if err := seed.Insert(t.Context(), pool, ds); err != nil {
		t.Fatal(err)
	}
	got, err = LongestValues(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	// The seed's name-long cohort holds the longest name on purpose.
	if got[content.VarFirstName] != seed.LongestFirstName {
		t.Errorf("first_name = %q, want %q", got[content.VarFirstName], seed.LongestFirstName)
	}
	// Reference: the longest city in the dataset, counted in runes. Byte
	// length would pick a shorter name with more Turkish letters.
	longestCity := 0
	for _, u := range ds.Users {
		longestCity = max(longestCity, utf8.RuneCountInString(u.City))
	}
	if n := utf8.RuneCountInString(got[content.VarCity]); n != longestCity {
		t.Errorf("city %q has %d runes, longest is %d", got[content.VarCity], n, longestCity)
	}
}
