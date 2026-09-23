package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/image/bmp"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// #165 step 3: what this instance accepts as an attachment stops being a Go
// map and becomes an operator setting.
//
// The reason it is safe to hand over is step 1, which is merged: every
// download is application/octet-stream with an attachment disposition and
// nothing is ever rendered, so the type list is no longer a security boundary.
// It is a policy choice — an IT team triaging a suspicious .exe has a real
// reason to attach one, and a deployment that wants PDF and nothing else has
// an equally real reason to say so.
//
// These tests drive the setting the way an operator would and then check the
// upload path, because "the value was saved" and "the value decides anything"
// are different facts. The scanner address was accepted, validated and stored
// for a release before anyone noticed nothing read it.

// setAllowedTypes writes the raw JSON value of the allowlist key directly
// through the domain service, which is how the other settings-driven upload
// tests arrange state (see TestUpload_UsesTheSavedScannerAddressNotOnlyTheEnvironment).
// It deliberately bypasses the HTTP validation so that these tests exercise the
// upload gate rather than the validator; the validator has its own test below.
func setAllowedTypes(t *testing.T, h *harness, rawJSON string) {
	t.Helper()
	require.NoError(t, h.adminSvc.SetRaw(context.Background(),
		admin.KeyAttachmentAllowedTypes, []byte(rawJSON)))
}

// ── Sample content ───────────────────────────────────────────────────────────
//
// Real bytes, not placeholders: the upload path checks magic bytes and decodes
// images, so a file named .png holding "hello" would be refused for a reason
// that has nothing to do with the allowlist and the test would pass for the
// wrong reason.

func samplePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func sampleJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	return buf.Bytes()
}

func sampleBMP(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	var buf bytes.Buffer
	require.NoError(t, bmp.Encode(&buf, img))
	return buf.Bytes()
}

// samplePDF and sampleZIP carry the signatures the upload path looks for.
func samplePDF() []byte { return []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF\n") }

// DOCX and XLSX are both ZIP containers, which is all the magic check knows.
func sampleZIP() []byte { return append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0}, 60)...) }

// A Windows executable: the type an IT team triaging a suspicious file wants
// to attach, and the reason the list is being opened up at all.
func sampleEXE() []byte {
	return append([]byte("MZ\x90\x00\x03\x00\x00\x00"), bytes.Repeat([]byte{0}, 120)...)
}

// ── The default ──────────────────────────────────────────────────────────────

// An instance that upgrades and never touches the setting must accept
// precisely what it accepted before — not a subset chosen as "safer", and not
// a superset because the new list was written from memory.
//
// Both halves matter. The accept half catches a default that lost an entry,
// which would break uploads that worked yesterday. The refuse half catches a
// default that gained one, which is the more dangerous direction: the whole
// point of the setting is that widening is a decision an operator makes on
// purpose.
func TestUpload_DefaultAllowedTypesAreExactlyTodaysSet(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := ticketForUpload(t, h)

	// The key is deliberately never written: this is the unset state.

	for _, tc := range []struct {
		name    string
		content []byte
	}{
		{"report.pdf", samplePDF()},
		{"report.docx", sampleZIP()},
		{"sheet.xlsx", sampleZIP()},
		{"notes.txt", []byte("plain text")},
		{"server.log", []byte("2026-09-23 boot")},
		{"photo.jpg", sampleJPEG(t)},
		{"photo.jpeg", sampleJPEG(t)},
		{"shot.png", samplePNG(t)},
		{"scan.bmp", sampleBMP(t)},
	} {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			res := uploadAttachment(t, h, id, tc.name, tc.content)
			defer res.Body.Close()
			require.Equal(t, http.StatusCreated, res.StatusCode,
				"%s was accepted before this change and must still be", tc.name)
		})
	}

	for _, name := range []string{"tool.exe", "archive.zip", "page.html", "vector.svg", "script.js"} {
		t.Run("refuses "+name, func(t *testing.T) {
			res := uploadAttachment(t, h, id, name, sampleEXE())
			defer res.Body.Close()
			require.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode,
				"%s was refused before this change; widening the default is not something an upgrade does silently", name)
		})
	}
}

// ── Extending ────────────────────────────────────────────────────────────────

// The case the change exists for. An IT department triaging a suspicious
// executable has a legitimate reason to attach one, and today there is no way
// to say so short of editing a Go file and redeploying.
//
// The upload is attempted before and after the setting changes, in one test,
// so that an implementation which simply accepts everything fails the first
// half rather than passing the second.
func TestUpload_ExtendingTheAllowlistMakesARefusedTypeWork(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := ticketForUpload(t, h)

	before := uploadAttachment(t, h, id, "sample.exe", sampleEXE())
	defer before.Body.Close()
	require.Equal(t, http.StatusUnsupportedMediaType, before.StatusCode,
		"precondition: .exe is not on the shipped default")

	setAllowedTypes(t, h, `[".pdf", ".docx", ".xlsx", ".txt", ".log", ".jpg", ".jpeg", ".png", ".bmp", ".exe"]`)

	after := uploadAttachment(t, h, id, "sample.exe", sampleEXE())
	defer after.Body.Close()
	require.Equal(t, http.StatusCreated, after.StatusCode,
		"an operator who has allowed .exe must be able to upload one")

	// The built-in extension-to-MIME map has no answer for .exe, and a stored
	// mime_type of "" would reach the ticket UI as a blank type column. The
	// issue settles this as "the detector's type if it has one, otherwise
	// application/octet-stream" — either satisfies this, which is deliberate:
	// which of the two it is belongs to the detection step, not to this one.
	var att struct {
		MimeType string `json:"mime_type"`
	}
	require.NoError(t, json.NewDecoder(after.Body).Decode(&att))
	require.NotEmpty(t, att.MimeType,
		"a type the built-in map does not know must still be stored with some MIME type")

	// And the setting must be read per upload rather than once at startup —
	// the same defect the scanner address had, where a saved value was never
	// consulted again.
	list := h.do(t, http.MethodGet, "/api/v1/tickets/"+id+"/attachments", nil)
	defer list.Body.Close()
	var attachments []struct {
		Filename string `json:"filename"`
	}
	require.NoError(t, json.NewDecoder(list.Body).Decode(&attachments))
	require.Len(t, attachments, 1, "exactly the accepted upload should be recorded")
	require.Equal(t, "sample.exe", attachments[0].Filename)
}

// ── Restricting ──────────────────────────────────────────────────────────────

// The other direction, which is the one a cautious deployment wants: PDF and
// nothing else. A setting that could only add would be half a control.
func TestUpload_RestrictingTheAllowlistRefusesAPreviouslyAcceptedType(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := ticketForUpload(t, h)

	setAllowedTypes(t, h, `[".pdf"]`)

	refused := uploadAttachment(t, h, id, "notes.txt", []byte("plain text"))
	defer refused.Body.Close()
	require.Equal(t, http.StatusUnsupportedMediaType, refused.StatusCode,
		".txt is on the shipped default but not on this operator's list")

	// Checked here rather than at the end: the helper requires the attachment
	// directory to be empty, so it has to run before the accepted upload below.
	assertUploadLeftNothingBehind(t, h, id)

	accepted := uploadAttachment(t, h, id, "report.pdf", samplePDF())
	defer accepted.Body.Close()
	require.Equal(t, http.StatusCreated, accepted.StatusCode,
		"the one type the operator kept must still work; an implementation that refuses everything is not a restriction")
}

// An empty list is a legitimate choice — "this instance takes no attachments
// at all" — and it is the one value most likely to be mistaken for "unset" by
// an implementation that reaches for a default whenever the list looks empty.
//
// The two states are different instructions and must not collapse into one.
func TestUpload_AnEmptyAllowlistMeansNoAttachmentsNotUnset(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := ticketForUpload(t, h)

	setAllowedTypes(t, h, `[]`)

	for _, tc := range []struct {
		name    string
		content []byte
	}{
		{"report.pdf", samplePDF()},
		{"notes.txt", []byte("plain text")},
		{"shot.png", samplePNG(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := uploadAttachment(t, h, id, tc.name, tc.content)
			defer res.Body.Close()
			require.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode,
				"an empty list means no attachments; falling back to the default here would silently ignore the operator")
		})
	}

	assertUploadLeftNothingBehind(t, h, id)
}

// ── Order of operations ──────────────────────────────────────────────────────

// The allowlist check runs on the claimed extension, before the bytes are read
// and long before anything expensive happens to them. That ordering is what
// makes a refusal cheap and what decides the status code a caller sees.
//
// Both cases below would return a different, plausible-looking error if the
// check moved later — and a test that only asserted "not 201" would not
// notice.
func TestUpload_TheTypeIsRefusedBeforeTheContentIsLookedAt(t *testing.T) {
	t.Run("before the image is decoded", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		id := ticketForUpload(t, h)

		setAllowedTypes(t, h, `[".pdf"]`)

		// A small file that decodes to 142 MB. If the allowlist were checked
		// after the decode this would be 422 invalid_image, which blames the
		// file for being a bad image when the real answer is that this
		// instance does not take PNGs at all.
		res := uploadAttachment(t, h, id, "bomb.png", decompressionBombPNG(t))
		defer res.Body.Close()
		require.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode,
			"a type that is not allowed must be refused as a type, not as a bad image")

		assertUploadLeftNothingBehind(t, h, id)
	})

	t.Run("before the scanner is asked", func(t *testing.T) {
		// A scanner that calls everything infected. If the allowlist were
		// checked after the scan, the answer would be 422 infected — which
		// tells an operator their user uploaded malware when in fact the file
		// was never eligible to be uploaded at all.
		h, cleanup := newHarnessWith(t, 0, fakeInfectedScanner(t))
		defer cleanup()
		id := ticketForUpload(t, h)

		setAllowedTypes(t, h, `[".pdf"]`)

		res := uploadAttachment(t, h, id, "notes.txt", []byte("plain text"))
		defer res.Body.Close()
		require.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode,
			"a type that is not allowed must never reach the scanner")

		assertUploadLeftNothingBehind(t, h, id)
	})
}

// decompressionBombPNG is a tiny file with an enormous raster: uniform colour
// compresses to almost nothing. The equivalent helper in image_bomb_test.go is
// in the internal test package and not reachable from here.
func decompressionBombPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 12000, 12000))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	require.Less(t, buf.Len(), 1<<20, "the point is that the file is small")
	return buf.Bytes()
}

// ── Validation ───────────────────────────────────────────────────────────────

// A setting that is accepted and then ignored is worse than a refusal — the
// same reasoning as the scan policy in #167, and the same reasoning as the
// ticket prefix before it, which was saved with a 204 and then silently
// dropped in favour of the default.
//
// Here the ignored value is what the instance will accept, so an operator who
// types "exe" instead of ".exe" and sees a 204 has been told their deployment
// now takes executables when it does not.
func TestSettings_RefusesAnInvalidAllowedTypeAndNamesIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// A signed-in administrator, not an API key: the key is session-gated, so
	// an API key is refused with 403 before validation is ever reached.
	sess := adminSession(t, h)

	for _, tc := range []struct {
		name  string
		entry string
	}{
		{"no leading dot", `"exe"`},
		{"uppercase", `".EXE"`},
		{"a dot and nothing else", `"."`},
		{"an inner dot", `".tar.gz"`},
		{"a space", `".p df"`},
		{"seventeen characters", `".abcdefghijklmnopq"`},
		{"not a string", `7`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{admin.KeyAttachmentAllowedTypes: json.RawMessage(
					`[".pdf", ` + tc.entry + `]`)})
			require.Equal(t, http.StatusBadRequest, res.StatusCode,
				"an entry that cannot be an extension must be refused, not saved and ignored; body: %s", raw)

			// Naming the entry is the difference between a refusal an operator
			// can act on and one they have to guess at. Quotes stripped
			// because the message is prose, not JSON.
			if bad := bytes.Trim([]byte(tc.entry), `"`); len(bad) > 0 && tc.entry != `7` {
				require.Contains(t, string(raw), string(bad),
					"the refusal must say which entry was wrong")
			}
		})
	}

	t.Run("an empty entry", func(t *testing.T) {
		res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAttachmentAllowedTypes: []string{".pdf", ""}})
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", raw)
	})

	t.Run("not an array at all", func(t *testing.T) {
		res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAttachmentAllowedTypes: ".pdf"})
		require.Equal(t, http.StatusBadRequest, res.StatusCode,
			"the value is a JSON array of extensions; a bare string is not one; body: %s", raw)
	})

	// The forms that should work, do. Without this a validator that refused
	// every write would pass everything above.
	for _, ok := range []map[string]any{
		{admin.KeyAttachmentAllowedTypes: []string{".pdf"}},
		{admin.KeyAttachmentAllowedTypes: []string{}},
		{admin.KeyAttachmentAllowedTypes: []string{".pdf", ".docx", ".exe", ".zip", ".7z"}},
		{admin.KeyAttachmentAllowedTypes: []string{".abcdefghijklmnop"}}, // sixteen, the limit
	} {
		res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", ok)
		require.Equal(t, http.StatusNoContent, res.StatusCode, "%v must be accepted; body: %s", ok, raw)
	}
}

// One bad entry rejects the whole write, including the other keys sent with it.
//
// Applying the valid half of a refused request is the shape that produces a
// configuration nobody chose: the operator sees a 400, believes nothing
// happened, and the instance is running with a name they did not confirm.
func TestSettings_AnInvalidAllowedTypeRejectsTheWholeWrite(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	const name = "Name From A Refused Write"

	res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		admin.KeySiteName:               name,
		admin.KeyAttachmentAllowedTypes: []string{".pdf", "exe"},
	})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", raw)

	require.NotEqual(t, name, h.adminSvc.SiteName(context.Background()),
		"a refused write must leave every key it carried untouched")
}

// ── Session gating ───────────────────────────────────────────────────────────

// What the instance accepts at all is a route to uploading what you have just
// allowed. A leaked API key must not be able to widen it, for the same reason
// it cannot repoint the identity provider or the virus scanner.
//
// The second half is what stops this being a lock on the door of an empty
// room: a signed-in administrator must be able to make the same change, and
// the change must take effect.
func TestSettings_AllowedTypesRequireASession(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := ticketForUpload(t, h)

	widened := []string{".pdf", ".docx", ".xlsx", ".txt", ".log", ".jpg", ".jpeg", ".png", ".bmp", ".exe"}

	t.Run("an API key is refused, and changes nothing", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAttachmentAllowedTypes: widened})
		defer res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode,
			"an API key must not be able to widen what this instance accepts")

		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
		require.Equal(t, "session_required", body.Error.Code)

		// Refused at the door is not enough; the list must be unchanged.
		up := uploadAttachment(t, h, id, "sample.exe", sampleEXE())
		defer up.Body.Close()
		require.Equal(t, http.StatusUnsupportedMediaType, up.StatusCode,
			"the refused write must not have taken effect")
	})

	t.Run("a signed-in administrator can, and it takes effect", func(t *testing.T) {
		sess := adminSession(t, h)

		res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAttachmentAllowedTypes: widened})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", raw)

		up := uploadAttachment(t, h, id, "sample.exe", sampleEXE())
		defer up.Body.Close()
		require.Equal(t, http.StatusCreated, up.StatusCode,
			"the whole point of the setting is that an administrator can change it")
	})
}

// adminSession logs the seeded administrator in for real and returns the
// cookie-carrying client. The session-gated keys cannot be reached with
// h.doAsAdmin, which authenticates with an API key.
func adminSession(t *testing.T, h *harness) *session {
	t.Helper()
	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)
	return s
}
