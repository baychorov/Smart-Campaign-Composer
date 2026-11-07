package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/baychorov/Smart-Campaign-Composer/backend/internal/content"
)

const longestName = "Muhammed Abdurrahman Şahabettin" // seed.LongestFirstName, 31 runes

var longest = content.Values{content.VarFirstName: longestName, content.VarCity: "Afyonkarahisar"}

var contentIn = ContentInput{
	Brief:    caseBrief,
	Audience: "Tüm koşullar: İstanbul'da yaşayan; son 30 günde sepete ürün eklemiş; son 30 günde alışveriş yapmamış",
	Notes:    []string{"Samimi bir dil istendi."},
}

func messageJSON(t *testing.T, m content.Message) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var goodMessage = content.Message{
	SMS:          "Merhaba {{first_name}}! Sepetindeki ürünler seni bekliyor: SEPET10 ile %10 indirim.",
	EmailSubject: "{{first_name}}, sepetin seni bekliyor",
	EmailBody:    "Merhaba {{first_name}},\n\n\"SEPET10\" koduyla %10 indirim.",
}

// recorder collects events and rebuilds the text a UI would show.
type recorder struct {
	events []ContentEvent
	shown  map[string]string
}

func newRecorder() *recorder { return &recorder{shown: map[string]string{}} }

func (r *recorder) emit(e ContentEvent) {
	r.events = append(r.events, e)
	switch e.Type {
	case ContentDelta:
		r.shown[e.Field] += e.Text
	case ContentReset:
		r.shown = map[string]string{}
	}
}

func (r *recorder) types() []ContentEventType {
	var out []ContentEventType
	for _, e := range r.events {
		if len(out) > 0 && e.Type == ContentDelta && out[len(out)-1] == ContentDelta {
			continue // collapse runs of deltas
		}
		out = append(out, e.Type)
	}
	return out
}

func TestContentSchemaMatchesMessage(t *testing.T) {
	s := ContentSchema()
	props := checkObject(t, "message", s, jsonFields(reflect.TypeFor[content.Message]()))
	for name, p := range props {
		if obj(t, p, name)["type"] != "string" {
			t.Errorf("%s must be a string", name)
		}
	}
	got := slices.Sorted(func(yield func(string) bool) {
		for k := range props {
			if !yield(k) {
				return
			}
		}
	})
	want := slices.Sorted(slices.Values(content.Fields))
	if !slices.Equal(got, want) {
		t.Errorf("schema fields %v, content.Fields %v", got, want)
	}
}

// TestGenerateContentStreamsWhatIsValidated: the deltas a UI shows add up
// to exactly the validated message (D-STREAM), even with escapes and
// Turkish characters split across chunks.
func TestGenerateContentStreamsWhatIsValidated(t *testing.T) {
	raw := messageJSON(t, goodMessage)
	// Split every 7 bytes: cuts multi-byte runes and \" escapes.
	var chunks []string
	for i := 0; i < len(raw); i += 7 {
		chunks = append(chunks, raw[i:min(i+7, len(raw))])
	}
	fake := NewFake(FakeResponse{Chunks: chunks})
	rec := newRecorder()

	res, err := GenerateContent(context.Background(), fake, contentIn, longest, rec.emit)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.types(); !slices.Equal(got, []ContentEventType{ContentStarted, ContentDelta, ContentValidated}) {
		t.Fatalf("event sequence %v", got)
	}
	final := rec.events[len(rec.events)-1].Result
	for _, f := range content.Fields {
		if rec.shown[f] != final.Message.Get(f) {
			t.Errorf("%s shown %q, validated %q", f, rec.shown[f], final.Message.Get(f))
		}
	}
	if res.Validation.Stats[content.FieldSMS].WorstCaseRunes <= res.Validation.Stats[content.FieldSMS].Runes {
		t.Error("worst-case length should exceed the template length with the long name")
	}
}

func TestGenerateContentRepair(t *testing.T) {
	tooLong := goodMessage
	// Fits as written (146 runes) but not with the 31-rune name (163).
	tooLong.SMS = "{{first_name}}" + strings.Repeat("a", 132)
	fake := NewFake(Text(messageJSON(t, tooLong)), Text(messageJSON(t, goodMessage)))
	rec := newRecorder()

	res, err := GenerateContent(context.Background(), fake, contentIn, longest, rec.emit)
	if err != nil {
		t.Fatal(err)
	}
	want := []ContentEventType{ContentStarted, ContentDelta, ContentReset, ContentDelta, ContentValidated}
	if got := rec.types(); !slices.Equal(got, want) {
		t.Fatalf("event sequence %v, want %v", got, want)
	}
	if res.Repairs != 1 {
		t.Errorf("repairs = %d", res.Repairs)
	}
	repair := fake.Calls()[1].Prompt
	for _, s := range []string{"[" + content.CodeTooLong + "]", "<previous_answer>", "163"} {
		if !strings.Contains(repair, s) {
			t.Errorf("repair prompt lacks %q", s)
		}
	}
	if rec.shown[content.FieldSMS] != goodMessage.SMS {
		t.Errorf("after reset the UI shows %q", rec.shown[content.FieldSMS])
	}
}

func TestGenerateContentInvalid(t *testing.T) {
	t.Run("gives up after max repairs", func(t *testing.T) {
		bad := goodMessage
		bad.SMS = "Merhaba {{last_name}}"
		var replies []FakeResponse
		for range MaxRepairs + 1 {
			replies = append(replies, Text(messageJSON(t, bad)))
		}
		rec := newRecorder()
		res, err := GenerateContent(context.Background(), NewFake(replies...), contentIn, longest, rec.emit)
		if !errors.Is(err, ErrInvalidOutput) {
			t.Fatalf("err = %v", err)
		}
		last := rec.events[len(rec.events)-1]
		if last.Type != ContentInvalid || len(last.Issues) == 0 || last.Issues[0].Code != content.CodeUnknownVariable {
			t.Fatalf("last event %+v", last)
		}
		if res.Repairs != MaxRepairs {
			t.Errorf("repairs = %d", res.Repairs)
		}
		for _, e := range rec.events {
			if e.Type == ContentValidated {
				t.Fatal("an invalid message must never be marked validated")
			}
		}
	})

	t.Run("unknown field is repaired", func(t *testing.T) {
		fake := NewFake(
			Text(`{"sms":"a","email_subject":"b","email_body":"c","html":"<b>x</b>"}`),
			Text(messageJSON(t, goodMessage)),
		)
		rec := newRecorder()
		if _, err := GenerateContent(context.Background(), fake, contentIn, longest, rec.emit); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fake.Calls()[1].Prompt, "["+IssueInvalidJSON+"]") {
			t.Error("repair prompt lacks invalid_json")
		}
	})

	t.Run("missing field is repaired", func(t *testing.T) {
		fake := NewFake(Text(`{"sms":"a","email_subject":"b"}`), Text(messageJSON(t, goodMessage)))
		if _, err := GenerateContent(context.Background(), fake, contentIn, longest, newRecorder().emit); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fake.Calls()[1].Prompt, "email_body ["+content.CodeEmpty+"]") {
			t.Errorf("repair prompt:\n%s", fake.Calls()[1].Prompt)
		}
	})
}

func TestGenerateContentErrors(t *testing.T) {
	t.Run("refusal: no terminal event, error returned", func(t *testing.T) {
		fake := NewFake(FakeResponse{Chunks: []string{`{"sms":"x`}, StopReason: StopRefusal, Refusal: &Refusal{Category: "cyber"}})
		rec := newRecorder()
		_, err := GenerateContent(context.Background(), fake, contentIn, longest, rec.emit)
		var re *RefusalError
		if !errors.As(err, &re) {
			t.Fatalf("err = %v", err)
		}
		for _, e := range rec.events {
			if e.Type == ContentValidated || e.Type == ContentInvalid {
				t.Fatalf("unexpected terminal event %v", e.Type)
			}
		}
	})

	t.Run("stream restart resets the UI", func(t *testing.T) {
		fake := NewFake(
			FakeResponse{Chunks: []string{`{"sms":"Yarım`}, Err: fmt.Errorf("%w: eof", ErrStreamInterrupted)},
			Text(messageJSON(t, goodMessage)),
		)
		rec := newRecorder()
		if _, err := GenerateContent(context.Background(), fake, contentIn, longest, rec.emit); err != nil {
			t.Fatal(err)
		}
		want := []ContentEventType{ContentStarted, ContentDelta, ContentReset, ContentDelta, ContentValidated}
		if got := rec.types(); !slices.Equal(got, want) {
			t.Fatalf("event sequence %v, want %v", got, want)
		}
		if rec.shown[content.FieldSMS] != goodMessage.SMS {
			t.Errorf("shown %q", rec.shown[content.FieldSMS])
		}
	})

	t.Run("invalid brief: no call, no events", func(t *testing.T) {
		fake := NewFake()
		rec := newRecorder()
		_, err := GenerateContent(context.Background(), fake, ContentInput{Brief: " "}, longest, rec.emit)
		if !errors.Is(err, ErrInvalidBrief) || len(fake.Calls()) != 0 || len(rec.events) != 0 {
			t.Fatalf("err %v, calls %d, events %d", err, len(fake.Calls()), len(rec.events))
		}
	})
}

// TestContentPromptHasNoPersonalData: the model gets the length budget of
// each variable, never the longest name itself.
func TestContentPromptHasNoPersonalData(t *testing.T) {
	fake := NewFake(Text(messageJSON(t, goodMessage)))
	if _, err := GenerateContent(context.Background(), fake, contentIn, longest, newRecorder().emit); err != nil {
		t.Fatal(err)
	}
	req := fake.Calls()[0]
	if strings.Contains(req.Prompt, longestName) || strings.Contains(req.Prompt, "Afyonkarahisar") {
		t.Fatal("prompt contains a customer value")
	}
	for _, s := range []string{"{{first_name}}: 31", "{{city}}: 14", "<brief>", "<audience>", "Samimi bir dil", "İstanbul'da yaşayan"} {
		if !strings.Contains(req.Prompt, s) {
			t.Errorf("prompt lacks %q:\n%s", s, req.Prompt)
		}
	}
	if req.System != contentSystemPrompt || req.Effort != EffortLow || req.Schema["properties"].(map[string]any)["sms"] == nil {
		t.Errorf("request = %+v", req)
	}
}

func TestDemoClient(t *testing.T) {
	d := &Demo{}
	t.Run("segment", func(t *testing.T) {
		res, err := ExtractSegment(context.Background(), d, caseBrief)
		if err != nil || !res.Validation.Approvable() {
			t.Fatalf("demo rule rejected: %v %+v", err, res.Issues)
		}
	})
	t.Run("content passes worst case with the longest seed name", func(t *testing.T) {
		rec := newRecorder()
		res, err := GenerateContent(context.Background(), d, contentIn, longest, rec.emit)
		if err != nil {
			t.Fatalf("demo content rejected: %v %+v", err, res.Issues)
		}
		if rec.shown[content.FieldSMS] != res.Validation.Message.SMS {
			t.Error("demo deltas do not add up to the message")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := NewDemo().Stream(ctx, Request{}, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
}
