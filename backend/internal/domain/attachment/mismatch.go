package attachment

import "strings"

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
// Decided from the media type and not from a list of extensions, and that is
// the point of this function existing at all. The list version was wrong: the
// settled design claimed the detector "leaves exactly two false positives,
// .js and .log", which was never measured and is not true. Measured, a
// container log is application/x-ndjson, a config file is text/xml, an
// exported contact is text/vcard — none of which were in the text set, so
// every one of them was treated as a file lying about itself and stored as
// suspicious-<crc32>.zip. A Kubernetes log arriving on a ticket renamed and
// wrapped is exactly the warning-on-ordinary-files failure this control
// exists to avoid.
//
// A media type rule does not drift the way a list does. Anything text/* is
// text by definition, and JSON is text that the IANA registry happens to file
// under application/*.
//
// text/html is the deliberate exception: it is the one textual type that runs
// when opened, because a browser is what opens it. HTML wearing a .txt is
// still worth telling someone about.
func isInertText(mediaType string) bool {
	switch mediaType {
	case "text/html":
		return false
	case "application/json", "application/x-ndjson":
		return true
	}
	return strings.HasPrefix(mediaType, "text/")
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
// Two rules relax the comparison, and no others:
//
//  1. Synonyms for one format collapse: .jpeg is .jpg. The only entry, and it
//     exists because the two spellings name a single format.
//  2. A text extension matches any content that is inert text. A .log holding
//     JSON, a .txt holding CSV, a .csv detected as plain text: all ordinary,
//     all things a help desk receives every day, and none of them anybody
//     lying about anything.
//
// Everything else that differs is a mismatch — including a claimed .pdf whose
// content is plain text, because "it is only text" is not a blanket excuse,
// and including content nothing recognised at all, which arrives as an empty
// detected extension. Saying nothing there would make an unidentifiable file
// look like a verified one.
//
// If a legitimate case still fires, widen one of those two rules rather than
// softening the flag. A warning that fires on ordinary files is one staff
// learn to click past, which is worse than no warning at all.
func IsMismatch(claimedExt, detectedExt, detectedMIME string) bool {
	claimed := normaliseExt(claimedExt)
	detected := normaliseExt(detectedExt)

	// An empty extension on either side is never a match: unidentified content
	// and an unnamed format are both "we could not confirm this".
	if claimed == "" || detected == "" {
		return true
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
