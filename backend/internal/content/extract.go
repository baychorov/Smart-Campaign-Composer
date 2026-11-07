package content

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Delta is newly decoded text of one top-level string field.
type Delta struct {
	Field string
	Text  string
}

// Extractor pulls the values of chosen top-level string fields out of a
// JSON object that arrives in arbitrary chunks, and returns the decoded
// text as soon as it is known (D-STREAM). It is a small byte-level state
// machine, not a full parser: it relies on the strict decode after the
// stream for validation and only has to be right for valid JSON.
//
// Guarantees, checked by splitting test documents at every byte offset:
//   - concatenating the deltas of a field gives exactly what json.Unmarshal
//     decodes, for any chunking;
//   - every Delta.Text is valid UTF-8: a multi-byte character or a \uXXXX
//     escape (or surrogate pair) split across chunks is held back until
//     complete;
//   - field order does not matter (the model writes keys alphabetically,
//     not in schema order; D-LLM-OUT), and other fields, nested objects and
//     arrays are skipped.
//
// The zero value is not usable; use NewExtractor. Not safe for concurrent
// use.
type Extractor struct {
	want map[string]bool

	depth    int  // nesting depth; the top-level object is depth 1
	inString bool // inside a string literal
	isKey    bool // the current string is a top-level key
	target   string
	expect   expectState
	lastKey  string

	keyBuf strings.Builder

	esc      bool   // after a backslash
	hex      []byte // digits of a \u escape being read
	high     rune   // pending high surrogate, 0 if none
	pending  []byte // decoded bytes of the target not yet emitted (incomplete UTF-8)
	outField string
	out      []Delta
}

type expectState int

const (
	expectKey   expectState = iota // at depth 1: next string is a key
	expectValue                    // at depth 1: next token is a value
)

// NewExtractor returns an extractor for the given top-level fields.
func NewExtractor(fields ...string) *Extractor {
	want := map[string]bool{}
	for _, f := range fields {
		want[f] = true
	}
	return &Extractor{want: want}
}

// Write consumes the next chunk and returns the decoded text it completed,
// at most one Delta per field, in the order the fields appeared.
func (x *Extractor) Write(chunk string) []Delta {
	x.out = nil
	for i := 0; i < len(chunk); i++ {
		x.step(chunk[i])
	}
	x.flush(false)
	return x.out
}

func (x *Extractor) step(c byte) {
	if x.inString {
		x.stringByte(c)
		return
	}
	switch c {
	case '"':
		x.inString = true
		x.isKey = x.depth == 1 && x.expect == expectKey
		x.target = ""
		if x.depth == 1 && x.expect == expectValue && x.want[x.lastKey] {
			x.target = x.lastKey
		}
		x.keyBuf.Reset()
	case '{', '[':
		x.depth++
		if x.depth == 1 {
			x.expect = expectKey
		}
	case '}', ']':
		x.depth--
		if x.depth == 1 {
			x.expect = expectKey // a nested value ended; ',' or '}' follows
		}
	case ':':
		if x.depth == 1 {
			x.expect = expectValue
		}
	case ',':
		if x.depth == 1 {
			x.expect = expectKey
		}
	}
	// Numbers, true/false/null and whitespace need no handling: a value is
	// always followed by ',' or '}', which reset the state.
}

func (x *Extractor) stringByte(c byte) {
	if x.hex != nil {
		x.hex = append(x.hex, c)
		if len(x.hex) == 4 {
			x.unicodeEscape(parseHex(x.hex))
			x.hex = nil
		}
		return
	}
	if x.esc {
		x.esc = false
		if c == 'u' {
			x.hex = make([]byte, 0, 4)
			return
		}
		x.emitRune(simpleEscape(c))
		return
	}
	switch c {
	case '\\':
		x.esc = true
	case '"':
		x.endString()
	default:
		x.emitByte(c)
	}
}

func (x *Extractor) endString() {
	x.inString = false
	if x.high != 0 {
		x.high = 0
		x.emitRuneNoSurrogate(utf8.RuneError)
	}
	if x.isKey {
		x.lastKey = x.keyBuf.String()
		x.isKey = false
		return
	}
	if x.target != "" {
		x.flush(true)
		x.target = ""
	}
}

// unicodeEscape handles a decoded \uXXXX, pairing surrogates the same way
// encoding/json does: a valid pair becomes one rune, anything else U+FFFD.
func (x *Extractor) unicodeEscape(r rune) {
	if x.high != 0 {
		high := x.high
		x.high = 0
		if dec := utf16.DecodeRune(high, r); dec != utf8.RuneError {
			x.emitRuneNoSurrogate(dec)
			return
		}
		x.emitRuneNoSurrogate(utf8.RuneError)
	}
	if utf16.IsSurrogate(r) {
		if r < 0xDC00 { // high surrogate: wait for its pair
			x.high = r
			return
		}
		r = utf8.RuneError
	}
	x.emitRuneNoSurrogate(r)
}

// emitRune is used for everything except \u escapes: a pending high
// surrogate without its pair becomes U+FFFD first.
func (x *Extractor) emitRune(r rune) {
	if x.high != 0 {
		x.high = 0
		x.emitRuneNoSurrogate(utf8.RuneError)
	}
	x.emitRuneNoSurrogate(r)
}

func (x *Extractor) emitRuneNoSurrogate(r rune) {
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], r)
	for _, b := range buf[:n] {
		x.emitByteRaw(b)
	}
}

func (x *Extractor) emitByte(c byte) {
	if x.high != 0 {
		x.high = 0
		x.emitRuneNoSurrogate(utf8.RuneError)
	}
	x.emitByteRaw(c)
}

func (x *Extractor) emitByteRaw(c byte) {
	if x.isKey {
		x.keyBuf.WriteByte(c)
		return
	}
	if x.target == "" {
		return
	}
	if x.outField != x.target {
		x.flush(true)
		x.outField = x.target
	}
	x.pending = append(x.pending, c)
}

// flush moves pending bytes to the output. Unless final, an incomplete
// UTF-8 sequence at the end stays pending for the next chunk.
func (x *Extractor) flush(final bool) {
	if len(x.pending) == 0 {
		return
	}
	n := len(x.pending)
	if !final {
		n = completeUTF8Prefix(x.pending)
	}
	if n == 0 {
		return
	}
	text := string(x.pending[:n])
	x.pending = append(x.pending[:0], x.pending[n:]...)
	if k := len(x.out); k > 0 && x.out[k-1].Field == x.outField {
		x.out[k-1].Text += text
	} else {
		x.out = append(x.out, Delta{Field: x.outField, Text: text})
	}
}

// completeUTF8Prefix returns the length of b without a trailing incomplete
// UTF-8 sequence.
func completeUTF8Prefix(b []byte) int {
	for back := 1; back <= utf8.UTFMax && back <= len(b); back++ {
		i := len(b) - back
		if utf8.RuneStart(b[i]) {
			if utf8.FullRune(b[i:]) {
				return len(b)
			}
			return i
		}
	}
	return len(b)
}

func simpleEscape(c byte) rune {
	switch c {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'r':
		return '\r'
	case 'b':
		return '\b'
	case 'f':
		return '\f'
	default: // '"', '\\', '/'
		return rune(c)
	}
}

func parseHex(h []byte) rune {
	var r rune
	for _, c := range h {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		default:
			return utf8.RuneError
		}
	}
	return r
}
