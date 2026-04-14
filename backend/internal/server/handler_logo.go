package server

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	logoMaxBytes  = 2 << 20 // 2 MB
	logoMaxWidth  = 320
	logoMaxHeight = 64
	logoSubdir    = "site"
	logoBasename  = "logo"
)

// svgForbidden rejects SVG content that could execute code when rendered.
var svgForbidden = []*regexp.Regexp{
	regexp.MustCompile(`(?i)<\s*script[\s>/]`),
	regexp.MustCompile(`(?i)\bon\w+\s*=`),
	regexp.MustCompile(`(?i)javascript\s*:`),
	regexp.MustCompile(`(?i)<\s*foreignObject[\s>/]`),
	regexp.MustCompile(`(?i)expression\s*\(`),
}

// detectLogoType inspects magic bytes and returns the canonical kind and
// file extension. Returns an error if the format is not allowed.
func detectLogoType(data []byte) (kind, ext string, _ error) {
	if len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "png", "png", nil
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "jpeg", "jpg", nil
	}
	if len(data) >= 6 && (bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))) {
		return "gif", "gif", nil
	}
	// SVG: look for <svg within the first 512 bytes (handles BOM and XML declarations).
	sniff := data
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	if strings.Contains(strings.ToLower(string(sniff)), "<svg") {
		return "svg", "svg", nil
	}
	return "", "", fmt.Errorf("unsupported file type: must be PNG, JPG, GIF, or SVG")
}

// sanitizeSVG rejects SVGs that are not well-formed XML or contain patterns
// that could execute code (scripts, event handlers, javascript: URIs, etc.).
func sanitizeSVG(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("SVG is not well-formed XML: %w", err)
		}
	}
	for _, re := range svgForbidden {
		if re.Match(data) {
			return fmt.Errorf("SVG contains disallowed content (scripts or event handlers are not permitted)")
		}
	}
	return nil
}

// fitWithin returns the largest dimensions that fit inside maxW×maxH while
// preserving the aspect ratio of srcW×srcH. If the source already fits,
// the original dimensions are returned unchanged.
func fitWithin(srcW, srcH, maxW, maxH int) (int, int) {
	if srcW <= maxW && srcH <= maxH {
		return srcW, srcH
	}
	scaleW := float64(maxW) / float64(srcW)
	scaleH := float64(maxH) / float64(srcH)
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	w := int(float64(srcW) * scale)
	h := int(float64(srcH) * scale)
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return w, h
}

// resizeRasterLogo decodes a PNG, JPEG, or GIF image, scales it to fit within
// logoMaxWidth × logoMaxHeight (nearest-neighbor, aspect-ratio preserved), and
// re-encodes the result as PNG. The returned bytes are always a valid PNG.
func resizeRasterLogo(data []byte, kind string) ([]byte, error) {
	var src image.Image
	var err error
	switch kind {
	case "png":
		src, err = png.Decode(bytes.NewReader(data))
	case "jpeg":
		src, err = jpeg.Decode(bytes.NewReader(data))
	case "gif":
		// gif.Decode returns the first frame — correct for logos.
		src, err = gif.Decode(bytes.NewReader(data))
	default:
		return nil, fmt.Errorf("unexpected raster kind: %s", kind)
	}
	if err != nil {
		return nil, fmt.Errorf("decoding image: %w", err)
	}

	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	dstW, dstH := fitWithin(srcW, srcH, logoMaxWidth, logoMaxHeight)

	// Build the output image using nearest-neighbor sampling. We always
	// re-encode as PNG to normalise the format and strip embedded metadata.
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for y := range dstH {
		for x := range dstW {
			srcX := bounds.Min.X + x*srcW/dstW
			srcY := bounds.Min.Y + y*srcH/dstH
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("encoding resized image: %w", err)
	}
	return buf.Bytes(), nil
}

// deleteLogoFile removes any existing logo files (png and svg) from logoDir.
func deleteLogoFile(logoDir string) {
	for _, ext := range []string{"png", "svg"} {
		path := filepath.Join(logoDir, logoBasename+"."+ext)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			slog.Warn("failed to remove old logo file", "path", path, "error", err)
		}
	}
}

// handleUploadLogo accepts a multipart/form-data POST with a "logo" file field,
// validates the type, resizes raster images to fit within 320×64, sanitizes
// SVGs, stores the result, and records the URL in settings.
func (s *Server) handleUploadLogo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, logoMaxBytes+4096)
	if err := r.ParseMultipartForm(logoMaxBytes); err != nil {
		Error(w, http.StatusBadRequest, "invalid_form", "file too large or invalid multipart form")
		return
	}

	f, _, err := r.FormFile("logo")
	if err != nil {
		Error(w, http.StatusBadRequest, "missing_field", "missing file field 'logo'")
		return
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, logoMaxBytes+1))
	if err != nil {
		Error(w, http.StatusInternalServerError, "read_error", "reading uploaded file")
		return
	}
	if int64(len(data)) > logoMaxBytes {
		Error(w, http.StatusBadRequest, "file_too_large", "file exceeds the 2 MB limit")
		return
	}

	kind, ext, err := detectLogoType(data)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_type", err.Error())
		return
	}

	var finalData []byte
	var finalExt string

	switch kind {
	case "svg":
		if err := sanitizeSVG(data); err != nil {
			Error(w, http.StatusBadRequest, "invalid_svg", err.Error())
			return
		}
		finalData = data
		finalExt = "svg"
	default:
		resized, err := resizeRasterLogo(data, kind)
		if err != nil {
			Error(w, http.StatusBadRequest, "invalid_image", fmt.Sprintf("processing %s: %s", ext, err))
			return
		}
		finalData = resized
		finalExt = "png"
	}

	logoDir := filepath.Join(s.cfg.AttachmentDir, logoSubdir)
	if err := os.MkdirAll(logoDir, 0o755); err != nil {
		Error(w, http.StatusInternalServerError, "storage_error", "creating storage directory")
		return
	}

	// Remove any previously stored logo before writing the new one.
	deleteLogoFile(logoDir)

	logoPath := filepath.Join(logoDir, logoBasename+"."+finalExt)
	if err := os.WriteFile(logoPath, finalData, 0o644); err != nil {
		Error(w, http.StatusInternalServerError, "storage_error", "writing logo file")
		return
	}

	logoURL := "/api/v1/logo"
	if err := s.adminSvc.SetString(r.Context(), "site_logo_url", logoURL); err != nil {
		Error(w, http.StatusInternalServerError, "settings_error", "saving logo URL to settings")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"url": logoURL})
}

// handleDeleteLogo removes the stored logo file and clears the setting.
func (s *Server) handleDeleteLogo(w http.ResponseWriter, r *http.Request) {
	logoDir := filepath.Join(s.cfg.AttachmentDir, logoSubdir)
	deleteLogoFile(logoDir)

	if err := s.adminSvc.SetString(r.Context(), "site_logo_url", ""); err != nil {
		Error(w, http.StatusInternalServerError, "settings_error", "clearing logo URL from settings")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleServeLogo serves the stored logo file. It is a public endpoint (no
// auth required) so the sidebar can display the logo before login.
func (s *Server) handleServeLogo(w http.ResponseWriter, r *http.Request) {
	logoDir := filepath.Join(s.cfg.AttachmentDir, logoSubdir)

	for _, entry := range []struct{ ext, mime string }{
		{"png", "image/png"},
		{"svg", "image/svg+xml"},
	} {
		path := filepath.Join(logoDir, logoBasename+"."+entry.ext)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		w.Header().Set("Content-Type", entry.mime)
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(data)
		return
	}

	http.NotFound(w, r)
}
