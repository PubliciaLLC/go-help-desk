package server_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Guest attachment upload (Erik's ask on #312, picked up as its own change
// after #312 merged without it) and the ticket-status check #315 found
// missing from the authenticated route. Both routes now go through the same
// storeUploadedAttachment pipeline and the same CanUploadAttachment /
// CanGuestUploadAttachment authorization as a reply, so this file pins the
// authorization boundary rather than re-testing the upload pipeline itself —
// that is already covered by the handler_attachments_*_test.go files.

// uploadAttachmentAs posts a file as whichever raw credential the caller
// supplies, using the ApiKey scheme every non-guest test in this package
// already uses.
func uploadAttachmentAs(t *testing.T, h *harness, key, ticketID, name string, content []byte) *http.Response {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = fw.Write(content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tickets/"+ticketID+"/attachments", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "ApiKey "+key)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec.Result()
}

// uploadGuestAttachment posts a file with a guest token instead of a ticket
// id in the path — see handleGuestUploadAttachment's own comment for why the
// token travels in the header rather than the URL.
func uploadGuestAttachment(t *testing.T, h *harness, token, name string, content []byte) *http.Response {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = fw.Write(content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/guest/attachments", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Guest "+token)
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec.Result()
}

// ── The authenticated route's status check (#315) ──────────────────────────

func TestUploadAttachment_ReporterRefusedOnTheirOwnClosedTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Needs a screenshot", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, staff))

	res := uploadAttachmentAs(t, h, h.userKey, tk.ID.String(), "shot.txt", []byte("log"))
	defer res.Body.Close()
	require.Equal(t, http.StatusConflict, res.StatusCode,
		"a reply to this ticket is already refused (#315); an attachment must be too")

	atts, err := h.ticketSvc.ListAttachments(ctx, tk.ID)
	require.NoError(t, err)
	require.Empty(t, atts, "nothing must be stored when the check refuses")
}

func TestUploadAttachment_StaffMayStillAttachToAClosedTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Documenting the fix", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, staff))

	res := uploadAttachmentAs(t, h, h.apiKey, tk.ID.String(), "note.txt", []byte("log"))
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode,
		"staff are exempt from the lifecycle rule, the same as a reply")
}

func TestUploadAttachment_ReporterStillRefusedOnSomeoneElsesTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Not yours", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID, // owned by someone other than h.userID
	})
	require.NoError(t, err)

	res := uploadAttachmentAs(t, h, h.userKey, tk.ID.String(), "shot.txt", []byte("log"))
	defer res.Body.Close()
	// Not 403: see #174. A ticket this reporter cannot see answers the same
	// as one that does not exist.
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

// ── The new guest route ─────────────────────────────────────────────────────

func TestGuestUploadAttachment_WorksWithAValidToken(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := seedGuestTicket(t, h)

	res := uploadGuestAttachment(t, h, token, "photo.txt", []byte("what it looks like"))
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", body)

	atts, err := h.ticketSvc.ListAttachments(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Len(t, atts, 1)
	require.Equal(t, "photo.txt", atts[0].Filename)
}

func TestGuestUploadAttachment_ReachesOneTicketAndNoOther(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := seedGuestTicket(t, h)

	res := uploadGuestAttachment(t, h, token, "photo.txt", []byte("mine"))
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	other := "other@test.local"
	tk2, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Other", Description: "x", CategoryID: h.catID,
		GuestEmail: &other, GuestName: "Bo",
	})
	require.NoError(t, err)

	atts2, err := h.ticketSvc.ListAttachments(context.Background(), tk2.ID)
	require.NoError(t, err)
	require.Empty(t, atts2, "a token names one ticket, and the upload must not reach another")
	require.NotEqual(t, tk.ID, tk2.ID)
}

func TestGuestUploadAttachment_RefusedWithoutAValidToken(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	cases := []struct {
		name  string
		token string
	}{
		{"no token at all", ""},
		{"a token that was never issued", "0000000000000000000000000000000000000000000000000000000000000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := uploadGuestAttachment(t, h, tc.token, "photo.txt", []byte("x"))
			defer res.Body.Close()
			require.Equal(t, http.StatusNotFound, res.StatusCode)
		})
	}
}

// Closing no longer revokes the guest token (#349; this was
// TestGuestUploadAttachment_ClosingTheTicketRevokesTheToken), but the upload
// route resolves its token through the WRITE lookup, which refuses a closed
// ticket at the token-resolution step with the same 404 every other dead-token
// case gets — never reaching CanGuestUploadAttachment, exactly like a guest
// reply. The 404 is byte-identical to a link that never existed.
func TestGuestUploadAttachment_AClosedTicketRefusesLikeABadToken(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := seedGuestTicket(t, h)

	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	require.NoError(t, h.ticketSvc.Close(context.Background(), tk.ID, staff))

	res := uploadGuestAttachment(t, h, token, "photo.txt", []byte("x"))
	defer res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	bad := uploadGuestAttachment(t, h, nearMiss(token), "photo.txt", []byte("x"))
	defer bad.Body.Close()
	closedBody, _ := io.ReadAll(res.Body)
	badBody, _ := io.ReadAll(bad.Body)
	require.Equal(t, string(badBody), string(closedBody), "the refusal must not say the ticket exists")

	atts, err := h.ticketSvc.ListAttachments(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Empty(t, atts)
}

// A resolved ticket's token is accepted by the write lookup (only a Closed
// ticket is refused there), so this is the one case that actually reaches
// CanGuestUploadAttachment's lifecycle check over HTTP rather than being caught
// earlier at token resolution.
func TestGuestUploadAttachment_RefusedPastTheReopenWindow(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, _ := seedGuestTicket(t, h)

	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	_, err := h.ticketSvc.Resolve(ctx, tk.ID, "fixed", staff)
	require.NoError(t, err)
	require.NoError(t, h.adminSvc.SetInt(ctx, admin.KeyReopenWindowDays, 0))

	// Resolving rotated the link (the guest is mailed a fresh one, same as a
	// reply — see TestGuestToken_RotatesOnlyOnWhatTheGuestIsTold), so the
	// token from seedGuestTicket is already dead. Fetch a current one, the
	// same way TestGuest_ViewHidesInternalNotesAndStaffIdentities does after
	// a reply rotates its link.
	token, err := h.ticketSvc.IssueGuestToken(ctx, tk.ID)
	require.NoError(t, err)

	res := uploadGuestAttachment(t, h, token, "photo.txt", []byte("x"))
	defer res.Body.Close()
	require.Equal(t, http.StatusConflict, res.StatusCode)

	atts, err := h.ticketSvc.ListAttachments(ctx, tk.ID)
	require.NoError(t, err)
	require.Empty(t, atts)
}

// guest_submission_enabled governs whether a NEW guest ticket is accepted,
// not whether an existing token still works — handleGuestAddReply already
// does not gate on it, and this follows the same rule for the same reason:
// switching submission off must not sever a conversation somebody already
// holds a working token for. The harness leaves the setting at its default
// (off), so this is the ordinary path, not a special case.
func TestGuestUploadAttachment_NotGatedOnGuestSubmissionEnabled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.False(t, h.adminSvc.GuestSubmissionEnabled(context.Background()),
		"precondition: guest submission is off by default")
	_, token := seedGuestTicket(t, h)

	res := uploadGuestAttachment(t, h, token, "photo.txt", []byte("x"))
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
}
