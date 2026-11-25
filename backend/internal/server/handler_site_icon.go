package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/siteicon"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// The site icon, the manifest that names it and the shell that links them
// (#708).
//
// An administrator uploads one square image. The server scales it into the
// seven files browsers, phones and desktops ask for (domain/siteicon), keeps
// them in the database beside a 512 px master, and serves only those: what a
// visitor receives is bytes this server encoded, never the upload.

const (
	siteIconPath = "/api/v1/branding/icons/"
	manifestPath = "/api/v1/branding/manifest.webmanifest"

	// The audit entity is the icon as a whole; like every setting it has a name
	// and no row id (see settingEntityID).
	siteIconEntity        = "site_icon"
	auditSiteIconUploaded = "site_icon_uploaded"
	auditSiteIconRecolour = "site_icon_background_changed"
	auditSiteIconRemoved  = "site_icon_removed"

	uploadIconTooLarge = "The file is larger than 5 MB."
)

// WithSiteIcons turns on the site icon. Without it the icon routes answer 404,
// the manifest and the shell name the built-in icon, and an administrator's
// write is refused.
func WithSiteIcons(store siteicon.Store) Option {
	return func(s *Server) { s.siteIcons = store }
}

// ── Public: the files ────────────────────────────────────────────────────────

// handleServeSiteIcon serves one generated file. Public and uncredentialed: a
// browser asks for it with none, and rendering it is the point (so there is no
// Sec-Fetch-Dest check, unlike an attachment). What keeps it safe is what it
// is: bytes this server encoded from a decoded image, served with a type that
// names them, nosniff and the sandboxing policy of the logo.
func (s *Server) handleServeSiteIcon(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "file")
	ctype, ok := siteicon.ContentType(name) // false for master.png and anything else
	if !ok || s.siteIcons == nil {
		http.NotFound(w, r)
		return
	}
	data, version, ok, err := s.siteIcons.File(r.Context(), name)
	if err != nil {
		slog.Error("reading the site icon", "file", name, "error", err)
		Error(w, http.StatusInternalServerError, "storage_error", "reading the icon")
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	etag := `"` + version + `"`
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("ETag", etag)
	// A URL that names the version in force can never change under a cache. One
	// that names none, or an older one, is answered with the current file and
	// must be asked about again.
	if r.URL.Query().Get("v") == version {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	// Stricter than the site-wide policy: see logoCSP.
	h.Set("Content-Security-Policy", logoCSP)

	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// etagMatches reports whether an If-None-Match value names etag: the tag itself,
// weak or not, anywhere in a comma-separated list, or "*".
func etagMatches(header, etag string) bool {
	for _, p := range strings.Split(header, ",") {
		p = strings.TrimSpace(p)
		if p == "*" || p == etag || strings.TrimPrefix(p, "W/") == etag {
			return true
		}
	}
	return false
}

// ── Public: the manifest ─────────────────────────────────────────────────────

type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

// webManifest is the document a browser reads to offer "install" and to name
// the app on a home screen. Only the two names and the icon's own facts are in
// it: nothing about the instance, its settings or its people. Field order is
// the order written.
type webManifest struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	ShortName       string         `json:"short_name"`
	StartURL        string         `json:"start_url"`
	Scope           string         `json:"scope"`
	Display         string         `json:"display"`
	BackgroundColor string         `json:"background_color"`
	ThemeColor      string         `json:"theme_color,omitempty"`
	Icons           []manifestIcon `json:"icons"`
}

func buildManifest(name, home string, icon *siteicon.Icon) webManifest {
	m := webManifest{
		ID: "/", Name: name, ShortName: home, StartURL: "/", Scope: "/", Display: "standalone",
		BackgroundColor: siteicon.DefaultBackground,
		Icons:           []manifestIcon{{Src: "/favicon.svg", Sizes: "any", Type: "image/svg+xml", Purpose: "any"}},
	}
	if icon == nil {
		return m
	}
	src := func(file string) string { return siteIconPath + file + "?v=" + icon.Version }
	m.BackgroundColor, m.ThemeColor = icon.Background, icon.Background
	m.Icons = []manifestIcon{
		{Src: src("icon-192.png"), Sizes: "192x192", Type: "image/png", Purpose: "any"},
		{Src: src("icon-512.png"), Sizes: "512x512", Type: "image/png", Purpose: "any"},
		{Src: src("icon-maskable-512.png"), Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
	}
	return m
}

// currentIcon is the stored icon's metadata, nil when there is none or it cannot
// be read: a public page never fails for want of its icon, it shows the built-in.
func (s *Server) currentIcon(ctx context.Context) *siteicon.Icon {
	if s.siteIcons == nil {
		return nil
	}
	icon, ok, err := s.siteIcons.Get(ctx)
	if err != nil {
		slog.Warn("reading the site icon; using the built-in one", "error", err)
		return nil
	}
	if !ok {
		return nil
	}
	return &icon
}

// siteNames is the site name and what a home screen shows under the icon.
func (s *Server) siteNames(ctx context.Context) (name, home string) {
	name = s.adminSvc.SiteName(ctx)
	if home = s.adminSvc.SiteShortName(ctx); home == "" {
		home = name
	}
	return name, home
}

func (s *Server) handleServeManifest(w http.ResponseWriter, r *http.Request) {
	name, home := s.siteNames(r.Context())
	body, err := json.Marshal(buildManifest(name, home, s.currentIcon(r.Context())))
	if err != nil {
		slog.Error("encoding the web manifest", "error", err)
		Error(w, http.StatusInternalServerError, "internal_error", "encoding the manifest")
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:])[:16] + `"`

	h := w.Header()
	h.Set("Content-Type", "application/manifest+json; charset=utf-8")
	// The link to this document never changes, so freshness comes from asking
	// each time and being told "not modified" when nothing has.
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(body)
}

// ── The shell's head ─────────────────────────────────────────────────────────

// ShellHead is what replaces the marked region of index.html on each request
// (NewSPAHandler): the icon links, the manifest link, the home screen name and
// the title. It never fails: on a read error it logs and answers the built-in
// block, so the page is always served.
func (s *Server) ShellHead(ctx context.Context) string {
	name, home := s.siteNames(ctx)
	return shellHeadBlock(name, home, s.currentIcon(ctx))
}

// shellHeadBlock renders the block, each tag on its own line, four spaces in. The
// names are an administrator's text and are escaped; the version and the colour
// are escaped too, though they hold only hex, because this is where a changed
// rule would otherwise turn into markup. The block begins and ends with the
// markers, so what is served can be found and replaced again.
func shellHeadBlock(name, home string, icon *siteicon.Icon) string {
	esc := html.EscapeString
	lines := []string{"<!-- ghd:head -->"}
	add := func(format string, args ...any) { lines = append(lines, "    "+fmt.Sprintf(format, args...)) }

	if icon == nil {
		add(`<link rel="icon" type="image/svg+xml" href="/favicon.svg" />`)
	} else {
		v := esc(icon.Version)
		add(`<link rel="icon" href="%sfavicon.ico?v=%s" sizes="16x16 32x32 48x48" />`, siteIconPath, v)
		add(`<link rel="icon" type="image/png" sizes="32x32" href="%sfavicon-32.png?v=%s" />`, siteIconPath, v)
		add(`<link rel="icon" type="image/png" sizes="16x16" href="%sfavicon-16.png?v=%s" />`, siteIconPath, v)
		add(`<link rel="apple-touch-icon" sizes="180x180" href="%sapple-touch-icon.png?v=%s" />`, siteIconPath, v)
	}
	// Unversioned on purpose: an installed app keeps the URL it was installed
	// from, and the manifest's own caching (no-cache and an ETag) keeps it fresh.
	add(`<link rel="manifest" href="%s" />`, manifestPath)
	if icon != nil {
		add(`<meta name="theme-color" content="%s" />`, esc(icon.Background))
	}
	add(`<meta name="apple-mobile-web-app-title" content="%s" />`, esc(home))
	add(`<title>%s</title>`, esc(name))
	lines = append(lines, "    <!-- /ghd:head -->")
	return strings.Join(lines, "\n")
}

// ── Admin: status ────────────────────────────────────────────────────────────

type siteIconFileStatus struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Sizes []int  `json:"sizes"`
	Bytes int    `json:"bytes"`
}

type siteIconStatus struct {
	Set        bool                 `json:"set"`
	Background string               `json:"background"`
	Version    string               `json:"version"`
	SourceSize int                  `json:"source_size"`
	UpdatedAt  *string              `json:"updated_at"`
	Files      []siteIconFileStatus `json:"files"`
}

// siteIconStatusOf is what GET, POST and PATCH answer with.
func (s *Server) siteIconStatusOf(ctx context.Context) (siteIconStatus, error) {
	none := siteIconStatus{Background: siteicon.DefaultBackground, Files: []siteIconFileStatus{}}
	if s.siteIcons == nil {
		return none, nil
	}
	icon, ok, err := s.siteIcons.Get(ctx)
	if err != nil {
		return none, err
	}
	if !ok {
		return none, nil
	}
	sizes, err := s.siteIcons.FileSizes(ctx)
	if err != nil {
		return none, err
	}
	at := icon.UpdatedAt.UTC().Format(time.RFC3339)
	st := siteIconStatus{
		Set: true, Background: icon.Background, Version: icon.Version, SourceSize: icon.SourceSize,
		UpdatedAt: &at, Files: make([]siteIconFileStatus, 0, len(siteicon.Served)),
	}
	for _, name := range siteicon.Served {
		st.Files = append(st.Files, siteIconFileStatus{
			Name: name, URL: siteIconPath + name + "?v=" + icon.Version, Sizes: siteicon.Sizes(name), Bytes: sizes[name],
		})
	}
	return st, nil
}

func (s *Server) writeSiteIconStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.siteIconStatusOf(r.Context())
	if err != nil {
		slog.Error("reading the site icon", "error", err)
		Error(w, http.StatusInternalServerError, "storage_error", "reading the icon")
		return
	}
	JSON(w, http.StatusOK, st)
}

func (s *Server) handleGetSiteIcon(w http.ResponseWriter, r *http.Request) {
	s.writeSiteIconStatus(w, r)
}

// ── Admin: upload ────────────────────────────────────────────────────────────

func iconEntry(actor uuid.UUID, action string, before, after map[string]any) audit.Entry {
	return audit.Entry{
		ID: uuid.New(), ActorID: &actor, EntityType: auditEntitySetting,
		EntityID: settingEntityID(siteIconEntity), Action: action,
		Before: before, After: after, CreatedAt: time.Now(),
	}
}

// iconFacts is what an entry records of an icon: a digest of the upload, the
// colour and the version of the files. Nothing of the picture.
func iconFacts(i siteicon.Icon) map[string]any {
	return map[string]any{"icon_sha256": i.SourceSHA256, "icon_background": i.Background, "icon_version": i.Version}
}

func (s *Server) refuseIconStorage(w http.ResponseWriter, what string, err error) {
	if err != nil {
		slog.Error(what, "error", err)
	}
	Error(w, http.StatusInternalServerError, "storage_error", "saving the icon")
}

// handleUploadSiteIcon takes one PNG, JPEG or GIF, checks it, and replaces the
// icon with what the server makes of it. The checks run cheapest first and the
// first to fail ends the request with nothing written; the costly ones (the
// decode) come after every test that needs only the header.
func (s *Server) handleUploadSiteIcon(w http.ResponseWriter, r *http.Request) {
	if s.siteIcons == nil {
		s.refuseIconStorage(w, "site icons are not configured", nil)
		return
	}
	tooLarge := func() { Error(w, http.StatusBadRequest, "file_too_large", uploadIconTooLarge) }
	unreadable := func(err error) {
		Error(w, http.StatusBadRequest, "invalid_image", "The image could not be read: "+err.Error())
	}

	// 1. The body is capped a little above the file, for the form's own
	// overhead; the file's own limit is enforced exactly below.
	r.Body = http.MaxBytesReader(w, r.Body, siteicon.MaxUploadBytes+1<<20)
	if err := r.ParseMultipartForm(siteicon.MaxUploadBytes); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			tooLarge()
		} else {
			Error(w, http.StatusBadRequest, "invalid_form", "invalid multipart form")
		}
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	// 2. The file.
	f, _, err := r.FormFile("icon")
	if err != nil {
		Error(w, http.StatusBadRequest, "missing_field", "missing file field 'icon'")
		return
	}
	defer f.Close()

	// 3. The colour, if one was sent. Empty is "not sent".
	background := ""
	if v := r.MultipartForm.Value["background"]; len(v) > 0 && v[0] != "" {
		if background, err = siteicon.ParseBackground(v[0]); err != nil {
			Error(w, http.StatusBadRequest, "invalid_background", "The background colour must be a hex colour such as #ffffff.")
			return
		}
	}

	// 4. The exact size.
	data, err := io.ReadAll(io.LimitReader(f, siteicon.MaxUploadBytes+1))
	if err != nil {
		Error(w, http.StatusInternalServerError, "read_error", "reading uploaded file")
		return
	}
	if len(data) > siteicon.MaxUploadBytes {
		tooLarge()
		return
	}

	// 5. The format, by its first bytes: whatever the file is called and
	// whatever type the part claims. No SVG, for the reason the logo has none.
	kind, _, err := detectLogoType(data)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_type", err.Error())
		return
	}

	// 6 and 7. The header, then the shape it reports. Both before any pixel is
	// decoded, so an image claiming to be enormous costs a few bytes to refuse.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		unreadable(err)
		return
	}
	var se *siteicon.SizeError
	if err := siteicon.CheckSide(cfg.Width, cfg.Height); errors.As(err, &se) {
		dims := fmt.Sprintf("The image is %d × %d pixels. ", se.W, se.H)
		switch se.Kind {
		case siteicon.NotSquare:
			Error(w, http.StatusBadRequest, "icon_not_square",
				dims+"The icon must be square: crop it to the same width and height and upload it again.")
		case siteicon.TooSmall:
			Error(w, http.StatusBadRequest, "icon_too_small",
				dims+fmt.Sprintf("The icon must be at least %d × %d.", siteicon.MinSide, siteicon.MinSide))
		default:
			Error(w, http.StatusBadRequest, "icon_too_large",
				dims+fmt.Sprintf("The icon can be at most %d × %d; scale it down and upload it again.", siteicon.MaxSide, siteicon.MaxSide))
		}
		return
	}

	// 8. What decoding would cost, from the header alone.
	if err := decodedSizeWithin(data, maxImagePixels, maxDecodedBytes); err != nil {
		unreadable(err)
		return
	}

	// 9 to 11. Decode and render, a few at a time (imageWork); the slot is given
	// back before the store is touched, so none is held while waiting for a lock.
	background = s.iconBackground(r.Context(), background)
	files, decodeErr, err := renderIcon(data, kind, cfg, background)
	if decodeErr != nil {
		unreadable(decodeErr)
		return
	}
	if err != nil {
		s.refuseIconStorage(w, "rendering the site icon", err)
		return
	}

	sum := sha256.Sum256(data)
	icon := siteicon.Icon{
		Background: background, Version: siteicon.Version(files), SourceSize: cfg.Width,
		SourceSHA256: hex.EncodeToString(sum[:]),
	}
	actor := authmw.GetActor(r).UserID
	err = s.siteIcons.Replace(r.Context(), icon, files, func(prev *siteicon.Icon) audit.Entry {
		var before map[string]any
		if prev != nil {
			before = iconFacts(*prev)
		}
		after := iconFacts(icon)
		after["icon_source_size"] = icon.SourceSize
		return iconEntry(actor, auditSiteIconUploaded, before, after)
	})
	if err != nil {
		s.refuseIconStorage(w, "saving the site icon", err)
		return
	}
	s.writeSiteIconStatus(w, r)
}

// iconBackground is the colour a new icon is filled with: the one sent, else the
// stored icon's, else white.
func (s *Server) iconBackground(ctx context.Context, sent string) string {
	if sent != "" {
		return sent
	}
	if icon := s.currentIcon(ctx); icon != nil {
		return icon.Background
	}
	return siteicon.DefaultBackground
}

// renderIcon decodes the checked upload and makes the eight files. decodeErr is
// the upload's fault (it does not decode, or is not the size its header said);
// err is ours.
func renderIcon(data []byte, kind string, cfg image.Config, background string) (out siteicon.Files, decodeErr, err error) {
	imageWork <- struct{}{}
	defer func() { <-imageWork }()

	var src image.Image
	var derr error
	switch kind {
	case "png":
		src, derr = png.Decode(bytes.NewReader(data))
	case "jpeg":
		src, derr = jpeg.Decode(bytes.NewReader(data))
	case "gif":
		// gif.Decode returns the first frame, which is the one an icon is.
		src, derr = gif.Decode(bytes.NewReader(data))
	default:
		return out, nil, fmt.Errorf("unexpected raster kind %q", kind)
	}
	if derr != nil {
		return out, derr, nil
	}
	if b := src.Bounds(); b.Dx() != cfg.Width || b.Dy() != cfg.Height {
		return out, fmt.Errorf("the decoded image is %d × %d, not the %d × %d its header says",
			b.Dx(), b.Dy(), cfg.Width, cfg.Height), nil
	}
	files, err := siteicon.Render(siteicon.MasterFrom(src), background)
	if err != nil {
		return out, nil, err
	}
	return files, nil, nil
}

// ── Admin: background ────────────────────────────────────────────────────────

// handleUpdateSiteIcon changes the colour behind the home screen icons. It is
// made from the stored files, under the row's lock, so a colour change cannot
// overwrite an upload that landed a moment before it.
func (s *Server) handleUpdateSiteIcon(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Background json.RawMessage `json:"background"`
	}
	if err := DecodeJSON(r, &in); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	var sent string
	_ = json.Unmarshal(in.Background, &sent) // not a string: left empty, and refused below
	background, err := siteicon.ParseBackground(sent)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_background", "The background colour must be a hex colour such as #ffffff.")
		return
	}
	if s.siteIcons == nil {
		s.refuseIconStorage(w, "site icons are not configured", nil)
		return
	}

	actor := authmw.GetActor(r).UserID
	found, err := s.siteIcons.Update(r.Context(), func(prev siteicon.Icon, stored siteicon.Files) (siteicon.Icon, siteicon.Files, audit.Entry, error) {
		if prev.Background == background {
			return prev, nil, audit.Entry{}, siteicon.ErrUnchanged
		}
		imageWork <- struct{}{}
		files, err := siteicon.Recolour(stored, background)
		<-imageWork
		if err != nil {
			return prev, nil, audit.Entry{}, err
		}
		next := prev
		next.Background, next.Version = background, siteicon.Version(files)
		facts := func(i siteicon.Icon) map[string]any {
			return map[string]any{"icon_background": i.Background, "icon_version": i.Version}
		}
		return next, files, iconEntry(actor, auditSiteIconRecolour, facts(prev), facts(next)), nil
	})
	if err != nil {
		s.refuseIconStorage(w, "changing the site icon background", err)
		return
	}
	if !found {
		Error(w, http.StatusNotFound, "no_icon", "No icon is uploaded.")
		return
	}
	s.writeSiteIconStatus(w, r)
}

// ── Admin: delete ────────────────────────────────────────────────────────────

// handleDeleteSiteIcon removes the icon. Always 204: with none stored there is
// nothing to do and nothing to record, which is what makes it safe to repeat.
func (s *Server) handleDeleteSiteIcon(w http.ResponseWriter, r *http.Request) {
	if s.siteIcons == nil {
		s.refuseIconStorage(w, "site icons are not configured", nil)
		return
	}
	actor := authmw.GetActor(r).UserID
	_, err := s.siteIcons.Delete(r.Context(), func(prev siteicon.Icon) audit.Entry {
		return iconEntry(actor, auditSiteIconRemoved, iconFacts(prev), nil)
	})
	if err != nil {
		s.refuseIconStorage(w, "removing the site icon", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
