package server

import (
	"encoding/json"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// restCreateChannel works out how a REST create arrived (#130) from the
// credential, never from the body. a is nil for a guest. sent is the raw
// "source_channel" member of the body, nil when the request did not send one.
//
// The one value a person chooses is "phone", and only a member of staff filing
// on someone's behalf from a browser session: that is the web form's picker. A
// machine credential (API key, OAuth client) is "api" and cannot name a channel
// at all, whatever it names: a caller-asserted channel is a column nobody can
// trust in a report. Anything sent that this does not accept is refused (400)
// rather than ignored, as a ticket quietly recorded under another channel is not
// what the caller asked for. The message is returned for the 400.
func restCreateChannel(a *authmw.Actor, sent json.RawMessage, onBehalf bool) (ch ticket.SourceChannel, refusal string) {
	switch {
	case a == nil:
		ch = ticket.ChannelGuest
	case a.Machine:
		ch = ticket.ChannelAPI
	default:
		ch = ticket.ChannelWeb
	}
	if sent == nil {
		return ch, ""
	}
	if a != nil && a.Machine {
		return "", "source_channel cannot be set through the API: it is recorded from how the request arrives"
	}
	var v string
	if a == nil || json.Unmarshal(sent, &v) != nil || v != string(ticket.ChannelPhone) {
		return "", `source_channel can only be "phone", chosen by staff filing for someone else in the web app`
	}
	if !onBehalf {
		return "", `source_channel "phone" is for filing a ticket on someone's behalf (reporter_user_id or reporter_email)`
	}
	return ticket.ChannelPhone, ""
}

// followUpChannel is the channel of a follow-up: the entry point of the
// request that made it, with the same credential rule as a create. It is never
// copied from the original ticket (which may have arrived by phone) and never
// chosen by the caller.
func followUpChannel(a *authmw.Actor) ticket.SourceChannel {
	ch, _ := restCreateChannel(a, nil, false)
	return ch
}
