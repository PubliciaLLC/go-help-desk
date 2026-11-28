package attachment_test

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"hash/crc32"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/attachment"
)

// What quarantine has to deliver to the person on the other end.
//
// The archive is not a formality. An analyst who is handed one of these opens
// it with the published password in whatever tool they already use, and the
// bytes that come out have to be the sample — not a truncated copy, not a
// recompressed one, not the wrapper. If any of that is off, the hash they
// computed from our UI describes a file they do not have, and the whole
// feature is worse than refusing the upload would have been.
//
// So this asserts the property rather than the implementation: a standard ZIP
// reader finds one entry, under the uploaded name, encrypted, and the bytes
// recovered with the password "infected" are byte-identical to what went in.
// Nothing here knows which library wrapped it.
func TestQuarantine_WrapsTheSampleSoItCanBeRecoveredExactly(t *testing.T) {
	// The password is published — in the issue, in the UI and here. Stated
	// directly because every other assertion in this file uses the literal
	// rather than the constant: if the constant changed, the round-trip below
	// would still pass for an implementation that followed it, and an
	// analyst's tooling would stop working.
	require.Equal(t, "infected", attachment.QuarantinePassword,
		"the archive password is a published convention, not an implementation detail")

	cases := []struct {
		name     string
		filename string
		sample   []byte
	}{
		{
			// The EICAR test string: what a scanner actually reports on, and
			// what anyone reproducing this by hand will use.
			name:     "the eicar test file",
			filename: "sample.exe",
			sample:   []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`),
		},
		{
			// Binary, with NUL bytes and a run of 0xFF, because a wrapper that
			// went through a string somewhere passes on ASCII and mangles this.
			name:     "arbitrary binary content",
			filename: "invoice.exe",
			sample:   append([]byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00\x00\x00\xff\xff\x00\x00"), bytes.Repeat([]byte{0x00, 0xde, 0xad, 0xbe, 0xef}, 300)...),
		},
		{
			// Filenames come from whoever uploaded the file. A name the
			// archive cannot carry means the analyst unwraps something called
			// something else.
			name:     "a non-ASCII filename survives into the entry",
			filename: "reçu — facture.exe",
			sample:   []byte("payload"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Kept so the call can be checked for having modified its input in
			// place — a wrapper that encrypts the caller's slice would leave
			// the handler hashing ciphertext.
			original := bytes.Clone(tc.sample)

			archive, err := attachment.Wrap(tc.sample, tc.filename, attachment.QuarantinePassword)
			require.NoError(t, err)
			require.NotEmpty(t, archive)
			require.Equal(t, original, tc.sample, "Quarantine must not modify the bytes it was given")

			// The sample must not survive verbatim anywhere in the archive.
			// This is the one job the password actually does: an on-access
			// scanner reading our storage, or the downloader's, must not find
			// the signature and eat the file out from under the ticket.
			require.False(t, bytes.Contains(archive, original),
				"the sample appears unencrypted in the archive; an on-access scanner will remove it")

			zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
			require.NoError(t, err, "a standard ZIP reader must be able to open it")
			require.Len(t, zr.File, 1, "one sample in, one entry out")

			f := zr.File[0]
			// The uploaded name, not the stored one. ".zip" is appended to
			// what the browser saves, so unwrapping has to produce the file
			// the ticket is about rather than "sample.exe.zip".
			require.Equal(t, tc.filename, f.Name)
			require.NotZero(t, f.Flags&0x1,
				"the entry is not flagged encrypted, so it is directly double-clickable")

			got := recoverZipCryptoEntry(t, f, "infected")
			require.Equal(t, original, got, "the entry must be byte-identical to the sample")
			require.Equal(t, crc32.ChecksumIEEE(original), f.CRC32,
				"the archive's own checksum must describe the sample")
		})
	}

	// Deliberately not asserted: that a wrong password fails to open. ZipCrypto's
	// check value is a single byte, so a wrong password opens roughly one time in
	// 256 and then yields garbage. That is fine here — the password is published
	// and protects nothing — but it means "wrong password is rejected" is not a
	// property this format has, and a test claiming it would be flaky.
}

// recoverZipCryptoEntry decrypts and decompresses one entry, given the
// password.
//
// Written out here rather than taken from a library on purpose. The
// implementation under test uses some ZIP library; if this used the same one,
// the two would agree about a shared bug and prove nothing. The algorithm
// below is PKWARE traditional encryption ("ZipCrypto") read off the APPNOTE
// specification, and it has been checked against an archive produced by
// github.com/yeka/zip.
//
// AES-encrypted ZIP is not handled, and that is the assertion: the issue
// rejects AES because Windows Explorer and macOS Archive Utility cannot open
// it, which is the friction quarantine exists to remove.
func recoverZipCryptoEntry(t *testing.T, f *zip.File, password string) []byte {
	t.Helper()

	rc, err := f.OpenRaw()
	require.NoError(t, err, "reading the entry's stored bytes")
	raw, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Greater(t, len(raw), 12, "an encrypted entry carries a 12-byte header")

	k := newZipCryptoKeys(password)
	plain := make([]byte, len(raw))
	for i, c := range raw {
		plain[i] = k.decryptByte(c)
	}
	// The first 12 bytes are the encryption header, not content.
	body := plain[12:]

	switch f.Method {
	case zip.Store:
		return body
	case zip.Deflate:
		out, err := io.ReadAll(flate.NewReader(bytes.NewReader(body)))
		require.NoError(t, err,
			"the entry did not decompress with the password %q; is it AES-encrypted?", password)
		return out
	default:
		t.Fatalf("entry uses compression method %d, which no ordinary ZIP tool reads", f.Method)
		return nil
	}
}

// zipCryptoKeys is the three-word key state from APPNOTE.TXT section 6.1.
type zipCryptoKeys struct{ k0, k1, k2 uint32 }

var zipCryptoCRCTable = crc32.MakeTable(crc32.IEEE)

func newZipCryptoKeys(password string) *zipCryptoKeys {
	k := &zipCryptoKeys{0x12345678, 0x23456789, 0x34567890}
	for i := 0; i < len(password); i++ {
		k.update(password[i])
	}
	return k
}

func (k *zipCryptoKeys) update(b byte) {
	k.k0 = zipCryptoCRCTable[(k.k0^uint32(b))&0xff] ^ (k.k0 >> 8)
	k.k1 += k.k0 & 0xff
	k.k1 = k.k1*134775813 + 1
	k.k2 = zipCryptoCRCTable[(k.k2^(k.k1>>24))&0xff] ^ (k.k2 >> 8)
}

func (k *zipCryptoKeys) decryptByte(c byte) byte {
	t := uint16(k.k2) | 2
	p := c ^ byte((t*(t^1))>>8)
	k.update(p)
	return p
}
