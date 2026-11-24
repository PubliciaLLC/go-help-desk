package server

import (
	"bytes"
	"fmt"
	"github.com/publiciallc/go-help-desk/backend/internal/antivirus"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
	"golang.org/x/image/bmp"
)

const (
	attachMaxBytes = 25 << 20 // 25 MB
	attachSubdir   = "tickets"
	jpegQuality    = 85
)

// allowedExt maps lowercase extensions to the MIME type we store.
var allowedExt = map[string]string{
	".pdf":  "application/pdf",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".txt":  "text/plain",
	".log":  "text/plain",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".bmp":  "image/bmp",
}

// imageExt lists extensions that are treated as raster images and recompressed.
var imageExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".bmp": true,
}

// magicOK does a quick sanity check on the first bytes for known binary types.
// For text types (txt, log) we skip magic checks.
func magicOK(data []byte, ext string) bool {
	if len(data) < 4 {
		return false
	}
	switch ext {
	case ".pdf":
		return bytes.HasPrefix(data, []byte("%PDF"))
	case ".docx", ".xlsx":
		// Both are ZIP-based Office Open XML formats.
		return bytes.HasPrefix(data, []byte("PK\x03\x04"))
	case ".jpg", ".jpeg":
		return data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF
	case ".png":
		return bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n"))
	case ".bmp":
		return data[0] == 'B' && data[1] == 'M'
	default:
		return true // txt, log: no magic
	}
}

// compressImage decodes any supported raster image and re-encodes it as
// whichever of JPEG (quality 85) or PNG is smaller. Returns the bytes and
// the chosen extension (".jpg" or ".png").
// maxImagePixels caps how large a decoded raster may be.
//
// The byte-size limit is not a memory limit. Compressed formats expand: a
// 169 KB PNG of uniform colour decodes to 12000×12000 — 142 MB of heap — and a
// 949 KB one reaches 30000×30000 and 859 MB. Under the 25 MB upload cap a
// single request could ask for roughly 20 GB, and requests run concurrently, so
// one authenticated user could take the process down by uploading to their own
// ticket.
//
// 25 megapixels is comfortably above any real photograph or screenshot (a 24 MP
// camera, a 5K display) and far below what it takes to exhaust a server.
const maxImagePixels = 25 << 20

// decodedSizeWithin reports whether the image's dimensions are within budget,
// reading only the header. This is the whole defence: it must happen before any
// full decode, because the allocation is the attack.
func decodedSizeWithin(data []byte, limit int64) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("reading image header: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return fmt.Errorf("image reports a non-positive size (%dx%d)", cfg.Width, cfg.Height)
	}
	if px := int64(cfg.Width) * int64(cfg.Height); px > limit {
		return fmt.Errorf("image is %dx%d (%d pixels); the limit is %d", cfg.Width, cfg.Height, px, limit)
	}
	return nil
}

func compressImage(data []byte, ext string) ([]byte, string, error) {
	// Before the decode, never after.
	if err := decodedSizeWithin(data, maxImagePixels); err != nil {
		return nil, "", err
	}

	var img image.Image
	var err error

	switch ext {
	case ".bmp":
		img, err = bmp.Decode(bytes.NewReader(data))
	default:
		img, _, err = image.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return nil, "", fmt.Errorf("decoding image: %w", err)
	}

	var jpegBuf, pngBuf bytes.Buffer

	if err := jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", fmt.Errorf("encoding JPEG: %w", err)
	}
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, "", fmt.Errorf("encoding PNG: %w", err)
	}

	if jpegBuf.Len() <= pngBuf.Len() {
		return jpegBuf.Bytes(), ".jpg", nil
	}
	return pngBuf.Bytes(), ".png", nil
}

// POST /api/v1/tickets/{id}/attachments
// Accepts multipart/form-data with field name "file" (one file per request).
// Only authenticated users (not guests) can upload attachments.
func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	if a == nil {
		Error(w, http.StatusUnauthorized, "unauthorized", "login required to upload attachments")
		return
	}

	ticketID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid ticket id")
		return
	}

	// Verify the ticket exists and the user is allowed to see it.
	t, err := s.tickets.GetByID(r.Context(), ticketID)
	if err != nil {
		handleError(w, err)
		return
	}
	if a.Role == "user" && (t.ReporterUserID == nil || *t.ReporterUserID != a.UserID) {
		Error(w, http.StatusForbidden, "forbidden", "not your ticket")
		return
	}

	// Parse the multipart body. Limit memory; spill to temp files.
	if err := r.ParseMultipartForm(attachMaxBytes); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "could not parse upload")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "field 'file' is required")
		return
	}
	defer file.Close()

	if header.Size > attachMaxBytes {
		Error(w, http.StatusRequestEntityTooLarge, "too_large", "file exceeds 25 MB limit")
		return
	}

	origName := header.Filename

	// The filename has to be something the database can hold, before anything
	// else happens to it.
	//
	// It is stored in a TEXT column, and Postgres is stricter about that than
	// Go is. It rejects bytes that are not valid UTF-8, and it also rejects a
	// NUL — which *is* valid UTF-8, so utf8.ValidString alone was not enough;
	// the first version of this check let "a\x00b.txt" through and it 500'd at
	// the insert exactly as before.
	//
	// Both arrive the same way. A multipart filename is raw bytes off the wire
	// with no encoding declared, and the RFC 5987 form (filename*=UTF-8'') is
	// percent-decoded by the MIME parser, which is how a NUL gets past a
	// parser that would otherwise refuse a raw control character.
	//
	// Without this, the bad name travelled the whole handler — extension
	// checked, content scanned, file written to disk — and failed at the
	// insert, returning 500 db_error. That blames the server for a malformed
	// request. See #169.
	//
	// This is not the only place a string can reach Postgres in a state it
	// refuses: a NUL inside a JSON string survives Go's decoder and 500s the
	// same way on ticket creation. That is a wider problem than attachments
	// and is not fixed here.
	if !utf8.ValidString(origName) || strings.ContainsRune(origName, 0) {
		Error(w, http.StatusBadRequest, "invalid_filename",
			"filename must be valid UTF-8 and contain no NUL")
		return
	}

	ext := strings.ToLower(filepath.Ext(origName))
	mime, ok := allowedExt[ext]
	if !ok {
		Error(w, http.StatusUnsupportedMediaType, "unsupported_type",
			"allowed types: PDF, DOCX, XLSX, TXT, LOG, JPEG, PNG, BMP")
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, attachMaxBytes+1))
	if err != nil {
		Error(w, http.StatusInternalServerError, "read_error", "failed to read upload")
		return
	}
	if int64(len(data)) > attachMaxBytes {
		Error(w, http.StatusRequestEntityTooLarge, "too_large", "file exceeds 25 MB limit")
		return
	}

	// Magic-byte validation.
	if !magicOK(data, ext) {
		Error(w, http.StatusUnsupportedMediaType, "invalid_file",
			"file content does not match the expected type")
		return
	}

	if !s.scanUpload(w, r, data, origName, ticketID) {
		return
	}

	// Image recompression: pick whichever of JPEG/PNG is smaller.
	storedExt := ext
	if imageExt[ext] {
		compressed, newExt, err := compressImage(data, ext)
		if err != nil {
			Error(w, http.StatusUnprocessableEntity, "invalid_image",
				"could not decode image: "+err.Error())
			return
		}
		data = compressed
		storedExt = newExt
		if storedExt == ".jpg" {
			mime = "image/jpeg"
		} else {
			mime = "image/png"
		}
	}

	// Write to disk with obfuscated filename: <uuid><ext>
	storageID := uuid.New()
	subdir := filepath.Join(s.cfg.AttachmentDir, attachSubdir, ticketID.String())
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		Error(w, http.StatusInternalServerError, "storage_error", "could not create storage directory")
		return
	}
	diskPath := filepath.Join(subdir, storageID.String()+storedExt)
	if err := os.WriteFile(diskPath, data, 0o644); err != nil {
		Error(w, http.StatusInternalServerError, "storage_error", "could not write file")
		return
	}

	att := ticket.Attachment{
		ID:          storageID,
		TicketID:    ticketID,
		Filename:    origName, // original name preserved for display
		MimeType:    mime,
		SizeBytes:   int64(len(data)),
		StoragePath: diskPath,
		CreatedAt:   time.Now(),
	}
	if err := s.tickets.CreateAttachment(r.Context(), att); err != nil {
		_ = os.Remove(diskPath)
		Error(w, http.StatusInternalServerError, "db_error", "could not record attachment")
		return
	}

	JSON(w, http.StatusCreated, att)
}

// GET /api/v1/tickets/{id}/attachments
func (s *Server) handleListAttachments(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	ticketID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid ticket id")
		return
	}

	t, err := s.tickets.GetByID(r.Context(), ticketID)
	if err != nil {
		handleError(w, err)
		return
	}
	if a != nil && a.Role == "user" && (t.ReporterUserID == nil || *t.ReporterUserID != a.UserID) {
		Error(w, http.StatusForbidden, "forbidden", "not your ticket")
		return
	}

	atts, err := s.tickets.ListAttachments(r.Context(), ticketID)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, atts)
}

// GET /api/v1/tickets/{id}/attachments/{attachId}
// Streams the file with the original filename in Content-Disposition.
func (s *Server) handleDownloadAttachment(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	ticketID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid ticket id")
		return
	}
	attID, err := uuid.Parse(chi.URLParam(r, "attachId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid attachment id")
		return
	}

	// Verify ticket ownership for regular users.
	t, err := s.tickets.GetByID(r.Context(), ticketID)
	if err != nil {
		handleError(w, err)
		return
	}
	if a != nil && a.Role == "user" && (t.ReporterUserID == nil || *t.ReporterUserID != a.UserID) {
		Error(w, http.StatusForbidden, "forbidden", "not your ticket")
		return
	}

	att, err := s.tickets.GetAttachment(r.Context(), attID)
	if err != nil {
		handleError(w, err)
		return
	}
	if att.TicketID != ticketID {
		Error(w, http.StatusNotFound, "not_found", "attachment not found on this ticket")
		return
	}

	// Refuse to answer a request that wants to render this.
	//
	// The headers below tell a browser to save the file, and a browser obeys
	// them for a navigation. It does not obey them for a subresource: put an
	// uploaded image behind <img src>, or behind a CSS url(), and it renders
	// on this origin no matter what Content-Type and Content-Disposition say.
	// Nothing here or in the frontend was stopping that; what stood in its
	// place was a test that reads our own source and hopes nobody writes an
	// <img>. Two rounds of adversarial review walked past that test five
	// different ways — a CSS background, an aliased import, createElement, an
	// innerHTML string, a tag name split over two lines — and each one was a
	// pattern a regular expression missed rather than a hole in the argument.
	// A check that has to keep up with how code can be written is a check
	// that loses.
	//
	// So ask the browser instead. Sec-Fetch-Dest says what the response is
	// going to be used for, the browser fills it in and page script cannot
	// forge it — a fetch() that sets the header itself has it dropped.
	//
	// Two values have to be allowed. "empty" is a script fetch and, less
	// obviously, an <a download> click: the HTML spec gives a hyperlink being
	// downloaded an empty destination, so the app's own download link arrives
	// as "empty" with mode "navigate". "document" is a plain <a href> with no
	// download attribute, or the URL typed into the address bar. Every
	// rendering context is something else.
	//
	// Allow list, not a block list: a destination nobody has invented yet
	// should be refused rather than served.
	//
	// Absent is allowed. Every command-line client sends nothing, and so do
	// browsers older than Chrome 80, Firefox 90 and Safari 16.4 — and for
	// those browsers this check simply does not apply. That is a gap, not a
	// reason the gap is harmless: Safari 16.3 renders an <img> like anything
	// else. Refusing an absent header would break curl and every older client
	// instead, which is worse, so the check protects what it can reach.
	//
	// And it only stops a request the browser makes for a rendering context.
	// Page script can fetch the bytes itself — Sec-Fetch-Dest: empty, which
	// has to be allowed — and render them without asking again. Nothing on
	// the server can tell that apart from a download. What stops that is not
	// writing it, which is what the frontend test is for.
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "document" && dest != "empty" {
		Error(w, http.StatusForbidden, "not_downloadable",
			"attachments can only be downloaded, not rendered in the page")
		return
	}

	f, err := os.Open(att.StoragePath)
	if err != nil {
		Error(w, http.StatusNotFound, "not_found", "file not found on disk")
		return
	}
	defer f.Close()

	// Every attachment is served as an unknown blob, whatever it claims to be.
	//
	// This used to send att.MimeType — the type derived from the uploaded
	// filename — so a PDF went out as application/pdf. Browsers open that in
	// their built-in viewer, and those viewers run JavaScript, so a malicious
	// PDF only stayed harmless because Content-Disposition told the browser to
	// save it instead. One header was holding the whole thing up.
	//
	// application/octet-stream is not a type any browser renders, so now
	// nothing is asking it to. Not a list of dangerous types either: such a
	// list has to stay correct as formats change, and it will not. The type is
	// no use on this response anyway — the operating system picks what opens a
	// downloaded file from its extension, not from a header it already obeyed
	// by saving the file.
	//
	// The stored mime_type stays what it was. It is a record of what the file
	// claims to be, so the attachment list can show "PDF"; it is no longer a
	// decision about how a browser should treat it.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", contentDisposition(att.Filename))
	// Vary, or the check above is decoration.
	//
	// That check runs per request; this response is cacheable for an hour and
	// a browser cache is keyed by URL. So once the URL has been fetched in a
	// way the check allows — the user's own download click, or any fetch() —
	// an <img> pointing at the same URL is answered from the cache and the
	// server never sees it. Measured in Chrome: without this header the image
	// loads and no second request arrives; with it, the image request reaches
	// the server and is refused.
	//
	// Naming the header here makes the cache key include it, so a request
	// with a different Sec-Fetch-Dest is a different entry and has to ask.
	w.Header().Set("Vary", "Sec-Fetch-Dest")
	w.Header().Set("Cache-Control", "private, max-age=3600")

	// ServeContent, not io.Copy: it handles range requests and conditional
	// gets. It would otherwise set Content-Type by sniffing the body, which is
	// exactly what this function has just decided against — so the header is
	// set above and ServeContent leaves an existing one alone.
	//
	// With one exception, checked rather than assumed: a request for several
	// ranges at once gets Content-Type: multipart/byteranges, because that is
	// what the body then is. Each part inside it still carries the blob type,
	// and no browser renders a byteranges response, so this is not a way back
	// to rendering — but the header on the response is not the one set above.
	http.ServeContent(w, r, att.Filename, att.CreatedAt, f)
}

// contentDisposition builds the header that names the downloaded file.
//
// Two forms, per RFC 6266. The plain filename= is ASCII only and is what old
// clients read; filename*= carries the real name as percent-encoded UTF-8 and
// is what everything current reads. Sending both means "café.pdf" arrives
// named "café.pdf" rather than "caf.pdf" or worse.
//
// This previously used fmt.Sprintf("%q"), which is Go quoting rather than HTTP
// quoting. It escaped quotes and non-printable characters, so it was not a way
// in, and a name like "café.pdf" did come out with the accent intact — %q
// leaves printable non-ASCII alone. What it did not do is follow the spec: a
// bare filename= is defined over a character set that has no room for UTF-8,
// so what a client makes of raw bytes there is up to the client. filename*=
// says which encoding is in use instead of hoping.
func contentDisposition(filename string) string {
	// Strip anything that cannot appear in a header value, and any path
	// separator: the name comes from an upload, and it decides what a browser
	// writes to disk.
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, filename)
	if clean == "" {
		clean = "attachment"
	}

	// A ceiling on the name. Nothing limits the length of an uploaded
	// filename — the column is TEXT and the multipart reader allows a 10 MB
	// part header — and the name goes out in a response header twice, once
	// percent-encoded. A 300 KB filename produced a 600 KB header, which is
	// past what browsers and proxies accept, so the download would simply
	// fail for everyone. Truncated on a rune boundary so the encoded form
	// stays valid UTF-8; the extension is kept, since that is what decides
	// what opens the file.
	const maxName = 200
	if len(clean) > maxName {
		ext := filepath.Ext(clean)
		if len(ext) > 32 {
			ext = ""
		}
		head := clean[:maxName-len(ext)]
		for len(head) > 0 && !utf8.ValidString(head) {
			head = head[:len(head)-1]
		}
		clean = head + ext
	}

	// The ASCII fallback. Anything outside ASCII becomes an underscore so the
	// plain form stays a legal quoted-string, while filename*= below carries
	// the real thing.
	ascii := strings.Map(func(r rune) rune {
		if r > 0x7e {
			return '_'
		}
		return r
	}, clean)
	// Escape what a quoted-string cannot hold bare. The backslash rule cannot
	// fire today because the map above removes backslashes, but it is here so
	// that stays a choice about path separators rather than the only thing
	// keeping this header well-formed.
	ascii = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(ascii)

	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		ascii, rfc5987(clean))
}

// rfc5987 percent-encodes a filename for the filename*= form.
//
// Not url.PathEscape, which was the first attempt. It lets exactly three
// characters through that RFC 5987 forbids — ":", "=" and "@" — and Go's own
// mime.ParseMediaType then refuses the header. A saved email named
// "user@example.com.eml" is enough to trigger it, which is not an exotic thing
// for a help desk to be handed.
//
// Encoded byte by byte rather than rune by rune, so a multi-byte character
// comes out as the percent-encoded UTF-8 the header claims to carry.
func rfc5987(s string) string {
	// RFC 5987 attr-char: letters, digits and these. Note "%", "*" and "\'"
	// are excluded on purpose — they are the encoding's own syntax.
	const attrChar = "!#$&+-.^_`|~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			strings.IndexByte(attrChar, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// scanUpload scans the file and applies the configured policy, reporting
// whether the upload may proceed. It has written the response when it returns
// false.
//
// The distinction this exists for: "scanned and clean" and "could not scan"
// used to be the same value. scanClamAV returned (infected bool, …) and every
// failure — no address, unreachable daemon, a write error mid-stream —
// returned false, which the caller read as clean. So an instance whose ClamAV
// had been dead for a month accepted everything, logged a warning nobody
// reads, and showed the administrator nothing.
//
// Now an unscannable file is refused by default. 503 rather than 4xx because
// nothing is wrong with the request: the server cannot currently do its job,
// and the caller should try again. Retry-After says so, which also covers the
// first few minutes after a fresh `docker compose up`, where clamav is still
// downloading its signature database and the application is already serving.
func (s *Server) scanUpload(w http.ResponseWriter, r *http.Request, data []byte, filename string, ticketID uuid.UUID) bool {
	ctx := r.Context()
	sc := s.scanner(ctx)
	policy := s.adminSvc.AttachmentScanPolicy(ctx, sc.Configured())
	if policy == antivirus.PolicyOff {
		return true
	}

	res := sc.Scan(ctx, data)
	switch res.Verdict {
	case antivirus.Infected:
		slog.WarnContext(ctx, "infected upload refused",
			"virus", res.Virus, "filename", filename, "ticket", ticketID)
		Error(w, http.StatusUnprocessableEntity, "infected",
			fmt.Sprintf("file rejected: %s", res.Virus))
		return false

	case antivirus.Unavailable:
		if policy == antivirus.PolicyPermissive {
			// The old behaviour, now reachable only by choosing it.
			slog.WarnContext(ctx, "accepting an unscanned upload: scan policy is permissive",
				"filename", filename, "ticket", ticketID, "reason", res.Reason)
			return true
		}
		slog.ErrorContext(ctx, "refusing an upload that could not be scanned",
			"filename", filename, "ticket", ticketID, "reason", res.Reason)
		// Deliberately says nothing about why the scanner is unreachable: the
		// reason names internal infrastructure, and the caller can do nothing
		// with it either way.
		w.Header().Set("Retry-After", "60")
		Error(w, http.StatusServiceUnavailable, "scanner_unavailable",
			"attachments cannot be accepted right now because the virus scanner is unavailable; please try again shortly")
		return false
	}
	return true
}
