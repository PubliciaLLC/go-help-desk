package attachment

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/rand"
	"fmt"
	"hash/crc32"
	"time"
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
func Wrap(data []byte, filename, password string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	if password == "" {
		w, err := zw.Create(filename)
		if err != nil {
			return nil, fmt.Errorf("creating the archive entry: %w", err)
		}
		if _, err := w.Write(data); err != nil {
			return nil, fmt.Errorf("writing the archive entry: %w", err)
		}
	} else if err := writeZipCryptoEntry(zw, data, filename, password); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("closing the archive: %w", err)
	}
	return buf.Bytes(), nil
}

// writeZipCryptoEntry adds one PKWARE-encrypted ("ZipCrypto") entry.
//
// Written here rather than taken from github.com/yeka/zip, which is the
// obvious candidate and is a whole fork of archive/zip — ~4,000 lines carried
// for one entry type, and a fork that has to be kept level with the standard
// library's own fixes by someone who is not us. The standard library already
// does every part of this except the cipher: CreateRaw takes a body the caller
// has compressed and encrypted, which is exactly what an encrypted entry is,
// because the encryption flag and the compressed size have to be in the local
// header before the body is written. What is left is the forty lines below.
//
// Store or Deflate are both legal here. Deflate matches what the unencrypted
// path produces, so the two tiers differ in the cipher and nothing else.
func writeZipCryptoEntry(zw *zip.Writer, data []byte, filename, password string) error {
	var deflated bytes.Buffer
	fw, err := flate.NewWriter(&deflated, flate.DefaultCompression)
	if err != nil {
		return fmt.Errorf("compressing the archive entry: %w", err)
	}
	if _, err := fw.Write(data); err != nil {
		return fmt.Errorf("compressing the archive entry: %w", err)
	}
	if err := fw.Close(); err != nil {
		return fmt.Errorf("compressing the archive entry: %w", err)
	}

	// Of the sample, not of the compressed or encrypted form: it is what a
	// recipient's ZIP tool checks the unwrapped file against.
	sum := crc32.ChecksumIEEE(data)

	body, err := zipCryptoEncrypt(deflated.Bytes(), sum, password)
	if err != nil {
		return err
	}

	fh := &zip.FileHeader{
		Name:   filename,
		Method: zip.Deflate,
		// Bit 0 is the one that makes this an encrypted entry, and it is what
		// stops a file manager offering to open it with a double click.
		Flags:              0x1,
		CreatorVersion:     zipVersion20,
		ReaderVersion:      zipVersion20,
		CRC32:              sum,
		CompressedSize64:   uint64(len(body)),
		UncompressedSize64: uint64(len(data)),
	}

	// Bit 11 says the name is UTF-8. archive/zip sets it in CreateHeader; on
	// the raw path every header field is the caller's, and without it a name
	// like "reçu.exe" is read as CP437 by anything that follows the spec.
	for i := 0; i < len(filename); i++ {
		if filename[i] >= 0x80 {
			fh.Flags |= 0x800
			break
		}
	}

	// Likewise the timestamp, which CreateRaw does not derive from Modified.
	// A zero date is not a missing date in this format — it decodes to an
	// impossible day of month zero, which some tools show and some reject.
	now := time.Now().UTC()
	fh.ModifiedDate = uint16(now.Day() | int(now.Month())<<5 | (now.Year()-1980)<<9)
	fh.ModifiedTime = uint16(now.Second()/2 | now.Minute()<<5 | now.Hour()<<11)

	w, err := zw.CreateRaw(fh)
	if err != nil {
		return fmt.Errorf("creating the archive entry: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("writing the archive entry: %w", err)
	}
	return nil
}

// zipVersion20 is "requires a ZIP 2.0 reader", which is what Deflate and
// PKWARE encryption need. CreateHeader fills this in; CreateRaw does not.
const zipVersion20 = 20

// zipCryptoEncrypt returns the entry body: a 12-byte encryption header
// followed by the compressed data, the whole thing under the stream cipher
// from APPNOTE.TXT section 6.1.
func zipCryptoEncrypt(compressed []byte, crc uint32, password string) ([]byte, error) {
	head := make([]byte, 12)
	if _, err := rand.Read(head[:11]); err != nil {
		return nil, fmt.Errorf("generating the encryption header: %w", err)
	}
	// The last header byte is the high byte of the CRC32, and it is the
	// entirety of the format's password check: one byte, so a wrong password
	// opens roughly one time in 256 and then yields garbage. That is fine
	// here — the password is published and protects nothing — but it is why
	// none of this should be read as confidentiality.
	head[11] = byte(crc >> 24)

	k := newZipCryptoKeys(password)
	out := make([]byte, 0, len(head)+len(compressed))
	for _, p := range head {
		out = append(out, k.encryptByte(p))
	}
	for _, p := range compressed {
		out = append(out, k.encryptByte(p))
	}
	return out, nil
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

func (k *zipCryptoKeys) update(plain byte) {
	k.k0 = zipCryptoCRCTable[(k.k0^uint32(plain))&0xff] ^ (k.k0 >> 8)
	k.k1 += k.k0 & 0xff
	k.k1 = k.k1*134775813 + 1
	k.k2 = zipCryptoCRCTable[(k.k2^(k.k1>>24))&0xff] ^ (k.k2 >> 8)
}

// encryptByte enciphers one byte and advances the key state. The state is
// advanced with the *plaintext*, which is what makes the decrypting side able
// to follow along from the ciphertext it recovers.
func (k *zipCryptoKeys) encryptByte(plain byte) byte {
	t := uint16(k.k2) | 2
	c := plain ^ byte((t*(t^1))>>8)
	k.update(plain)
	return c
}
