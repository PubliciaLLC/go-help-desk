package notification_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// The switch that decides a candidate is carried apart from the reason (#670):
// the reason still says why the person is a candidate (and so which template
// and which send-time check), the switch says whose preference applies. Empty,
// it is the reason, so every existing caller is unchanged.
func TestStaffRecipients_TheSwitchDecidesAndTheReasonIsReported(t *testing.T) {
	a := uuid.New()
	onlySLA := notification.StaffPrefs{SLA: true}
	onlyOld := notification.StaffPrefs{Activity: true, Group: true, Assigned: true}
	cases := []struct {
		name  string
		c     notification.StaffCandidate
		ov    notification.StaffOverrides
		want  bool
		wantR notification.StaffSwitch
	}{
		{"the SLA switch lets the assignee through whatever Activity says",
			notification.StaffCandidate{UserID: a, Prefs: onlySLA, Reason: notification.SwitchActivity, Switch: notification.SwitchSLA}, notification.StaffOverrides{}, true, notification.SwitchActivity},
		{"the Activity switch does not apply to it",
			notification.StaffCandidate{UserID: a, Prefs: onlyOld, Reason: notification.SwitchActivity, Switch: notification.SwitchSLA}, notification.StaffOverrides{}, false, ""},
		{"the Group switch does not apply to a group member's alert",
			notification.StaffCandidate{UserID: a, Prefs: onlyOld, Reason: notification.SwitchGroup, Switch: notification.SwitchSLA}, notification.StaffOverrides{}, false, ""},
		{"a follower answers to the SLA switch",
			notification.StaffCandidate{UserID: a, Prefs: onlySLA, Reason: notification.SwitchFollow, Switch: notification.SwitchSLA}, notification.StaffOverrides{}, true, notification.SwitchFollow},
		{"the SLA override forces it off",
			notification.StaffCandidate{UserID: a, Prefs: onlySLA, Reason: notification.SwitchGroup, Switch: notification.SwitchSLA}, notification.StaffOverrides{SLA: notification.OverrideOff}, false, ""},
		{"the SLA override forces it on",
			notification.StaffCandidate{UserID: a, Prefs: notification.StaffPrefs{}, Reason: notification.SwitchGroup, Switch: notification.SwitchSLA}, notification.StaffOverrides{SLA: notification.OverrideOn}, true, notification.SwitchGroup},
		{"the Activity override does not touch it",
			notification.StaffCandidate{UserID: a, Prefs: onlySLA, Reason: notification.SwitchActivity, Switch: notification.SwitchSLA}, notification.StaffOverrides{Activity: notification.OverrideOff}, true, notification.SwitchActivity},
		{"an empty switch is the reason: off",
			notification.StaffCandidate{UserID: a, Prefs: onlySLA, Reason: notification.SwitchActivity}, notification.StaffOverrides{}, false, ""},
		{"an empty switch is the reason: on",
			notification.StaffCandidate{UserID: a, Prefs: onlyOld, Reason: notification.SwitchActivity}, notification.StaffOverrides{}, true, notification.SwitchActivity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := notification.StaffRecipients([]notification.StaffCandidate{tc.c}, nil, tc.ov)
			if !tc.want {
				require.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			require.Equal(t, tc.wantR, got[0].Reason)
		})
	}
}

func TestSLAStaffMailEvents_AreNotWebhookEvents(t *testing.T) {
	require.Equal(t, notification.EventType("staff.sla_breached"), notification.EventStaffSLABreached)
	require.Equal(t, notification.EventType("staff.sla_at_risk"), notification.EventStaffSLAAtRisk)
	id := uuid.New()
	for _, typ := range []notification.EventType{notification.EventStaffSLABreached, notification.EventStaffSLAAtRisk} {
		ev := notification.Event{Type: typ, TicketID: uuid.New(), StaffUserID: &id}
		require.True(t, notification.Carries("email", ev))
		require.False(t, notification.Carries("webhook", ev))
	}
}
