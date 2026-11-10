package content

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Issue codes. All are errors: a message with any issue cannot be approved.
const (
	CodeEmpty             = "empty"
	CodeTooLong           = "too_long"
	CodeUnknownVariable   = "unknown_variable"
	CodeMalformedVariable = "malformed_variable"
	CodeMultilineSubject  = "multiline_subject"
	CodeInvalidChar       = "invalid_char"
	CodeVariableSuffix    = "variable_suffix"
)

// Issue is one validation error. Message is Turkish: it is shown to the
// marketer and sent back to the LLM in the repair prompt.
type Issue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// FieldStats reports the length of a field. WorstCaseRunes is the length
// after filling every variable with its longest possible value; the limit
// applies to it.
type FieldStats struct {
	Runes          int `json:"runes"`
	WorstCaseRunes int `json:"worst_case_runes"`
	Limit          int `json:"limit"`
}

// Result is the outcome of Validate. Message is the normalised message
// (surrounding whitespace trimmed); use it, not the input.
type Result struct {
	Message Message               `json:"message"`
	Errors  []Issue               `json:"errors"`
	Stats   map[string]FieldStats `json:"stats"`
	// WorstCase holds the values used for the worst-case render.
	// It is not serialised: the values are customer data, and Stats already
	// carries the resulting lengths.
	WorstCase Values `json:"-"`
}

// Valid reports whether the message has no errors.
func (r Result) Valid() bool { return len(r.Errors) == 0 }

// Validate checks a message against D-CONTENT. longest holds the longest
// value of each variable in the audience; a missing entry counts as empty,
// so only the fallback is considered for it. Lengths are counted in runes,
// never bytes (len would count "ş" as 2).
func Validate(m Message, longest Values) Result {
	worst := worstCase(longest)
	res := Result{Stats: map[string]FieldStats{}, WorstCase: worst}

	for _, f := range Fields {
		text := strings.TrimSpace(m.Get(f))
		res.Message.set(f, text)
		limit := Limit(f)
		errorf := func(code, format string, args ...any) {
			res.Errors = append(res.Errors, Issue{Path: f, Code: code, Message: fmt.Sprintf(format, args...)})
		}

		if text == "" {
			errorf(CodeEmpty, "boş olamaz")
			res.Stats[f] = FieldStats{Limit: limit}
			continue
		}
		if f == FieldEmailSubject && strings.ContainsAny(text, "\r\n") {
			errorf(CodeMultilineSubject, "email konusu tek satır olmalı")
		}
		if r, ok := invalidChar(text); ok {
			errorf(CodeInvalidChar, "geçersiz karakter: %U", r)
		}

		parts, terrs := parse(text, Variables())
		for _, e := range terrs {
			errorf(e.code, "%s", e.msg)
		}
		if bad, ok := suffixedVariable(parts); ok {
			errorf(CodeVariableSuffix, "Değişkene ek getirilemez: %s", bad)
		}
		stats := FieldStats{Runes: utf8.RuneCountInString(text), Limit: limit}
		if len(terrs) == 0 {
			rendered, _ := Render(text, worst) // valid template: cannot fail
			stats.WorstCaseRunes = utf8.RuneCountInString(rendered)
			if stats.WorstCaseRunes > limit {
				if stats.WorstCaseRunes == stats.Runes {
					errorf(CodeTooLong, "en fazla %d karakter olabilir, %d karakter", limit, stats.Runes)
				} else {
					errorf(CodeTooLong, "değişkenler en uzun değerle doldurulunca %d karakter oluyor, en fazla %d olabilir (şablon %d karakter)",
						stats.WorstCaseRunes, limit, stats.Runes)
				}
			}
		}
		res.Stats[f] = stats
	}
	return res
}

// suffixedVariable finds an allowed variable with a suffix written onto
// it, such as "{{first_name}}'in", and returns it as written. A Turkish
// suffix follows the sound of the word (Ayşe'nin, Ahmet'in, "Değerli
// Müşterimiz'in"), so one fixed suffix is wrong for some recipients. Only a
// letter or an apostrophe right after "}}" counts: "{{first_name}}," and
// "{{first_name}} için" are fine. A variable that is not allowed is already
// an unknown_variable error.
//
// This is a validation rule only (approval, generation repair), not part of
// parse: Render must keep sending messages approved before it existed.
func suffixedVariable(parts []part) (string, bool) {
	for i, p := range parts {
		if !slices.Contains(Variables(), p.name) || i+1 >= len(parts) {
			continue
		}
		next := parts[i+1].text
		end := strings.IndexFunc(next, func(r rune) bool { return !isSuffixRune(r) })
		if end < 0 {
			end = len(next)
		}
		if end > 0 {
			return "{{" + p.name + "}}" + next[:end], true
		}
	}
	return "", false
}

// isSuffixRune: a letter, or an apostrophe in any of its usual forms.
func isSuffixRune(r rune) bool {
	switch r {
	case '\'', '\u2019', '\u2018', '\u02BC', '`':
		return true
	}
	return unicode.IsLetter(r)
}

// invalidChar finds control characters other than newline and tab, and
// invalid UTF-8. These would be invisible to the marketer.
func invalidChar(s string) (rune, bool) {
	for _, r := range s {
		if r == utf8.RuneError {
			return r, true
		}
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return r, true
		}
	}
	return 0, false
}
