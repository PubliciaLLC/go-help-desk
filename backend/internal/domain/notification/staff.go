package notification

import "github.com/google/uuid"

// Staff email (#441). Staff are mailed when a ticket is assigned and when a
// requester replies. Who is mailed is decided by three per-user switches and
// an administrator's override of each; this file is that rule and nothing
// else, so it can be read and tested without a database or a mail server.

// StaffSwitch names one of the three per-user email switches. A switch is
// also the REASON a person is being considered for an email: the same person
// can be a candidate for two reasons (the assignee who also belongs to the
// assigned group), and is mailed once if either reason passes.
type StaffSwitch string

const (
	// SwitchAssigned: a ticket is assigned to me.
	SwitchAssigned StaffSwitch = "assigned"
	// SwitchActivity: a requester replied on a ticket assigned to me. Ticket
	// followers will be candidates under this switch too once following
	// exists (PR-D of #464); they are added where candidates are built, not
	// here.
	SwitchActivity StaffSwitch = "activity"
	// SwitchGroup: a ticket assigned to a group I belong to.
	SwitchGroup StaffSwitch = "group"
	// SwitchFollow: a ticket I follow. Nothing builds this candidate until
	// following exists (PR-D of #464); it is here so the switch rule and the
	// send-time check are settled first. It answers to the activity switch.
	SwitchFollow StaffSwitch = "follow"
)

// OverrideMode is an administrator's decision about one switch for everyone.
type OverrideMode string

const (
	// OverrideUser leaves the switch to each person. The default.
	OverrideUser OverrideMode = "user"
	OverrideOn   OverrideMode = "on"
	OverrideOff  OverrideMode = "off"
)

// ValidOverrideMode reports whether s is one of the three spellings the
// settings endpoint accepts.
func ValidOverrideMode(s string) bool {
	switch OverrideMode(s) {
	case OverrideUser, OverrideOn, OverrideOff:
		return true
	}
	return false
}

// StaffPrefs is one person's own three switches.
type StaffPrefs struct {
	Assigned, Activity, Group bool
}

// StaffOverrides is the administrator's decision for each switch. The zero
// value defers every switch to the user.
type StaffOverrides struct {
	Assigned, Activity, Group OverrideMode
}

// Allows reports whether the switch sw is on for a person with prefs p: the
// administrator's word if they gave one, the person's own otherwise. An
// unrecognised mode is not a decision, so it defers to the person.
func (o StaffOverrides) Allows(sw StaffSwitch, p StaffPrefs) bool {
	var mode OverrideMode
	var own bool
	switch sw {
	case SwitchAssigned:
		mode, own = o.Assigned, p.Assigned
	case SwitchActivity, SwitchFollow:
		mode, own = o.Activity, p.Activity
	case SwitchGroup:
		mode, own = o.Group, p.Group
	}
	switch mode {
	case OverrideOn:
		return true
	case OverrideOff:
		return false
	}
	return own
}

// Forced reports whether the administrator has decided the switch for
// everyone, so a person's own choice is not what applies.
func (o StaffOverrides) Forced(sw StaffSwitch) bool {
	var mode OverrideMode
	switch sw {
	case SwitchAssigned:
		mode = o.Assigned
	case SwitchActivity, SwitchFollow:
		mode = o.Activity
	case SwitchGroup:
		mode = o.Group
	}
	return mode == OverrideOn || mode == OverrideOff
}

// StaffCandidate is a person with a reason to hear about an event. Callers
// pass only people who can receive staff mail (active, staff or admin, not a
// service account); this file does not know what a user is.
type StaffCandidate struct {
	UserID uuid.UUID
	Prefs  StaffPrefs
	Reason StaffSwitch
	// Mail is the staff.* event the person would be mailed with, so a person
	// who qualifies for several kinds of one change keeps the mail of the
	// first that passes (#506). Unused by the switch rule.
	Mail EventType
}

// StaffRecipient is one person to mail and the reason that let them through:
// the first of their reasons whose switch was on.
type StaffRecipient struct {
	UserID uuid.UUID
	Reason StaffSwitch
	Mail   EventType
}

// StaffRecipients returns who is mailed for one event: one entry per person,
// in the order they first qualify.
//
// Nobody is mailed about their own action, and that is checked after the
// switches so that an administrator forcing a switch on cannot mail someone
// about what they just did themselves. actor is nil for an event with no staff
// member behind it (a guest's reply, the auto-close scheduler).
func StaffRecipients(cands []StaffCandidate, actor *uuid.UUID, ov StaffOverrides) []StaffRecipient {
	var out []StaffRecipient
	seen := make(map[uuid.UUID]bool, len(cands))
	for _, c := range cands {
		if seen[c.UserID] {
			continue
		}
		if actor != nil && c.UserID == *actor {
			continue
		}
		if !ov.Allows(c.Reason, c.Prefs) {
			continue
		}
		seen[c.UserID] = true
		out = append(out, StaffRecipient{UserID: c.UserID, Reason: c.Reason, Mail: c.Mail})
	}
	return out
}
