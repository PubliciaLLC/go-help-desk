package attachment

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/yeka/zip"
)

// Wrap returns a ZIP archive holding data as a single entry named filename,
// encrypted with password when one is given.
//
// One function for both tiers, because they differ in exactly one value. A
// file whose content contradicts its name is wrapped with no password: it is
// not refused, which would close off the suspicious file a ticket is often
// about, and not merely flagged, which still puts a web page on someone's disk
// called report.pdf. The archive name is the warning, and it keeps working
// after the file has been forwarded or saved to a share, where our UI does
// not.
//
// A file the scanner identified as malicious is wrapped with
// QuarantinePassword, so an on-access scanner does not eat the sample before
// an analyst sees it. That protection is not extended to a merely
// unidentified file: blinding the recipient's own antivirus over a wrong
// extension would be the wrong trade. Both are undoubleclickable either way,
// which is the part that matters for the first tier.
//
// The entry carries the uploaded name, so unwrapping produces the file the
// ticket is about rather than the wrapper's name.
//
// On the library: an earlier version of this file implemented the PKWARE
// stream cipher by hand, on the argument that this package is a fork of
// archive/zip and the standard library's CreateRaw already does every part of
// an encrypted entry except the cipher itself. That argument was about the
// size of a dependency and it does not reach the question it was used to
// answer. Writing a cipher by hand is a thing not to do, and "this one is
// weak on purpose so it hardly counts" is the reasoning that makes it a rule
// rather than a preference. The library is written by people who have read
// the specification more carefully than we are going to, and the 55 KB it
// costs is not worth one line of argument.
func Wrap(data []byte, filename, password string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	w, err := newEntry(zw, entryName(filename), password)
	if err != nil {
		return nil, fmt.Errorf("creating the archive entry: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return nil, fmt.Errorf("writing the archive entry: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("closing the archive: %w", err)
	}
	return buf.Bytes(), nil
}

// entryName is the uploaded filename reduced to something that cannot be read
// as a path by whatever unpacks the archive.
//
// Go's multipart reader runs filepath.Base over an uploaded name, which on
// Linux removes a forward-slash path and leaves backslashes alone — so
// `..\..\Users\Public\evil.exe` arrives intact and, without this, goes
// into the archive verbatim. Modern extractors sanitise it; WinRAR has had
// traversal bugs as recently as 2025, and an archive we built is not the
// place to find out which extractor the reader has.
//
// The same strip that contentDisposition applies to the download header, for
// the same reason: the name decides where somebody's machine puts the file.
func entryName(filename string) string {
	clean := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' {
			return -1
		}
		return r
	}, filename)
	if clean == "" {
		return "attachment"
	}
	return clean
}

// newEntry starts the single entry, encrypted or not.
//
// StandardEncryption is PKWARE's original stream cipher — "ZipCrypto" —
// rather than AES. Weak, which is the point: the password is published, so
// there is no confidentiality to protect, and AES-256 ZIP is opaque to
// Windows Explorer and macOS Archive Utility. Removing that friction for an
// analyst is the entire reason the password exists.
//
// Built through CreateHeader rather than the library's Create/Encrypt
// shortcuts, because those leave two fields unset that a reader outside Go
// needs:
//
// Bit 11 says the name is UTF-8. Without it the name has no declared
// encoding and a spec-following tool reads it as CP437, so "reçu — facture.exe"
// unpacks as mojibake. The library has had an open issue for this since 2024;
// setting the flag ourselves costs one line and does not depend on that being
// fixed.
//
// The MS-DOS timestamp is not optional in this format. Zero is not "no date",
// it decodes to day-of-month zero, which some tools display and some reject.
func newEntry(zw *zip.Writer, filename, password string) (io.Writer, error) {
	fh := &zip.FileHeader{
		Name:   filename,
		Method: zip.Deflate,
	}
	// SetModTime, not a Modified field: this package forked archive/zip
	// before that field existed, which is its own small argument for pinning
	// what we depend on rather than assuming a fork keeps pace.
	fh.SetModTime(time.Now().UTC())
	for i := 0; i < len(filename); i++ {
		if filename[i] >= 0x80 {
			fh.Flags |= 0x800
			break
		}
	}
	if password != "" {
		fh.SetPassword(password)
		fh.SetEncryptionMethod(zip.StandardEncryption)
	}
	return zw.CreateHeader(fh)
}
