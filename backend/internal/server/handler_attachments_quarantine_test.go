package server_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// eicar is the industry's harmless stand-in for a virus. The fake scanner
// below answers FOUND to anything, so the content is not what triggers the
// verdict — but a test about malware handling that uses "hello" as the sample
// invites someone to read the recovered bytes as incidental. They are the
// point: this is what an analyst gets back out.
const eicar = `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`

// attachmentJSON is what the API returns for one attachment.
//
// The three pointers are pointers because nil is a fact: the file predates the
// inspection. A test that decoded them into strings could not tell "not
// recorded" from "recorded as empty", which is the distinction the whole
// nullable-column design rests on.
type attachmentJSON struct {
	ID           string  `json:"id"`
	TicketID     string  `json:"ticket_id"`
	Filename     string  `json:"filename"`
	MimeType     string  `json:"mime_type"`
	SizeBytes    int64   `json:"size_bytes"`
	DetectedMime *string `json:"detected_mime"`
	SHA256       *string `json:"sha256"`
	VirusName    *string `json:"virus_name"`
}

// An ordinary help desk must not start storing malware because nobody said
// otherwise, and an IT security team that asked for it must get it. Both halves
// are one setting, so both are here: the shipped default and a typo-free
// "refuse" both refuse, and only the operator's explicit "quarantine" accepts.
//
// The unset case is the one that matters most. It is the state of every
// instance that upgrades into this feature and never opens the settings page.
func TestUpload_InfectedIsRefusedUnlessTheOperatorChoseQuarantine(t *testing.T) {
	cases := []struct {
		name       string
		handling   string // "" means the key is never written
		wantStatus int
	}{
		{"unset, which is what every upgraded instance has", "", http.StatusUnprocessableEntity},
		{"refuse, chosen explicitly", "refuse", http.StatusUnprocessableEntity},
		{"quarantine, chosen explicitly", "quarantine", http.StatusCreated},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarnessWith(t, 0, fakeInfectedScanner(t))
			defer cleanup()
			ctx := context.Background()
			id := ticketForUpload(t, h)

			if tc.handling != "" {
				require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentInfectedHandling, tc.handling))
			}

			res := uploadAttachment(t, h, id, "notes.txt", []byte(eicar))
			defer res.Body.Close()
			raw, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus, res.StatusCode, "body: %s", raw)

			if tc.wantStatus != http.StatusUnprocessableEntity {
				return
			}

			var body struct {
				Error struct{ Code, Message string } `json:"error"`
			}
			require.NoError(t, json.Unmarshal(raw, &body))
			require.Equal(t, "infected", body.Error.Code)
			// A refusal that left the sample on disk would be a silent
			// quarantine with none of the labelling, which is the worst of
			// both.
			assertUploadLeftNothingBehind(t, h, id)
		})
	}
}

// The whole feature, end to end, from the analyst's side.
//
// Quarantine is only worth having if what comes back out is usable: the exact
// sample, under a name that will not run, with the hash they look up and the
// name their scanner gave it. Each of those has a plausible wrong version that
// still produces a 201 and a file, so each is asserted rather than inferred.
func TestUpload_QuarantineStoresTheSampleWrappedAndSaysWhatItIs(t *testing.T) {
	h, cleanup := newHarnessWith(t, 0, fakeInfectedScanner(t))
	defer cleanup()
	ctx := context.Background()
	id := ticketForUpload(t, h)

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentInfectedHandling, "quarantine"))

	sample := []byte(eicar)
	// Computed here from the bytes that go up the wire, with the standard
	// library, so it is an independent answer rather than whatever the
	// implementation decided to hash.
	wantHash := hex.EncodeToString(sha256Sum(sample))

	res := uploadAttachment(t, h, id, "notes.txt", sample)
	defer res.Body.Close()
	rawBody, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", rawBody)

	var created attachmentJSON
	require.NoError(t, json.Unmarshal(rawBody, &created))

	// Read back over the wire too. The POST response is built in the handler
	// and could carry fields the insert never stored; the list is what the
	// ticket page actually renders.
	stored := listQuarantineAttachments(t, h, id)
	require.Len(t, stored, 1)
	att := stored[0]
	require.Equal(t, created.Filename, att.Filename, "the POST response and the stored row must agree")

	// The name gains .zip so nothing downstream double-clicks a .exe.
	//
	// That 201 above is also how we know the suffix is added after the
	// allowlist, not before: ".zip" is not an accepted upload type on this
	// instance, so a handler that appended it first would have refused its own
	// wrapper.
	require.Equal(t, "notes.txt.zip", att.Filename,
		"the stored name must say it is an archive")

	// The scanner's name for it. "Infected" alone tells an analyst nothing;
	// the family name is the first thing they act on, and it is the only part
	// of this that cannot be recomputed later from the file.
	require.NotNil(t, att.VirusName, "a quarantined file must record what it was identified as")
	require.Equal(t, "Eicar-Test-Signature", *att.VirusName)

	// The hash is of the sample, not of our wrapper.
	//
	// This is the assertion the feature turns on. An implementation that
	// hashed after wrapping still returns a 64-character hex string, still
	// renders in the UI and still links to VirusTotal — and sends the analyst
	// to a page about a ZIP file nobody else has ever seen.
	require.NotNil(t, att.SHA256, "a quarantined file must record its hash")
	require.Equal(t, wantHash, *att.SHA256,
		"the SHA-256 must be of the raw sample, taken before it was wrapped")

	// Now the bytes on the way back out.
	down := downloadAttachment(t, h, id, att.ID)
	defer down.Body.Close()
	require.Equal(t, http.StatusOK, down.StatusCode)
	archive, err := io.ReadAll(down.Body)
	require.NoError(t, err)

	_, params, err := mime.ParseMediaType(down.Header.Get("Content-Disposition"))
	require.NoError(t, err)
	require.Equal(t, "notes.txt.zip", params["filename"],
		"what the browser writes to disk must carry the .zip too")

	// Stated the other way round as well, because it is cheap and it is the
	// mistake: the stored blob's own hash must not be what we reported.
	require.NotEqual(t, hex.EncodeToString(sha256Sum(archive)), *att.SHA256,
		"the recorded hash is the hash of the stored archive, which is useless to an analyst")

	// Wrapped, not merely renamed. If the sample sits in storage in the clear,
	// the host AV an infosec deployment certainly runs will quarantine it out
	// from under the ticket, and the attachment vanishes a day later.
	require.False(t, bytes.Contains(archive, sample),
		"the sample is stored unencrypted; host AV will delete it")

	// Wrapped on the way in, not on the way out.
	//
	// An implementation that stored the sample raw and wrapped it while
	// serving would satisfy everything above. It would also leave live malware
	// sitting in the attachment directory, and an infosec deployment very
	// likely runs host AV over its own storage — which quarantines the file
	// out from under the application, and the ticket's attachment vanishes a
	// day later.
	require.Equal(t, archive, onlyStoredFile(t, h),
		"the archive must be what is on disk, not something assembled at download time")

	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	require.NoError(t, err, "the stored file must be a ZIP an ordinary tool opens")
	require.Len(t, zr.File, 1)
	f := zr.File[0]
	require.Equal(t, "notes.txt", f.Name,
		"unwrapping must produce the uploaded file, not the wrapper's name")
	require.NotZero(t, f.Flags&0x1, "the entry must be encrypted, or the password is theatre")

	require.Equal(t, sample, recoverZipCryptoEntry(t, f, "infected"),
		"the published password must recover the sample byte for byte")
}

// An operator who has not allowed a type has not asked us to store it,
// whatever it contains. So the allowlist decides first, on the uploaded name,
// and an infected .exe on an instance that does not accept .exe is refused as
// a type — not quarantined, and not scanned.
//
// The scanner here counts what it is asked to look at. Asserting only the
// status code would let a handler that scans first and checks the type
// afterwards pass, and that ordering means every refused upload is still read,
// hashed and shipped to the scanner.
func TestUpload_ADisallowedTypeIsRefusedBeforeItIsEverScanned(t *testing.T) {
	addr, scans := countingInfectedScanner(t)
	h, cleanup := newHarnessWith(t, 0, addr)
	defer cleanup()
	ctx := context.Background()
	id := ticketForUpload(t, h)

	// Quarantine is on, so the only thing standing between this file and
	// storage is the type check.
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentInfectedHandling, "quarantine"))

	res := uploadAttachment(t, h, id, "payload.exe", []byte(eicar))
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode,
		"a type the instance does not accept is refused as a type: %s", raw)

	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Equal(t, "unsupported_type", body.Error.Code,
		"refused as a type, not as malware")

	require.Zero(t, scans(), "a file of a type we do not accept must never reach the scanner")
	assertUploadLeftNothingBehind(t, h, id)
}

// The loud treatment has to stay rare to stay meaningful, and rare means it
// fires on a stored Infected verdict and on nothing else.
//
// So with quarantine switched on and a scanner that finds nothing, an ordinary
// attachment is stored exactly as before: its own name, its own bytes, and a
// null virus_name — which is the one place in this work where NULL is a fact
// rather than an absence. It means "this file was not identified as
// malicious", and the UI reads it that way.
func TestUpload_AnUninfectedFileIsNotWrappedOrLabelled(t *testing.T) {
	h, cleanup := newHarnessWith(t, 0, fakeCleanScanner(t))
	defer cleanup()
	ctx := context.Background()
	id := ticketForUpload(t, h)

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentInfectedHandling, "quarantine"))

	content := []byte("just a log line\n")
	res := uploadAttachment(t, h, id, "server.log", content)
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", raw)

	// The key has to be present and null, not absent. An absent key decodes
	// into the same nil as a null one here, so it is checked on the raw JSON.
	var asMap map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &asMap))
	require.Contains(t, asMap, "virus_name",
		"the field must exist on every attachment, or the list cannot tell clean from unknown")

	stored := listQuarantineAttachments(t, h, id)
	require.Len(t, stored, 1)
	att := stored[0]
	require.Nil(t, att.VirusName, "nothing was found, so there is no detection name")
	require.Equal(t, "server.log", att.Filename, "a clean file keeps its own name")

	down := downloadAttachment(t, h, id, att.ID)
	defer down.Body.Close()
	got, err := io.ReadAll(down.Body)
	require.NoError(t, err)
	require.Equal(t, content, got, "a clean file is stored as itself, not wrapped")
}

// Nothing here is retroactive, in either direction.
//
// Turning quarantine on does not rescan history: a file stored last month
// stays exactly as it was stored, and the UI must not imply it was looked at
// again. Turning it back off does not unwrap what is already wrapped either —
// the archive on disk and the detection recorded against it are a record of
// what happened at upload time, and a setting change is not a new fact about
// an old file.
func TestQuarantine_IsNotRetroactiveInEitherDirection(t *testing.T) {
	// Two harnesses, one after the other rather than side by side. Each seeds
	// the same fixed user emails inside its own uncommitted transaction, so
	// two live at once block each other on the unique index and the test
	// hangs rather than fails.

	t.Run("turning it on leaves what is already stored alone", func(t *testing.T) {
		h, cleanup := newHarnessWith(t, 0, fakeCleanScanner(t))
		defer cleanup()
		ctx := context.Background()
		id := ticketForUpload(t, h)

		content := []byte("captured before anyone turned anything on\n")
		res := uploadAttachment(t, h, id, "before.log", content)
		require.Equal(t, http.StatusCreated, res.StatusCode)
		res.Body.Close()

		before := listQuarantineAttachments(t, h, id)
		require.Len(t, before, 1)

		require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentInfectedHandling, "quarantine"))

		after := listQuarantineAttachments(t, h, id)
		require.Equal(t, before, after,
			"turning quarantine on must not touch an attachment that was already stored")

		down := downloadAttachment(t, h, id, after[0].ID)
		defer down.Body.Close()
		got, err := io.ReadAll(down.Body)
		require.NoError(t, err)
		require.Equal(t, content, got, "an existing file must not be rewrapped by a settings change")
	})

	t.Run("turning it back off leaves what is already quarantined alone", func(t *testing.T) {
		h, cleanup := newHarnessWith(t, 0, fakeInfectedScanner(t))
		defer cleanup()
		ctx := context.Background()
		id := ticketForUpload(t, h)

		require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentInfectedHandling, "quarantine"))
		res := uploadAttachment(t, h, id, "sample.txt", []byte(eicar))
		raw, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", raw)

		quarantined := listQuarantineAttachments(t, h, id)
		require.Len(t, quarantined, 1)

		require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentInfectedHandling, "refuse"))

		stillThere := listQuarantineAttachments(t, h, id)
		require.Equal(t, quarantined, stillThere,
			"switching back to refuse must not alter a file that was already quarantined")

		down := downloadAttachment(t, h, id, stillThere[0].ID)
		defer down.Body.Close()
		require.Equal(t, http.StatusOK, down.StatusCode,
			"a quarantined file stays downloadable after the setting changes")
	})
}

// --- helpers -------------------------------------------------------------

// onlyStoredFile returns the bytes of the single file under the harness's
// attachment directory.
//
// Walked rather than looked up by path: nothing here should need to know the
// layout the handler happens to use, and a check that knows it is a check that
// passes when the layout changes and the assertion stops being made.
func onlyStoredFile(t *testing.T, h *harness) []byte {
	t.Helper()
	var found []string
	require.NoError(t, filepath.WalkDir(h.attachDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			found = append(found, path)
		}
		return nil
	}))
	require.Len(t, found, 1, "expected exactly one stored file under %s", h.attachDir)
	b, err := os.ReadFile(found[0])
	require.NoError(t, err)
	return b
}

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

func listQuarantineAttachments(t *testing.T, h *harness, ticketID string) []attachmentJSON {
	t.Helper()
	res := h.do(t, http.MethodGet, "/api/v1/tickets/"+ticketID+"/attachments", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var out []attachmentJSON
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return out
}

func downloadAttachment(t *testing.T, h *harness, ticketID, attachID string) *http.Response {
	t.Helper()
	return h.do(t, http.MethodGet,
		"/api/v1/tickets/"+ticketID+"/attachments/"+attachID, nil)
}

// countingInfectedScanner is fakeInfectedScanner with a tally of how many
// files it was actually handed, so a test can assert that something was never
// scanned rather than only that it was refused.
//
// PING is not counted: reachability probes are not scans, and counting them
// would make the tally depend on whether anything asked for scanner health.
func countingInfectedScanner(t *testing.T) (addr string, scans func() int) {
	t.Helper()
	var n atomic.Int64

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
				n.Add(1)
				for {
					var size [4]byte
					if _, err := io.ReadFull(r, size[:]); err != nil {
						return
					}
					sz := binary.BigEndian.Uint32(size[:])
					if sz == 0 {
						break
					}
					if _, err := io.CopyN(io.Discard, r, int64(sz)); err != nil {
						return
					}
				}
				_, _ = conn.Write([]byte("stream: Eicar-Test-Signature FOUND\x00"))
			}()
		}
	}()

	return "tcp://" + ln.Addr().String(), func() int { return int(n.Load()) }
}

// recoverZipCryptoEntry decrypts and decompresses one entry with the given
// password.
//
// Deliberately written out rather than taken from a ZIP library: the code
// under test uses one, and if this used the same one the two would agree about
// a shared bug and prove nothing. This is PKWARE traditional encryption
// ("ZipCrypto") read off the APPNOTE specification, checked against an archive
// produced by github.com/yeka/zip.
//
// There is a near-identical copy in internal/domain/attachment. Two test
// packages cannot share a helper, and duplicating forty lines is a smaller
// cost than weakening either assertion to "it looks like a ZIP".
//
// AES-encrypted ZIP is not handled, and that is itself the assertion: AES-256
// ZIP is opaque to Windows Explorer and macOS Archive Utility, which is the
// friction quarantine exists to remove.
func recoverZipCryptoEntry(t *testing.T, f *zip.File, password string) []byte {
	t.Helper()

	rc, err := f.OpenRaw()
	require.NoError(t, err, "reading the entry's stored bytes")
	raw, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Greater(t, len(raw), 12, "an encrypted entry carries a 12-byte header")

	k := newZipCryptoKeys(password)
	plain := make([]byte, len(raw))
	for i, c := range raw {
		plain[i] = k.decryptByte(c)
	}
	body := plain[12:] // the first 12 bytes are the encryption header

	switch f.Method {
	case zip.Store:
		return body
	case zip.Deflate:
		out, err := io.ReadAll(flate.NewReader(bytes.NewReader(body)))
		require.NoError(t, err,
			"the entry did not decompress with the password %q; is it AES-encrypted?", password)
		return out
	default:
		t.Fatalf("entry uses compression method %d, which no ordinary ZIP tool reads", f.Method)
		return nil
	}
}

// zipCryptoKeys is the three-word key state from APPNOTE.TXT section 6.1.
type zipCryptoKeys struct{ k0, k1, k2 uint32 }

var zipCryptoCRCTable = crc32.MakeTable(crc32.IEEE)

func newZipCryptoKeys(password string) *zipCryptoKeys {
	k := &zipCryptoKeys{0x12345678, 0x23456789, 0x34567890}
	for i := 0; i < len(password); i++ {
		k.update(password[i])
	}
	return k
}

func (k *zipCryptoKeys) update(b byte) {
	k.k0 = zipCryptoCRCTable[(k.k0^uint32(b))&0xff] ^ (k.k0 >> 8)
	k.k1 += k.k0 & 0xff
	k.k1 = k.k1*134775813 + 1
	k.k2 = zipCryptoCRCTable[(k.k2^(k.k1>>24))&0xff] ^ (k.k2 >> 8)
}

func (k *zipCryptoKeys) decryptByte(c byte) byte {
	t := uint16(k.k2) | 2
	p := c ^ byte((t*(t^1))>>8)
	k.update(p)
	return p
}
