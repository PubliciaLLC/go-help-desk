package attachment

import (
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

// textExt is the set of extensions a person uses when they mean "this is
// text": the ones whose legitimate contents carry no signature of any kind.
var textExt = map[string]bool{
	".txt": true,
	".log": true,
	".csv": true,
	".md":  true,
}

// isInertText reports whether a detected media type is text that displays
// rather than runs when somebody opens it.
//
// Answered by asking the detector, not by a rule of our own. The library
// arranges every type it knows into a tree, and a type whose ancestry passes
// through text/plain is one the library is willing to call text: text/html,
// text/vcard, application/json, application/x-ndjson, application/geo+json,
// image/svg+xml, application/xhtml+xml and application/x-subrip all do. A ZIP
// and everything packaged inside one do not — they descend from
// application/zip.
//
// Three earlier versions of this got it wrong in the same way each time, by
// writing a rule that was right for the cases in front of it and wrong for
// the family it was generalised to:
//
//   - A list of extensions. Measured, a container log is
//     application/x-ndjson, a config file is text/xml and an exported contact
//     is text/vcard, none of which were on it, so every one of them was
//     treated as a file lying about itself and stored as
//     suspicious-<crc32>.zip. A Kubernetes log arriving on a ticket renamed
//     and wrapped is the warning-on-ordinary-files failure this control
//     exists to avoid.
//   - A list of media types — text/* plus JSON and NDJSON. It flagged a
//     GeoJSON document under notes.txt, because the registry files that under
//     its own name.
//   - Anything ending +json or +xml, which those endings do mean. But a Visio
//     drawing is application/vnd.ms-visio.drawing.main+xml and is a ZIP
//     archive, so a ZIP under a .txt name came back with an affirmative "no
//     contradiction" while a plain ZIP under the identical name was flagged.
//
// The tree has no such gap, because it is the same source that produced the
// media type being judged. A rule written from the detector's own answers
// cannot disagree with the detector.
//
// text/html is in, and used to be excluded on the argument that HTML is the
// one textual type that runs when it is opened. That argument is false in the
// way that matters: what opens a file is chosen by its name, not by its
// content. A thing called notes.log opens in a text editor whatever bytes are
// inside it, and this application never renders an attachment at all — every
// download is an octet-stream blob with an attachment disposition. The
// exclusion protected nothing, and it flagged an ordinary help desk
// attachment: a captured HTTP response saved as a .log, which is the example
// DESIGN.md and #165 both use for "ordinary".
func isInertText(mediaType string) bool {
	m := mimetype.Lookup(mediaType)
	if m == nil {
		// A name the tree does not hold. Only reachable if this is called
		// with something Detect did not produce, since Detect's answers all
		// come from the tree. text/* is text by definition whatever the tree
		// knows, and anything else we cannot vouch for.
		return strings.HasPrefix(mediaType, "text/")
	}
	for ; m != nil; m = m.Parent() {
		if m.Is("text/plain") {
			return true
		}
	}
	return false
}

// IsTextExtension reports whether an extension is one a person uses when they
// mean "this is text".
//
// Exported because the upload handler needs the same answer this package
// decides mismatches with, and two copies of a four-entry list is how the two
// drift apart. The handler's use is the containment decision: a file named as
// text is never wrapped or refused for its content, because the name is what
// decides how it opens on the reader's machine.
func IsTextExtension(ext string) bool {
	return textExt[normaliseExt(ext)]
}

// IsMismatch reports whether a file's content contradicts the extension it was
// uploaded under. detectedExt and detectedMIME are what Detect returned;
// arguments are normalised here rather than by the caller.
//
// A mismatch is recorded and shown, never blocked. Legitimate ones exist, and
// because downloads are served as octet-stream with an attachment disposition
// and nothing is ever rendered, a mismatch is not a risk to this server. It is
// a deception risk for the person about to open the file, which is why they
// are told.
//
// Three rules relax the comparison, and no others:
//
//  1. Synonyms for one format collapse: .jpeg is .jpg. The only entry, and it
//     exists because the two spellings name a single format.
//  2. A text extension matches any content that is inert text. A .log holding
//     JSON, a .txt holding CSV, a .csv detected as plain text: all ordinary,
//     all things a help desk receives every day, and none of them anybody
//     lying about anything.
//  3. Content nothing recognised at all — an empty detected extension — under
//     a text extension is not a mismatch. The detector failing to place a file
//     is a limitation of the detector, not evidence of deception, and one NUL
//     byte in a log from a process that died mid-write is enough to trigger
//     it. Under a claimed binary format it stays a mismatch: a PDF that cannot
//     be identified as a PDF is worth a sentence, and saying nothing would
//     make an unidentifiable file look like a verified one.
//
// Everything else that differs is a mismatch — including a claimed .pdf whose
// content is plain text, because "it is only text" is not a blanket excuse,
// and including a gzip under a .log, because a text extension is not a blanket
// pass either.
//
// If a legitimate case still fires, widen one of those rules rather than
// softening the flag. A warning that fires on ordinary files is one staff
// learn to click past, which is worse than no warning at all.
func IsMismatch(claimedExt, detectedExt, detectedMIME string) bool {
	claimed := normaliseExt(claimedExt)
	detected := normaliseExt(detectedExt)

	// A file with no extension claims nothing, so there is nothing to confirm.
	if claimed == "" {
		return true
	}
	// Rule 3: unplaceable content under a text name is the detector's limit,
	// not a deception. Note what it tests — the media type, not the extension.
	//
	// application/octet-stream is what this detector says when it cannot place
	// a file at all. An empty extension is NOT the same statement — the
	// library returns none for eight media types, seven of which it recognised
	// perfectly well and simply has no filename extension for:
	// application/x-elf, x-ole-storage, x-executable, x-object, x-coredump,
	// tzif and zlib.
	//
	// Written against the extension, this rule said a Linux executable or an
	// OLE2 compound document — the container for macro-bearing legacy Office
	// files, MSI installers and .msg mail — matched a .txt name, while a
	// Windows executable under that same name was flagged. The same deception,
	// opposite answers, decided by whether the library happened to know a file
	// extension for the format.
	//
	// This is the fourth time a relaxation here has been written in the
	// detector's extension vocabulary when the fact lives in its MIME
	// vocabulary; rule 2 above was moved off an extension list for the same
	// reason. Extensions are for the claimed side, where they come from a
	// filename and are all anyone has.
	if detectedMIME == "application/octet-stream" {
		return !textExt[claimed]
	}
	if claimed == detected {
		return false
	}
	return !(textExt[claimed] && isInertText(detectedMIME))
}

// normaliseExt lowercases an extension, gives it the leading dot the rest of
// this package assumes, and collapses the one synonym. A pure function that
// cannot be called wrongly is worth four lines.
func normaliseExt(ext string) string {
	e := strings.ToLower(ext)
	if e == "" {
		return ""
	}
	if !strings.HasPrefix(e, ".") {
		e = "." + e
	}
	if e == ".jpeg" {
		return ".jpg"
	}
	return e
}
