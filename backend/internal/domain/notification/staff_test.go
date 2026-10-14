package notification_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

var allOn = notification.StaffPrefs{Assigned: true, Activity: true, Group: true}

func TestStaffOverrides_Allows(t *testing.T) {
	cases := []struct {
		name string
		ov   notification.StaffOverrides
		sw   notification.StaffSwitch
		pref notification.StaffPrefs
		want bool
	}{
		{"zero value defers to the user: on", notification.StaffOverrides{}, notification.SwitchAssigned, allOn, true},
		{"zero value defers to the user: off", notification.StaffOverrides{}, notification.SwitchAssigned, notification.StaffPrefs{}, false},
		{"force on beats a user who turned it off", notification.StaffOverrides{Assigned: notification.OverrideOn}, notification.SwitchAssigned, notification.StaffPrefs{}, true},
		{"force off beats a user who left it on", notification.StaffOverrides{Assigned: notification.OverrideOff}, notification.SwitchAssigned, allOn, false},
		{"activity override reads the activity pref", notification.StaffOverrides{Activity: notification.OverrideOff}, notification.SwitchActivity, allOn, false},
		{"group override reads the group pref", notification.StaffOverrides{Group: notification.OverrideOn}, notification.SwitchGroup, notification.StaffPrefs{}, true},
		{"an override on one switch leaves the others alone", notification.StaffOverrides{Assigned: notification.OverrideOff}, notification.SwitchGroup, allOn, true},
		{"the user's own preference is per switch", notification.StaffOverrides{}, notification.SwitchActivity, notification.StaffPrefs{Assigned: true, Group: true}, false},
		{"an unrecognised mode defers to the user", notification.StaffOverrides{Assigned: "bogus"}, notification.SwitchAssigned, allOn, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.ov.Allows(tc.sw, tc.pref))
		})
	}
}

func TestStaffRecipients(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	cand := func(id uuid.UUID, sw notification.StaffSwitch, p notification.StaffPrefs) notification.StaffCandidate {
		return notification.StaffCandidate{UserID: id, Reason: sw, Prefs: p}
	}

	cases := []struct {
		name  string
		cands []notification.StaffCandidate
		actor *uuid.UUID
		ov    notification.StaffOverrides
		want  []uuid.UUID
	}{
		{"nobody to tell", nil, nil, notification.StaffOverrides{}, nil},
		{"each switch on mails its candidate", []notification.StaffCandidate{
			cand(a, notification.SwitchAssigned, allOn), cand(b, notification.SwitchGroup, allOn),
		}, nil, notification.StaffOverrides{}, []uuid.UUID{a, b}},
		{"a switch the user turned off skips them", []notification.StaffCandidate{
			cand(a, notification.SwitchAssigned, notification.StaffPrefs{Activity: true, Group: true}),
			cand(b, notification.SwitchActivity, notification.StaffPrefs{Assigned: true, Group: true}),
			cand(c, notification.SwitchGroup, notification.StaffPrefs{Assigned: true, Activity: true}),
		}, nil, notification.StaffOverrides{}, nil},
		{"admin force-on mails a user who turned it off", []notification.StaffCandidate{
			cand(a, notification.SwitchActivity, notification.StaffPrefs{}),
		}, nil, notification.StaffOverrides{Activity: notification.OverrideOn}, []uuid.UUID{a}},
		{"admin force-off silences a user who left it on", []notification.StaffCandidate{
			cand(a, notification.SwitchGroup, allOn), cand(b, notification.SwitchAssigned, allOn),
		}, nil, notification.StaffOverrides{Group: notification.OverrideOff}, []uuid.UUID{b}},
		{"nobody is mailed about their own action", []notification.StaffCandidate{
			cand(a, notification.SwitchAssigned, allOn), cand(b, notification.SwitchGroup, allOn),
		}, &a, notification.StaffOverrides{}, []uuid.UUID{b}},
		{"the actor is skipped even when an override forces the switch on", []notification.StaffCandidate{
			cand(a, notification.SwitchAssigned, notification.StaffPrefs{}),
		}, &a, notification.StaffOverrides{Assigned: notification.OverrideOn}, nil},
		{"one email per person: assignee who is also a group member", []notification.StaffCandidate{
			cand(a, notification.SwitchAssigned, allOn), cand(a, notification.SwitchGroup, allOn), cand(b, notification.SwitchGroup, allOn),
		}, nil, notification.StaffOverrides{}, []uuid.UUID{a, b}},
		{"a second reason rescues a person whose first switch is off", []notification.StaffCandidate{
			cand(a, notification.SwitchAssigned, notification.StaffPrefs{Group: true}),
			cand(a, notification.SwitchGroup, notification.StaffPrefs{Group: true}),
		}, nil, notification.StaffOverrides{}, []uuid.UUID{a}},
		{"a person listed twice for the same reason is mailed once", []notification.StaffCandidate{
			cand(a, notification.SwitchGroup, allOn), cand(a, notification.SwitchGroup, allOn),
		}, nil, notification.StaffOverrides{}, []uuid.UUID{a}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, notification.StaffRecipients(tc.cands, tc.actor, tc.ov))
		})
	}
}

func TestValidOverrideMode(t *testing.T) {
	for _, ok := range []string{"user", "on", "off"} {
		require.True(t, notification.ValidOverrideMode(ok), ok)
	}
	for _, bad := range []string{"", "ON", "yes", "true", " on"} {
		require.False(t, notification.ValidOverrideMode(bad), bad)
	}
}
