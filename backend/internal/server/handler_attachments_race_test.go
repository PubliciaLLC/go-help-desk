package server_test

import (
	"context"
	"io/fs"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// closeTicketWhenTheUploadPipelineReadsTheAllowlist plays the lost race for an
// upload against the real stack: the ticket is open when the handler authorises
// the upload, and a close commits before the attachment row is written.
//
// An upload is authorised first and written last, with the file read, checked
// and stored in between, and the first thing that happens after the
// authorisation is a read of the attachment_allowed_types setting. So the
// settings table is replaced, inside the harness transaction (rolled back with
// everything else), by a view that closes the ticket as a side effect of
// serving that one key. Nothing is mocked: the handler, the service, the row
// lock and Postgres are the real ones, and the close lands exactly between the
// check and the insert.
//
// A trigger on the attachments insert cannot do this: it fires after the
// service has already locked the row and found it open.
func closeTicketWhenTheUploadPipelineReadsTheAllowlist(t *testing.T, h *harness, ticketID string) {
	t.Helper()
	ctx := context.Background()
	// The allowlist needs a row to be served from.
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAttachmentAllowedTypes, []byte(`[".txt"]`)))

	closed := statusIDNamed(t, h, ticket.StatusNameClosed).String()
	for _, stmt := range []string{
		`ALTER TABLE settings RENAME TO settings_real`,
		`CREATE FUNCTION race_close_ticket() RETURNS boolean LANGUAGE plpgsql AS $$
		 BEGIN
		   UPDATE tickets SET status_id = '` + closed + `', closed_at = now()
		   WHERE id = '` + ticketID + `' AND closed_at IS NULL;
		   RETURN true;
		 END $$`,
		`CREATE VIEW settings AS
		   SELECT s.key,
		          CASE WHEN s.key = 'attachment_allowed_types' AND race_close_ticket()
		               THEN s.value ELSE s.value END AS value
		   FROM settings_real s`,
	} {
		_, err := h.tx.ExecContext(ctx, stmt)
		require.NoError(t, err, stmt)
	}
}

func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			found = append(found, path)
		}
		return nil
	}))
	return found
}

func TestUpload_AGuestUploadLosingTheRaceToTheCloseIsTheGenericNotFound(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := seedGuestTicket(t, h)
	closeTicketWhenTheUploadPipelineReadsTheAllowlist(t, h, tk.ID.String())

	res := uploadGuestAttachment(t, h, token, "photo.txt", []byte("what it looks like"))
	body := readBody(t, res)

	require.Equal(t, http.StatusNotFound, res.StatusCode,
		"the ticket closed after the upload was authorised: the guest is told what a bad link is told; %s", body)
	require.Equal(t, `{"error":{"code":"not_found","message":"not found"}}`+"\n", body)
	require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID), "the race really happened")
	atts, err := h.ticketSvc.ListAttachments(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Empty(t, atts, "no row for a closed ticket")
	require.Empty(t, filesUnder(t, h.attachDir), "and the stored file was removed")
}

func TestUpload_AReporterUploadLosingTheRaceToTheCloseIsConflict(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Needs a screenshot", Description: "x", CategoryID: h.catID, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	closeTicketWhenTheUploadPipelineReadsTheAllowlist(t, h, tk.ID.String())

	res := uploadAttachmentAs(t, h, h.userKey, tk.ID.String(), "shot.txt", []byte("log"))
	body := readBody(t, res)

	require.Equal(t, http.StatusConflict, res.StatusCode, body)
	require.Contains(t, body, "ticket_closed")
	require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID), "the race really happened")
	atts, err := h.ticketSvc.ListAttachments(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Empty(t, atts)
	require.Empty(t, filesUnder(t, h.attachDir))
}

// Staff are not asked: the same race for a member of staff stores the upload,
// because staff may still annotate a closed ticket.
func TestUpload_AStaffUploadRacingTheCloseStillLands(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Documenting the fix", Description: "x", CategoryID: h.catID, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	closeTicketWhenTheUploadPipelineReadsTheAllowlist(t, h, tk.ID.String())

	res := uploadAttachmentAs(t, h, h.apiKey, tk.ID.String(), "note.txt", []byte("log"))
	body := readBody(t, res)

	require.Equal(t, http.StatusCreated, res.StatusCode, body)
	require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
}
