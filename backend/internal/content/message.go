// Package content defines campaign messages and their rules: character
// limits counted in runes after a worst-case render of personalisation
// variables (R-CNT-1..3, DECISIONS D-CONTENT), and an incremental JSON
// string extractor for streaming model output (D-STREAM). It is pure: no
// I/O and no imports of other business packages.
package content

// Message is the SMS and email copy of a campaign. Text may contain
// {{variables}}; see Variables.
type Message struct {
	SMS          string `json:"sms"`
	EmailSubject string `json:"email_subject"`
	EmailBody    string `json:"email_body"`
}

// JSON field names, also used as Issue paths and stream field ids.
const (
	FieldSMS          = "sms"
	FieldEmailSubject = "email_subject"
	FieldEmailBody    = "email_body"
)

// Fields lists the message fields in display order.
var Fields = []string{FieldSMS, FieldEmailSubject, FieldEmailBody}

// Limits in runes, applied to the worst-case render. The email body has no
// limit in the case; 2000 keeps a runaway generation in check.
const (
	MaxSMSRunes          = 160 // R-CNT-1
	MaxEmailSubjectRunes = 80  // R-CNT-2
	MaxEmailBodyRunes    = 2000
)

// Limit returns the rune limit of a field.
func Limit(field string) int {
	switch field {
	case FieldSMS:
		return MaxSMSRunes
	case FieldEmailSubject:
		return MaxEmailSubjectRunes
	default:
		return MaxEmailBodyRunes
	}
}

// Get returns the text of a field.
func (m Message) Get(field string) string {
	switch field {
	case FieldSMS:
		return m.SMS
	case FieldEmailSubject:
		return m.EmailSubject
	default:
		return m.EmailBody
	}
}

func (m *Message) set(field, v string) {
	switch field {
	case FieldSMS:
		m.SMS = v
	case FieldEmailSubject:
		m.EmailSubject = v
	default:
		m.EmailBody = v
	}
}
