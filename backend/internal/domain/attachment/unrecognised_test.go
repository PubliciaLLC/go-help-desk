package attachment_test

import (
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/attachment"
)

// "The detector could not place this" is a statement about the media type, not
// about the extension.
//
// This is the fourth round of the same mistake, and the first one to name it:
// every relaxation of the mismatch rule has been written against the
// detector's *extension* vocabulary when the fact lives in its *MIME*
// vocabulary. Rule 2 was moved off an extension list onto isInertText for
// exactly this reason. Rule 3 then used an empty extension as a proxy for
// "unrecognised" — and the proxy has nine members, only one of which is
// actually unrecognised.
//
// The library returns no extension for application/x-elf,
// application/x-ole-storage, application/x-executable, application/x-object,
// application/x-coredump, application/x-mach-binary variants, application/tzif
// and application/zlib, as well as for application/octet-stream. So a Linux
// executable or an OLE2 compound document — the container for macro-bearing
// legacy Office files, MSI installers and .msg mail — arrived under a .txt
// name and was recorded as matching it, while a Windows executable under the
// same name was flagged. Same deception, opposite answer, decided by whether
// the library happened to know a file extension for the format.
//
// The rule is on the media type now. Extensions are for the claimed side,
// where they come from a filename and are all anyone has.
func TestIsMismatch_UnrecognisedIsAboutTheMediaTypeNotTheExtension(t *testing.T) {
	cases := []struct {
		name     string
		mime     string
		wantText bool // flagged under a .txt name?
	}{
		{
			// The case rule 3 exists for: a log with a NUL in it, a BOM-less
			// UTF-16 file. The detector genuinely cannot place these, and
			// under a text name that is its limit rather than a deception.
			name: "content the detector could not place", mime: "application/octet-stream",
			wantText: false,
		},
		{
			// Everything below is a format the detector DID place. It has no
			// filename extension to offer, which says nothing about the file.
			name: "a Linux executable", mime: "application/x-elf", wantText: true,
		},
		{
			name: "an OLE2 compound document", mime: "application/x-ole-storage", wantText: true,
		},
		{
			name: "a core dump", mime: "application/x-coredump", wantText: true,
		},
		{
			name: "zlib-compressed data", mime: "application/zlib", wantText: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The detector returns no extension for every one of these.
			if got := attachment.IsMismatch(".txt", "", tc.mime); got != tc.wantText {
				t.Errorf("IsMismatch(\".txt\", \"\", %q) = %v, want %v",
					tc.mime, got, tc.wantText)
			}
			// Under a name promising a specific format, all of them contradict
			// it — including the unplaceable one, because a PDF that cannot be
			// identified as a PDF is worth saying something about.
			if !attachment.IsMismatch(".pdf", "", tc.mime) {
				t.Errorf("IsMismatch(\".pdf\", \"\", %q) = false, want true", tc.mime)
			}
		})
	}
}

// And the answer must not depend on whether the library happens to know an
// extension for the format.
//
// A PE executable and an ELF executable named notes.txt are the same event. A
// rule that flags one and not the other is not a rule, it is an artefact of
// the library's lookup table.
func TestIsMismatch_TwoExecutablesUnderATextNameAgree(t *testing.T) {
	const pe = "application/vnd.microsoft.portable-executable" // library knows ".exe"
	const elf = "application/x-elf"                            // library knows no extension

	got := attachment.IsMismatch(".txt", ".exe", pe)
	want := attachment.IsMismatch(".txt", "", elf)
	if got != want {
		t.Errorf("PE under .txt = %v but ELF under .txt = %v; the same deception "+
			"must not get opposite answers because one format has a known extension",
			got, want)
	}
	if !got {
		t.Error("an executable named .txt is a contradiction and must be flagged")
	}
}
