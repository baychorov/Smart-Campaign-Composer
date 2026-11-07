package content

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Personalisation variables (R-CNT-3). Only these names are allowed; any
// other {{name}} is an error, so a typo never reaches a customer as raw
// template text.
const (
	VarFirstName = "first_name"
	VarCity      = "city"
)

// Fallbacks replace an empty value at render time (D-CONTENT). city is NOT
// NULL in the schema, so it needs none.
var Fallbacks = map[string]string{
	VarFirstName: "Değerli Müşterimiz",
	VarCity:      "",
}

// Variables lists the allowed variable names.
func Variables() []string {
	return []string{VarFirstName, VarCity}
}

// Values maps a variable name to a value for rendering.
type Values map[string]string

// part is a piece of a parsed template: literal text or a variable.
type part struct {
	text string
	name string // non-empty for a variable
}

// templateError describes a problem found while parsing a template.
type templateError struct {
	code string
	msg  string
}

// parse splits a template into parts. Variables are {{name}} with optional
// spaces inside the braces. It reports unknown names and malformed braces.
func parse(s string) ([]part, []templateError) {
	var parts []part
	var errs []templateError
	for {
		open := strings.Index(s, "{{")
		if open < 0 {
			break
		}
		closeIdx := strings.Index(s[open+2:], "}}")
		if closeIdx < 0 {
			errs = append(errs, templateError{CodeMalformedVariable, "\"{{\" kapatılmamış"})
			break
		}
		raw := s[open+2 : open+2+closeIdx]
		name := strings.TrimSpace(raw)
		if name == "" || strings.ContainsAny(name, "{} \t\n") {
			errs = append(errs, templateError{CodeMalformedVariable, fmt.Sprintf("geçersiz değişken: {{%s}}", raw)})
		} else if !slices.Contains(Variables(), name) {
			errs = append(errs, templateError{CodeUnknownVariable,
				fmt.Sprintf("bilinmeyen değişken {{%s}}; kullanılabilenler: {{%s}}", name, strings.Join(Variables(), "}}, {{"))})
		}
		parts = append(parts, part{text: s[:open]}, part{name: name})
		s = s[open+2+closeIdx+2:]
	}
	if strings.Contains(s, "}}") {
		errs = append(errs, templateError{CodeMalformedVariable, "\"}}\" için açılış yok"})
	}
	return append(parts, part{text: s}), errs
}

// Render fills the variables of a valid template. Empty values use the
// fallback. It returns an error for an invalid template; callers normally
// validate first.
func Render(tmpl string, v Values) (string, error) {
	parts, errs := parse(tmpl)
	if len(errs) > 0 {
		return "", fmt.Errorf("render template: %s", errs[0].msg)
	}
	var b strings.Builder
	for _, p := range parts {
		if p.name == "" {
			b.WriteString(p.text)
			continue
		}
		val := v[p.name]
		if strings.TrimSpace(val) == "" {
			val = Fallbacks[p.name]
		}
		b.WriteString(val)
	}
	return b.String(), nil
}

// WorstCaseRunes is how many runes a variable can render to at most.
func WorstCaseRunes(longest Values, name string) int {
	return utf8.RuneCountInString(worstCase(longest)[name])
}

// worstCase returns, per variable, the longest text it can render to: the
// longest value in the audience or the fallback, whichever is longer.
func worstCase(longest Values) Values {
	out := Values{}
	for _, name := range Variables() {
		v, fb := longest[name], Fallbacks[name]
		if utf8.RuneCountInString(fb) > utf8.RuneCountInString(v) {
			v = fb
		}
		out[name] = v
	}
	return out
}
