package attachment

import "strings"

// textExt is the set of extensions whose legitimate files are plain text
// carrying no signature of any kind, which is why no detector can tell them
// apart from each other.
//
// Deliberately small, and deliberately a code change rather than
// configuration: an operator who allows .yaml gets mismatch flags until
// someone decides .yaml belongs here, and that is the right direction to fail.
// The flag then says "we could not confirm this is what it claims", which is
// true.
var textExt = map[string]bool{
	".txt": true,
	".log": true,
	".csv": true,
	".md":  true,
}

// IsMismatch reports whether a file's content contradicts the extension it was
// uploaded under. detectedExt is what Detect returned; both arguments are
// normalised here rather than by the caller.
//
// A mismatch is recorded and shown, never blocked. Legitimate ones exist — a
// .log holding a captured HTML response is an ordinary help desk attachment —
// and because downloads are served as octet-stream with an attachment
// disposition and nothing is ever rendered, a mismatch is not a risk to this
// server. It is a deception risk for the person about to open the file, which
// is why they are told.
//
// Two normalisations apply before the comparison, and no others:
//
//  1. Synonyms for one format collapse: .jpeg is .jpg. This is the only entry,
//     and it exists because the two spellings name a single format.
//  2. Two text extensions match each other. A .csv detected as .txt is the
//     case the rule exists for — a log or a CSV has no signature, so a
//     detector can only ever guess between them — and it runs both ways,
//     because a comma-separated file named .txt detects as .csv and flagging
//     that would be a warning on an entirely ordinary file.
//
// Everything else that differs is a mismatch, including a claimed .pdf whose
// content is plain text: "detected plain text" is not a blanket excuse, it is
// precisely the deception this control is for. So is content nothing
// recognised at all, which is reported as an empty detected extension — saying
// nothing there would make an unidentifiable file look like a verified one.
//
// If a legitimate case turns out to fire, the fix is to widen one of those two
// rules — not to soften the flag. A warning that fires on ordinary files is
// one staff learn to click past, which is worse than no warning at all.
func IsMismatch(claimedExt, detectedExt string) bool {
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
	return !(textExt[claimed] && textExt[detected])
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
