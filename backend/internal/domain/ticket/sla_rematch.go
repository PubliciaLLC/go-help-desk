package ticket

import "time"

// pausedSecondsAt is the ticket's pause total as of at: every closed Pending
// interval, plus the part of an open one already elapsed. A restarted SLA
// record stores it as its base (paused_seconds_at_start), so pause time before
// the restart does not count toward the new record. The open interval is
// clipped to at, the way sla.Elapsed clips it.
func pausedSecondsAt(t Ticket, at time.Time) int64 {
	n := t.SLAPausedSeconds
	if t.PendingSince != nil && at.After(*t.PendingSince) {
		n += int64(at.Sub(*t.PendingSince) / time.Second)
	}
	return n
}
