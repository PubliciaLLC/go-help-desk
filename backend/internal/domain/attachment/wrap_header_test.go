package attachment_test

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/attachment"
)

// The archive has to be readable by whatever the person who downloads it uses,
// which is not Go.
//
// These exist because the property tests did not catch a whole implementation
// being replaced. They checked the encrypted bit, the CRC, the name as Go's
// own reader returns it, and a round-trip — all of which passed when the
// library swap silently dropped the UTF-8 name flag and the timestamp. A test
// that only proves Go can read Go's output is not testing the thing that
// matters.
func TestWrap_HeaderFieldsAReaderOutsideGoNeeds(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		wantUTF8 bool
	}{
		{
			// Bit 11 declares the name is UTF-8. Without it the name has no
			// declared encoding at all and a spec-following tool reads it as
			// CP437, so an accented or CJK name unpacks as mojibake.
			name:     "a name that needs the UTF-8 flag",
			filename: "reçu — 請求書.exe",
			wantUTF8: true,
		},
		{
			// Not set when it is not needed: a plain ASCII name is identical
			// in both encodings, and setting it anyway would be noise.
			name:     "a name that does not",
			filename: "sample.exe",
			wantUTF8: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, password := range []string{"", attachment.QuarantinePassword} {
				archive, err := attachment.Wrap([]byte("payload"), tc.filename, password)
				require.NoError(t, err)

				flags := localHeaderFlags(t, archive)
				require.Equal(t, tc.wantUTF8, flags&0x800 != 0,
					"bit 11 (UTF-8 name) with password %q: flags were 0x%04x", password, flags)
				require.Equal(t, password != "", flags&0x1 != 0,
					"bit 0 (encrypted) must follow whether a password was given")

				// A zero MS-DOS date is not "no date" in this format: it
				// decodes to day-of-month zero, which some tools display and
				// others refuse.
				zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
				require.NoError(t, err)
				require.Len(t, zr.File, 1)
				require.False(t, zr.File[0].Modified.IsZero(),
					"an entry with no timestamp decodes to an impossible date")
				require.WithinDuration(t, time.Now().UTC(), zr.File[0].Modified.UTC(), time.Hour)
			}
		})
	}
}

// The name inside the archive decides where the reader's machine puts the
// file, so it must not be readable as a path.
//
// Go's multipart reader runs filepath.Base over an uploaded name, which on
// Linux strips a forward-slash path and leaves backslashes untouched — so a
// Windows-style traversal arrived intact and went into the archive verbatim.
// Modern extractors sanitise it; an archive we built is not where anyone
// should discover which extractor the reader has.
func TestWrap_TheEntryNameCannotBeReadAsAPath(t *testing.T) {
	cases := []struct{ name, filename, want string }{
		{"a windows traversal", `..\..\..\Users\Public\evil.exe`, "......UsersPublicevil.exe"},
		{"a unix traversal", "../../evil.exe", "....evil.exe"},
		{"an absolute path", "/etc/cron.d/evil", "etccron.devil"},
		{"a name that is only separators", `\\/\\`, "attachment"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			archive, err := attachment.Wrap([]byte("payload"), tc.filename, attachment.QuarantinePassword)
			require.NoError(t, err)

			zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
			require.NoError(t, err)
			require.Len(t, zr.File, 1)
			require.Equal(t, tc.want, zr.File[0].Name)
			require.NotContains(t, zr.File[0].Name, "/")
			require.NotContains(t, zr.File[0].Name, `\`)
		})
	}
}

// localHeaderFlags reads the general purpose bit flag straight out of the
// first local file header, rather than asking a library what it thinks.
func localHeaderFlags(t *testing.T, archive []byte) uint16 {
	t.Helper()
	require.Greater(t, len(archive), 8)
	require.Equal(t, []byte("PK\x03\x04"), archive[:4], "not a local file header")
	return binary.LittleEndian.Uint16(archive[6:8])
}
