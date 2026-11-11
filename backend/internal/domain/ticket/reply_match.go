package ticket

import (
	"time"

	"github.com/google/uuid"
)

// MaxSnippetRunes is the longest ReplyMatch.Snippet a listing carries.
const MaxSnippetRunes = 300

// Markers that wrap each matched word in a ReplyMatch.Snippet. Control
// characters rather than HTML tags: the page splits on them and renders text
// nodes inside <mark>, never innerHTML, so a reply that contains the characters
// itself can at worst move a highlight. It cannot inject markup.
const (
	SnippetStart = "\x02"
	SnippetStop  = "\x03"
)

// ReplyMatch says where a search found a ticket's text in its conversation:
// the newest reply the viewer may read that holds every word of the search.
// A ticket that did not match through a reply has none.
//
// It carries no author. A search must not be a way to learn who wrote what, and
// the audit mask on requester names has to hold here too.
//
// Which replies were read is decided by Filter.IncludeInternal and nothing
// else. For a requester Internal is therefore always false: the query never
// reads an internal note for them.
type ReplyMatch struct {
	ReplyID   uuid.UUID `json:"reply_id"`
	Internal  bool      `json:"internal"`
	CreatedAt time.Time `json:"created_at"`
	Snippet   string    `json:"snippet"`
}
