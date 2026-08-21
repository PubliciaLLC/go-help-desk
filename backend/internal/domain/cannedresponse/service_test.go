package cannedresponse_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/cannedresponse"
	"github.com/stretchr/testify/require"
)

// fakeStore is an in-memory implementation of cannedresponse.Store.
type fakeStore struct {
	items map[uuid.UUID]cannedresponse.CannedResponse
}

func newFakeStore() *fakeStore {
	return &fakeStore{items: make(map[uuid.UUID]cannedresponse.CannedResponse)}
}

func (f *fakeStore) Create(_ context.Context, cr cannedresponse.CannedResponse) error {
	f.items[cr.ID] = cr
	return nil
}

func (f *fakeStore) Get(_ context.Context, id uuid.UUID) (cannedresponse.CannedResponse, error) {
	cr, ok := f.items[id]
	if !ok {
		return cannedresponse.CannedResponse{}, errors.New("canned response not found")
	}
	return cr, nil
}

func (f *fakeStore) Update(_ context.Context, cr cannedresponse.CannedResponse) error {
	if _, ok := f.items[cr.ID]; !ok {
		return errors.New("canned response not found")
	}
	f.items[cr.ID] = cr
	return nil
}

func (f *fakeStore) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.items, id)
	return nil
}

func (f *fakeStore) List(_ context.Context) ([]cannedresponse.CannedResponse, error) {
	out := make([]cannedresponse.CannedResponse, 0, len(f.items))
	for _, cr := range f.items {
		out = append(out, cr)
	}
	return out, nil
}

func ptr(id uuid.UUID) *uuid.UUID { return &id }

// ── Create validation ───────────────────────────────────────────────────────

func TestService_Create(t *testing.T) {
	catID := uuid.New()
	typeID := uuid.New()

	cases := []struct {
		name       string
		respName   string
		body       string
		categoryID *uuid.UUID
		typeID     *uuid.UUID
		wantErr    bool
	}{
		{name: "global", respName: "Ack", body: "We received your request.", wantErr: false},
		{name: "category scoped", respName: "Reset", body: "To reset...", categoryID: ptr(catID), wantErr: false},
		{name: "category+type scoped", respName: "Closure", body: "Resolved.", categoryID: ptr(catID), typeID: ptr(typeID), wantErr: false},
		{name: "empty name", respName: "  ", body: "x", wantErr: true},
		{name: "empty body", respName: "Ack", body: "   ", wantErr: true},
		{name: "type without category", respName: "Bad", body: "x", typeID: ptr(typeID), wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := cannedresponse.NewService(newFakeStore())
			cr, err := svc.Create(context.Background(), tc.respName, tc.body, tc.categoryID, tc.typeID, 0)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotEqual(t, uuid.Nil, cr.ID)
		})
	}
}

func TestService_Create_SortOrderOutOfRange(t *testing.T) {
	svc := cannedresponse.NewService(newFakeStore())
	_, err := svc.Create(context.Background(), "Ack", "body", nil, nil, math.MaxInt32+1)
	require.Error(t, err)
}

func TestService_Create_TrimsWhitespace(t *testing.T) {
	svc := cannedresponse.NewService(newFakeStore())
	cr, err := svc.Create(context.Background(), "  Ack  ", "  hello  ", nil, nil, 0)
	require.NoError(t, err)
	require.Equal(t, "Ack", cr.Name)
	require.Equal(t, "hello", cr.Body)
}

// ── Update validation ───────────────────────────────────────────────────────

func TestService_Update(t *testing.T) {
	svc := cannedresponse.NewService(newFakeStore())
	cr, err := svc.Create(context.Background(), "Ack", "body", nil, nil, 0)
	require.NoError(t, err)

	cr.Name = "Acknowledgement"
	_, err = svc.Update(context.Background(), cr)
	require.NoError(t, err)

	got, err := svc.Get(context.Background(), cr.ID)
	require.NoError(t, err)
	require.Equal(t, "Acknowledgement", got.Name)
}

func TestService_Update_Invalid(t *testing.T) {
	svc := cannedresponse.NewService(newFakeStore())
	cr, err := svc.Create(context.Background(), "Ack", "body", nil, nil, 0)
	require.NoError(t, err)

	cr.Body = "   "
	_, err = svc.Update(context.Background(), cr)
	require.Error(t, err)

	cr.Body = "ok"
	cr.CategoryID = nil
	cr.TypeID = ptr(uuid.New())
	_, err = svc.Update(context.Background(), cr)
	require.Error(t, err)
}

func TestService_Update_NotFound(t *testing.T) {
	svc := cannedresponse.NewService(newFakeStore())
	_, err := svc.Update(context.Background(), cannedresponse.CannedResponse{
		ID:   uuid.New(),
		Name: "Ack",
		Body: "body",
	})
	require.Error(t, err)
}

// ── Availability filtering (the picker rule) ────────────────────────────────

func TestService_Available(t *testing.T) {
	ctx := context.Background()
	catA := uuid.New()
	catB := uuid.New()
	typeX := uuid.New()
	typeY := uuid.New()

	svc := cannedresponse.NewService(newFakeStore())
	global, _ := svc.Create(ctx, "Global", "g", nil, nil, 0)
	catAResp, _ := svc.Create(ctx, "CatA", "a", ptr(catA), nil, 0)
	catAtypeX, _ := svc.Create(ctx, "CatA-TypeX", "ax", ptr(catA), ptr(typeX), 0)
	catBResp, _ := svc.Create(ctx, "CatB", "b", ptr(catB), nil, 0)

	ids := func(crs []cannedresponse.CannedResponse) map[uuid.UUID]bool {
		m := make(map[uuid.UUID]bool, len(crs))
		for _, cr := range crs {
			m[cr.ID] = true
		}
		return m
	}

	t.Run("category A, type X", func(t *testing.T) {
		got, err := svc.Available(ctx, catA, ptr(typeX))
		require.NoError(t, err)
		m := ids(got)
		require.True(t, m[global.ID])
		require.True(t, m[catAResp.ID])
		require.True(t, m[catAtypeX.ID])
		require.False(t, m[catBResp.ID])
	})

	t.Run("category A, type Y excludes type-X response", func(t *testing.T) {
		got, err := svc.Available(ctx, catA, ptr(typeY))
		require.NoError(t, err)
		m := ids(got)
		require.True(t, m[global.ID])
		require.True(t, m[catAResp.ID])
		require.False(t, m[catAtypeX.ID])
		require.False(t, m[catBResp.ID])
	})

	t.Run("category A, no type excludes type-scoped response", func(t *testing.T) {
		got, err := svc.Available(ctx, catA, nil)
		require.NoError(t, err)
		m := ids(got)
		require.True(t, m[global.ID])
		require.True(t, m[catAResp.ID])
		require.False(t, m[catAtypeX.ID])
	})

	t.Run("unrelated category sees only global", func(t *testing.T) {
		got, err := svc.Available(ctx, uuid.New(), nil)
		require.NoError(t, err)
		m := ids(got)
		require.True(t, m[global.ID])
		require.Len(t, got, 1)
	})
}

func TestService_Available_SortedByOrderThenName(t *testing.T) {
	ctx := context.Background()
	svc := cannedresponse.NewService(newFakeStore())
	_, _ = svc.Create(ctx, "Bravo", "b", nil, nil, 1)
	_, _ = svc.Create(ctx, "Alpha", "a", nil, nil, 1)
	_, _ = svc.Create(ctx, "First", "f", nil, nil, 0)

	got, err := svc.Available(ctx, uuid.New(), nil)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, "First", got[0].Name) // sort_order 0
	require.Equal(t, "Alpha", got[1].Name) // sort_order 1, name A < B
	require.Equal(t, "Bravo", got[2].Name)
}
