package server

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// auditName is what a diff id resolves to when an audit view is rendered
// (#422). Masked mirrors actor_masked: it tells a client that "Requester" is
// the mask and not an account that happens to carry that display name.
type auditName struct {
	Name   string `json:"name"`
	Masked bool   `json:"masked,omitempty"`
}

type auditNameKind int

const (
	auditNameStatus auditNameKind = iota
	auditNameUser
)

// auditNameFields are the diff keys whose ids are resolved, and what each
// names. Deliberately a short list, not "any key ending in _id": a key is
// resolved only once it is also on audit.Redact's allow-list, because a name
// says more than its id does. assignee_group_id is allow-listed but has no
// lookup on purpose: only a group delete writes it, and the group is gone by
// the time anyone reads the entry, so a lookup could never find a name and the
// raw id is all there is to show. category_id and the like are neither written
// to audit entries nor allow-listed today; they join here in the change that
// allow-lists them.
var auditNameFields = map[string]auditNameKind{
	"status_id":        auditNameStatus,
	"assignee_user_id": auditNameUser,
}

// auditNames holds the resolved names for one page of audit entries.
//
// It is built from the diffs as they are about to be sent, after audit.Redact,
// which keeps the existing rules in force without restating them: a viewer who
// gets no diff (the staff setting off, a reporting user) hands in none and
// gets no names; an entry outside a staff member's scope is not on the page,
// so its ids are never looked at; a field that is not allow-listed is not in
// the diff to be read.
//
// The lookups are batched for the whole page: one read of the statuses table
// if any status id is present, one query for all the user ids. Never one per
// entry.
type auditNames struct {
	statuses map[string]string // id -> name
	users    map[string]auditName
}

func (s *Server) resolveAuditNames(ctx context.Context, maskRequesters bool, diffs ...map[string]any) auditNames {
	statusIDs := map[string]bool{}
	userIDs := map[uuid.UUID]bool{}
	for _, d := range diffs {
		for key, kind := range auditNameFields {
			id, ok := diffID(d[key])
			if !ok {
				continue
			}
			switch kind {
			case auditNameStatus:
				statusIDs[id.String()] = true
			case auditNameUser:
				userIDs[id] = true
			}
		}
	}

	res := auditNames{statuses: map[string]string{}, users: map[string]auditName{}}
	// A failed lookup costs the names, not the page: the ids are still there to
	// show, as for an actor whose lookup fails.
	if len(statusIDs) > 0 {
		sts, err := s.tickets.ListStatuses(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "resolving audit status names failed", "error", err)
		}
		for _, st := range sts {
			if statusIDs[st.ID.String()] {
				res.statuses[st.ID.String()] = st.Name
			}
		}
	}
	if len(userIDs) > 0 {
		ids := make([]uuid.UUID, 0, len(userIDs))
		for id := range userIDs {
			ids = append(ids, id)
		}
		us, err := s.users.GetByIDs(ctx, ids)
		if err != nil {
			slog.ErrorContext(ctx, "resolving audit user names failed", "error", err)
		}
		for _, u := range us {
			// The same rule, and the same per-view setting, as the actor column.
			a := auditActorName(u, maskRequesters)
			res.users[u.ID.String()] = auditName{Name: a.name, Masked: a.masked}
		}
	}
	return res
}

// forEntry returns the names for one entry's diff, keyed by the id as it
// appears there, or nil when nothing resolved (so the field is omitted).
func (n auditNames) forEntry(before, after map[string]any) map[string]auditName {
	var out map[string]auditName
	for _, d := range []map[string]any{before, after} {
		for key, kind := range auditNameFields {
			id, ok := diffID(d[key])
			if !ok {
				continue
			}
			var name auditName
			var found bool
			switch kind {
			case auditNameStatus:
				var st string
				st, found = n.statuses[id.String()]
				name = auditName{Name: st}
			case auditNameUser:
				name, found = n.users[id.String()]
			}
			if !found {
				continue
			}
			if out == nil {
				out = map[string]auditName{}
			}
			out[id.String()] = name
		}
	}
	return out
}

// diffID reads a diff value as an id. Values come back from the database as
// strings; a UUID is accepted too for an entry built in-process. Anything else
// (nil for "nobody", a number, a nested value) is not an id.
func diffID(v any) (uuid.UUID, bool) {
	switch x := v.(type) {
	case string:
		id, err := uuid.Parse(x)
		return id, err == nil
	case uuid.UUID:
		return x, true
	}
	return uuid.UUID{}, false
}
