package dashboard

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

func TestPickAttention(t *testing.T) {
	base := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	newTicket := func(created time.Time) ticket.Ticket {
		return ticket.Ticket{ID: uuid.New(), CreatedAt: created}
	}
	a := newTicket(base.Add(1 * time.Hour))
	b := newTicket(base.Add(2 * time.Hour))
	c := newTicket(base.Add(3 * time.Hour))
	d := newTicket(base.Add(4 * time.Hour))
	reopenedAt := base.Add(10 * time.Hour)
	reopened := newTicket(base)
	reopened.LastReopenedAt = &reopenedAt

	cases := []struct {
		name  string
		lists []AttentionList
		limit int
		want  []AttentionItem
	}{
		{
			name:  "all empty",
			lists: []AttentionList{{Reason: ReasonBreached}, {Reason: ReasonReopened}},
			limit: AttentionLimit,
			want:  []AttentionItem{},
		},
		{
			name:  "no lists",
			limit: AttentionLimit,
			want:  []AttentionItem{},
		},
		{
			name:  "zero limit",
			lists: []AttentionList{{Reason: ReasonBreached, Tickets: []ticket.Ticket{a}}},
			limit: 0,
			want:  []AttentionItem{},
		},
		{
			name: "reasons in the given order",
			lists: []AttentionList{
				{Reason: ReasonBreached, Tickets: []ticket.Ticket{a}},
				{Reason: ReasonAtRisk, Tickets: []ticket.Ticket{b}},
				{Reason: ReasonUnassigned, Tickets: []ticket.Ticket{c}},
			},
			limit: AttentionLimit,
			want: []AttentionItem{
				{Reason: ReasonBreached, Since: a.CreatedAt, Ticket: a},
				{Reason: ReasonAtRisk, Since: b.CreatedAt, Ticket: b},
				{Reason: ReasonUnassigned, Since: c.CreatedAt, Ticket: c},
			},
		},
		{
			name: "a ticket is listed once under its first reason",
			lists: []AttentionList{
				{Reason: ReasonBreached, Tickets: []ticket.Ticket{a}},
				{Reason: ReasonReopened, Tickets: []ticket.Ticket{a, reopened}},
			},
			limit: AttentionLimit,
			want: []AttentionItem{
				{Reason: ReasonBreached, Since: a.CreatedAt, Ticket: a},
				{Reason: ReasonReopened, Since: reopenedAt, Ticket: reopened},
			},
		},
		{
			name: "a ticket repeated inside one list is listed once",
			lists: []AttentionList{
				{Reason: ReasonAtRisk, Tickets: []ticket.Ticket{b, b}},
			},
			limit: AttentionLimit,
			want: []AttentionItem{
				{Reason: ReasonAtRisk, Since: b.CreatedAt, Ticket: b},
			},
		},
		{
			name: "stops at the limit",
			lists: []AttentionList{
				{Reason: ReasonBreached, Tickets: []ticket.Ticket{a, b}},
				{Reason: ReasonAtRisk, Tickets: []ticket.Ticket{c, d}},
			},
			limit: 3,
			want: []AttentionItem{
				{Reason: ReasonBreached, Since: a.CreatedAt, Ticket: a},
				{Reason: ReasonBreached, Since: b.CreatedAt, Ticket: b},
				{Reason: ReasonAtRisk, Since: c.CreatedAt, Ticket: c},
			},
		},
		{
			name: "a breached backlog fills the list before other reasons",
			lists: []AttentionList{
				{Reason: ReasonBreached, Tickets: []ticket.Ticket{a, b, c, d}},
				{Reason: ReasonUnassigned, Tickets: []ticket.Ticket{reopened}},
			},
			limit: AttentionLimit,
			want: []AttentionItem{
				{Reason: ReasonBreached, Since: a.CreatedAt, Ticket: a},
				{Reason: ReasonBreached, Since: b.CreatedAt, Ticket: b},
				{Reason: ReasonBreached, Since: c.CreatedAt, Ticket: c},
				{Reason: ReasonBreached, Since: d.CreatedAt, Ticket: d},
				{Reason: ReasonUnassigned, Since: reopened.CreatedAt, Ticket: reopened},
			},
		},
		{
			name: "reopened is measured from the reopen instant",
			lists: []AttentionList{
				{Reason: ReasonReopened, Tickets: []ticket.Ticket{reopened}},
			},
			limit: AttentionLimit,
			want: []AttentionItem{
				{Reason: ReasonReopened, Since: reopenedAt, Ticket: reopened},
			},
		},
		{
			name: "reopened without a reopen instant falls back to created",
			lists: []AttentionList{
				{Reason: ReasonReopened, Tickets: []ticket.Ticket{a}},
			},
			limit: AttentionLimit,
			want: []AttentionItem{
				{Reason: ReasonReopened, Since: a.CreatedAt, Ticket: a},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PickAttention(tc.lists, tc.limit)
			require.NotNil(t, got)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestAttentionConstants(t *testing.T) {
	require.Equal(t, 5, AttentionLimit)
	require.Equal(t, 7*24*time.Hour, ReopenedWindow)
	require.Equal(t, AttentionReason("breached"), ReasonBreached)
	require.Equal(t, AttentionReason("at_risk"), ReasonAtRisk)
	require.Equal(t, AttentionReason("unassigned"), ReasonUnassigned)
	require.Equal(t, AttentionReason("reopened"), ReasonReopened)
}
