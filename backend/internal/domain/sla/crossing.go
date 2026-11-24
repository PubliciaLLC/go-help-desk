package sla

import (
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// AlertFreshness is how far past a threshold a crossing can be and still be
// announced. It guards against a flood (a tightened policy, a server that was
// down), so it is a constant, not a setting.
const AlertFreshness = 15 * time.Minute

// Target names one of a ticket's two targets.
type Target string

const (
	TargetResponse   Target = "response"
	TargetResolution Target = "resolution"
)

// Event is the name an SLA notification is raised under. The zero value means
// nothing is announced.
type Event string

const (
	EventBreached Event = "sla.breached"
	EventAtRisk   Event = "sla.at_risk"
)

// Crossing is one threshold of one target as of a reading. Due means the SLA
// clock has reached it. Fresh means it is Due and within AlertFreshness of it.
type Crossing struct {
	Due   bool
	Fresh bool
}

// Crossed holds the crossings of one record. Index 0 is the response target and
// index 1 the resolution target, the same order an event lists its targets in.
type Crossed struct {
	Breach [2]Crossing
	AtRisk [2]Crossing
}

// Stamped says which stamp columns one statement newly set: NULL before, set
// after. Index 0 is response, index 1 resolution.
type Stamped struct {
	Breach [2]bool
	AtRisk [2]bool
}

// Announcement is what one sweep pass raises for one ticket. Event is the zero
// value when nothing is raised. AtRiskTargets is set only on EventBreached, and
// only when a target that has not breached went at risk in the same pass.
// Stale counts the newly stamped crossings that were not fresh, and so were
// stamped silently.
type Announcement struct {
	Event         Event
	Targets       []Target
	AtRiskTargets []Target
	Stale         int
}

// Crossings reads which thresholds the record has reached as of now, on the
// SLA clock. A met target never crosses: its clock is frozen. At risk is
// never reported at or past 100%, so a breach and an at-risk crossing of one
// target are never both due.
func Crossings(rec Record, p Policy, t ticket.Ticket, now time.Time) Crossed {
	var c Crossed
	elapsed := rec.Elapsed(t, now)
	c.Breach[0], c.AtRisk[0] = crossingsOf(elapsed, targetDur(p.ResponseTargetMin), rec.FirstResponseAt != nil)
	c.Breach[1], c.AtRisk[1] = crossingsOf(elapsed, targetDur(p.ResolutionTargetMin), rec.ResolvedAt != nil)
	return c
}

func targetDur(min int) time.Duration { return time.Duration(min) * time.Minute }

func crossingsOf(elapsed, target time.Duration, met bool) (breach, risk Crossing) {
	switch {
	case met:
		return Crossing{}, Crossing{}
	case elapsed >= target:
		return Crossing{Due: true, Fresh: elapsed-target <= AlertFreshness}, Crossing{}
	case atRisk(elapsed, target):
		// elapsed - 80% of target <= AlertFreshness, multiplied by 5 so the
		// 80% line stays exact integer arithmetic.
		return Crossing{}, Crossing{Due: true, Fresh: elapsed*5-target*4 <= 5*AlertFreshness}
	default:
		return Crossing{}, Crossing{}
	}
}

// Announce decides the one event a pass raises for a ticket. newly is what the
// pass's stamp statement set, and c is the reading it was decided from. Only a
// crossing that was newly stamped and is fresh is announced. The rest are
// counted as Stale. A breach outranks an at-risk crossing, and an at-risk
// crossing of the other target rides along on the breach.
func Announce(newly Stamped, c Crossed) Announcement {
	var a Announcement
	var breached, risk []Target
	for i, name := range [2]Target{TargetResponse, TargetResolution} {
		if newly.Breach[i] {
			if c.Breach[i].Fresh {
				breached = append(breached, name)
			} else {
				a.Stale++
			}
		}
		if newly.AtRisk[i] {
			if c.AtRisk[i].Fresh {
				risk = append(risk, name)
			} else {
				a.Stale++
			}
		}
	}
	switch {
	case len(breached) > 0:
		a.Event = EventBreached
		a.Targets = breached
		a.AtRiskTargets = risk
	case len(risk) > 0:
		a.Event = EventAtRisk
		a.Targets = risk
	}
	return a
}
