package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// uploadAttachment posts a file and returns the response.
func uploadAttachment(t *testing.T, h *harness, ticketID, name string, content []byte) *http.Response {
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
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec.Result()
}

func ticketForUpload(t *testing.T, h *harness) string {
	t.Helper()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Attachment", Description: "has one", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)
	return tk.ID.String()
}

// The defect this closes.
//
// The harness configures no scanner address, so every scan is Unavailable —
// exactly the state of an instance whose ClamAV container is down, or was
// never deployed. The old code returned "not infected" for that and accepted
// the file with one slog.Warn.
//
// Under the default policy the upload is now refused. 503, not 4xx: nothing is
// wrong with the request, the server cannot currently do its job.
func TestUpload_IsRefusedWhenItCannotBeScanned(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := ticketForUpload(t, h)

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentScanPolicy, "required"))

	res := uploadAttachment(t, h, id, "notes.txt", []byte("hello"))
	defer res.Body.Close()

	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode,
		"an unscannable upload must not be accepted as clean")
	require.NotEmpty(t, res.Header.Get("Retry-After"),
		"a 503 the caller should retry needs to say when")

	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "scanner_unavailable", body.Error.Code)
	require.NotContains(t, body.Error.Message, "dial",
		"the refusal must not describe internal infrastructure")
}

// Permissive is the old behaviour, reachable only by choosing it.
func TestUpload_PermissiveAcceptsWhatItCannotScan(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := ticketForUpload(t, h)

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentScanPolicy, "permissive"))

	res := uploadAttachment(t, h, id, "notes.txt", []byte("hello"))
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

// Off means off, and says so rather than being inferred from an unset variable.
func TestUpload_OffSkipsScanningEntirely(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := ticketForUpload(t, h)

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentScanPolicy, "off"))

	res := uploadAttachment(t, h, id, "notes.txt", []byte("hello"))
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

// With no scanner configured and nothing saved, the default is off — an
// instance that never asked for scanning keeps working. Configuring an address
// and saving nothing else defaults to required, because configuring a scanner
// and then accepting unscanned files is not a position anyone holds on
// purpose.
func TestScanPolicy_DefaultsOnWhetherAScannerIsConfigured(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.Equal(t, "off", string(h.adminSvc.AttachmentScanPolicy(ctx, false)),
		"no scanner, no policy: an instance that never asked for scanning keeps working")
	require.Equal(t, "required", string(h.adminSvc.AttachmentScanPolicy(ctx, true)),
		"a configured scanner defaults to refusing what it cannot check")
}

// A value that is neither valid nor absent must not disable scanning. It falls
// back the same way an empty one does, so a typo cannot quietly turn the
// control off.
func TestScanPolicy_AnUnrecognisedValueDoesNotDisableScanning(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentScanPolicy, "enabled"))
	require.Equal(t, "required", string(h.adminSvc.AttachmentScanPolicy(ctx, true)),
		"a typo must not read as 'off'")
}

// And the settings endpoint refuses it up front, so the operator learns.
func TestSettings_RefuseAnInvalidScanPolicyAndAddress(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// A signed-in administrator, not an API key: both keys are session-gated,
	// so a key is refused with 403 before validation is ever reached.
	sess := &session{h: h}
	res, body := sess.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "admin login; body: %s", body)

	for _, tc := range []struct{ name, key, value, code string }{
		{"policy typo", admin.KeyAttachmentScanPolicy, "enabled", "invalid_scan_policy"},
		{"policy case", admin.KeyAttachmentScanPolicy, "REQUIRED", "invalid_scan_policy"},
		{"address with no scheme", admin.KeyAttachmentScanAddress, "clamav:3310", "invalid_scanner_address"},
		{"address with no port", admin.KeyAttachmentScanAddress, "tcp://clamav", "invalid_scanner_address"},
		{"address with an unknown scheme", admin.KeyAttachmentScanAddress, "http://clamav:3310", "invalid_scanner_address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{tc.key: tc.value})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", raw)

			var body struct {
				Error struct{ Code string } `json:"error"`
			}
			require.NoError(t, json.Unmarshal(raw, &body))
			require.Equal(t, tc.code, body.Error.Code)
		})
	}

	// And the forms that should work, do.
	for _, ok := range []map[string]any{
		{admin.KeyAttachmentScanPolicy: "required"},
		{admin.KeyAttachmentScanPolicy: "off"},
		{admin.KeyAttachmentScanAddress: "tcp://clamav:3310"},
		{admin.KeyAttachmentScanAddress: "unix:///var/run/clamav/clamd.sock"},
		{admin.KeyAttachmentScanAddress: ""},
	} {
		res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", ok)
		require.Equal(t, http.StatusNoContent, res.StatusCode, "%v; body: %s", ok, raw)
	}
}

// An administrator must be able to see that scanning is degraded without
// reading logs or waiting for an upload to fail.
func TestSecurityWarnings_ReportWhatScanningIsActuallyDoing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentScanPolicy, "required"))

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)

	var body struct {
		AttachmentScanning struct {
			Policy     string `json:"policy"`
			Configured bool   `json:"configured"`
			Reachable  bool   `json:"reachable"`
			Effect     string `json:"effect"`
		} `json:"attachment_scanning"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))

	require.Equal(t, "required", body.AttachmentScanning.Policy)
	require.False(t, body.AttachmentScanning.Configured, "the harness configures no scanner")
	require.False(t, body.AttachmentScanning.Reachable,
		"reachability is a live ping, not what the settings claim")
	require.Contains(t, body.AttachmentScanning.Effect, "refus",
		"an administrator needs the consequence in words, not two booleans to combine")
}

// Reachability has to be a live ping, not "an address is configured".
//
// The difference is the whole value of the field: an instance whose ClamAV
// container died still has an address in its configuration, and reporting that
// as healthy is exactly the blindness this issue is about. Here the address
// points at a port with nothing behind it.
func TestSecurityWarnings_ReachabilityIsAPingNotASetting(t *testing.T) {
	h, cleanup := newHarnessWith(t, 0, "tcp://127.0.0.1:1")
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)

	var body struct {
		AttachmentScanning struct {
			Configured bool   `json:"configured"`
			Reachable  bool   `json:"reachable"`
			Policy     string `json:"policy"`
			Effect     string `json:"effect"`
		} `json:"attachment_scanning"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))

	require.True(t, body.AttachmentScanning.Configured, "an address is set")
	require.False(t, body.AttachmentScanning.Reachable,
		"nothing is listening there, and saying otherwise is the blindness this fixes")
	require.Equal(t, "required", body.AttachmentScanning.Policy,
		"a configured address defaults to required")
	require.Contains(t, body.AttachmentScanning.Effect, "refus")
}

// And with a configured-but-dead scanner, uploads are refused rather than
// accepted — the production shape of the defect, where an address exists and
// the container is gone.
func TestUpload_RefusedWhenAConfiguredScannerIsDead(t *testing.T) {
	h, cleanup := newHarnessWith(t, 0, "tcp://127.0.0.1:1")
	defer cleanup()
	id := ticketForUpload(t, h)

	res := uploadAttachment(t, h, id, "notes.txt", []byte("hello"))
	defer res.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode,
		"a dead scanner with an address configured must refuse, not accept")
}

// The infected path had no test at all: mutants that returned "upload allowed"
// for an Infected verdict, and that answered 201 instead of 422, both survived
// the whole suite. That is the one path whose job is to block malware.
//
// Needs a scanner that finds something, so it runs a fake clamd that answers
// FOUND to everything.
func TestUpload_InfectedFileIsRefused(t *testing.T) {
	addr := fakeInfectedScanner(t)
	h, cleanup := newHarnessWith(t, 0, addr)
	defer cleanup()
	id := ticketForUpload(t, h)

	res := uploadAttachment(t, h, id, "notes.txt", []byte("pretend this is nasty"))
	defer res.Body.Close()

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode,
		"an infected upload must be refused, and not as a 503 the caller retries")

	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "infected", body.Error.Code)
	require.Contains(t, body.Error.Message, "Eicar-Test-Signature",
		"naming what was found is how an operator tells a false positive from a real one")
}

// A clean verdict from a live scanner accepts the upload, so the refusal above
// is the scanner talking rather than the policy refusing everything.
func TestUpload_CleanFileFromALiveScannerIsAccepted(t *testing.T) {
	addr := fakeCleanScanner(t)
	h, cleanup := newHarnessWith(t, 0, addr)
	defer cleanup()
	id := ticketForUpload(t, h)

	res := uploadAttachment(t, h, id, "notes.txt", []byte("harmless"))
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

// The saved address must actually be used. It was accepted, validated, stored
// — and read by nothing, because the Scanner was built once at startup from
// the environment. An operator moving ClamAV would have saved a new address,
// got a 204, and silently stopped being protected.
func TestUpload_UsesTheSavedScannerAddressNotOnlyTheEnvironment(t *testing.T) {
	// Environment points nowhere; the setting points at a scanner that finds
	// something. If the setting is ignored, the scan is Unavailable and the
	// answer is 503 rather than 422.
	h, cleanup := newHarnessWith(t, 0, "")
	defer cleanup()
	ctx := context.Background()
	id := ticketForUpload(t, h)

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentScanAddress, fakeInfectedScanner(t)))

	res := uploadAttachment(t, h, id, "notes.txt", []byte("pretend this is nasty"))
	defer res.Body.Close()
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode,
		"the saved address must be the one that gets dialled")
}

// And the setting wins over the environment, so moving the scanner works
// without a redeploy.
func TestUpload_TheSavedAddressWinsOverTheEnvironment(t *testing.T) {
	h, cleanup := newHarnessWith(t, 0, "tcp://127.0.0.1:1") // dead
	defer cleanup()
	ctx := context.Background()
	id := ticketForUpload(t, h)

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentScanAddress, fakeCleanScanner(t)))

	res := uploadAttachment(t, h, id, "notes.txt", []byte("harmless"))
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode,
		"a live saved address must override a dead environment one")
}

// Where the scanner lives is a route to disabling scanning: point it at a
// daemon that answers OK to everything and every upload passes. A machine
// credential must not be able to do that, for the same reason it cannot
// repoint the identity provider.
func TestSettings_ScannerKeysRequireASession(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, key := range []string{admin.KeyAttachmentScanAddress, admin.KeyAttachmentScanPolicy} {
		t.Run(key, func(t *testing.T) {
			value := "off"
			if key == admin.KeyAttachmentScanAddress {
				value = "tcp://evil.test:3310"
			}
			res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{key: value})
			defer res.Body.Close()
			require.Equal(t, http.StatusForbidden, res.StatusCode,
				"an API key must not be able to disable or redirect scanning")

			var body struct {
				Error struct{ Code string } `json:"error"`
			}
			require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
			require.Equal(t, "session_required", body.Error.Code)
		})
	}
}

// fakeClamdReplying answers every scan with reply, and PING with PONG.
func fakeClamdReplying(t *testing.T, reply string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				r := bufio.NewReader(conn)
				cmd, err := r.ReadString(0)
				if err != nil {
					return
				}
				if cmd == "zPING\x00" {
					_, _ = conn.Write([]byte("PONG\x00"))
					return
				}
				for {
					var size [4]byte
					if _, err := io.ReadFull(r, size[:]); err != nil {
						return
					}
					n := binary.BigEndian.Uint32(size[:])
					if n == 0 {
						break
					}
					if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
						return
					}
				}
				_, _ = conn.Write([]byte(reply + "\x00"))
			}()
		}
	}()
	return "tcp://" + ln.Addr().String()
}

func fakeInfectedScanner(t *testing.T) string {
	return fakeClamdReplying(t, "stream: Eicar-Test-Signature FOUND")
}

func fakeCleanScanner(t *testing.T) string {
	return fakeClamdReplying(t, "stream: OK")
}
