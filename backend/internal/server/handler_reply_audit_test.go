package server_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// Replies and internal notes reach the audit log (CTO ruling, 2026-10-20):
// who wrote and when, never the words.

const (
	replyWords = "the-secret-words-of-a-reply"
	noteWords  = "the-secret-words-of-a-note"
)

type replyAuditEntry struct {
	Action      string         `json:"action"`
	EntityID    string         `json:"entity_id"`
	ActorID     *string        `json:"actor_id"`
	ActorName   string         `json:"actor_name"`
	ActorMasked bool           `json:"actor_masked"`
	Before      map[string]any `json:"before"`
	After       map[string]any `json:"after"`
}

// ticketWithRequesterReplyAndStaffNote files a ticket as the requester, has
// the requester reply and staff leave an internal note.
func ticketWithRequesterReplyAndStaffNote(t *testing.T, h *harness) string {
	t.Helper()
	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "Reply audit", "description": "x", "category_id": h.catID.String(), "priority": "low",
	})
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, res, &created)
	require.NotEmpty(t, created.ID)

	res = h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+created.ID+"/replies", map[string]any{"body": replyWords})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	res = h.do(t, http.MethodPost, "/api/v1/tickets/"+created.ID+"/replies", map[string]any{"body": noteWords, "internal": true})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	return created.ID
}

func TestReplyAudit_FeedShowsReplyAndNoteWithoutText(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := ticketWithRequesterReplyAndStaffNote(t, h)

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id+"/audit", nil)
	raw, err := readAllBody(res)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, raw)
	require.NotContains(t, raw, replyWords)
	require.NotContains(t, raw, noteWords)

	var entries []replyAuditEntry
	require.NoError(t, json.Unmarshal([]byte(raw), &entries))
	byAction := map[string]replyAuditEntry{}
	for _, e := range entries {
		byAction[e.Action] = e
	}
	reply, note := byAction["reply_added"], byAction["note_added"]
	require.NotEmpty(t, reply.Action, "no reply_added entry: %s", raw)
	require.NotEmpty(t, note.Action, "no note_added entry: %s", raw)

	// The requester's reply is masked by default; staff's note is named.
	require.True(t, reply.ActorMasked)
	require.Equal(t, "Requester", reply.ActorName)
	require.NotNil(t, reply.ActorID)
	require.Equal(t, "Staff", note.ActorName)
	require.False(t, note.ActorMasked)

	// The diff carries the reply id and the flag, nothing else.
	require.Equal(t, true, note.After["internal"])
	require.Equal(t, false, reply.After["internal"])
	require.Len(t, note.After, 2)
	require.NotEmpty(t, note.After["reply_id"])
}

func TestReplyAudit_AdminViewShowsThemWithoutText(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := ticketWithRequesterReplyAndStaffNote(t, h)

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?action=note_added", nil)
	raw, err := readAllBody(res)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, raw)
	require.NotContains(t, raw, noteWords)
	var page struct {
		Entries []replyAuditEntry `json:"entries"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &page))
	require.Len(t, page.Entries, 1)
	require.Equal(t, id, page.Entries[0].EntityID)
}

func TestReplyAudit_RequesterStillGets403OnTheFeed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := ticketWithRequesterReplyAndStaffNote(t, h)

	res := h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+id+"/audit", nil)
	raw, _ := readAllBody(res)
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode, raw)
	require.NotContains(t, raw, "reply_added")
	require.NotContains(t, raw, "note_added")
}

func TestReplyAudit_StaffScopeAppliesInTheAdminView(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// A ticket the staff member has no part in, with a reply and a note on it.
	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "not the staff user's business", "category_id": h.catID.String(), "priority": "low",
	})
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, res, &created)
	for _, internal := range []bool{false, true} {
		res = h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+created.ID+"/replies",
			map[string]any{"body": "words", "internal": internal})
		res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode)
	}
	enableScope(t, h)

	for _, who := range []struct {
		name string
		do   func(*testing.T, string, string, any) *http.Response
		sees bool
	}{
		{"staff", h.do, false},
		{"admin", h.doAsAdmin, true},
	} {
		res = who.do(t, http.MethodGet, "/api/v1/admin/audit?entity_type=ticket", nil)
		var got adminAuditResponse
		decodeJSON(t, res, &got)
		saw := 0
		for _, e := range got.Entries {
			if e.EntityID == created.ID && (e.Action == "reply_added" || e.Action == "note_added") {
				saw++
			}
		}
		if who.sees {
			require.Equal(t, 2, saw, who.name)
		} else {
			require.Zero(t, saw, "%s saw reply entries on a ticket outside their scope", who.name)
		}
	}
}

// exclude_action leaves entries out in the query, so the page, the offset and
// the total still describe one sequence (as for the follow entries).
func TestReplyAudit_ExcludeActionKeepsPagingAndTotalConsistent(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := ticketWithRequesterReplyAndStaffNote(t, h)
	for i := 0; i < 3; i++ {
		res := h.do(t, http.MethodPost, "/api/v1/tickets/"+id+"/replies", map[string]any{"body": "more", "internal": true})
		res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode)
	}

	type page struct {
		Entries []replyAuditEntry `json:"entries"`
		Total   int               `json:"total"`
		HasMore bool              `json:"has_more"`
	}
	get := func(who func(*testing.T, string, string, any) *http.Response, q string) page {
		res := who(t, http.MethodGet, "/api/v1/admin/audit?entity_type=ticket&"+q, nil)
		var p page
		decodeJSON(t, res, &p)
		require.Equal(t, http.StatusOK, res.StatusCode)
		return p
	}
	const ex = "exclude_action=reply_added&exclude_action=note_added"

	all := get(h.doAsAdmin, "limit=100")
	others := 0
	for _, e := range all.Entries {
		if e.Action != "reply_added" && e.Action != "note_added" {
			others++
		}
	}
	require.Greater(t, len(all.Entries), others, "fixture has no reply entries to hide")

	for _, who := range []struct {
		name string
		do   func(*testing.T, string, string, any) *http.Response
	}{{"admin", h.doAsAdmin}, {"staff", h.do}} {
		whole := get(who.do, ex+"&limit=100")
		for _, e := range whole.Entries {
			require.NotContains(t, []string{"reply_added", "note_added"}, e.Action, who.name)
		}
		require.Equal(t, len(whole.Entries), whole.Total, "%s: total counts what is hidden", who.name)

		// Page one entry at a time: the same sequence, the same total.
		var seq []string
		for off := 0; off < whole.Total; off++ {
			p := get(who.do, ex+"&limit=1&offset="+strconv.Itoa(off))
			require.Equal(t, whole.Total, p.Total, "%s: total moved with the offset", who.name)
			require.Len(t, p.Entries, 1)
			require.Equal(t, off < whole.Total-1, p.HasMore, "%s: has_more at %d", who.name, off)
			seq = append(seq, p.Entries[0].Action)
		}
		var want []string
		for _, e := range whole.Entries {
			want = append(want, e.Action)
		}
		require.Equal(t, want, seq, who.name)
	}
	require.Equal(t, others, get(h.doAsAdmin, ex+"&limit=100").Total)
}

func TestReplyAudit_ExcludeActionRefusesBadText(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?exclude_action=%00", nil)
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}
