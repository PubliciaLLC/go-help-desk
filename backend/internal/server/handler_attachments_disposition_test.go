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
// because the upload path is not the only guard and most of these strings
// never get that far: Go's multipart reader runs filepath.Base over the
// filename, which removes a forward-slash path on Linux, and its MIME header
// parser refuses a request outright if the filename holds a control
// character. A backslash path is the one that does survive an upload today —
// filepath.Base leaves it alone on Linux, and a Windows browser saving the
// file would read it as a path. The rest are checked here so this function is
// correct on its own terms, and stays correct if it is ever called with a
// name from somewhere other than a multipart upload.
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
			// The name a browser gives a second copy of a download. It broke
			// the header when the extended form was percent-encoded as a URL
			// path, because "(" and ")" are legal there and not here.
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
		})
	}
}
