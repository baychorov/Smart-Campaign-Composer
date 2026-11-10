package content

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const longestName = "Muhammed Abdurrahman Şahabettin" // 31 runes, 32 bytes

var audience = Values{VarFirstName: longestName, VarCity: "Afyonkarahisar"}

func validMsg() Message {
	return Message{SMS: "Merhaba {{first_name}}!", EmailSubject: "Konu", EmailBody: "Gövde"}
}

func codes(issues []Issue, path string) []string {
	var out []string
	for _, is := range issues {
		if is.Path == path {
			out = append(out, is.Code)
		}
	}
	return out
}

func TestValidateRuneLimits(t *testing.T) {
	tr := func(n int) string { return strings.Repeat("ş", n) } // 2 bytes per rune
	tests := []struct {
		name  string
		field string
		text  string
		ok    bool
	}{
		{"sms 160 Turkish runes (320 bytes) ok", FieldSMS, tr(160), true},
		{"sms 161 Turkish runes too long", FieldSMS, tr(161), false},
		{"sms emoji counted as one rune", FieldSMS, strings.Repeat("😀", 160), true},
		{"subject 80 ok", FieldEmailSubject, tr(80), true},
		{"subject 81 too long", FieldEmailSubject, tr(81), false},
		{"body 2000 ok", FieldEmailBody, tr(2000), true},
		{"body 2001 too long", FieldEmailBody, tr(2001), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validMsg()
			m.set(tt.field, tt.text)
			res := Validate(m, audience)
			if res.Valid() != tt.ok {
				t.Fatalf("valid = %v, errors %v", res.Valid(), res.Errors)
			}
			if got := res.Stats[tt.field].Runes; got != utf8.RuneCountInString(tt.text) {
				t.Errorf("runes = %d", got)
			}
		})
	}
}

// TestValidateWorstCase: a template that fits as written but not once the
// longest name is filled in must be rejected (D-CONTENT).
func TestValidateWorstCase(t *testing.T) {
	// "{{first_name}}" is 14 runes; the longest name is 31: +17.
	base := strings.Repeat("a", 160-31) // fits exactly with the longest name
	m := validMsg()

	m.SMS = base + "{{first_name}}"
	res := Validate(m, audience)
	if !res.Valid() {
		t.Fatalf("exact fit rejected: %v", res.Errors)
	}
	if s := res.Stats[FieldSMS]; s.WorstCaseRunes != 160 || s.Runes != 160-31+14 {
		t.Errorf("stats = %+v", s)
	}

	m.SMS = base + "x{{first_name}}"
	res = Validate(m, audience)
	if got := codes(res.Errors, FieldSMS); len(got) != 1 || got[0] != CodeTooLong {
		t.Fatalf("errors = %v, want too_long", res.Errors)
	}
	if !strings.Contains(res.Errors[0].Message, "161") {
		t.Errorf("message should mention the rendered length: %q", res.Errors[0].Message)
	}
}

// TestValidateFallbackCountsWhenLonger: if every name in the audience is
// short or empty, the fallback "Değerli Müşterimiz" sets the worst case.
func TestValidateFallbackCountsWhenLonger(t *testing.T) {
	m := validMsg()
	m.EmailSubject = strings.Repeat("a", 80-18) + "{{first_name}}"
	if res := Validate(m, Values{VarFirstName: "Ali"}); !res.Valid() {
		t.Fatalf("fits with the 18-rune fallback: %v", res.Errors)
	}
	m.EmailSubject = "a" + m.EmailSubject
	res := Validate(m, Values{VarFirstName: "Ali"})
	if res.Valid() {
		t.Fatal("must count the fallback, which is longer than Ali")
	}
	if res.WorstCase[VarFirstName] != Fallbacks[VarFirstName] {
		t.Errorf("worst case = %q", res.WorstCase[VarFirstName])
	}
	// A nil map (no audience data) still applies the fallback.
	if Validate(m, nil).Valid() {
		t.Fatal("nil values must use the fallback length")
	}
}

func TestValidateVariables(t *testing.T) {
	tests := []struct {
		sms  string
		code string // "" means valid
	}{
		{"Merhaba {{first_name}}", ""},
		{"Merhaba {{ first_name }}!", ""},
		{"Merhaba {{first_name}}, {{city}}", CodeUnknownVariable}, // withdrawn, see TestValidateCityWithdrawn
		{"Merhaba {{firstname}}", CodeUnknownVariable},
		{"Merhaba {{last_name}}", CodeUnknownVariable},
		{"Merhaba {{FIRST_NAME}}", CodeUnknownVariable},
		{"Merhaba {{first_name}", CodeMalformedVariable},
		{"Merhaba first_name}}", CodeMalformedVariable},
		{"Merhaba {{}}", CodeMalformedVariable},
		{"Merhaba {{first name}}", CodeMalformedVariable},
		{"Kod: {SEPET10}", ""}, // single braces are plain text
	}
	for _, tt := range tests {
		t.Run(tt.sms, func(t *testing.T) {
			m := validMsg()
			m.SMS = tt.sms
			got := codes(Validate(m, audience).Errors, FieldSMS)
			if tt.code == "" && len(got) > 0 {
				t.Fatalf("unexpected errors %v", got)
			}
			if tt.code != "" && (len(got) == 0 || got[0] != tt.code) {
				t.Fatalf("errors %v, want %s", got, tt.code)
			}
		})
	}
}

// A suffix written onto a variable is wrong for some recipients (Ayşe'nin,
// Ahmet'in); punctuation, spaces and separate words after it are fine.
func TestValidateVariableSuffix(t *testing.T) {
	tests := []struct {
		text string
		bad  string // the reported variable as written; "" means valid
	}{
		// What the model normally writes must stay valid.
		{"Merhaba {{first_name}},", ""},
		{"Merhaba {{first_name}}! Sepetin seni bekliyor.", ""},
		{"{{first_name}}, sepetin seni bekliyor", ""},
		{"Merhaba {{first_name}}.\n\nSana özel fırsat", ""},
		{"{{first_name}} için özel indirim", ""},
		{"Merhaba {{first_name}}", ""},
		{"{{first_name}} - %30 indirim", ""},
		{"Merhaba {{first_name}}:)", ""},
		{"{{first_name}}\nyeni satır", ""},
		{"Merhaba {{first_name}} 🎉", ""},
		{"Kod: SEPET30 ({{first_name}})", ""},
		// Suffixes, with each apostrophe form and without one.
		{"{{first_name}}'in sepeti", "{{first_name}}'in"},
		{"{{first_name}}’in sepeti", "{{first_name}}’in"},
		{"{{first_name}}ʼe özel", "{{first_name}}ʼe"},
		{"{{first_name}}cim", "{{first_name}}cim"},
		{"Sevgili {{ first_name }}'cığım", "{{first_name}}'cığım"},
		{"Merhaba {{first_name}}, {{first_name}}'e özel", "{{first_name}}'e"},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			m := validMsg()
			m.EmailBody = tt.text
			res := Validate(m, audience)
			if tt.bad == "" {
				if !res.Valid() {
					t.Fatalf("unexpected errors %v", res.Errors)
				}
				return
			}
			if len(res.Errors) != 1 || res.Errors[0].Path != FieldEmailBody || res.Errors[0].Code != CodeVariableSuffix ||
				res.Errors[0].Message != "Değişkene ek getirilemez: "+tt.bad {
				t.Fatalf("errors %v, want one variable_suffix for %s", res.Errors, tt.bad)
			}
		})
	}

	// Every field is checked.
	m := Message{SMS: "{{first_name}}'e selam", EmailSubject: "{{first_name}}'in sepeti", EmailBody: "Gövde"}
	res := Validate(m, audience)
	if got := codes(res.Errors, FieldSMS); len(got) != 1 || got[0] != CodeVariableSuffix {
		t.Errorf("sms errors %v", got)
	}
	if got := codes(res.Errors, FieldEmailSubject); len(got) != 1 || got[0] != CodeVariableSuffix {
		t.Errorf("subject errors %v", got)
	}

	// Sending is not affected: a message approved before this rule still renders.
	if out, err := Render("{{first_name}}'in sepeti", Values{VarFirstName: "Ayşe"}); err != nil || out != "Ayşe'in sepeti" {
		t.Errorf("render: %q, %v", out, err)
	}
}

// {{city}} was withdrawn (2026-11-10): a new message cannot use it, in any
// field, with or without a suffix (one error, not two). A message approved
// before still renders it, so its sending does not fail.
func TestValidateCityWithdrawn(t *testing.T) {
	for _, text := range []string{"{{city}} için fırsat", "{{city}}'dan selam", "Merhaba {{first_name}}, {{ city }}"} {
		for _, f := range Fields {
			m := validMsg()
			m.set(f, text)
			res := Validate(m, audience)
			if got := codes(res.Errors, f); len(got) != 1 || got[0] != CodeUnknownVariable {
				t.Errorf("%s %q: errors %v, want [unknown_variable]", f, text, res.Errors)
			}
		}
	}
	if got := Validate(Message{SMS: "{{city}}", EmailSubject: "a", EmailBody: "b"}, audience).Errors[0].Message; !strings.Contains(got, "kullanılabilenler: {{first_name}}") || strings.Contains(got, "city}}, ") {
		t.Errorf("message %q", got)
	}

	out, err := Render("Merhaba {{first_name}}, {{city}}'de indirim!", Values{VarFirstName: "Ayşe", VarCity: "İzmir"})
	if err != nil || out != "Merhaba Ayşe, İzmir'de indirim!" {
		t.Fatalf("render of an older message: %q, %v", out, err)
	}
	if _, err := Render("{{country}}", Values{}); err == nil {
		t.Error("render accepted an unknown variable")
	}
}

func TestValidateShape(t *testing.T) {
	m := Message{SMS: "  \n ", EmailSubject: "Satır 1\nSatır 2", EmailBody: "a\x00b"}
	res := Validate(m, audience)
	if got := codes(res.Errors, FieldSMS); len(got) != 1 || got[0] != CodeEmpty {
		t.Errorf("sms errors %v, want empty", got)
	}
	if got := codes(res.Errors, FieldEmailSubject); len(got) != 1 || got[0] != CodeMultilineSubject {
		t.Errorf("subject errors %v, want multiline_subject", got)
	}
	if got := codes(res.Errors, FieldEmailBody); len(got) != 1 || got[0] != CodeInvalidChar {
		t.Errorf("body errors %v, want invalid_char", got)
	}

	// Surrounding whitespace is trimmed and not counted; newlines inside the
	// body are kept.
	m = Message{SMS: "  " + strings.Repeat("a", 160) + "\n", EmailSubject: " Konu ", EmailBody: "Satır 1\nSatır 2"}
	res = Validate(m, audience)
	if !res.Valid() {
		t.Fatalf("errors %v", res.Errors)
	}
	if res.Message.EmailSubject != "Konu" || res.Message.EmailBody != "Satır 1\nSatır 2" {
		t.Errorf("normalised message = %+v", res.Message)
	}
}

func TestRender(t *testing.T) {
	got, err := Render("Merhaba {{first_name}}, {{ city }} için fırsat", Values{VarFirstName: "Ayşe", VarCity: "İzmir"})
	if err != nil || got != "Merhaba Ayşe, İzmir için fırsat" {
		t.Fatalf("got %q, %v", got, err)
	}
	got, _ = Render("Merhaba {{first_name}}", Values{VarFirstName: "  "})
	if got != "Merhaba Değerli Müşterimiz" {
		t.Errorf("empty name should use the fallback, got %q", got)
	}
	if _, err := Render("{{unknown}}", nil); err == nil {
		t.Error("unknown variable must fail to render")
	}
}
