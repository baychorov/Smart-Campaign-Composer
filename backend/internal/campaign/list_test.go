package campaign

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The list filter's SQL: only fixed text, every value a $n parameter
// (R-AUD-1 applies to the marketer's input as well as the model's).
func TestListQueryWhere(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	q := ListQuery{
		Statuses: []string{ListDraft, ListInvalid, string(StatusScheduled), string(StatusCancelled)},
		ID:       987654,
		From:     from,
		To:       from.AddDate(0, 0, 10),
		Before:   123456,
	}
	where, args, err := q.where()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"987654", "123456", "2026", "scheduled", "cancelled"} {
		if strings.Contains(where, v) {
			t.Errorf("value %q in the SQL text: %s", v, where)
		}
	}
	if len(args) != 5 { // the plain statuses as one array, id, from, to, before
		t.Fatalf("args %v", args)
	}
	if got := strings.Count(where, "$"); got != len(args) {
		t.Fatalf("%d placeholders for %d args: %s", got, len(args), where)
	}

	if where, args, err := (ListQuery{}).where(); err != nil || where != "true" || len(args) != 0 {
		t.Fatalf("empty query: %q %v %v", where, args, err)
	}
}

func TestListQueryRefuses(t *testing.T) {
	for name, q := range map[string]ListQuery{
		"unknown status":   {Statuses: []string{"archived"}},
		"SQL as a status":  {Statuses: []string{"draft' OR 1=1 --"}},
		"empty status":     {Statuses: []string{""}},
		"from after to":    {From: time.Now(), To: time.Now().Add(-time.Hour)},
		"empty date range": {From: time.Unix(0, 0), To: time.Unix(0, 0)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := q.where(); !errors.Is(err, ErrBadQuery) {
				t.Fatalf("err %v, want ErrBadQuery", err)
			}
		})
	}
}
