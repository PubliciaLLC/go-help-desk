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

	f, err := os.Open(att.StoragePath)
	if err != nil {
		Error(w, http.StatusNotFound, "not_found", "file not found on disk")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", att.MimeType)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename=%q`, att.Filename))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, att.Filename, att.CreatedAt, f)
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
	policy := s.adminSvc.AttachmentScanPolicy(ctx, s.scanner.Configured())
	if policy == antivirus.PolicyOff {
		return true
	}

	res := s.scanner.Scan(ctx, data)
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
