// Package siteicon is the site icon (#708): one square image an administrator
// uploads, from which the server makes every size a browser, a phone or a
// desktop wants, and the rules that decide what may be uploaded.
//
// It holds the data (Icon, Files), the pure functions that turn an image into
// those files (Render, EncodeICO, Version) and the Store contract. It reads no
// request and touches no database; the handler and the store do both.
package siteicon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

const (
	// MaxUploadBytes is the largest file an administrator may upload.
	MaxUploadBytes = 5 << 20
	// MinSide and MaxSide bound the uploaded square. Below 512 there is not
	// enough picture for the largest icon; above 4096 the decode is a cost
	// with no visible return.
	MinSide = 512
	MaxSide = 4096
	// MasterSide is the side of the stored master every output is made from.
	MasterSide = 512
	// MaskableContent is the side of the picture inside the maskable icon: 80%
	// of 512, the safe zone a circular mask leaves. MaskableOffset centres it.
	MaskableContent = 410
	MaskableOffset  = (MasterSide - MaskableContent) / 2
	// DefaultBackground fills the transparent parts of the home screen icons
	// until an administrator chooses another.
	DefaultBackground = "#ffffff"
)

// Master is the stored name of the 512 px master. It is never served.
const Master = "master.png"

// Served is every file the server hands out, in the order the version digest,
// the admin file list and the previews use.
var Served = []string{
	"favicon.ico", "favicon-16.png", "favicon-32.png",
	"apple-touch-icon.png", "icon-192.png", "icon-512.png", "icon-maskable-512.png",
}

// Icon is the metadata of the stored icon.
type Icon struct {
	Background   string // "#rrggbb"
	Version      string // 16 hex characters, see Version
	SourceSize   int    // side of the uploaded square
	SourceSHA256 string // of the uploaded bytes
	UpdatedAt    time.Time
}

// Files maps a stored name to its bytes: Master and the Served names, always
// all eight.
type Files map[string][]byte

// ContentType is the media type of a served file; false for the master and for
// any name that is not served.
func ContentType(name string) (string, bool) {
	switch name {
	case "favicon.ico":
		return "image/x-icon", true
	case "favicon-16.png", "favicon-32.png", "apple-touch-icon.png",
		"icon-192.png", "icon-512.png", "icon-maskable-512.png":
		return "image/png", true
	}
	return "", false
}

// Sizes lists the pixel sides a served file contains: three for the .ico, one
// for every other.
func Sizes(name string) []int {
	switch name {
	case "favicon.ico":
		return []int{16, 32, 48}
	case "favicon-16.png":
		return []int{16}
	case "favicon-32.png":
		return []int{32}
	case "apple-touch-icon.png":
		return []int{180}
	case "icon-192.png":
		return []int{192}
	case "icon-512.png", "icon-maskable-512.png":
		return []int{512}
	}
	return nil
}

// SizeKind says which rule an uploaded image's dimensions broke.
type SizeKind int

const (
	NotSquare SizeKind = iota + 1
	TooSmall
	TooLarge
)

// SizeError is returned by CheckSide.
type SizeError struct {
	Kind SizeKind
	W, H int
}

func (e *SizeError) Error() string {
	switch e.Kind {
	case NotSquare:
		return fmt.Sprintf("the image is %d x %d pixels; the icon must be square", e.W, e.H)
	case TooSmall:
		return fmt.Sprintf("the image is %d x %d pixels; the icon must be at least %d x %d", e.W, e.H, MinSide, MinSide)
	default:
		return fmt.Sprintf("the image is %d x %d pixels; the icon can be at most %d x %d", e.W, e.H, MaxSide, MaxSide)
	}
}

// CheckSide judges the dimensions an image's header reports. Shape first: a
// 600 x 400 image is not square, and calling it too small would send the
// administrator to the wrong fix. There is no tolerance; a 513 x 512 image is
// refused.
func CheckSide(w, h int) error {
	switch {
	case w != h:
		return &SizeError{Kind: NotSquare, W: w, H: h}
	case w < MinSide:
		return &SizeError{Kind: TooSmall, W: w, H: h}
	case w > MaxSide:
		return &SizeError{Kind: TooLarge, W: w, H: h}
	}
	return nil
}

// ErrInvalidBackground is returned by ParseBackground.
var ErrInvalidBackground = errors.New("the background must be a hex colour such as #ffffff")

// ParseBackground accepts "#rrggbb" in either case and returns it in lower
// case, the one spelling that is stored.
func ParseBackground(s string) (string, error) {
	if len(s) != 7 || s[0] != '#' {
		return "", ErrInvalidBackground
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return "", ErrInvalidBackground
		}
	}
	return strings.ToLower(s), nil
}

// Version is a digest of the served files, 16 lower-case hex characters, so
// that every replica holding the same files names them the same and a changed
// background (which changes four of them) changes it. The master is left out:
// it is never served, and the files made from it already carry its content. A
// served file that is missing counts as empty.
func Version(f Files) string {
	h := sha256.New()
	for _, name := range Served {
		sum := sha256.Sum256(f[name])
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Store persists the icon. One icon per instance; every write carries its audit
// entry, committed with it.
type Store interface {
	// Get returns the metadata, and false when no icon is stored.
	Get(ctx context.Context) (Icon, bool, error)
	// File returns one stored file and the icon's version. ok is false when no
	// icon is stored or the name is not one of its files.
	File(ctx context.Context, name string) (data []byte, version string, ok bool, err error)
	// FileSizes returns the byte length of each served file, for the admin
	// status. Empty when no icon is stored.
	FileSizes(ctx context.Context) (map[string]int, error)
	// Replace locks the row, builds the audit entry from what is stored (nil
	// if none) and writes the metadata row, all eight files and the entry in
	// one transaction.
	Replace(ctx context.Context, icon Icon, files Files, entry func(prev *Icon) audit.Entry) error
	// Delete removes the icon and its files and writes the entry, in one
	// transaction. With nothing stored it writes nothing and reports false.
	Delete(ctx context.Context, entry func(prev Icon) audit.Entry) (deleted bool, err error)
	// Update changes the stored icon under the row's lock: it reads the
	// metadata and all eight files, hands them to decide, and writes what
	// decide returns with its audit entry, in one transaction. With nothing
	// stored it does nothing and reports false. decide returning ErrUnchanged
	// writes nothing and is not an error. It is how a background change is
	// made from the stored files without a concurrent upload being overwritten
	// by a colour change computed from the one before it.
	Update(ctx context.Context, decide func(prev Icon, stored Files) (Icon, Files, audit.Entry, error)) (found bool, err error)
}

// ErrUnchanged is what an Update's decide returns when there is nothing to write.
var ErrUnchanged = errors.New("the icon is already as asked")
