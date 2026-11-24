package notification

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The SLA sweep's two events and the typed field mail reads (#670). The field is
// stored by the outbox like FollowUp and is never part of the webhook body.

func TestSLAEvents_Names(t *testing.T) {
	require.Equal(t, "sla.breached", string(EventSLABreached))
	require.Equal(t, "sla.at_risk", string(EventSLAAtRisk))
}

func TestEventCodec_RoundTripsTheSLAAlert(t *testing.T) {
	cases := []struct {
		name  string
		alert *SLAAlert
	}{
		{"none", nil},
		{"breached", &SLAAlert{Breached: []SLATarget{SLATargetResponse}}},
		{"breached and at risk", &SLAAlert{
			Breached: []SLATarget{SLATargetResponse}, AtRisk: []SLATarget{SLATargetResolution},
		}},
		{"at risk", &SLAAlert{AtRisk: []SLATarget{SLATargetResponse, SLATargetResolution}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := EncodeEvent(Event{
				Type: EventSLABreached, TicketID: uuid.New(), OccurredAt: time.Unix(0, 0).UTC(), SLA: tc.alert,
			})
			require.NoError(t, err)
			got, err := DecodeEvent(b)
			require.NoError(t, err)
			require.Equal(t, tc.alert, got.SLA)
			if tc.alert == nil {
				require.NotContains(t, string(b), `"sla"`, "absent unless set, as the other flags are")
			}
		})
	}

	got, err := DecodeEvent([]byte(`{"type":"ticket.created","ticket_id":"11111111-2222-3333-4444-555555555555","occurred_at":"2026-01-01T00:00:00Z"}`))
	require.NoError(t, err)
	require.Nil(t, got.SLA, "a row queued before the field existed")
}

func TestChanges_CarriesTheSLAAlert(t *testing.T) {
	tid := uuid.New()
	alert := &SLAAlert{Breached: []SLATarget{SLATargetResolution}}
	c := &Changes{}
	c.Raise(Event{Type: EventSLABreached, TicketID: tid, SLA: alert})
	evs := c.Events()
	require.Len(t, evs, 1)
	require.Equal(t, alert, evs[0].SLA)
	require.Equal(t, []EventType{EventSLABreached}, evs[0].Kinds, "its own name only")
}

// Until the webhook and staff-mail tasks of #670 land, the outbox still has to
// queue the two events on both channels, one row each.
func TestCarries_SLAEvents(t *testing.T) {
	for _, typ := range []EventType{EventSLABreached, EventSLAAtRisk} {
		ev := Event{Type: typ, Kinds: []EventType{typ}}
		require.True(t, Carries("webhook", ev), typ)
		require.True(t, Carries("email", ev), typ)
		require.False(t, Carries("verification", ev), typ)
	}
}

// The two names are webhook events, ordinary ones (a "*" hook gets them, they
// are not opt-in), last in KindOrder because the sweep raises them in a
// transaction of their own (#670).
func TestSLAEvents_AreOrdinaryWebhookEventsAndLast(t *testing.T) {
	for _, n := range []EventType{EventSLABreached, EventSLAAtRisk} {
		require.True(t, IsWebhookEvent(string(n)), "%s must be a name a hook can list", n)
		require.Contains(t, WebhookEvents, n)
		require.False(t, IsOptInWebhookEvent(n), "%s goes to every hook that lists *", n)
		require.True(t, Event{Type: n, Kinds: []EventType{n}}.MailsStaff(), n)
	}
	require.Equal(t, []EventType{EventSLABreached, EventSLAAtRisk}, KindOrder[len(KindOrder)-2:])
	require.Equal(t, EventTicketUnfollowed, KindOrder[len(KindOrder)-3], "after every ticket kind")
}
