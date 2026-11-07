package content

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

// extractAll feeds chunks and returns the concatenated text per field,
// failing if any delta is invalid UTF-8.
func extractAll(t *testing.T, chunks []string) map[string]string {
	t.Helper()
	x := NewExtractor(Fields...)
	got := map[string]string{}
	for _, c := range chunks {
		for _, d := range x.Write(c) {
			if !utf8.ValidString(d.Text) {
				t.Fatalf("delta for %s is invalid UTF-8: %q (chunks %q)", d.Field, d.Text, chunks)
			}
			if d.Text == "" {
				t.Fatalf("empty delta for %s", d.Field)
			}
			got[d.Field] += d.Text
		}
	}
	return got
}

// want decodes doc with encoding/json, the reference.
func want(t *testing.T, doc string) map[string]string {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(doc), &raw); err != nil {
		t.Fatalf("test document is not valid JSON: %v\n%s", err, doc)
	}
	out := map[string]string{}
	for _, f := range Fields {
		if s, ok := raw[f].(string); ok && s != "" {
			out[f] = s
		}
	}
	return out
}

func assertFields(t *testing.T, got, want map[string]string, chunks []string) {
	t.Helper()
	for _, f := range Fields {
		if got[f] != want[f] {
			t.Fatalf("field %s:\n got %q\nwant %q\nchunks %q", f, got[f], want[f], chunks)
		}
	}
}

// escapeAll writes s as a JSON string with every non-ASCII rune as \uXXXX
// (surrogate pairs above U+FFFF), the form a model may also produce.
func escapeAll(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r < 0x20 || r > 0x7e:
			if r > 0xFFFF {
				r -= 0x10000
				fmt.Fprintf(&b, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
			} else {
				fmt.Fprintf(&b, `\u%04X`, r)
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

var sampleDocs = []string{
	// Alphabetical key order, as the model writes it.
	`{"email_body":"Merhaba {{first_name}},\nsepetinde ürünler seni bekliyor. \"SEPET10\" kodu ile %10 indirim!","email_subject":"İstanbul'a özel: sepetin seni bekliyor 🛒","sms":"Merhaba {{first_name}}! Sepetindeki ürünler için %10 indirim: SEPET10. Çağrı & bilgi"}`,
	// Schema order, extra fields, nested values and whitespace.
	"{ \"sms\" : \"Kısa \\\"SMS\\\" metni\\\\yol\" ,\n \"notes\": [\"x\", {\"sms\": \"iç içe, alınmamalı\"}], \"n\": 12.5, \"ok\": true, \"z\": null,\n \"email_subject\":\"Konu\", \"email_body\" : \"Gövde\\t\\/\\u00e7\" }",
	// Escaped non-ASCII, including a surrogate pair.
	`{"sms":` + escapeAll("Şanlıurfa'da İĞÜŞÖÇ ığüşöç 😀 son gün") + `,"email_subject":` + escapeAll("Özel fırsat 🎉") + `,"email_body":` + escapeAll("Gövde\nikinci satır ✓") + `}`,
	// Lone and mismatched surrogates decode to U+FFFD like encoding/json.
	`{"sms":"a\ud83db\udc00c\ud83d\u0041d\ud83d","email_subject":"\ud83d\ud83d\ude00","email_body":"x"}`,
	// A key equal to a field name inside another string must not confuse it.
	`{"notes":"\"sms\":\"no\"","sms":"evet","email_subject":"","email_body":"b"}`,
}

// TestExtractorEverySplit splits each document into two chunks at every
// byte offset, and into three chunks at every pair of offsets for the
// shorter documents. That covers splits inside multi-byte characters,
// escapes, \uXXXX digits and between surrogate halves.
func TestExtractorEverySplit(t *testing.T) {
	for i, doc := range sampleDocs {
		t.Run(fmt.Sprintf("doc%d", i), func(t *testing.T) {
			w := want(t, doc)
			assertFields(t, extractAll(t, []string{doc}), w, []string{doc})
			for a := 0; a <= len(doc); a++ {
				chunks := []string{doc[:a], doc[a:]}
				assertFields(t, extractAll(t, chunks), w, chunks)
			}
			if len(doc) > 220 {
				return
			}
			for a := 0; a <= len(doc); a++ {
				for b := a; b <= len(doc); b++ {
					chunks := []string{doc[:a], doc[a:b], doc[b:]}
					assertFields(t, extractAll(t, chunks), w, chunks)
				}
			}
		})
	}
}

// TestExtractorRandom: random messages, random encodings, random chunking.
func TestExtractorRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune("aıiİIğĞüÜşŞöÖçÇ {}\"\\/\n\t%&<>'😀🎉✓x1")
	randText := func() string {
		n := rng.IntN(40)
		rs := make([]rune, n)
		for i := range rs {
			rs[i] = alphabet[rng.IntN(len(alphabet))]
		}
		return string(rs)
	}
	for iter := range 2000 {
		m := Message{SMS: randText(), EmailSubject: randText(), EmailBody: randText()}
		var doc string
		if iter%2 == 0 {
			b, _ := json.Marshal(m) // escapes <, >, & as \u003c etc.
			doc = string(b)
		} else {
			doc = `{"email_body":` + escapeAll(m.EmailBody) + `,"extra":[1,{"a":"}"}],"sms":` + escapeAll(m.SMS) + `,"email_subject":` + escapeAll(m.EmailSubject) + `}`
		}
		var chunks []string
		for rest := doc; rest != ""; {
			n := 1 + rng.IntN(8)
			n = min(n, len(rest))
			chunks = append(chunks, rest[:n])
			rest = rest[n:]
		}
		assertFields(t, extractAll(t, chunks), want(t, doc), chunks)
	}
}

func TestExtractorDeltaOrderAndGrouping(t *testing.T) {
	x := NewExtractor(Fields...)
	got := x.Write(`{"email_subject":"Ko","sms":"Merhaba`)
	wantDeltas := []Delta{{FieldEmailSubject, "Ko"}, {FieldSMS, "Merhaba"}}
	if fmt.Sprint(got) != fmt.Sprint(wantDeltas) {
		t.Fatalf("deltas = %v, want %v", got, wantDeltas)
	}
	if got := x.Write(` dünya"}`); fmt.Sprint(got) != fmt.Sprint([]Delta{{FieldSMS, " dünya"}}) {
		t.Fatalf("deltas = %v", got)
	}
}

func TestExtractorHoldsBackSplitEscape(t *testing.T) {
	x := NewExtractor(FieldSMS)
	if got := x.Write(`{"sms":"A\u01`); len(got) != 1 || got[0].Text != "A" {
		t.Fatalf("first chunk deltas = %v, want only \"A\"", got)
	}
	if got := x.Write(`30B"}`); len(got) != 1 || got[0].Text != "İB" {
		t.Fatalf("second chunk deltas = %v, want \"İB\"", got)
	}
}
