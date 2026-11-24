package notify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// announced collects what the ticket service raises, standing in for the
// outbox write the production transaction runner makes.
type announced struct{ events []notification.Event }

func (a *announced) Dispatch(_ context.Context, ev notification.Event) error {
	a.events = append(a.events, ev)
	return nil
}

// pgSource is the production source's placement read: the ticket service's
// own method (the production adapter, server.webhookEventSource, only calls
// it, and is tested in the server package).
type pgSource struct {
	*fakeSource
	svc *ticket.Service
}

func (s *pgSource) Placement(ctx context.Context, st notification.TicketState) (notification.Placement, error) {
	return s.svc.PlacementNames(ctx, st)
}

// A ticket created through the real service, in real Postgres, with a real
// classification and a real routing, produces a Slack and a Teams delivery
// whose body names the classification and the holder. The names are not in
// the event that was raised: they are read when the message is built.
func TestPlacement_ATicketCreatedThroughTheServiceIsDeliveredWithItsPlacement(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	now := time.Now().UTC().Truncate(time.Millisecond)
	cats := categorystore.New(q)
	cat := category.Category{ID: uuid.New(), Name: "Hardware " + suffix, Active: true}
	require.NoError(t, cats.CreateCategory(ctx, cat))
	typ := category.Type{ID: uuid.New(), CategoryID: cat.ID, Name: "Printer & <Scanner> " + suffix, Active: true}
	require.NoError(t, cats.CreateType(ctx, typ))
	item := category.Item{ID: uuid.New(), TypeID: typ.ID, Name: "Toner " + suffix, Active: true}
	require.NoError(t, cats.CreateItem(ctx, item))
	grp := group.Group{ID: uuid.New(), Name: "IT Support " + suffix}
	require.NoError(t, groupstore.New(q).Create(ctx, grp))
	mk := func(role user.Role, name string) user.User {
		u := user.User{ID: uuid.New(), Email: name + "-" + suffix + "@test.local", DisplayName: name + " " + suffix,
			Role: role, CreatedAt: now, UpdatedAt: now}
		require.NoError(t, userstore.New(q).Create(ctx, u))
		return u
	}
	reporter, jordan := mk(user.RoleUser, "Reporter"), mk(user.RoleStaff, "Jordan Blake")

	sink := &announced{}
	ts := ticketstore.New(q)
	svc := ticket.NewService(ts, ts, sink, auditstore.New(q), testutil.NewJoiningTxRunner(q, sink), nil)
	require.NoError(t, svc.LoadSystemStatuses(ctx))

	create := func(in ticket.CreateInput) notification.Event {
		t.Helper()
		in.DescriptionFormat, in.SourceChannel, in.ReporterUserID = ticket.TextFormatPlain, ticket.ChannelWeb, &reporter.ID
		in.Subject, in.Description = "Test 3", "it is broken"
		sink.events = nil
		_, err := svc.Create(ctx, in)
		require.NoError(t, err)
		for _, ev := range sink.events {
			if ev.Type == notification.EventTicketCreated {
				return ev
			}
		}
		t.Fatal("the service raised no ticket.created")
		return notification.Event{}
	}

	cases := []struct {
		name    string
		in      ticket.CreateInput
		want    []string // each must be in the message
		notWant []string
	}{
		{"classified three deep, routed to a group and a person",
			ticket.CreateInput{CategoryID: cat.ID, TypeID: &typ.ID, ItemID: &item.ID,
				AutoAssign: []ticket.Assignment{{UserID: &jordan.ID, GroupID: &grp.ID}}},
			[]string{cat.Name + "SEP", item.Name, "Group: " + grp.Name, "Assignee: " + jordan.DisplayName},
			[]string{"Unassigned"}},
		{"category only, nobody holds it",
			ticket.CreateInput{CategoryID: cat.ID},
			[]string{cat.Name, "Unassigned"},
			[]string{"SEP", "Group:", "Assignee:"}},
	}
	for _, tc := range cases {
		for _, format := range []string{"slack", "teams"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				ev := create(tc.in)
				require.NotContains(t, mustJSON(t, ev), cat.Name, "the raised event holds ids, not names")

				rc := newReceiver(t, 200)
				bodies := capture(rc)
				hook := hookFor(rc.srv.URL)
				hook.PayloadFormat = format
				rig := newQueueRig(t, rc.srv.Client(), hook)
				rig.disp.queue.Source = &pgSource{fakeSource: newFakeSource(), svc: svc}
				require.NoError(t, rig.front.Dispatch(ctx, ev))
				rig.drain(t)

				require.Len(t, bodies(), 1)
				require.True(t, json.Valid([]byte(bodies()[0])))
				var msg any
				require.NoError(t, json.Unmarshal([]byte(bodies()[0]), &msg))
				flat := mustJSONString(t, msg)
				// The separator between levels is a ">" that Slack escapes.
				sep := " > "
				if format == "slack" {
					sep = " &gt; "
				}
				for _, w := range tc.want {
					require.Contains(t, flat, mustJSONString(t, strings.ReplaceAll(w, "SEP", sep)), w)
				}
				for _, w := range tc.notWant {
					require.NotContains(t, flat, mustJSONString(t, strings.ReplaceAll(w, "SEP", sep)), w)
				}
			})
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// mustJSONString is the string as it appears inside a JSON document, minus its
// quotes, so a comparison is made on the decoded text of both sides.
func mustJSONString(t *testing.T, v any) string {
	t.Helper()
	s := mustJSON(t, v)
	if _, ok := v.(string); ok {
		return s[1 : len(s)-1]
	}
	return s
}
