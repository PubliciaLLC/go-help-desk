package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/server"
)

// #458: when ATTACHMENT_DIR cannot be written, uploads answer a clear 503
// instead of failing partway, an administrator is told, downloads of files
// already stored keep working, and fixing the directory recovers without a
// restart. Everything else on the instance runs.

type storageWarning struct {
	Writable bool   `json:"writable"`
	Problem  string `json:"problem"`
}

func attachmentStorageWarning(t *testing.T, h *harness) storageWarning {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var body struct {
		AttachmentStorage storageWarning `json:"attachment_storage"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	return body.AttachmentStorage
}

func requireUnavailable(t *testing.T, res *http.Response) {
	t.Helper()
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode, string(raw))
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &body), string(raw))
	require.Equal(t, "attachments_unavailable", body.Error.Code, string(raw))
	require.NotEmpty(t, body.Error.Message)
}

// breakStorage makes the attachment directory unusable for a reason that holds
// whoever runs the tests (root ignores permission bits): the path is a regular
// file, so it can be neither created in nor listed. fixStorage puts a
// directory back.
func breakStorage(t *testing.T, h *harness) {
	t.Helper()
	require.NoError(t, os.RemoveAll(h.attachDir))
	require.NoError(t, os.WriteFile(h.attachDir, []byte("not a directory"), 0o600))
}

func fixStorage(t *testing.T, h *harness) {
	t.Helper()
	require.NoError(t, os.Remove(h.attachDir))
	require.NoError(t, os.MkdirAll(h.attachDir, 0o700))
}

func TestAttachmentStorage_HealthyByDefault(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	w := attachmentStorageWarning(t, h)
	require.True(t, w.Writable)
	require.Empty(t, w.Problem)
}

func TestAttachmentStorage_UnwritableRefusesStaffUploadsAndTellsAdmins(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := ticketForUpload(t, h)
	breakStorage(t, h)

	requireUnavailable(t, uploadAttachment(t, h, id, "notes.txt", []byte("hello")))

	w := attachmentStorageWarning(t, h)
	require.False(t, w.Writable)
	require.NotEmpty(t, w.Problem, "an administrator needs the reason")
	require.NotContains(t, w.Problem, h.attachDir, "the reason must not carry the server's path")
}

func TestAttachmentStorage_UnwritableRefusesRequesterUploads(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain,
		Subject: "Mine", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	breakStorage(t, h)

	// The reporter's own key on their own ticket: authorised, so only the
	// storage can be what refuses it.
	requireUnavailable(t, uploadAttachmentAs(t, h, h.userKey, tk.ID.String(), "shot.txt", []byte("log")))
}

func TestAttachmentStorage_UnwritableRefusesGuestUploads(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	_, token := seedGuestTicket(t, h)
	setGuestUploads(t, h, true) // off by default (#445)
	breakStorage(t, h)

	requireUnavailable(t, uploadGuestAttachment(t, h, token, "photo.txt", []byte("what it looks like")))
}

func TestAttachmentStorage_RecoversWithoutARestart(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := ticketForUpload(t, h)

	breakStorage(t, h)
	requireUnavailable(t, uploadAttachment(t, h, id, "notes.txt", []byte("hello")))

	fixStorage(t, h)
	require.True(t, attachmentStorageWarning(t, h).Writable)
	res := uploadAttachment(t, h, id, "notes.txt", []byte("hello"))
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

// An empty ATTACHMENT_DIR must not pass by writing the probe into the system
// temp directory.
func TestAttachmentStorage_EmptyDirIsUnwritable(t *testing.T) {
	require.EqualError(t, server.CheckAttachmentDir(""), "ATTACHMENT_DIR is empty")

	h, cleanup := newHarnessCfg(t, 0, "", func(c *config.Config) { c.AttachmentDir = "" })
	defer cleanup()
	id := ticketForUpload(t, h)

	w := attachmentStorageWarning(t, h)
	require.False(t, w.Writable)
	require.Equal(t, "ATTACHMENT_DIR is empty", w.Problem)
	requireUnavailable(t, uploadAttachment(t, h, id, "notes.txt", []byte("hello")))
}

func TestAttachmentStorage_RestOfTheInstanceKeepsRunning(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	breakStorage(t, h)

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/settings", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}

// A read-only directory (as opposed to a missing one) still serves what is in
// it. Skipped as root, which ignores the permission bits being tested.
func TestAttachmentStorage_ReadOnlyDirectoryStillServesDownloads(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	h, cleanup := newHarness(t)
	defer cleanup()
	id := ticketForUpload(t, h)

	up := uploadAttachment(t, h, id, "notes.txt", []byte("hello"))
	defer up.Body.Close()
	require.Equal(t, http.StatusCreated, up.StatusCode)
	var att struct {
		ID string `json:"id"`
	}
	raw, _ := io.ReadAll(up.Body)
	require.NoError(t, json.Unmarshal(raw, &att), string(raw))

	// Read and traverse, no write. Restored before the harness's TempDir is
	// removed, or the removal itself fails.
	require.NoError(t, os.Chmod(h.attachDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(h.attachDir, 0o700) })

	requireUnavailable(t, uploadAttachment(t, h, id, "more.txt", []byte("again")))

	dl := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id+"/attachments/"+att.ID, nil)
	defer dl.Body.Close()
	require.Equal(t, http.StatusOK, dl.StatusCode)
	body, _ := io.ReadAll(dl.Body)
	require.Equal(t, "hello", string(body))
}
