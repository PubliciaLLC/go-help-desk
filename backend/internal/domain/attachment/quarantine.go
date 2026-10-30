package attachment

// QuarantinePassword is the password of the archive an infected upload is
// wrapped in. It is published here, in the UI and in the issue, and it
// protects nothing — that is deliberate.
//
// Its only two jobs are that the stored bytes are not directly double-clickable
// and that an on-access scanner, on our storage or the downloader's, does not
// eat them. `infected` is the long-standing convention for moving samples, so
// an analyst's tooling already knows what to do with it.
const QuarantinePassword = "infected"

// The encryption Wrap applies with this password is ZipCrypto rather than AES:
// weak, which is fine when the password is published, and readable by Windows
// Explorer and macOS Archive Utility, which AES-256 ZIP is not. Removing that
// friction is the whole point — nobody should read it as confidentiality.
//
// This is not the auto-ZIPping argued against for ordinary uploads. That was
// about wrapping files nobody had scanned, chosen by extension, and destroying
// the hash an analyst needs. Here the file has been scanned and identified,
// the hash is taken from the raw bytes beforehand, and what is wrapped is
// precisely what is known to be dangerous. That is quarantine, not evasion.
