package ticket

// ReplySearchMeta is what a listing could not do about reply text, for a
// request that asked for reply search. The zero value says nothing, and a
// caller that gets it sends no signal at all: that is what a listing that did
// not ask for reply search always returns (the store fills it only when it did).
type ReplySearchMeta struct {
	// Searched is nil when the store has no verdict. A pointer to false means
	// replies were not fully searched (the time cap was hit, or the index is not
	// ready), so the page falls back to ticket text and says so. A pointer to
	// true means they were.
	Searched *bool
	// IndexBuilding is true while the reply index is not ready to be used.
	IndexBuilding bool
}

// ListResult is a page of tickets and what the query had to say about how it
// got them. GET /tickets answers with a bare array, so Replies travels in
// response headers.
type ListResult struct {
	Tickets []Ticket
	Replies ReplySearchMeta
}
