package server_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
)

// countingDBTX counts the statements a store sends to the database.
type countingDBTX struct {
	dbgen.DBTX
	n atomic.Int64
}

func (c *countingDBTX) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	c.n.Add(1)
	return c.DBTX.ExecContext(ctx, q, a...)
}

func (c *countingDBTX) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	c.n.Add(1)
	return c.DBTX.QueryContext(ctx, q, a...)
}

func (c *countingDBTX) QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row {
	c.n.Add(1)
	return c.DBTX.QueryRowContext(ctx, q, a...)
}

// A page of `classified` entries names its categories, types and items with one
// query per table, not one per id (1.3.0 audit D3; the lookup used to be a point
// query per distinct id, about 1,200 on a full admin page). The count is pinned
// here: a page of 30 entries holds 90 ids and costs 3 statements.
func TestAuditNames_CatalogueLookupIsOneQueryPerTable(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := createTicketVia(t, h, h.doAsUser)

	const n = 30
	want := map[string]string{}
	for i := 0; i < n; i++ {
		cat, err := h.categorySvc.CreateCategory(ctx, fmt.Sprintf("Category %d", i), i)
		require.NoError(t, err)
		typ, err := h.categorySvc.CreateType(ctx, cat.ID, fmt.Sprintf("Type %d", i), 1)
		require.NoError(t, err)
		item, err := h.categorySvc.CreateItem(ctx, typ.ID, fmt.Sprintf("Item %d", i), 1)
		require.NoError(t, err)
		plantEntry(t, h, id, fmt.Sprintf("classified_%d", i), nil, map[string]any{
			"category_id": cat.ID.String(), "type_id": typ.ID.String(), "item_id": item.ID.String(),
		})
		want[cat.ID.String()] = fmt.Sprintf("Category %d", i)
		want[typ.ID.String()] = fmt.Sprintf("Type %d", i)
		want[item.ID.String()] = fmt.Sprintf("Item %d", i)
	}

	counter := &countingDBTX{DBTX: h.tx}
	h.srv.SetCategoriesForTest(category.NewService(categorystore.New(dbgen.New(counter))))

	for _, view := range []string{"admin", "ticket"} {
		t.Run(view, func(t *testing.T) {
			counter.n.Store(0)
			_, es := namesView(t, h, h.doAsAdmin, view, id)
			require.Equal(t, int64(3), counter.n.Load(),
				"90 ids across three tables: one statement per table, however many ids")

			// Not vacuous: the names did resolve.
			got := map[string]string{}
			for _, e := range es {
				for k, v := range e.Names {
					got[k] = v.Name
				}
			}
			for k, name := range want {
				require.Equal(t, name, got[k], "the name for %s", k)
			}
		})
	}
}

// With no catalogue id on the page there is nothing to look up, and nothing is
// asked of the three tables.
func TestAuditNames_NoCatalogueIdsNoCatalogueQueries(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := createTicketVia(t, h, h.doAsUser)
	plantEntry(t, h, id, "planted_none", nil, map[string]any{"priority": "low"})

	counter := &countingDBTX{DBTX: h.tx}
	h.srv.SetCategoriesForTest(category.NewService(categorystore.New(dbgen.New(counter))))

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id+"/audit", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	res.Body.Close()
	require.Zero(t, counter.n.Load())
}

// A category deleted since it was written keeps its id and gets no name, and
// does not cost the names of the others on the page.
func TestAuditNames_DeletedCatalogueEntryStaysUnnamed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := createTicketVia(t, h, h.doAsUser)
	cat, err := h.categorySvc.CreateCategory(ctx, "Kept", 90)
	require.NoError(t, err)
	gone := "00000000-0000-4000-8000-000000000629"
	plantEntry(t, h, id, "classified_mixed", nil, map[string]any{"category_id": cat.ID.String(), "type_id": gone, "item_id": gone})

	_, es := namesView(t, h, h.doAsAdmin, "ticket", id)
	e := entryNamed(t, es, "classified_mixed")
	require.Equal(t, namesResolve{Name: "Kept"}, e.Names[cat.ID.String()])
	require.NotContains(t, e.Names, gone)
}
