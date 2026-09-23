package attachment_test

import (
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/attachment"
)

// Whether the content contradicts the name.
//
// The rule has exactly two exceptions and a catch-all, and the value of the
// whole control depends on the exceptions staying that small. Widen them and
// the flag stops meaning anything; drop one and the flag fires on ordinary
// files, which is worse than no flag because staff learn to click past it.
//
// Nothing here blocks an upload. A mismatch is recorded and shown, because
// after step 1 nothing renders and the risk is not to this server — it is that
// someone is about to open a file that is not what the help desk called it.
func TestIsMismatch(t *testing.T) {
	cases := []struct {
		name        string
		claimedExt  string
		detectedExt string
		want        bool
	}{
		// --- The ordinary case: content agrees with the name. ---
		{
			name:       "a PDF named .pdf",
			claimedExt: ".pdf", detectedExt: ".pdf",
			want: false,
		},
		{
			name:       "a PNG named .png",
			claimedExt: ".png", detectedExt: ".png",
			want: false,
		},
		{
			// The case Go's own sniffer gets wrong, calling it application/zip.
			// Every DOCX on the instance would be flagged.
			name:       "a DOCX named .docx",
			claimedExt: ".docx", detectedExt: ".docx",
			want: false,
		},

		// --- Rule 1: .jpeg and .jpg are one format spelled two ways. ---
		{
			name:       "a JPEG named .jpeg, detected as .jpg",
			claimedExt: ".jpeg", detectedExt: ".jpg",
			want: false,
		},
		{
			// The reverse direction. The detector does not currently produce
			// ".jpeg", but the rule is about two spellings of one format, not
			// about which side a particular detector puts them on.
			name:       "a JPEG named .jpg, detected as .jpeg",
			claimedExt: ".jpg", detectedExt: ".jpeg",
			want: false,
		},

		// --- Rule 2: content detected as plain text matches a text extension. ---
		{
			name:       "text named .txt",
			claimedExt: ".txt", detectedExt: ".txt",
			want: false,
		},
		{
			// The case the rule exists for. A log file has no signature, so
			// the detector can only ever call it .txt; flagging that would
			// mean flagging every log anyone attaches.
			name:       "a log file, which content cannot tell from .txt",
			claimedExt: ".log", detectedExt: ".txt",
			want: false,
		},
		{
			name:       "a CSV the detector read as plain text",
			claimedExt: ".csv", detectedExt: ".txt",
			want: false,
		},
		{
			name:       "markdown, which is plain text",
			claimedExt: ".md", detectedExt: ".txt",
			want: false,
		},
		{
			// The limit of rule 2, and the case that decides whether the rule
			// is useful. "Detected plain text" must not become a blanket
			// excuse: a PDF whose content is plain text is precisely the
			// deception this control is for.
			name:       "a .pdf whose content is plain text",
			claimedExt: ".pdf", detectedExt: ".txt",
			want: true,
		},

		// --- The catch-all: anything else that differs. ---
		{
			// The headline case from the issue.
			name:       "HTML named .pdf",
			claimedExt: ".pdf", detectedExt: ".html",
			want: true,
		},
		{
			// The documented gap this step closes. It stays a 201 — the file
			// is accepted and the fact is recorded.
			name:       "HTML named .txt",
			claimedExt: ".txt", detectedExt: ".html",
			want: true,
		},
		{
			// A legitimate mismatch: a captured HTML response saved as a log
			// is an ordinary help desk attachment. It is still flagged,
			// because the flag says what the file is, not that someone did
			// something wrong. Rule 2 does not reach it — the content was
			// identified as HTML, not as plain text.
			name:       "a .log holding a captured HTML response",
			claimedExt: ".log", detectedExt: ".html",
			want: true,
		},
		{
			name:       "HTML named .png",
			claimedExt: ".png", detectedExt: ".html",
			want: true,
		},
		{
			name:       "a PNG named .jpg",
			claimedExt: ".jpg", detectedExt: ".png",
			want: true,
		},
		{
			// A plain archive under an Office name. There is no "ZIP family"
			// exception: the normalisations are the two above and no others.
			name:       "a plain ZIP named .docx",
			claimedExt: ".docx", detectedExt: ".zip",
			want: true,
		},

		// --- Nothing recognisable is a mismatch, not a pass. ---
		{
			// Saying nothing here would make an unidentifiable file look
			// exactly like a verified one.
			name:       "content that could not be identified, named .pdf",
			claimedExt: ".pdf", detectedExt: "",
			want: true,
		},
		{
			// Including under a text extension, where "we could not confirm
			// this" is the honest answer rather than "close enough to text".
			name:       "content that could not be identified, named .txt",
			claimedExt: ".txt", detectedExt: "",
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := attachment.IsMismatch(tc.claimedExt, tc.detectedExt)
			if got != tc.want {
				t.Errorf("IsMismatch(%q, %q) = %v, want %v",
					tc.claimedExt, tc.detectedExt, got, tc.want)
			}
		})
	}
}

// Both arguments are normalised, not just one.
//
// The claimed extension comes from a filename and the detected one from a
// library, so the two sides arrive in different shapes and neither caller is
// asked to tidy up first. A normaliser applied to one argument only passes
// every case in the table above, because every entry there is already in the
// canonical form.
func TestIsMismatch_NormalisesBothArguments(t *testing.T) {
	spellings := []string{".pdf", "pdf", ".PDF", "PDF", ".Pdf"}

	for _, claimed := range spellings {
		for _, detected := range spellings {
			if attachment.IsMismatch(claimed, detected) {
				t.Errorf("IsMismatch(%q, %q) = true: these are the same extension",
					claimed, detected)
			}
		}
	}

	// And normalisation must not flatten a real difference into a match.
	for _, claimed := range spellings {
		for _, detected := range []string{".html", "html", ".HTML", "HTML"} {
			if !attachment.IsMismatch(claimed, detected) {
				t.Errorf("IsMismatch(%q, %q) = false: HTML is not a PDF",
					claimed, detected)
			}
		}
	}
}
