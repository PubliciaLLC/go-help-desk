package server

import (
	"encoding/json"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// restTextFormat reads the optional "format" field of a REST create body.
//
// A missing key means plain (#443): existing integrations that never send the
// field see no change. Anything that is present must be exactly "plain" or
// "markdown" — an explicit "", a wrong-case "Markdown", "html" or a number are
// all refused, because a client that sends the field means it. The field is
// held as raw JSON so a number reaches this check and gets invalid_format
// instead of a generic decode error.
func restTextFormat(raw json.RawMessage) (ticket.TextFormat, error) {
	if raw == nil {
		return ticket.TextFormatPlain, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ticket.ErrInvalidTextFormat
	}
	return ticket.ParseTextFormat(s)
}
