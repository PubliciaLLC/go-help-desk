package server

import (
	"mime"
	"strings"
	"testing"
)

// The filename in a download header is chosen by whoever uploaded the file.
// It decides what the browser writes to disk and it is interpolated into a
// header, so it is attacker-controlled input on both counts.
//
// This tests contentDisposition directly rather than through an upload,
// because the upload path is not the only guard and several of these strings
// never get that far. Go's multipart reader runs filepath.Base over the
// filename, so a forward-slash path is already gone on Linux, and its MIME
// header parser refuses the whole request if the filename holds a control
// character — with one exception: a tab gets through, which the end-to-end
// test covers. A backslash path also survives, because filepath.Base leaves
// backslashes alone on Linux and a Windows browser then reads the name as a
// path.
//
// The rest are here so this function is correct on its own terms, and stays
// correct if it is ever handed a name from somewhere other than a multipart
// upload.
func TestContentDisposition(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		want     string // the filename a current browser should use
	}{
		{
			name:     "ordinary name",
			filename: "report.pdf",
			want:     "report.pdf",
		},
		{
			name:     "non-ASCII survives intact",
			filename: "café — rapport №5.txt",
			want:     "café — rapport №5.txt",
		},
		{
			name:     "a quote cannot close the quoted string",
			filename: `a"b.txt`,
			want:     `a"b.txt`,
		},
		{
			// A saved email, named after who sent it. This is the case that
			// broke the header when the extended form was percent-encoded as
			// a URL path: url.PathEscape lets ":", "=" and "@" through, and
			// RFC 5987 allows none of the three.
			name:     "an address in the name",
			filename: "user@example.com.eml",
			want:     "user@example.com.eml",
		},
		{
			name:     "an equals sign in the name",
			filename: "invoice=2026-09.pdf",
			want:     "invoice=2026-09.pdf",
		},
		{
			name:     "a colon in the name",
			filename: "meeting: notes.txt",
			want:     "meeting: notes.txt",
		},
		{
			// The three characters Go's parser lets through bare: "%", "*"
			// and "'". A loose encoder that emits them is not caught by
			// asking whether the header parses, because it does.
			name:     "the encoding's own syntax in the name",
			filename: "it's a 100% *draft*.txt",
			want:     "it's a 100% *draft*.txt",
		},
		{
			name:     "a name a browser itself would pick",
			filename: "report(1).pdf",
			want:     "report(1).pdf",
		},
		{
			name:     "a backslash path is flattened",
			filename: `..\..\windows\system32\evil.txt`,
			want:     "....windowssystem32evil.txt",
		},
		{
			name:     "a forward-slash path is flattened",
			filename: "../../etc/passwd.txt",
			want:     "....etcpasswd.txt",
		},
		{
			name:     "CRLF cannot start a header of its own",
			filename: "a.txt\r\nX-Injected: yes",
			want:     "a.txtX-Injected: yes",
		},
		{
			name:     "a name that is entirely removed still has one",
			filename: "///",
			want:     "attachment",
		},
		{
			name:     "an empty name still has one",
			filename: "",
			want:     "attachment",
		},
		{
			// Nothing limits the length of an uploaded filename, and the name
			// goes into the header twice — once percent-encoded, so three
			// bytes per character. Left alone, one upload produces a response
			// header no browser or proxy will accept, and the download fails
			// for everybody.
			name:     "an absurd name is cut down, keeping the extension",
			filename: strings.Repeat("é", 5000) + ".pdf",
			want:     strings.Repeat("é", 98) + ".pdf",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := contentDisposition(tc.filename)

			// Nothing that ends the header or starts another one.
			for _, r := range got {
				if r < 0x20 || r == 0x7f {
					t.Fatalf("control character %q in header %q", r, got)
				}
			}

			// Short enough that a browser and anything in front of it will
			// accept the response. Both forms plus the percent-encoding, so
			// the header is several times the name.
			if len(got) > 2000 {
				t.Fatalf("header is %d bytes, too long to be delivered", len(got))
			}

			typ, params, err := mime.ParseMediaType(got)
			if err != nil {
				t.Fatalf("header does not parse: %q: %v", got, err)
			}
			if typ != "attachment" {
				t.Fatalf("disposition is %q, want attachment: %q", typ, got)
			}

			// ParseMediaType prefers filename*= and returns what it decodes
			// under the plain "filename" key, so this is the name a current
			// browser uses.
			if params["filename"] != tc.want {
				t.Fatalf("filename = %q, want %q (header %q)",
					params["filename"], tc.want, got)
			}
			if strings.ContainsAny(params["filename"], `/\`) {
				t.Fatalf("path separator survived into %q", params["filename"])
			}

			// Both forms have to be present: filename*= is ignored by old
			// clients, and filename= is what they fall back to.
			if !strings.Contains(got, "filename=") || !strings.Contains(got, "filename*=UTF-8''") {
				t.Fatalf("header is missing one of the two RFC 6266 forms: %q", got)
			}

			// And the extended form has to hold only what RFC 5987 allows.
			//
			// Checking that the header parses is not enough to catch a
			// loose encoder. Measured, because the first two guesses at
			// which characters those are were both wrong: Go's parser
			// refuses a bare "(", ")", ":", "=" and "@", and accepts a bare
			// "%", "*" and "'" — the three that are the encoding's own
			// syntax. A bare "%" is the nastiest, because the header still
			// parses and the client quietly falls back to the ASCII name.
			// So this reads the grammar off the RFC rather than off a
			// parser, and the cases below include all three.
			_, star, ok := strings.Cut(got, "filename*=UTF-8''")
			if !ok {
				t.Fatalf("no extended form in %q", got)
			}
			for i := 0; i < len(star); i++ {
				if star[i] == '%' {
					if i+2 >= len(star) || !isHex(star[i+1]) || !isHex(star[i+2]) {
						t.Fatalf("truncated percent escape at %d in %q", i, star)
					}
					i += 2
					continue
				}
				if !isAttrChar(star[i]) {
					t.Fatalf("%q is not an RFC 5987 attr-char, in %q", star[i], star)
				}
			}
		})
	}
}

// isAttrChar is RFC 5987 attr-char, written out from the RFC rather than
// shared with the encoder under test: a table both sides read proves nothing.
func isAttrChar(c byte) bool {
	if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'F' || c >= 'a' && c <= 'f'
}
