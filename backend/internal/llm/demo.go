package llm

import (
	"context"
	"time"
)

// Demo is the Client used with LLM_PROVIDER=mock (D-LLM-MOCK): it needs no
// key and makes no network call. It answers every segment request with the
// case brief's rule and every content request with a fixed message,
// streamed in small chunks so the UI shows a realistic flow. The UI shows a
// "mock" badge when it is active.
type Demo struct {
	// Delay is the pause between chunks. Zero streams instantly (tests).
	Delay time.Duration
}

// NewDemo returns a Demo with a visible streaming speed.
func NewDemo() *Demo { return &Demo{Delay: 25 * time.Millisecond} }

const demoSegment = `{"assumptions":[],"audience_scope":"filtered","common":[{"field":"city","op":"in","type":"attribute","values":["İstanbul"]},{"event":"add_to_cart","occurred":true,"type":"event","within_days":30},{"event":"purchase","occurred":false,"type":"event","within_days":30}],"groups":[],"notes":["Demo modu: bu kural sabittir, brief'ten üretilmedi."],"status":"ok","unsupported":[]}`

const demoContent = `{"email_body":"Merhaba {{first_name}},\n\nSepetine eklediğin ürünler hâlâ seni bekliyor. Bu hafta sonuna kadar SEPET10 koduyla tüm sepetinde %10 indirim seni bekliyor.\n\nKeyifli alışverişler!","email_subject":"{{first_name}}, sepetindeki ürünler seni bekliyor","sms":"Merhaba {{first_name}}! Sepetindeki ürünler seni bekliyor. SEPET10 koduyla %10 indirim bu hafta sonuna kadar geçerli."}`

// Stream implements Client.
func (d *Demo) Stream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	text := demoSegment
	if props, ok := req.Schema["properties"].(map[string]any); ok {
		if _, isContent := props["sms"]; isContent {
			text = demoContent
		}
	}
	runes := []rune(text)
	const chunkRunes = 6
	for i := 0; i < len(runes); i += chunkRunes {
		if d.Delay > 0 {
			select {
			case <-ctx.Done():
				return Response{}, ctx.Err()
			case <-time.After(d.Delay):
			}
		} else if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		if onDelta != nil {
			onDelta(string(runes[i:min(i+chunkRunes, len(runes))]))
		}
	}
	return Response{Text: text, StopReason: StopEndTurn}, nil
}
