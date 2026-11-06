package attachment_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/attachment"
)

// The hash is the value an analyst types into VirusTotal and the value a
// chain-of-custody record has to state, so it is checked against published
// vectors rather than against a second call to crypto/sha256 in the test. A
// test that hashes the input itself and compares proves the two calls agree;
// it does not prove either is SHA-256, and it would sail past a digest that
// was truncated, upper-cased or base64.
//
// Vectors produced with `shasum -a 256`, independently of any Go code.
func TestSHA256(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{
			// An empty upload still has an identity.
			name: "empty input",
			data: []byte{},
			want: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "abc",
			data: []byte("abc"),
			want: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		},
		{
			name: "hello world",
			data: []byte("hello world"),
			want: "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
		},
		{
			// Every byte value including NUL and everything outside ASCII.
			// Attachments are binary; an implementation that went through a
			// string conversion somewhere would show up here.
			name: "every byte value, 0x00 to 0xff",
			data: func() []byte {
				b := make([]byte, 256)
				for i := range b {
					b[i] = byte(i)
				}
				return b
			}(),
			want: "40aff2e9d2d8922e47afd4648e6967497158785fbd1da870e7110266bf944880",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := attachment.SHA256(tc.data)
			if got != tc.want {
				t.Errorf("SHA256() = %q, want %q", got, tc.want)
			}
			// Spelled out separately so a wrong-shaped answer says which way
			// it is wrong rather than only that it differs.
			if len(got) != 64 {
				t.Errorf("SHA256() is %d characters, want 64 hex characters", len(got))
			}
			if got != strings.ToLower(got) {
				t.Errorf("SHA256() = %q, want lowercase hex", got)
			}
		})
	}
}

// The whole file, not a prefix.
//
// Detect deliberately reads only the first few kilobytes, which is what keeps
// a 25 MB upload cheap. The hash cannot take that shortcut: two samples that
// differ only late in the file are different samples, and a prefix hash would
// call them the same one. That is the difference between a chain-of-custody
// record and a guess.
func TestSHA256_ReadsPastAnyPrefix(t *testing.T) {
	const size = 1 << 20 // comfortably past any sniffing window

	a := bytes.Repeat([]byte{'A'}, size)
	b := bytes.Repeat([]byte{'A'}, size)
	b[size-1] = 'B' // the only difference, in the last byte

	hashA := attachment.SHA256(a)
	hashB := attachment.SHA256(b)

	if hashA == hashB {
		t.Fatalf("two files differing only in their last byte hashed the same (%q); "+
			"the hash must cover the whole file, not a prefix", hashA)
	}
}
