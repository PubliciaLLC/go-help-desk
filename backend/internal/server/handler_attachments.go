package server

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net"
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
	attachMaxBytes    = 25 << 20 // 25 MB
	attachSubdir      = "tickets"
	jpegQuality       = 85
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
func compressImage(data []byte, ext string) ([]byte, string, error) {
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

// scanClamAV sends data to a running clamd daemon and returns true if the
// file is infected. If the daemon is unreachable, it logs a warning and
// returns false (non-fatal) so uploads are not blocked by an unavailable scanner.
func scanClamAV(addr string, data []byte) (infected bool, virusName string) {
	if addr == "" {
		return false, ""
	}

	var conn net.Conn
	var err error

	if strings.HasPrefix(addr, "unix://") {
		conn, err = net.DialTimeout("unix", strings.TrimPrefix(addr, "unix://"), 5*time.Second)
	} else {
		host := strings.TrimPrefix(addr, "tcp://")
		conn, err = net.DialTimeout("tcp", host, 5*time.Second)
	}
	if err != nil {
		slog.Warn("ClamAV unreachable — skipping virus scan", "addr", addr, "error", err)
		return false, ""
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	// INSTREAM protocol: zINSTREAM\0, then chunks of [4-byte len][data], terminated by [0 0 0 0].
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		slog.Warn("ClamAV write error", "error", err)
		return false, ""
	}

	chunkSize := 4096
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[i:end]
		sz := make([]byte, 4)
		binary.BigEndian.PutUint32(sz, uint32(len(chunk)))
		if _, err := conn.Write(sz); err != nil {
			slog.Warn("ClamAV chunk write error", "error", err)
			return false, ""
		}
		if _, err := conn.Write(chunk); err != nil {
			slog.Warn("ClamAV chunk write error", "error", err)
			return false, ""
		}
	}
	// Terminator.
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		slog.Warn("ClamAV terminator write error", "error", err)
		return false, ""
	}

	resp, err := io.ReadAll(conn)
	if err != nil {
		slog.Warn("ClamAV response read error", "error", err)
		return false, ""
	}

	result := strings.TrimRight(string(resp), "\x00\n")
	slog.Debug("ClamAV result", "result", result)

	if strings.Contains(result, "FOUND") {
		// Format: "stream: <VirusName> FOUND"
		parts := strings.Fields(result)
		name := ""
		if len(parts) >= 2 {
			name = parts[len(parts)-2]
		}
		return true, name
	}
	return false, ""
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

	// Virus scan (skipped when ClamAV is not configured).
	if infected, virusName := scanClamAV(s.cfg.ClamAVAddr, data); infected {
		slog.Warn("infected file blocked", "virus", virusName, "filename", origName, "ticket", ticketID)
		Error(w, http.StatusUnprocessableEntity, "infected",
			fmt.Sprintf("file rejected: virus detected (%s)", virusName))
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
