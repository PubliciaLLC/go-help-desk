package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/publiciallc/go-help-desk/backend/internal/antivirus"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/attachment"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
	"golang.org/x/image/bmp"
)

const (
	attachMaxBytes = 25 << 20 // 25 MB

	// bodyTransferTimeout is how long one attachment body may take to arrive
	// or to leave, replacing the server-wide 15s read and 30s write deadlines
	// for these two routes alone.
	//
	// Those deadlines cover a whole request, body included, and the app is
	// exposed directly in docker-compose with no proxy in front. At 15
	// seconds, a 25 MB upload needs a sustained 13 Mbit/s uplink and a 10 MB
	// one needs 5.3 — and a user below that got "could not parse upload",
	// which blames their request for their connection. Downloads were cut off
	// mid-stream below about 7 Mbit/s.
	//
	// Five minutes is 25 MB at roughly 700 kbit/s: a bad hotel connection
	// still finishes, and a connection that has genuinely stopped is still
	// closed. The short deadlines stay everywhere else, where a body is a
	// JSON document and slow means something is wrong.
	bodyTransferTimeout = 5 * time.Minute

	// maxFilenameBytes is the longest uploaded name that is stored. See the
	// check in handleUploadAttachment for why the ceiling exists at all.
	maxFilenameBytes = 255
	attachSubdir     = "tickets"
	jpegQuality      = 85
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

// maxDecodedBytes is the real budget, and the pixel cap above is the second
// half of it.
//
// Counting pixels alone was wrong, and wrong in the direction that matters: it
// assumed four bytes each. A 16-bit RGBA PNG decodes to EIGHT, so a 5120x5120
// image — 26.2 megapixels, which passes the pixel cap — is 210 MB decoded, and
// both encoders then run on it. Measured: one such file is 214 KB on the wire
// and left 403 MB live on the heap, and five concurrent uploads of it reached
// 1,990 MB. A reporting-role user uploading to their own ticket could do it,
// which makes a 214 KB request a way to have the help desk killed for running
// out of memory.
//
// 100 MB is what 25 megapixels was always meant to cost. Both caps are kept
// because they bound different things: this one bounds the pixel buffer, and
// the pixel count still bounds the per-pixel work the encoders do on a cheap
// format — a 100-megapixel greyscale image is only 100 MB but is not something
// a help desk receives.
const maxDecodedBytes = 100 << 20

// bytesPerPixel is how much heap one pixel of this colour model costs once
// decoded.
//
// Unknown models get the worst case rather than a guess. The point of this
// function is to be pessimistic where it is unsure: an underestimate is the
// bug it exists to fix.
func bytesPerPixel(m color.Model) int64 {
	switch m {
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.YCbCrModel:
		// Three planes. 4:4:4 is the worst case and the only one worth
		// budgeting for; a subsampled JPEG costs less.
		return 3
	case color.RGBAModel, color.NRGBAModel, color.CMYKModel:
		return 4
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	}
	if _, ok := m.(color.Palette); ok {
		return 1
	}
	return 8
}

// decodedSizeWithin reports whether the image fits in budget, reading only the
// header. This is the whole defence: it must happen before any full decode,
// because the allocation is the attack.
func decodedSizeWithin(data []byte, maxPixels, maxBytes int64) error {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("reading image header: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return fmt.Errorf("image reports a non-positive size (%dx%d)", cfg.Width, cfg.Height)
	}
	px := int64(cfg.Width) * int64(cfg.Height)
	if px > maxPixels {
		return fmt.Errorf("image is %dx%d (%d pixels); the limit is %d", cfg.Width, cfg.Height, px, maxPixels)
	}

	// JPEG is asked directly, because its decoder allocates far more than the
	// picture it produces and the colour model says nothing about it — and a
	// JPEG this cannot read is REFUSED rather than fallen back on.
	//
	// The fallback was the hole. It took the colour model's number whenever
	// the header could not be parsed, which is the number that does not know
	// about progressive coefficients or RGB conversion — so anything that
	// made the parser give up got the budget the parser exists to replace.
	// Eight bytes of malformed tail did it: a real 5120x5120 progressive
	// image estimated at 75 MB, accepted, and decoded to 375 MB.
	//
	// Every real JPEG parses. A JPEG this cannot read is one somebody built
	// to be unreadable, and "I cannot tell how much memory this needs" is not
	// a reason to allow it — it is the reason to refuse it. That is the
	// difference between failing open and failing closed, and it is worth
	// more than any single thing the parser knows: a future gap in it becomes
	// a refusal rather than a way through.
	need := px * bytesPerPixel(cfg.ColorModel)
	switch format {
	case "jpeg":
		b, ok := jpegDecodeBytes(data)
		if !ok {
			return errors.New("this JPEG's header could not be read, so there is no way to tell " +
				"how much memory decoding it would take")
		}
		need = b
	case "png":
		// A grayscale PNG carrying a tRNS chunk decodes four times wider than
		// its colour model says, and DecodeConfig cannot tell you so.
		//
		// The reason is an ordering detail in image/png: DecodeConfig returns
		// as soon as it reaches IDAT, and for a non-paletted image the colour
		// model it reports comes from IHDR's colour type alone. There is no
		// branch in it for transparency. But parsetRNS sets useTransparent,
		// and readImagePass reads that flag and builds an NRGBA instead of a
		// Gray, or an NRGBA64 instead of a Gray16.
		//
		// Measured, 5120x5120: 8-bit grayscale estimated at 25 MB and cost
		// 100 MB; 16-bit estimated at 50 MB and cost 200 MB, which is twice
		// the whole per-request budget on one upload. Both compress to almost
		// nothing, because the pixels can all be the same value.
		//
		// Only grayscale moves. Truecolor allocates 4 or 8 bytes either way —
		// RGBA without the chunk, NRGBA with it — and paletted stays one byte
		// per pixel whatever its palette holds.
		//
		// Walking only as far as IDAT is safe here, unlike the equivalent
		// question in JPEG, where a marker after the scan still counted. Go's
		// decoder enforces chunk order: for a grayscale image tRNS is only
		// accepted at dsSeenIHDR, so one placed after the pixel data is a
		// chunkOrderError and the file does not decode at all.
		if pngUsesTransparency(data) {
			switch cfg.ColorModel {
			case color.GrayModel:
				need = px * 4 // image.NRGBA
			case color.Gray16Model:
				need = px * 8 // image.NRGBA64
			}
		}
	}
	if need > maxBytes {
		return fmt.Errorf("image is %dx%d and would need %d MB to decode; the limit is %d MB",
			cfg.Width, cfg.Height, need>>20, maxBytes>>20)
	}
	return nil
}

// pngUsesTransparency reports whether the decoder will treat this PNG as
// carrying transparency — a tRNS chunk before the pixel data.
//
// It answers TRUE when the chunk stream cannot be walked, which is the
// fail-closed direction: an unreadable stream is budgeted as though the
// expensive thing were there rather than as though it were not. That is the
// same rule the JPEG header follows, arrived at differently — a JPEG that
// cannot be read is refused outright, but here refusing would throw out
// files that decode perfectly well, so the conservative estimate does the
// job instead.
//
// Walking only as far as IDAT is safe, unlike the equivalent question in
// JPEG where a marker after the scan still counted. image/png enforces chunk
// order: for a grayscale image tRNS is accepted only at dsSeenIHDR, so one
// placed after the pixel data is a chunkOrderError and the file does not
// decode at all.
func pngUsesTransparency(data []byte) bool {
	const sig = 8 // \x89PNG\r\n\x1a\n
	if len(data) < sig {
		return true
	}
	for off := sig; ; {
		// length (4) + type (4) + data + crc (4)
		if off+8 > len(data) {
			return true
		}
		length := int64(binary.BigEndian.Uint32(data[off : off+4]))
		switch string(data[off+4 : off+8]) {
		case "tRNS":
			return true
		case "IDAT", "IEND":
			return false
		}
		// int64 throughout: a length near 2^32 overflows an int on a 32-bit
		// build, and the offset then walks backwards into a loop with no end.
		next := int64(off) + 8 + length + 4
		if length < 0 || next > int64(len(data)) {
			return true
		}
		off = int(next)
	}
}

// jpegDecodeBytes is what image/jpeg will allocate for this file, read out of
// its own header. The second return is false for anything that is not a JPEG
// with a frame header this understands.
//
// A budget built from the colour model alone was wrong for JPEG, and wrong in
// the direction that matters. Two shapes get nowhere near their estimate:
//
//   - A progressive JPEG holds every DCT coefficient in memory until the
//     image is reconstructed — 256 bytes per 8x8 block per component, which
//     for 4:4:4 is twelve bytes a pixel on top of the three the picture
//     needs.
//   - A CMYK JPEG decodes into a YCbCr image, a separate black plane, and
//     then a third CMYK image, so it costs roughly eight bytes a pixel where
//     the colour model says four.
//
// Measured, both pass the pixel cap exactly at 5120x5120 and then allocate
// four to six times the ceiling: a 308 KB progressive file leaves 375 MB
// live, and a 615 KB progressive CMYK one leaves 600 MB. That is the same
// denial of service the byte budget was written to close, arriving through a
// file type this application accepts by default and any signed-in reporter
// can upload.
//
// The arithmetic is the decoder's own, which is why it is worth reading the
// header rather than applying a blanket surcharge: a blanket one large enough
// to be safe for 4:4:4 would refuse ordinary 4:2:0 photographs, and this
// matches every fixture measured to the megabyte.
func jpegDecodeBytes(data []byte) (int64, bool) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 0, false
	}

	// Collected in one pass, because the decoder reads them all before it
	// allocates anything and the order they arrive in is the file's choice.
	var (
		haveFrame   bool
		progressive bool
		width       int64
		height      int64
		comps       []sampling
		ids         []byte

		jfif                bool
		adobeTransform      byte
		adobeTransformValid bool
	)

	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			i++
			continue
		}
		marker := data[i+1]
		// Markers that carry no length: padding, the standalone ones, and a
		// stuffed zero.
		//
		// The zero matters. Go's decoder treats FF 00 outside a scan as
		// extraneous and skips it; this used to read the next two bytes as a
		// segment length, overrun the buffer, and give up — throwing away a
		// frame header it had already parsed correctly. Eight bytes of tail
		// were enough to do it. That is fixed at the other end too, by
		// refusing a JPEG this cannot read rather than falling back, but a
		// parser that agrees with the decoder is better than one that only
		// fails safely when it disagrees.
		if marker == 0x00 || marker == 0xFF || marker == 0x01 || marker == 0xD8 ||
			(marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		// End of image. The decoder stops here and so does this.
		if marker == 0xD9 {
			break
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		if segLen < 2 || i+2+segLen > len(data) {
			return 0, false
		}

		// Start of scan. Skip its header and then the compressed data after
		// it, and keep going — do NOT stop here.
		//
		// Stopping at the scan was the obvious reading and it was wrong. Go's
		// decoder keeps reading markers until end-of-image and decides
		// whether the file is RGB at the very end, so an Adobe marker placed
		// AFTER the scan data still flips it — and a parser that stopped at
		// the scan never saw it. Measured: the same 5120x5120 image with its
		// Adobe marker moved to just before end-of-image was estimated at
		// 37 MB, sailed through the budget, and decoded to 137 MB. The whole
		// value of this estimate is that the number means something.
		//
		// DecodeConfig stops at the scan too, so the colour-model fallback
		// would not have caught it either.
		if marker == 0xDA {
			i += 2 + segLen
			for i+1 < len(data) {
				if data[i] != 0xFF {
					i++
					continue
				}
				// Inside compressed data a 0xFF is either stuffed with a
				// following zero, a fill byte, or a restart marker. None of
				// those ends the scan.
				next := data[i+1]
				if next == 0x00 || next == 0xFF || (next >= 0xD0 && next <= 0xD7) {
					i += 2
					continue
				}
				break
			}
			continue
		}

		payload := data[i+4 : i+2+segLen]

		switch {
		case marker == 0xE0: // APP0
			jfif = len(payload) >= 5 && string(payload[:5]) == "JFIF\x00"

		case marker == 0xEE: // APP14
			if len(payload) >= 12 && string(payload[:5]) == "Adobe" {
				adobeTransformValid = true
				adobeTransform = payload[11]
			}

		case isJPEGFrameHeader(marker):
			if len(payload) < 6 {
				return 0, false
			}
			height = int64(payload[1])<<8 | int64(payload[2])
			width = int64(payload[3])<<8 | int64(payload[4])
			count := int(payload[5])
			if width <= 0 || height <= 0 || count <= 0 || count > 4 {
				return 0, false
			}
			if len(payload) < 6+3*count {
				return 0, false
			}
			for c := range count {
				b := payload[6+3*c:]
				h := int64(b[1] >> 4)
				v := int64(b[1] & 0x0f)
				if h < 1 || v < 1 || h > 4 || v > 4 {
					return 0, false
				}
				comps = append(comps, sampling{h, v})
				ids = append(ids, b[0])
			}
			progressive = marker == 0xC2 || marker == 0xC6 || marker == 0xCA || marker == 0xCE
			haveFrame = true
		}
		i += 2 + segLen
	}

	if !haveFrame {
		return 0, false
	}

	var hmax, vmax int64 = 1, 1
	for _, c := range comps {
		hmax = max(hmax, c.h)
		vmax = max(vmax, c.v)
	}

	// Blocks across and down, in units of the largest sampling factor.
	mxx := (width + 8*hmax - 1) / (8 * hmax)
	myy := (height + 8*vmax - 1) / (8 * vmax)

	var total int64
	for _, c := range comps {
		total += (mxx * 8 * c.h) * (myy * 8 * c.v)
		if progressive {
			total += mxx * myy * c.h * c.v * 256
		}
	}

	// A whole second image, at full resolution, whenever the decoder converts
	// rather than returning the planes it already has.
	//
	// Four components is the CMYK case: applyBlack builds a CMYK image beside
	// the planes and the black plane. Three components does it too whenever
	// the file says it is RGB rather than YCbCr, which was missed the first
	// time and is not a rare shape — cjpeg -rgb writes one, and so does
	// anything Adobe tags with transform 0. Measured, a 6 MB RGB 4:4:4 file
	// estimated at 75 MB and left 175 MB live.
	//
	// The test for RGB is the decoder's own, because guessing it wrong in
	// either direction is expensive: assume every three-component file
	// converts and an ordinary 24-megapixel 4:2:0 photograph goes from 34 MB
	// to 130 MB and is refused.
	if len(comps) == 4 || jpegIsRGB(jfif, adobeTransformValid, adobeTransform, ids) {
		total += width * height * 4
	}
	return total, true
}

// jpegIsRGB mirrors image/jpeg's own isRGB: a three-component file whose
// samples are red, green and blue rather than luma and chroma. The decoder
// allocates a whole extra image to convert one.
//
// JFIF settles it: that header means YCbCr, whatever else the file says.
// Otherwise an Adobe marker with a transform of zero means RGB, and failing
// that the component identifiers are read as letters — 'R', 'G', 'B'.
func jpegIsRGB(jfif, adobeValid bool, adobeTransform byte, ids []byte) bool {
	if len(ids) != 3 {
		return false
	}
	if jfif {
		return false
	}
	if adobeValid && adobeTransform == 0 {
		return true
	}
	return ids[0] == 'R' && ids[1] == 'G' && ids[2] == 'B'
}

// isJPEGFrameHeader reports whether a marker starts a frame header (SOF0
// through SOF15, less the two that are not frames: DHT at 0xC4 and DAC at
// 0xC8).
func isJPEGFrameHeader(m byte) bool {
	return m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8
}

// imageWork bounds how many images are decoded and re-encoded at once.
//
// The size check above bounds ONE request. Nothing bounds how many arrive
// together, and the handler has no other concurrency limit — the rate limiter
// covers login, signup and guest resend, not uploads. Five requests each
// legitimately inside the budget still add up, so the budget has to be a
// budget for the process, not for a request.
//
// A package-level value rather than a field on Server, deliberately and
// against the usual rule: what this protects is the machine's memory. Two
// Servers in one test process share one heap, and a limiter each would not
// bound it.
var imageWork = make(chan struct{}, 4)

func compressImage(data []byte, ext string) ([]byte, string, error) {
	// Before the decode, never after.
	if err := decodedSizeWithin(data, maxImagePixels, maxDecodedBytes); err != nil {
		return nil, "", err
	}

	imageWork <- struct{}{}
	defer func() { <-imageWork }()

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

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, "", fmt.Errorf("encoding PNG: %w", err)
	}

	// JPEG is only a candidate for an image with nothing transparent in it.
	//
	// JPEG has no alpha channel, so encoding a transparent image to it does
	// not compress the transparency — it deletes it, replacing every
	// see-through pixel with opaque black. Measured: a 313 KB PNG with a
	// fully transparent half came back 37 KB, "smaller", and every pixel that
	// had been invisible was black. The file kept its .png name, so the
	// person who uploaded a logo or an annotated screenshot got back a
	// different picture under the name they chose, with nothing said.
	//
	// Smaller is the wrong question when the two candidates are not the same
	// image. Every type the standard library decodes answers Opaque; an
	// unknown one is assumed to have transparency, because the cost of being
	// wrong that way is a larger file and the cost of being wrong the other
	// way is a destroyed one.
	opaque := false
	if o, ok := img.(interface{ Opaque() bool }); ok {
		opaque = o.Opaque()
	}
	if !opaque {
		return pngBuf.Bytes(), ".png", nil
	}

	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", fmt.Errorf("encoding JPEG: %w", err)
	}

	if jpegBuf.Len() <= pngBuf.Len() {
		return jpegBuf.Bytes(), ".jpg", nil
	}
	return pngBuf.Bytes(), ".png", nil
}

// POST /api/v1/tickets/{id}/attachments
// Accepts multipart/form-data with field name "file" (one file per request).
// Authenticated users only; a guest uploads through handleGuestUploadAttachment
// instead, which resolves its own ticket from a token rather than a path
// parameter.
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

	// The same ownership-and-lifecycle rule a reply is authorised by (#315: a
	// reporter could previously attach to their own Closed ticket, because
	// this handler checked ownership and nothing else).
	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}
	reopenDays := s.adminSvc.ReopenWindowDays(r.Context())
	if err := s.tickets.CanUploadAttachment(r.Context(), ticketID, actor, reopenDays); err != nil {
		handleError(w, err)
		return
	}

	s.storeUploadedAttachment(w, r, ticketID, actor)
}

// storeUploadedAttachment is the multipart handling, validation, scanning and
// storage shared by an authenticated upload and a guest one — everything
// past authorisation, which is the only place the two callers differ.
//
// This body is up to 25 MB and the server-wide read deadline assumes a
// body is a JSON document. See bodyTransferTimeout.
//
// Best effort: SetReadDeadline fails on a connection that does not
// support one, which in this codebase means a test using an unusual
// transport. Failing the upload over it would be worse than keeping the
// shorter deadline.
//
// actor is who is uploading, for the last check before the row is written: a
// guest is Actor{Role: RoleUser} with a nil UserID, which is also how a close
// that wins the race is answered (the generic 404 for a guest, 409 for a
// signed-in reporter).
func (s *Server) storeUploadedAttachment(w http.ResponseWriter, r *http.Request, ticketID uuid.UUID, actor ticket.Actor) {
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(bodyTransferTimeout)); err != nil {
		slog.DebugContext(r.Context(), "could not extend the upload read deadline", "error", err)
	}

	// Cap the body before parsing it, not after.
	//
	// ParseMultipartForm's argument is a MEMORY limit, not a body limit:
	// everything past it spills to a temp file with no ceiling, so a 120 MB
	// body was read to completion and written to disk in full before the
	// handler answered 413. Any authenticated user could run several at once
	// and fill the container's writable layer. MaxBytesReader stops the read
	// at the limit instead, which is what the logo handler already does.
	//
	// The extra megabyte is for the multipart framing — boundaries, part
	// headers, the field name — so that a file of exactly attachMaxBytes is
	// refused by the size check below, with the message about the size, and
	// not by the reader with a parse error.
	r.Body = http.MaxBytesReader(w, r.Body, attachMaxBytes+(1<<20))

	if err := r.ParseMultipartForm(attachMaxBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			// The same status, code and message the two size checks further
			// down already return, so a client that handles one handles all
			// three. The only difference is where it is decided: here, before
			// the body has been read, rather than after.
			Error(w, http.StatusRequestEntityTooLarge, "too_large", "file exceeds 25 MB limit")
			return
		}
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

	// And no character whose job is to make the name display as something
	// other than what it is.
	//
	// Three families, all accepted before this and all stored verbatim:
	// C0 and C1 control characters, including ESC — so a name carrying a
	// terminal escape sequence coloured or moved the cursor in any log or
	// shell that printed it, and CR LF split it across lines; and the bidi
	// overrides, where U+202E turns "invoice<U+202E>txt.pdf" into
	// "invoicefdp.txt" on screen, which is the oldest trick there is for
	// making an executable look like a document.
	//
	// The download header already strips ASCII controls, so this is not about
	// the header. It is about every other place the name is shown — the
	// ticket page, a log line, the entry name inside a quarantine archive —
	// none of which can sanitise a name they were handed as truth.
	if i := strings.IndexFunc(origName, deceptiveRune); i >= 0 {
		Error(w, http.StatusBadRequest, "invalid_filename",
			"filename contains a control or text-direction character")
		return
	}

	// And it has to be a length something downstream can hold.
	//
	// Nothing bounded this before, and the multipart reader allows a 10 MB
	// part header, so a filename of any length reached storage. A ZIP entry
	// name is a 16-bit field: over 65,535 bytes the writer we wrap with
	// silently truncates the length rather than refusing, which produced an
	// archive no tool could open — accepted with a 201 and written to disk,
	// so the ticket carried a file nobody could ever read back.
	//
	// 255 bytes is the limit almost every filesystem the download lands on
	// imposes anyway, so this refuses at the door what the reader's own
	// machine would refuse at the end.
	if len(origName) > maxFilenameBytes {
		Error(w, http.StatusBadRequest, "invalid_filename",
			fmt.Sprintf("filename must be %d bytes or fewer", maxFilenameBytes))
		return
	}

	// What this instance accepts is the operator's setting, not a map in this
	// file, and it is read per upload rather than once at startup — the defect
	// the scanner address had, where a saved value was never consulted again.
	//
	// Checked here on the claimed extension: after the filename is known to be
	// storable, and before the body is read, the content is detected or the
	// scanner is asked. A type this instance does not take is refused as a
	// type, rather than later as a bad image or as malware, and a refusal
	// costs nothing.
	ext := strings.ToLower(filepath.Ext(origName))
	allowed := s.adminSvc.AllowedTypes(r.Context())
	if !allowed[ext] {
		what := ext + " files"
		if ext == "" {
			what = "files with no extension"
		}
		Error(w, http.StatusUnsupportedMediaType, "unsupported_type",
			"this instance does not accept "+what)
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

	// What the bytes actually are, and what they hash to.
	//
	// Both are taken here, on the upload exactly as it arrived: before
	// recompression rewrites an image and before a wrap puts it inside an
	// archive. They answer the same question — what did this person send us —
	// which is what identifies a sample to an analyst and what a
	// chain-of-custody record has to state. For a recompressed image the
	// stored file's hash is therefore not this hash, which is a fact the UI
	// has to say rather than leave to be discovered.
	//
	// This replaced magicOK, which checked a hard-coded signature per
	// extension and ended in `default: return true`. That was not a text
	// special case but a default, so every extension without an entry — .txt
	// and .log then, anything an operator adds now — was never looked at.
	detectedExt, detectedMime := attachment.Detect(data)
	sha := attachment.SHA256(data)

	// A content contradiction is recorded, never refused. Legitimate ones
	// exist — a .log holding a captured HTML response is an ordinary help desk
	// attachment — and since every download is an octet-stream blob with an
	// attachment disposition, a mismatch is not a risk to this server. It is a
	// deception risk for the person about to open the file, which is why they
	// are told instead of the upload being rejected.
	mismatch := attachment.IsMismatch(ext, detectedExt, detectedMime)

	// And judged only where we can judge.
	//
	// The same reasoning that stops containment firing on an operator-added
	// extension applies to the flag, and the flag is the half staff actually
	// read. On an instance that added .htm, every genuine HTML page was
	// recorded as a contradiction and shown as "Content looks like HTML, not
	// a .htm file" — a false sentence about an ordinary file, which is the
	// failure this whole rule exists to avoid. The detector spells the format
	// .html; the operator spelled it .htm; nobody lied.
	//
	// nil rather than false: "no contradiction" is also a claim, and we cannot
	// make it either. What we can say is what the content is, and
	// detected_mime carries that — a row with a detected type and no verdict
	// is legible as "we looked and could not judge", which is the truth, and
	// is distinguishable from a row that predates detection and has neither.
	var judged *bool
	if shippedExt(ext) {
		judged = &mismatch
	}

	// What the scanner made of it, and what the operator chose to do with
	// that. An empty name means the file was not identified as malicious —
	// either it was scanned and found clean, or the policy meant it was never
	// scanned at all. scanUpload has already written the response and
	// returned false for everything that is refused.
	virusName, ok := s.scanUpload(w, r, data, origName, ticketID)
	if !ok {
		return
	}

	// The stored mime_type is data about what the file claims to be, not a
	// decision about how it is served: every download is octet-stream with an
	// attachment disposition (#165 step 1). Once an operator can allow .exe,
	// the built-in map has no answer for the extension, so the content
	// detector answers instead — and octet-stream stands in when it cannot
	// place the bytes either. A blank type column is not an answer.
	//
	// It describes the file as stored, which is why the wrap below overwrites
	// it: detected_mime keeps the answer for the bytes that arrived.
	mime, known := allowedExt[ext]
	if !known {
		mime = detectedMime
	}

	// The two things that can rewrite an upload before it is stored, and they
	// are alternatives: a wrapped file is an archive, so recompressing it
	// would mean asking the JPEG encoder to read a ZIP.
	storedName := origName
	storedExt := ext
	switch {
	case virusName != "":
		// Quarantine: the operator chose to keep a file the scanner
		// identified, so it is stored wrapped rather than refused.
		//
		// Unlike the tier below this one carries a password, and the password
		// is published — it protects nothing and is not meant to. Its only
		// jobs are that the stored bytes are not directly double-clickable,
		// and that an on-access scanner, on our storage or the downloader's,
		// does not eat the sample out from under the ticket a day later.
		//
		// Wrapped here, on the way in, rather than at download time. The
		// alternative leaves live malware sitting in the attachment directory
		// of a deployment that very likely runs host AV over it.
		archive, err := attachment.Wrap(data, origName, attachment.QuarantinePassword)
		if err != nil {
			Error(w, http.StatusInternalServerError, "storage_error",
				"could not wrap the file")
			return
		}
		// The uploaded name plus .zip, so nothing downstream double-clicks a
		// .exe. Appended after the allowlist has already had its say on the
		// uploaded name, and after the hash and the detected type were taken
		// from the uploaded bytes: .zip describes our wrapper, not the sample,
		// and it does not need to be an accepted type for this to work.
		storedName = origName + ".zip"
		data = archive
		storedExt = ".zip"
		mime = "application/zip"

	case mismatch && shippedExt(ext) && !attachment.IsTextExtension(ext) && !allowed[detectedExt]:
		// A file whose content contradicts its name, where the name claims a
		// binary format and the content is not something this instance
		// accepts. What happens to it is the operator's decision, and the
		// default is a refusal with the same status, code and message 1.2.0
		// refused with. Not the same *set* of files, though: DESIGN.md has
		// the measured table of what an upgrade moves in each direction.
		//
		// Two conditions keep ordinary files out of this arm, and both are
		// load-bearing.
		//
		// A claimed text extension never reaches here at all. Wrapping
		// contains a file by taking away the name that decides how it opens,
		// and a file named .log already opens in a text editor whatever is
		// inside it — so there is nothing to contain, only something to say.
		// Refusing one was the regression that made a NUL-padded crash log, a
		// UTF-16 .txt and a gzipped rotated log all answer 415 on an instance
		// that had changed no setting. Such a file is still flagged and still
		// recorded; it is simply stored under its own name.
		//
		// The operator's own allowlist is the judgement for everything else,
		// so there is no second list to keep correct — a mismatch of an
		// accepted type falls past this arm untouched by the setting.
		//
		// That lookup is the detector's extension against a list of the
		// operator's spellings, which are two vocabularies, and the answer
		// differs where they disagree: an instance allowing .pdf and .htm
		// contains genuine HTML named report.pdf, where one allowing .pdf and
		// .html only flags it. Both operators allowed HTML; one spelled it
		// the way the detector does. Deliberate, and not a synonym table for
		// the same reason shippedExt is not one — but the reason it is
		// tolerable here is the direction of the error. A file only reaches
		// this line by already lying about its name, so the strict answer is
		// containment of something that was lying, not a refusal of an
		// ordinary file. DESIGN.md names it under "Accepted there means the
		// detector's spelling".
		//
		// Reached only after the quarantine arm above has had its say. A file
		// the scanner identified is governed by attachment_infected_handling
		// and never by this setting: "the scanner named this" and "the
		// content is not what the name says" are different claims, and an
		// operator may reasonably keep one and refuse the other.
		if s.adminSvc.MismatchHandling(r.Context()) != admin.MismatchHandlingWrap {
			// The same 415 as before this feature existed, verbatim, so an
			// operator who was relying on that response still gets it.
			//
			// detected_mime, sha256 and content_mismatch were computed above
			// regardless — they are facts about what arrived rather than
			// enforcement — and are simply not written, because nothing is
			// stored.
			Error(w, http.StatusUnsupportedMediaType, "invalid_file",
				"file content does not match the expected type")
			return
		}

		// Wrapped rather than refused, because the operator asked for it.
		// Refusing closes off the case a help desk is otherwise good at: the
		// suspicious file a user reported is exactly the file a ticket is
		// about. Flagging it alone would be too quiet — it still lands on
		// someone's disk named report.pdf. The archive name is the warning,
		// and unlike our UI it survives being forwarded or saved to a share.
		//
		// No password, unlike an infected file. That password stops an
		// on-access scanner eating a known sample; this file is not known-bad,
		// we could not identify it, and blinding the recipient's antivirus
		// over a wrong extension would be the wrong trade.
		archive, err := attachment.Wrap(data, origName, "")
		if err != nil {
			Error(w, http.StatusInternalServerError, "storage_error",
				"could not wrap the file")
			return
		}
		// Named after the CRC32 of the file inside it — the checksum the
		// archive already carries for its one entry, so the name refers to a
		// value a recipient can verify and costs nothing to produce.
		storedName = fmt.Sprintf("suspicious-%08x.zip", crc32.ChecksumIEEE(data))
		data = archive
		storedExt = ".zip"
		mime = "application/zip"

	case imageExt[ext]:
		// Image recompression: pick whichever of JPEG/PNG is smaller.
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
	// 0700 and 0600, not 0755 and 0644. Nothing but this process ever reads
	// these files — they are served through a handler that checks who is
	// asking — and on a host where the help desk shares a machine with other
	// accounts, world-readable meant every local user could read every
	// customer's attachments, quarantined malware included. Inside the Docker
	// image this changes nothing; outside it, it is the difference.
	if err := os.MkdirAll(subdir, 0o700); err != nil {
		Error(w, http.StatusInternalServerError, "storage_error", "could not create storage directory")
		return
	}
	diskPath := filepath.Join(subdir, storageID.String()+storedExt)
	if err := os.WriteFile(diskPath, data, 0o600); err != nil {
		// A partial write leaves a file behind, and nothing in this system
		// ever deletes an attachment file — so an orphan is permanent. The
		// database-failure path below already cleans up after itself; this
		// one did not.
		_ = os.Remove(diskPath)
		Error(w, http.StatusInternalServerError, "storage_error", "could not write file")
		return
	}

	att := ticket.Attachment{
		ID:          storageID,
		TicketID:    ticketID,
		Filename:    storedName, // the uploaded name, unless the file was wrapped
		MimeType:    mime,
		SizeBytes:   int64(len(data)),
		StoragePath: diskPath,
		CreatedAt:   time.Now(),

		// Of the bytes as uploaded, both of them.
		DetectedMime:    &detectedMime,
		SHA256:          &sha,
		ContentMismatch: judged,
	}
	// NULL on everything else, and there it is a fact rather than an absence:
	// it means this file was not identified as malicious.
	if virusName != "" {
		att.VirusName = &virusName
	}
	if err := s.tickets.CreateAttachment(r.Context(), att, actor); err != nil {
		_ = os.Remove(diskPath)
		if errors.Is(err, ticket.ErrClosed) {
			// The ticket closed while the file was being read and scanned.
			if actor.UserID == nil {
				guestWriteError(w, err)
			} else {
				handleError(w, err)
			}
			return
		}
		Error(w, http.StatusInternalServerError, "db_error", "could not record attachment")
		return
	}

	s.addReputationURL(r.Context(), &att)
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
		ticketNotFound(w, ticketID.String())
		return
	}

	atts, err := s.tickets.ListAttachments(r.Context(), ticketID)
	if err != nil {
		handleError(w, err)
		return
	}
	// The verdict is a staff triage tool and it costs an operator's quota, so
	// a customer refreshing their own ticket does not spend one. The link
	// beside it is free and everybody gets that.
	staff := a != nil && a.Role != user.RoleUser
	for i := range atts {
		s.addReputationURL(r.Context(), &atts[i])
		if staff {
			// r.Context(), so a reader who closes the tab takes the lookup
			// with them. Nothing here can fail the response: see
			// addReputation.
			s.addReputation(r.Context(), &atts[i])
		}
	}
	JSON(w, http.StatusOK, atts)
}

// POST /api/v1/tickets/{id}/attachments/{attachId}/reputation
//
// Re-checks one file's verdict because a person asked, rather than because a
// page was rendered. Somebody looking at a quarantined sample and wondering
// whether the world has caught up since is exactly who should be able to find
// out, and an operator who set the automatic interval to "never" to save quota
// did not mean "nobody may ever ask".
//
// Staff only, at the route, for the reason the lazy lookup checks the role
// before spending one: a reporting customer refreshing their own ticket must
// not cost the operator a third party's allowance. Everything else — the
// weekly floor, the refusal on a detection, the budget — belongs to
// reputation.Service, which is where the same rules already govern the
// automatic half.
func (s *Server) handleRecheckAttachmentReputation(w http.ResponseWriter, r *http.Request) {
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

	att, err := s.tickets.GetAttachment(r.Context(), attID)
	if err != nil {
		handleError(w, err)
		return
	}
	if att.TicketID != ticketID {
		Error(w, http.StatusNotFound, "not_found", "attachment not found on this ticket")
		return
	}

	// No hash is the same answer as no verdict: there is nothing to look up
	// and nothing to re-check. Attachments that predate the inspection are the
	// case, and they render with no reputation row at all.
	if att.SHA256 == nil || *att.SHA256 == "" {
		noVerdictToRecheck(w)
		return
	}

	svcs := s.reputationServices(r.Context())

	// ?provider=virustotal re-checks one service rather than all of them.
	//
	// Optional, and its absence means "every enabled provider", which is what
	// the single-provider control did before there was more than one. The UI
	// gives each provider its own Check again control, because each verdict
	// has its own expiry clock, so it names the one the reader clicked.
	if want := r.URL.Query().Get("provider"); want != "" {
		svcs = onlyProvider(svcs, want)
		if len(svcs) == 0 {
			// Misspelled, or switched off since the page was rendered. Both
			// mean the same thing to the caller — there is no such lookup on
			// this instance — and splitting them would make a client learn a
			// second code to handle identically.
			Error(w, http.StatusBadRequest, "invalid_provider",
				"that reputation provider is not enabled on this instance")
			return
		}
	}

	if len(svcs) == 0 {
		// No cache wired, or no provider enabled: the feature is off, and a
		// control that cannot work should say so rather than report a refusal
		// that sounds temporary.
		reputationUnavailable(w, "the reputation lookup is not configured on this instance")
		return
	}

	// Every selected provider is asked, and the refusals are collected rather
	// than returned from the first one. An exhausted VirusTotal allowance must
	// not stop CIRCL being re-checked, for the same reason it does not stop
	// CIRCL being looked up: the budgets are per provider because the
	// allowances are.
	var (
		refreshed bool
		final     int // refused because the verdict cannot change
		notCached int // refused because nothing has been looked up yet
		tooSoon   time.Time
		lastState reputation.State
	)
	for _, svc := range svcs {
		rep, err := svc.Refresh(r.Context(), *att.SHA256)
		switch {
		case err == nil:
			refreshed = true

		case errors.Is(err, reputation.ErrNotCached):
			notCached++

		case errors.Is(err, reputation.ErrDetectionIsFinal):
			final++
			lastState = rep.State

		case errors.Is(err, reputation.ErrTooSoon):
			// The earliest clearing time across the refused providers, so the
			// Retry-After a reader waits out is one that actually clears
			// something.
			next := rep.FetchedAt.Add(reputation.ManualRefreshFloor)
			if tooSoon.IsZero() || next.Before(tooSoon) {
				tooSoon = next
			}

		default:
			// The reason goes to the operator's log and not to the caller;
			// nothing here carries an API key — see reputationServices.
			// WARN is for something the operator has to fix — a rejected key,
			// a provider that is down. A refusal we made ourselves is not
			// that: a spent allowance, or a request that ran out of the time
			// budget before this provider's turn, are both the system working
			// as configured, and logging them at WARN buries the one line
			// that is worth reading.
			level := slog.LevelWarn
			if errors.Is(err, reputation.ErrRateLimited) ||
				errors.Is(err, reputation.ErrQuotaExceeded) ||
				errors.Is(err, reputation.ErrDeadlinePassed) {
				level = slog.LevelDebug
			}
			slog.Log(r.Context(), level, "attachment reputation re-check did not complete",
				"error", err, "provider", svc.Provider())
		}
	}

	// The refusal reported is the one that is true of EVERY provider asked,
	// which is why these are counted rather than returned from the first
	// failure. Telling a reader "already identified as malicious" because one
	// of four said so, while the other three were merely checked yesterday,
	// would be false about three of them.
	switch {
	case refreshed:
		// At least one answer changed hands, so the page is rebuilt below.

	case final == len(svcs):
		// Two verdicts are final and they are opposites, so one sentence
		// cannot serve both: telling a person that a file NSRL has on file is
		// "already identified as malicious" is false, alarming, and about the
		// one verdict here they are entitled to find reassuring.
		//
		// The CODE stays the same for both. To a client this is one outcome —
		// the verdict cannot change, so the control is not armed — and
		// splitting it would make every caller learn a second code to handle
		// identically.
		msg := "this file is already identified as malicious; engines do not un-flag a file, " +
			"so re-checking it would tell nobody anything"
		if lastState == reputation.Known {
			msg = "this file is already known: a named feed has this exact hash in its " +
				"catalogue, and a catalogue entry does not decay, so re-checking it " +
				"would tell nobody anything"
		}
		Error(w, http.StatusConflict, "detection_is_final", msg)
		return

	case notCached == len(svcs):
		noVerdictToRecheck(w)
		return

	case !tooSoon.IsZero():
		// The floor is a week per hash per provider. A refusal that does not
		// say when it clears is not actionable, so both the header and the
		// message carry it.
		if wait := time.Until(tooSoon); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		}
		Error(w, http.StatusTooManyRequests, "checked_recently",
			"this file was checked less than seven days ago; it can be checked again on "+
				tooSoon.UTC().Format("2 January 2006"))
		return

	default:
		// A re-check that did not happen, and the reader asked for it, so it
		// gets a refusal rather than a page that silently shows the old
		// answer.
		reputationUnavailable(w, "the reputation service could not be asked right now; please try again shortly")
		return
	}

	// Rebuilt through the ordinary render path so the re-checked provider and
	// its siblings are described by exactly the same code. Refresh has already
	// stored the fresh verdict, so this reads it back from the cache rather
	// than spending a second lookup on it.
	s.addReputationURL(r.Context(), &att)
	s.addReputation(r.Context(), &att)
	JSON(w, http.StatusOK, att)
}

// onlyProvider narrows a set of lookup services to the one named, or to none
// when that provider is not among them.
func onlyProvider(svcs []*reputation.Service, name string) []*reputation.Service {
	for _, svc := range svcs {
		if svc.Provider() == name {
			return []*reputation.Service{svc}
		}
	}
	return nil
}

// noVerdictToRecheck is the refusal for a file nobody has looked up.
//
// Deliberately not a lookup: the ordinary lazy one covers a file with no
// verdict, and a re-check that quietly became a first lookup would be a second
// way to spend the allowance with none of the rules on it.
func noVerdictToRecheck(w http.ResponseWriter) {
	Error(w, http.StatusConflict, "no_verdict",
		"nothing has been looked up for this file yet, so there is nothing to re-check")
}

func reputationUnavailable(w http.ResponseWriter, message string) {
	w.Header().Set("Retry-After", "60")
	Error(w, http.StatusServiceUnavailable, "reputation_unavailable", message)
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
		ticketNotFound(w, ticketID.String())
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

	// The same reasoning as the upload, pointing the other way: a 25 MB file
	// leaving over a slow link outlasts the server-wide 30s write deadline
	// and the download is cut off part-written. See bodyTransferTimeout.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(bodyTransferTimeout)); err != nil {
		slog.DebugContext(r.Context(), "could not extend the download write deadline", "error", err)
	}

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
// whether the upload may proceed and, when it does, the scanner's name for
// whatever it found. It has written the response when it returns false.
//
// A non-empty name is how the caller knows to quarantine, and it is only ever
// non-empty when the scanner returned a verdict: the setting is not a second
// opinion about files nobody looked at, so an instance with scanning off
// quarantines nothing however it is configured.
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
func (s *Server) scanUpload(w http.ResponseWriter, r *http.Request, data []byte, filename string, ticketID uuid.UUID) (string, bool) {
	ctx := r.Context()
	sc := s.scanner(ctx)
	policy := s.adminSvc.AttachmentScanPolicy(ctx, sc.Configured())
	if policy == antivirus.PolicyOff {
		return "", true
	}

	res := sc.Scan(ctx, data)
	switch res.Verdict {
	case antivirus.Infected:
		if s.adminSvc.InfectedHandling(ctx) == admin.InfectedHandlingQuarantine {
			// The operator asked for the sample. An IT security team triaging
			// the suspicious .exe a user reported needs to attach precisely
			// the file the ticket is about, and refusing it is refusing the
			// case.
			slog.WarnContext(ctx, "quarantining an infected upload",
				"virus", res.Virus, "filename", filename, "ticket", ticketID)
			return res.Virus, true
		}
		slog.WarnContext(ctx, "infected upload refused",
			"virus", res.Virus, "filename", filename, "ticket", ticketID)
		Error(w, http.StatusUnprocessableEntity, "infected",
			fmt.Sprintf("file rejected: %s", res.Virus))
		return "", false

	case antivirus.Unavailable:
		if policy == antivirus.PolicyPermissive {
			// The old behaviour, now reachable only by choosing it.
			slog.WarnContext(ctx, "accepting an unscanned upload: scan policy is permissive",
				"filename", filename, "ticket", ticketID, "reason", res.Reason)
			return "", true
		}
		slog.ErrorContext(ctx, "refusing an upload that could not be scanned",
			"filename", filename, "ticket", ticketID, "reason", res.Reason)
		// Deliberately says nothing about why the scanner is unreachable: the
		// reason names internal infrastructure, and the caller can do nothing
		// with it either way.
		w.Header().Set("Retry-After", "60")
		Error(w, http.StatusServiceUnavailable, "scanner_unavailable",
			"attachments cannot be accepted right now because the virus scanner is unavailable; please try again shortly")
		return "", false
	}
	return "", true
}

// addReputationURL fills in where a person can read a public report on this
// file's hash.
//
// Not a function of which providers are enabled, and that is the correction
// this replaced a setting-driven version with. Which rows get one at all is a
// separate question, answered by worthLookingUp below.
//
// A link is not a lookup. A lookup is this server sending a
// customer's file hash to a third party — the operator's decision, their API
// allowance, and what the per-provider toggles govern. A link sends nothing
// from here: it is an anchor the analyst clicks in their own browser, under
// their own account or none, exactly as if they had copied the hash off the
// page and pasted it themselves, which they can do anyway because the hash is
// right there with a copy control.
//
// Switching VirusTotal off means "do not send my customers' hashes to
// VirusTotal from my server". It does not mean "my staff may never look at
// VirusTotal", and treating the two as one thing takes a decision away from
// the analyst that was never the operator's to make — while achieving
// nothing, because the hash is on the page either way.
//
// VirusTotal and not another service because its page is the one every analyst
// already knows: no account needed, and the complete report renders logged
// out. An enabled provider's own link sits next to its own verdict in the
// expanded view, where a link and a verdict from the same service belong
// together.
func (s *Server) addReputationURL(ctx context.Context, att *ticket.Attachment) {
	if att.SHA256 == nil || *att.SHA256 == "" || !worthLookingUp(att) {
		return
	}
	url := reputation.HashLink(*att.SHA256)
	if url == "" {
		return
	}
	att.ReputationURL = &url
}

// worthLookingUp reports whether this instance found anything about the file
// worth a second opinion.
//
// The link used to go on every attachment with a hash, which since detection
// landed is all of them — so a holiday-request PDF on a printer ticket carried
// a link to VirusTotal and a line of explanatory text underneath it. That is
// noise on the rows where nothing is wrong, and noise is what teaches people
// to stop reading the rows where something is.
//
// Three conditions earn it, and they are all this instance's own findings
// rather than anyone else's opinion:
//
//   - the scanner named it, so an analyst is working this row already
//   - the content contradicts the name and was not a type this instance
//     accepts, so it was wrapped — and nothing looked it up, because only
//     quarantined files are looked up, which makes the link the only outside
//     opinion available on that file
//   - the content contradicts the name but the type was allowed, so it was
//     stored under its own name. A weaker signal, and still the row where a
//     curious person would check.
//
// A file whose content matches its name and which the scanner passed gets
// nothing. Note what is NOT consulted: the reputation verdict. Only
// quarantined files are ever looked up (see addReputation), so five of these
// six conditions can never carry one — and on the sixth, suppressing the link
// because a provider said "clean" or "known" would be second-guessing the
// person doing the triage.
//
// The hash itself is still shown wherever it was recorded. It is a fact about
// the file rather than a claim by anybody, it costs nothing, and an analyst
// with their own account can paste it where they like.
func worthLookingUp(att *ticket.Attachment) bool {
	if att.VirusName != nil {
		return true
	}
	return att.ContentMismatch != nil && *att.ContentMismatch
}

// newReputationProvider builds one named provider with the key it needs.
//
// s.repOpts is empty in production, so every provider points at the real
// service. A test passes reputation.WithBaseURL through WithReputationLookup.
func (s *Server) newReputationProvider(name, apiKey string) reputation.Provider {
	switch name {
	case reputation.ProviderMetaDefender:
		return reputation.NewMetaDefender(apiKey, s.repOpts...)
	case reputation.ProviderPolySwarm:
		return reputation.NewPolySwarm(apiKey, s.repOpts...)
	case reputation.ProviderCIRCL:
		// No key, because there is none to give it: hashlookup authenticates
		// nobody, and there is no CIRCL key setting for one to come from.
		return reputation.NewCIRCL(s.repOpts...)
	}
	return reputation.NewVirusTotal(apiKey, s.repOpts...)
}

// shippedExt reports whether this extension is one of the nine this project
// ships, each of which was checked against the detector.
//
// Not the same claim as "their spelling is the detector's own", which is
// false for two of the nine: the detector calls a .log a .txt and a .jpeg a
// .jpg. Those two survive because IsMismatch relaxes for them — .jpeg and
// .jpg collapse to one spelling, and a .log matches any inert text — not
// because the names agree. The invariant is therefore that every shipped
// extension is either the detector's spelling or covered by one of those
// relaxations, and TestShippedExtensions_AreNeverAContradictionOfThemselves
// walks the shipped list and fails on any entry that is neither.
//
// Containment means being confident the name lied, and for an extension an
// operator added we cannot establish what it promised. The detector reports
// one canonical spelling per format: ".htm" is HTML and ".tif" is TIFF, but it
// calls them ".html" and ".tiff", so an operator who allowed ".htm" and
// received genuine HTML got a 415 reading "file content does not match the
// expected type" — a sentence that is false about a file matching its name
// exactly, refusing a type they had explicitly allowed.
//
// A synonym table is not the fix, and it was tried: ".jpeg" is in one and it
// only ever covered the set we ship. The two libraries involved do not agree
// on names for the same format either — the standard library calls a Windows
// executable application/x-msdownload where the detector calls it
// application/vnd.microsoft.portable-executable — so there is no canonical
// mapping to build a bigger table out of.
//
// What we can say honestly is narrower: the nine we ship were checked against
// the detector, so a contradiction under one of those names is a contradiction
// we can stand behind. Anything else is stored under its own name, with the
// detected type recorded and no verdict — neither contained nor flagged, since
// both would be claims we cannot make. The operator asked for the type; the
// least we owe them is not to refuse it while telling them something untrue
// about why.
func shippedExt(ext string) bool {
	_, ok := allowedExt[ext]
	return ok
}

// deceptiveRune reports whether a rune's purpose is to make text display as
// something other than what it is.
//
// The C0 and C1 control blocks, plus the bidirectional formatting characters.
// Tab, carriage return and newline are in C0 and are refused with the rest:
// none of them belongs in a filename, and a name that splits across two lines
// in a log is exactly the problem.
//
// The bidi set is the whole of it, not just U+202E. An override can be opened
// with LRO or RLO and closed with PDF, and the isolates (U+2066-U+2069) do the
// same job with different characters — leaving any one of them in means the
// trick still works, with one more keystroke.
func deceptiveRune(r rune) bool {
	switch {
	case r < 0x20, r == 0x7F: // C0 and DEL
		return true
	case r >= 0x80 && r <= 0x9F: // C1
		return true
	case r >= 0x202A && r <= 0x202E: // LRE, RLE, PDF, LRO, RLO
		return true
	case r >= 0x2066 && r <= 0x2069: // LRI, RLI, FSI, PDI
		return true
	}
	return false
}

// sampling is one JPEG component's horizontal and vertical sampling factors,
// which is what decides how much of the image that component's plane holds.
type sampling struct{ h, v int64 }
