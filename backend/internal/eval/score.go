package eval

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"time"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/segment"
)

// Run is what one extraction of a brief produced.
type Run struct {
	Status string // a segment.Status, or StatusInvalid / StatusRefusal / StatusError
	// Rule is the validated, normalised rule; nil when Status is not a
	// segment status.
	Rule  *segment.Rule
	Error string
	// Audience is the sorted ids of the users the rule selects (reachable
	// on SMS or email); nil when the rule is not approvable.
	Audience []int64
	// Unsafe is set when the compiled SQL failed the safety check.
	Unsafe string
	// SendAt is the send time the model read from the brief, or nil.
	SendAt *time.Time

	Repairs      int
	Calls        int
	InputTokens  int64
	OutputTokens int64
	Duration     time.Duration
}

// Verdict is the score of one run.
type Verdict struct {
	Correct  bool
	Problems []string // why the run is wrong, in Turkish for the report
	// Extra and Missing compare the audience with the case's first
	// expected rule; both are 0 when the audience matched.
	Extra, Missing int
}

// Score checks one run against its case. expected holds the audience of
// each of c.Rules, in the same order.
func Score(c Case, expected [][]int64, r Run) Verdict {
	var v Verdict
	problem := func(format string, args ...any) { v.Problems = append(v.Problems, fmt.Sprintf(format, args...)) }

	if r.Unsafe != "" {
		problem("güvenlik: %s", r.Unsafe)
	}
	if !slices.Contains(c.Status, r.Status) {
		msg := fmt.Sprintf("status %s, beklenen %s", r.Status, strings.Join(c.Status, " / "))
		if r.Error != "" {
			msg += " (" + r.Error + ")"
		}
		problem("%s", msg)
	}

	switch c.Audience {
	case AudienceExact:
		switch {
		case r.Audience == nil:
			problem("onaylanabilir bir kural çıkmadı")
		case !slices.ContainsFunc(expected, func(e []int64) bool { return slices.Equal(e, r.Audience) }):
			v.Extra, v.Missing = diff(r.Audience, expected[0])
			problem("kitle farklı: %d fazla, %d eksik kişi", v.Extra, v.Missing)
		}
	case AudienceAny:
		if r.Audience == nil {
			problem("onaylanabilir bir kural çıkmadı")
		}
	case AudienceNone:
		if r.Audience != nil {
			problem("kitle beklenmiyordu ama %d kişilik onaylanabilir bir kural çıktı", len(r.Audience))
		}
	}

	if r.Rule != nil {
		for _, want := range c.Reasons {
			if !slices.ContainsFunc(r.Rule.Unsupported, func(u segment.Unsupported) bool { return u.Reason == want }) {
				problem("unsupported listesinde %s sebebi yok", want)
			}
		}
		if c.NeedsAssumption && len(r.Rule.Assumptions) == 0 {
			problem("varsayım yazılmadı")
		}
		switch want, ok := parseSendAt(c.SendAt); {
		case !ok && r.SendAt != nil:
			problem("gönderim zamanı beklenmiyordu, %s önerildi", formatSendAt(r.SendAt))
		case ok && (r.SendAt == nil || !r.SendAt.Equal(want)):
			problem("gönderim zamanı %s, beklenen %s", formatSendAt(r.SendAt), c.SendAt)
		}
	}

	v.Correct = len(v.Problems) == 0
	return v
}

// diff counts the ids only in got (extra) and only in want (missing). Both
// slices are sorted.
func diff(got, want []int64) (extra, missing int) {
	i, j := 0, 0
	for i < len(got) && j < len(want) {
		switch {
		case got[i] == want[j]:
			i++
			j++
		case got[i] < want[j]:
			extra++
			i++
		default:
			missing++
			j++
		}
	}
	return extra + len(got) - i, missing + len(want) - j
}

// fingerprint identifies a run's result for the consistency check: two
// runs agree when they have the same status, select the same users and
// suggest the same send time.
func fingerprint(r Run) string {
	h := fnv.New64a()
	for _, id := range r.Audience {
		fmt.Fprintf(h, "%d,", id)
	}
	return fmt.Sprintf("%s/%d/%x/%s", r.Status, len(r.Audience), h.Sum64(), formatSendAt(r.SendAt))
}

func parseSendAt(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	return t, s != "" && err == nil
}

// formatSendAt writes a send time in Turkey time; "yok" when there is none.
func formatSendAt(t *time.Time) string {
	if t == nil {
		return "yok"
	}
	return t.In(segment.Istanbul).Format(time.RFC3339)
}

// Consistent reports whether all runs of a brief gave the same result. A
// failed call (StatusError) never counts as consistent.
func Consistent(runs []Run) bool {
	if len(runs) == 0 {
		return false
	}
	first := fingerprint(runs[0])
	for _, r := range runs {
		if r.Status == StatusError || fingerprint(r) != first {
			return false
		}
	}
	return true
}
