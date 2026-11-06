package emailtemplate_test

import (
	"context"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"
)

// The domain package is only the shape of what is stored. The point of the
// split is that sending holds a Reader and nothing that can write: a Store is
// usable as a Reader, never the other way round.
var _ emailtemplate.Reader = emailtemplate.Store(nil)

// recorder is the smallest Store, to show the contract Apply documents: the
// entry callback sees the row as it was, nil if there was none, and may decline
// to write an entry by returning nil.
type recorder struct {
	rows map[string]emailtemplate.Override
	log  []audit.Entry
}

func (r *recorder) Overrides(_ context.Context, names ...string) (map[string]emailtemplate.Override, error) {
	out := map[string]emailtemplate.Override{}
	for _, n := range names {
		if o, ok := r.rows[n]; ok {
			out[n] = o
		}
	}
	return out, nil
}

func (r *recorder) List(context.Context) ([]emailtemplate.Override, error) { return nil, nil }

func (r *recorder) Apply(_ context.Context, name string, next *emailtemplate.Override, entry func(*emailtemplate.Override) *audit.Entry) error {
	var prev *emailtemplate.Override
	if o, ok := r.rows[name]; ok {
		prev = &o
	}
	if e := entry(prev); e != nil {
		r.log = append(r.log, *e)
	}
	if next == nil {
		delete(r.rows, name)
	} else {
		r.rows[name] = *next
	}
	return nil
}

func (r *recorder) Update(ctx context.Context, name string, decide func(prev *emailtemplate.Override) (*emailtemplate.Override, *audit.Entry, error)) error {
	var prev *emailtemplate.Override
	if o, ok := r.rows[name]; ok {
		prev = &o
	}
	next, e, err := decide(prev)
	if err != nil {
		return err
	}
	if e != nil {
		r.log = append(r.log, *e)
	}
	if next == nil {
		delete(r.rows, name)
	} else {
		r.rows[name] = *next
	}
	return nil
}

func TestApplyContract_EntryIsToldWhatWasThereAndMayDeclineToWrite(t *testing.T) {
	var s emailtemplate.Store = &recorder{rows: map[string]emailtemplate.Override{}}
	ctx := context.Background()

	var seen []*emailtemplate.Override
	note := func(prev *emailtemplate.Override) *audit.Entry { seen = append(seen, prev); return nil }
	_ = s.Apply(ctx, "ticket_created", &emailtemplate.Override{Name: "ticket_created", Body: "b"}, note)
	_ = s.Apply(ctx, "ticket_created", nil, note)

	if len(seen) != 2 || seen[0] != nil || seen[1] == nil || seen[1].Body != "b" {
		t.Fatalf("the entry was not told what was there: %+v", seen)
	}
	got, _ := s.Overrides(ctx, "ticket_created")
	if len(got) != 0 {
		t.Fatalf("a revert left the row: %+v", got)
	}
}
