package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// Whether a file whose content contradicts its name is refused or stored
// wrapped is the operator's decision, and refusing is the decision every
// instance that upgrades into this feature already has.
//
// The setting is a deliberate mirror of attachment_infected_handling: same
// shape, same words, same default. It is the same question about a different
// claim — does this instance store a file it has reason to distrust, or turn
// it away — and an operator who has reasoned about one should not have to
// start over on the other.

// htmlNamedPDF is the case the whole setting is about: HTML arriving as
// report.pdf. It is a mismatch, and text/html is not on any shipped
// allowlist, so it is the one condition this setting governs.
var htmlNamedPDF = []byte("<html><script>alert(1)</script></html>")

// The default refuses, an explicit "refuse" refuses, only an explicit "wrap"
// stores — and a typo refuses rather than landing on the permissive option.
//
// The unset row is the one that matters most. It is the state of every
// instance that upgrades and never opens the settings page, and before this
// branch that instance answered 415. It still does.
func TestUpload_AContentMismatchIsRefusedUnlessTheOperatorChoseWrap(t *testing.T) {
	cases := []struct {
		name       string
		handling   string // "" means the key is never written
		wantStatus int
	}{
		{"unset, which is what every upgraded instance has", "", http.StatusUnsupportedMediaType},
		{"refuse, chosen explicitly", "refuse", http.StatusUnsupportedMediaType},
		{"wrap, chosen explicitly", "wrap", http.StatusCreated},
		{"a typo falls back to refuse, never to the permissive option", "Wrap", http.StatusUnsupportedMediaType},
		{"the other setting's value is not this setting's value", "quarantine", http.StatusUnsupportedMediaType},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			id := ticketForUpload(t, h)

			if tc.handling != "" {
				require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentMismatchHandling, tc.handling))
			}

			res := uploadAttachment(t, h, id, "report.pdf", htmlNamedPDF)
			defer res.Body.Close()
			raw, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus, res.StatusCode, "body: %s", raw)

			if tc.wantStatus == http.StatusCreated {
				// Stored, and stored under the name that is the warning.
				require.Equal(t,
					fmt.Sprintf("suspicious-%08x.zip", crc32.ChecksumIEEE(htmlNamedPDF)),
					storedFilenameOf(t, raw))
				return
			}

			// The refusal an operator relying on the pre-branch behaviour
			// already has wired into whatever reads it: same status, same
			// code, same message.
			var body struct {
				Error struct{ Code, Message string } `json:"error"`
			}
			require.NoError(t, json.Unmarshal(raw, &body))
			require.Equal(t, "invalid_file", body.Error.Code)
			require.Equal(t, "file content does not match the expected type", body.Error.Message)

			// A refusal that left the file on disk would be a wrap with none
			// of the labelling.
			assertUploadLeftNothingBehind(t, h, id)
		})
	}
}

// A mismatch whose detected type IS on the allowlist is untouched by this
// setting: flagged, and stored under its own name, under both values.
//
// There is nothing to contain in a real PNG named .jpg — the operator accepts
// PNGs — only something to say. Without this the setting will eventually be
// "simplified" into refusing every mismatch, which is a warning-shaped refusal
// on an ordinary file.
//
// It is also where the recording is pinned. detected_mime, sha256 and
// content_mismatch are facts about what arrived rather than enforcement, so
// they are written whatever the setting says.
func TestUpload_AMismatchOfAnAllowedTypeIsStoredUnderEitherSetting(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	realPNG := buf.Bytes()

	for _, handling := range []string{"refuse", "wrap"} {
		t.Run(handling, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			id := ticketForUpload(t, h)

			require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentMismatchHandling, handling))

			res := uploadAttachment(t, h, id, "report.pdf", realPNG)
			defer res.Body.Close()
			raw, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", raw)

			var att attachmentJSON
			require.NoError(t, json.Unmarshal(raw, &att))
			require.Equal(t, "report.pdf", att.Filename,
				"an accepted type is neither wrapped nor refused, however it was named")

			require.NotNil(t, att.DetectedMime, "what arrived is recorded whatever the setting says")
			require.Equal(t, "image/png", *att.DetectedMime)
			require.NotNil(t, att.SHA256)
			require.NotEmpty(t, *att.SHA256)

			tid, err := uuid.Parse(id)
			require.NoError(t, err)
			list, err := h.ticketSvc.ListAttachments(ctx, tid)
			require.NoError(t, err)
			require.Len(t, list, 1)
			require.NotNil(t, list[0].ContentMismatch)
			require.True(t, *list[0].ContentMismatch,
				"a PNG named .pdf is still a contradiction and is still recorded")
		})
	}
}

// A file that is both infected and a content mismatch is quarantined, and this
// setting has nothing to say about it.
//
// The two claims are different — "the scanner named this" and "the content is
// not what the name says" — and an operator may reasonably store one and
// refuse the other. The quarantine arm comes first in the handler; if it ever
// stops coming first, an instance that chose to keep samples starts answering
// 415 to the exact file it asked to keep.
func TestUpload_AnInfectedMismatchIsQuarantinedWhateverTheMismatchSettingSays(t *testing.T) {
	h, cleanup := newHarnessWith(t, 0, fakeInfectedScanner(t))
	defer cleanup()
	ctx := context.Background()
	id := ticketForUpload(t, h)

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentInfectedHandling, "quarantine"))
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentMismatchHandling, admin.MismatchHandlingRefuse))

	res := uploadAttachment(t, h, id, "report.pdf", htmlNamedPDF)
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, res.StatusCode,
		"infected handling governs this file, not the mismatch setting; body: %s", raw)

	var att attachmentJSON
	require.NoError(t, json.Unmarshal(raw, &att))
	require.Equal(t, "report.pdf.zip", att.Filename, "the quarantine name, not the suspicious one")
	require.NotNil(t, att.VirusName, "the scanner identified this one")
}

// And the settings endpoint refuses a value it would otherwise accept and then
// ignore, naming the setting.
func TestSettings_RefuseAnInvalidMismatchHandling(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// A signed-in administrator, not an API key: the setting is session-gated,
	// so a key is refused with 403 before validation is ever reached.
	sess := &session{h: h}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	for _, bad := range []string{"Wrap", "quarantine", "zip", ""} {
		res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAttachmentMismatchHandling: bad})
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "%q; body: %s", bad, raw)

		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		require.NoError(t, json.Unmarshal(raw, &body))
		require.Equal(t, "invalid_mismatch_handling", body.Error.Code)
	}

	for _, ok := range []string{"refuse", "wrap"} {
		res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAttachmentMismatchHandling: ok})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "%q; body: %s", ok, raw)
	}
}

// It decides what this instance will hold, so it is session-gated, exactly as
// the infected-handling setting and the allowlist are. A leaked API key must
// not be able to switch an instance into storing files it has decided to turn
// away.
func TestSettings_MismatchHandlingIsSessionGated(t *testing.T) {
	require.Contains(t, admin.AuthCriticalKeys(), admin.KeyAttachmentMismatchHandling)
}

// storedFilenameOf reads the filename out of an already-read 201 body.
func storedFilenameOf(t *testing.T, raw []byte) string {
	t.Helper()
	var att struct {
		Filename string `json:"filename"`
	}
	require.NoError(t, json.Unmarshal(raw, &att))
	return att.Filename
}
