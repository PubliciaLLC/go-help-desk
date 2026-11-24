package notify

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// The webhook side of the two SLA events (#670): what the raw body carries for
// each of the three shapes the sweep raises, that the keys survive the outbox
// and a Redeliver, who is told, and the chat headlines.

var (
	slaTicketID = uuid.MustParse("11111111-2222-3333-4444-555555555555")
	slaPolicyID = uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	slaRecordID = uuid.MustParse("99999999-8888-7777-6666-555555555555")
	slaWhen     = time.Date(2026, 11, 23, 9, 30, 0, 0, time.UTC)
)

// slaAlertEvent is an event the way the sweep raises it (slaEvent in
// domain/sla): no actor, ids and enums in Payload, the typed field for mail,
// the ticket as read in the transaction. atRisk is empty for an sla.at_risk
// event, whose targets are the at-risk ones.
func slaAlertEvent(typ notification.EventType, targets, atRisk []string) notification.Event {
	ev := notification.Event{
		Type: typ, Kinds: []notification.EventType{typ}, TicketID: slaTicketID, OccurredAt: slaWhen,
		TrackingNumber: "GHD-2026-000007", Subject: "SECRET-SUBJECT", StatusName: "SECRET-STATUS",
		Payload: map[string]any{"targets": targets, "policy_id": slaPolicyID, "record_id": slaRecordID},
		Ticket:  &notification.TicketState{CategoryID: uuid.New(), Priority: "high"},
		SLA:     &notification.SLAAlert{},
	}
	enums := func(in []string) (out []notification.SLATarget) {
		for _, t := range in {
			out = append(out, notification.SLATarget(t))
		}
		return out
	}
	if typ == notification.EventSLABreached {
		ev.SLA.Breached, ev.SLA.AtRisk = enums(targets), enums(atRisk)
		if len(atRisk) > 0 {
			ev.Payload["at_risk_targets"] = atRisk
		}
	} else {
		ev.SLA.AtRisk = enums(targets)
	}
	return ev
}

func breachOnly() notification.Event {
	return slaAlertEvent(notification.EventSLABreached, []string{"response"}, nil)
}

func breachAndAtRisk() notification.Event {
	return slaAlertEvent(notification.EventSLABreached, []string{"response"}, []string{"resolution"})
}

func atRiskOnly() notification.Event {
	return slaAlertEvent(notification.EventSLAAtRisk, []string{"response", "resolution"}, nil)
}

// The three shapes, byte for byte. The event goes through the outbox codec
// too, because that is what a delivery reads: the lists come back as []any and
// the ids as strings, and the body must not change for it.
func TestRawBody_SLAShapes(t *testing.T) {
	const (
		ids  = `"policy_id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","record_id":"99999999-8888-7777-6666-555555555555"`
		mid  = `","TicketID":"11111111-2222-3333-4444-555555555555","ActorID":null,"Payload":{`
		tail = `},"OccurredAt":"2026-11-23T09:30:00Z"}`
	)
	tests := []struct {
		name string
		ev   notification.Event
		want string
	}{
		{"breach only", breachOnly(),
			`{"Type":"sla.breached` + mid + ids + `,"targets":["response"]` + tail},
		{"breach plus at_risk_targets", breachAndAtRisk(),
			`{"Type":"sla.breached` + mid + `"at_risk_targets":["resolution"],` + ids + `,"targets":["response"]` + tail},
		{"at risk only", atRiskOnly(),
			`{"Type":"sla.at_risk` + mid + ids + `,"targets":["response","resolution"]` + tail},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stored, err := encodeEvent(tc.ev)
			require.NoError(t, err)
			decoded, err := decodeEvent(stored)
			require.NoError(t, err)
			for _, e := range []notification.Event{tc.ev, decoded} {
				body, err := rawBody(e)
				require.NoError(t, err)
				require.Equal(t, tc.want, string(body))
				require.NotContains(t, string(body), "SECRET")
				require.NotContains(t, string(body), "GHD-2026", "no tracking number on the wire")
			}
		})
	}
}

// at_risk_targets belongs to sla.breached alone, and is absent, never an empty
// list, when the pass named none.
func TestRawKeysFor_SLA(t *testing.T) {
	require.ElementsMatch(t, []string{"targets", "at_risk_targets", "policy_id", "record_id"}, rawKeysFor(notification.EventSLABreached))
	require.ElementsMatch(t, []string{"targets", "policy_id", "record_id"}, rawKeysFor(notification.EventSLAAtRisk))

	// A key an at-risk event should never have is kept off the wire.
	ev := atRiskOnly()
	ev.Payload["at_risk_targets"] = []string{"resolution"}
	body, err := rawBody(ev)
	require.NoError(t, err)
	require.NotContains(t, string(body), "at_risk_targets")

	body, err = rawBody(breachOnly())
	require.NoError(t, err)
	require.NotContains(t, string(body), "at_risk_targets", "absent, not an empty list")
}

// targets is a list on the wire after the outbox and after the copy kept for a
// Redeliver, never a string or an object.
func TestSLATargetsSurviveTheCodecAsAList(t *testing.T) {
	stored, err := encodeEvent(breachAndAtRisk())
	require.NoError(t, err)
	ev, err := decodeEvent(stored)
	require.NoError(t, err)
	require.Equal(t, []any{"response"}, ev.Payload["targets"])
	require.Equal(t, []any{"resolution"}, ev.Payload["at_risk_targets"])
	require.Equal(t, breachAndAtRisk().SLA, ev.SLA, "the typed field too")

	hist, err := encodeEvent(historyEvent(ev))
	require.NoError(t, err)
	kept, err := decodeEvent(hist)
	require.NoError(t, err)
	body, err := rawBody(kept)
	require.NoError(t, err)
	var got struct{ Payload map[string]any }
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, map[string]any{
		"targets": []any{"response"}, "at_risk_targets": []any{"resolution"},
		"policy_id": slaPolicyID.String(), "record_id": slaRecordID.String(),
	}, got.Payload)
	require.Nil(t, kept.SLA, "the copy kept for a replay holds no typed mail field")
}

// The copy kept for Redeliver holds the keys, and a replay is the same body.
func TestRedeliver_SLAKeepsItsKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		ev   notification.Event
		keys []string
	}{
		{"breach only", breachOnly(), []string{"targets", "policy_id", "record_id"}},
		{"breach plus at risk", breachAndAtRisk(), []string{"targets", "at_risk_targets", "policy_id", "record_id"}},
		{"at risk only", atRiskOnly(), []string{"targets", "policy_id", "record_id"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := newReceiver(t, 200)
			bodies := capture(rc)
			hook := hookOn(rc.srv.URL, string(tc.ev.Type))
			rig := newQueueRig(t, rc.srv.Client(), hook)

			require.NoError(t, rig.front.Dispatch(context.Background(), tc.ev))
			rig.drain(t)
			require.Equal(t, 1, rc.count())
			require.Equal(t, string(tc.ev.Type), rc.header(0, "X-GHD-Event"))
			first := rig.hooks.last()

			require.NoError(t, rig.disp.Redeliver(context.Background(), hook.ID, first.DeliveryID))
			rig.drain(t)
			require.Equal(t, 2, rc.count())
			require.Equal(t, bodyWithoutTimes(t, bodies()[0]), bodyWithoutTimes(t, bodies()[1]), "a replay carries the same keys")
			var got struct{ Payload map[string]any }
			require.NoError(t, json.Unmarshal([]byte(bodies()[1]), &got))
			require.ElementsMatch(t, tc.keys, keysOf(got.Payload))
			require.IsType(t, []any{}, got.Payload["targets"])
		})
	}
}

// A replay of a mixed event keeps the chat headline: the key it is read from is
// in the history.
func TestRedeliver_SLAChatHeadlineKeepsTheMixedCase(t *testing.T) {
	rc := newReceiver(t, 200)
	bodies := capture(rc)
	hook := slackHook(rc.srv.URL, "sla.breached")
	rig := newQueueRig(t, rc.srv.Client(), hook)

	require.NoError(t, rig.front.Dispatch(context.Background(), breachAndAtRisk()))
	rig.drain(t)
	require.Contains(t, bodies()[0], "SLA breached; another target at risk")
	require.NoError(t, rig.disp.Redeliver(context.Background(), hook.ID, rig.hooks.last().DeliveryID))
	rig.drain(t)
	require.Len(t, bodies(), 2)
	require.Contains(t, bodies()[1], "SLA breached; another target at risk")
}

// "*" and a legacy empty list are told of both events, as ordinary names; a hook
// that lists one is told of that one only.
func TestSubscribedKinds_SLAEvents(t *testing.T) {
	mixed := breachAndAtRisk()
	tests := []struct {
		name   string
		events []string
		ev     notification.Event
		want   bool
	}{
		{"star, breached", []string{"*"}, breachOnly(), true},
		{"star, at risk", []string{"*"}, atRiskOnly(), true},
		{"star, mixed", []string{"*"}, mixed, true},
		{"empty list, breached", nil, breachOnly(), true},
		{"empty list, at risk", nil, atRiskOnly(), true},
		{"named, breached", []string{"sla.breached"}, breachOnly(), true},
		{"named, at risk", []string{"sla.at_risk"}, atRiskOnly(), true},
		{"breached hook is not told of at risk", []string{"sla.breached"}, atRiskOnly(), false},
		{"at risk hook is not told of a breach", []string{"sla.at_risk"}, breachOnly(), false},
		// The documented known limit: a pass that breached one target and put
		// the other at risk is one sla.breached, so a hook that lists only
		// sla.at_risk never hears of that at-risk crossing, and its stamp is set.
		{"at risk hook is not told of a mixed pass", []string{"sla.at_risk"}, mixed, false},
		{"both names, mixed", []string{"sla.at_risk", "sla.breached"}, mixed, true},
		{"a ticket name does not cover it", []string{"ticket.assigned"}, breachOnly(), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := subscribedKind(hookOn("http://x", tc.events...), tc.ev)
			require.Equal(t, tc.want, ok)
		})
	}
}

// Dispatched through the queue: the star hook gets both events, the at-risk-only
// hook gets the at-risk event and not the mixed breach.
func TestOneDelivery_SLAEventsReachTheHooksThatSubscribe(t *testing.T) {
	star, atRisk, breached := newReceiver(t, 200), newReceiver(t, 200), newReceiver(t, 200)
	rig := newQueueRig(t, star.srv.Client(),
		hookOn(star.srv.URL, "*"), hookOn(atRisk.srv.URL, "sla.at_risk"), hookOn(breached.srv.URL, "sla.breached"))

	require.NoError(t, rig.front.Dispatch(context.Background(), breachAndAtRisk()))
	rig.drain(t)
	require.Equal(t, 1, star.count())
	require.Equal(t, "sla.breached", star.header(0, "X-GHD-Event"))
	require.Equal(t, 1, breached.count())
	require.Zero(t, atRisk.count(), "the at-risk crossing rode on the breach")

	require.NoError(t, rig.front.Dispatch(context.Background(), atRiskOnly()))
	rig.drain(t)
	require.Equal(t, 2, star.count())
	require.Equal(t, "sla.at_risk", star.header(1, "X-GHD-Event"))
	require.Equal(t, 1, atRisk.count())
	require.Equal(t, 1, breached.count())
}

// The per-hook ticket filter reads Event.Ticket, which the sweep fills in, as it
// does for any change.
func TestQueueFilter_SLAEventsReadTheTicketState(t *testing.T) {
	match, miss, inverted := newReceiver(t, 200), newReceiver(t, 503), newReceiver(t, 200)
	hm := filtered(hookOn(match.srv.URL, "sla.breached"), notification.WebhookFilter{Priorities: []string{"high"}})
	hx := filtered(hookOn(miss.srv.URL, "*"), notification.WebhookFilter{Priorities: []string{"low"}})
	hi := filtered(hookOn(inverted.srv.URL, "*"), notification.WebhookFilter{Priorities: []string{"high"}, Invert: true})
	rig := newQueueRig(t, match.srv.Client(), hm, hx, hi)
	spy := spyOn(rig)

	for _, ev := range []notification.Event{breachOnly(), atRiskOnly()} {
		require.NoError(t, rig.front.Dispatch(context.Background(), ev))
	}
	rig.drain(t)

	require.Equal(t, []uuid.UUID{hm.ID}, spy.targets(t), "only the hook whose filter admits a high ticket, for the breach it names")
	require.Equal(t, 1, match.count())
	require.Zero(t, miss.count())
	require.Zero(t, inverted.count())
}

func TestHeadline_SLA(t *testing.T) {
	tests := []struct {
		name string
		ev   notification.Event
		want string
	}{
		{"breached", breachOnly(), "SLA breached"},
		{"breached, other target at risk", breachAndAtRisk(), "SLA breached; another target at risk"},
		{"at risk", atRiskOnly(), "SLA at risk"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// As delivered: through the outbox, where the lists are []any.
			stored, err := encodeEvent(tc.ev)
			require.NoError(t, err)
			decoded, err := decodeEvent(stored)
			require.NoError(t, err)
			for _, e := range []notification.Event{tc.ev, decoded} {
				s := summarize(e, fixtureBaseURL)
				require.Equal(t, tc.want, s.Headline)
				require.Empty(t, s.Also, "an SLA event never shares an event with a ticket kind")
				require.Empty(t, s.StatusName)
			}
		})
	}
}

// Every renderer says it, and Jira keeps the event name in its event key, with
// no key of its own for the targets.
func TestRenderers_SLAEvents(t *testing.T) {
	for _, f := range []Format{FormatSlack, FormatTeams, FormatDiscord} {
		t.Run(string(f), func(t *testing.T) {
			b, err := bodyFor(authstore.WebhookConfig{PayloadFormat: string(f)}, breachAndAtRisk(), nil, fixtureBaseURL)
			require.NoError(t, err)
			require.Contains(t, string(b), "SLA breached; another target at risk")
			require.Contains(t, string(b), "/tickets/"+slaTicketID.String())
		})
	}
	b, err := bodyFor(authstore.WebhookConfig{PayloadFormat: string(FormatJIRA)}, breachAndAtRisk(), nil, fixtureBaseURL)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, "sla.breached", got["event"])
	require.NotContains(t, got, "also")
	require.NotContains(t, got, "targets")
	require.NotContains(t, got, "at_risk_targets")
}
