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

// RepliesStatus says what became of a request to search replies. It is empty
// unless replies were asked for (Filter.SearchReplies with searchable words).
type RepliesStatus string

const (
	// RepliesNotRequested: replies were not asked for, so there is nothing to say.
	RepliesNotRequested RepliesStatus = ""
	// RepliesSearched: the reply arm ran to the end.
	RepliesSearched RepliesStatus = "searched"
	// RepliesPreparing: the server is still filling and indexing the replies
	// after an upgrade (internal/database/replyindex). Replies were skipped;
	// the page says "Reply search is still being prepared."
	RepliesPreparing RepliesStatus = "preparing"
	// RepliesTimedOut: the reply arm took longer than the limit and was dropped.
	// The tickets are those matching on subject, description and number. The page
	// says the replies were not fully searched.
	RepliesTimedOut RepliesStatus = "timed_out"
)

// ListResult is a page of tickets and what became of the reply search that
// helped choose it. A caller that wants the flag reads RepliesSearched.
type ListResult struct {
	Tickets []Ticket
	Replies RepliesStatus
}

// RepliesSearched is the `replies_searched` flag of a response: false only when
// replies were asked for and not (fully) searched. When they were not asked for
// there is nothing to flag, and it is true.
func (r ListResult) RepliesSearched() bool {
	return r.Replies != RepliesPreparing && r.Replies != RepliesTimedOut
}

// ReplyMatchesApply says whether a caller should read ReplyMatches for this
// page: only when the reply arm ran. A page chosen without it must not show
// reply excerpts as if replies had been searched.
func (r ListResult) ReplyMatchesApply() bool { return r.Replies == RepliesSearched }
