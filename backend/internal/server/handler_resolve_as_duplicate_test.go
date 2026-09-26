package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// POST /tickets/{id}/links with resolve_as_duplicate is only exercised at the
// domain level (service_atomic_test.go); nothing drives it through the real
// HTTP handler. See #201.
//
// This covers 403-nothing-written and 200-with-ticket-body. The third
// contract case #201 names is "409" — but #194, filed and fixed in this same
// bundle, explicitly changed that: resolving a duplicate whose exact
// duplicate_of link already exists must now succeed (200, idempotent)
// instead of aborting with 409. There is no other route left to a genuine
// 409 through this flag (the unique-constraint violation is the only way
// CreateLink conflicts, and #194 turns exactly that case into a no-op), so
// the third case here pins the idempotent-success contract #194 established
// in place of the now-superseded 409 expectation. The plain (non-resolve)
// AddLink 409 contract is already covered at the HTTP layer by
// TestAddLink_ReturnsDuplicateLinkWith409.
func TestResolveAsDuplicate_HTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	source, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Source", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	target, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Target", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	base := "/api/v1/tickets/" + source.ID.String() + "/links"
	var firstResolvedAt, secondResolvedAt *time.Time

	t.Run("403: a reporting user may not resolve, and nothing is written", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodPost, base, map[string]any{
			"target_id": target.ID.String(), "link_type": "duplicate_of",
			"resolve_as_duplicate": true, "resolution_notes": "nope",
		})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)

		links, err := h.ticketSvc.ListLinks(ctx, source.ID)
		require.NoError(t, err)
		require.Empty(t, links, "nothing may be written when the actor is refused")

		stored, err := h.ticketSvc.GetByID(ctx, source.ID)
		require.NoError(t, err)
		require.Nil(t, stored.ResolvedAt, "the ticket must remain unresolved")
	})

	t.Run("200: staff resolving as duplicate gets the resolved ticket back", func(t *testing.T) {
		res := h.do(t, http.MethodPost, base, map[string]any{
			"target_id": target.ID.String(), "link_type": "duplicate_of",
			"resolve_as_duplicate": true, "resolution_notes": "first resolve",
		})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)

		var got ticket.Ticket
		require.NoError(t, json.Unmarshal(b, &got))
		require.NotNil(t, got.ResolutionNotes)
		require.Equal(t, "first resolve", *got.ResolutionNotes)
		require.NotNil(t, got.ResolvedAt)
		firstResolvedAt = got.ResolvedAt

		links, err := h.ticketSvc.ListLinks(ctx, source.ID)
		require.NoError(t, err)
		require.Len(t, links, 1)
		require.Equal(t, ticket.LinkDuplicateOf, links[0].LinkType)
	})

	t.Run("200 again: the identical link already existing is satisfied, and DIFFERENT notes genuinely re-resolve (#194, #225)", func(t *testing.T) {
		res := h.do(t, http.MethodPost, base, map[string]any{
			"target_id": target.ID.String(), "link_type": "duplicate_of",
			"resolve_as_duplicate": true, "resolution_notes": "second resolve",
		})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode,
			"an already-satisfied identical link must resolve, not 409; body: %s", b)

		// #225: the source ticket is already Resolved, but this call's notes
		// genuinely differ from what's stored — a legitimate re-resolve, not
		// a double-submit, so the new notes must win rather than being
		// silently dropped in favor of the first call's. The original
		// resolved_at must still be preserved (the reopen-window anchor),
		// which is applyStatusTimestamps' existing rule, untouched by #225.
		var got ticket.Ticket
		require.NoError(t, json.Unmarshal(b, &got))
		require.NotNil(t, got.ResolutionNotes)
		require.Equal(t, "second resolve", *got.ResolutionNotes,
			"a genuine re-resolve with different notes must update them, not silently drop them (#225)")
		require.NotNil(t, got.ResolvedAt)
		// Truncated to Postgres' timestamptz precision (microseconds): the
		// FIRST response's value is still the in-process nanosecond-precision
		// time.Time from before its own round trip through the database, so
		// comparing at full precision would fail on the sub-microsecond
		// digits alone, not on any real difference.
		require.True(t, firstResolvedAt.Truncate(time.Microsecond).Equal(got.ResolvedAt.Truncate(time.Microsecond)),
			"a re-resolve must preserve the ORIGINAL resolved_at, not restart the reopen window")
		secondResolvedAt = got.ResolvedAt

		// Still exactly one link — the second call did not duplicate the row.
		links, err := h.ticketSvc.ListLinks(ctx, source.ID)
		require.NoError(t, err)
		require.Len(t, links, 1)
	})

	t.Run("200 again: IDENTICAL notes and target against an already-resolved ticket is a true no-op (#209, #225)", func(t *testing.T) {
		res := h.do(t, http.MethodPost, base, map[string]any{
			"target_id": target.ID.String(), "link_type": "duplicate_of",
			"resolve_as_duplicate": true, "resolution_notes": "second resolve",
		})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)

		var got ticket.Ticket
		require.NoError(t, json.Unmarshal(b, &got))
		require.NotNil(t, got.ResolutionNotes)
		require.Equal(t, "second resolve", *got.ResolutionNotes,
			"a true double-submit (same notes, same target) must not change the stored notes")
		require.NotNil(t, got.ResolvedAt)
		require.True(t, secondResolvedAt.Truncate(time.Microsecond).Equal(got.ResolvedAt.Truncate(time.Microsecond)))

		links, err := h.ticketSvc.ListLinks(ctx, source.ID)
		require.NoError(t, err)
		require.Len(t, links, 1, "a true double-submit must not duplicate the link")
	})
}
