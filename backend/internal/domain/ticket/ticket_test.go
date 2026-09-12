package ticket_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/stretchr/testify/require"
)

func TestGenerateTrackingNumber(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		year   int
		seq    int64
		want   ticket.TrackingNumber
	}{
		{name: "default prefix", prefix: "GHD", year: 2026, seq: 1, want: "GHD-2026-000001"},
		{name: "sequence is zero-padded to six", prefix: "GHD", year: 2026, seq: 42, want: "GHD-2026-000042"},
		{name: "six digits is not truncated", prefix: "GHD", year: 2026, seq: 999999, want: "GHD-2026-999999"},
		{name: "beyond six digits keeps growing", prefix: "GHD", year: 2026, seq: 1000000, want: "GHD-2026-1000000"},

		// An instance that predates the rename sets this back, so its series
		// stays consistent with tracking numbers already in customers' inboxes.
		{name: "a configured prefix is used", prefix: "OHD", year: 2025, seq: 7, want: "OHD-2025-000007"},
		{name: "digits are allowed", prefix: "IT2", year: 2026, seq: 3, want: "IT2-2026-000003"},
		{name: "eight characters is the limit", prefix: "ABCDEFGH", year: 2026, seq: 3, want: "ABCDEFGH-2026-000003"},

		// Falling back rather than refusing: this runs when a customer is
		// opening a ticket, and an admin's typo must not stop them.
		{name: "empty falls back to the default", prefix: "", year: 2026, seq: 5, want: "GHD-2026-000005"},
		{name: "lowercase falls back", prefix: "ghd", year: 2026, seq: 5, want: "GHD-2026-000005"},
		{name: "a hyphen falls back", prefix: "GH-D", year: 2026, seq: 5, want: "GHD-2026-000005"},
		{name: "too long falls back", prefix: "ABCDEFGHI", year: 2026, seq: 5, want: "GHD-2026-000005"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ticket.GenerateTrackingNumber(tc.prefix, tc.year, tc.seq))
		})
	}
}

func TestValidateTrackingPrefix(t *testing.T) {
	for _, ok := range []string{"GHD", "OHD", "A", "IT2", "ABCDEFGH", "12345678"} {
		require.NoError(t, ticket.ValidateTrackingPrefix(ok), "%q should be accepted", ok)
	}

	// A hyphen is rejected because it is the field separator: "GH-D-2026-000001"
	// cannot be split back into prefix, year and sequence unambiguously.
	for _, bad := range []string{"", " ", "ghd", "GH D", "GH-D", "GHD!", "ABCDEFGHI", "GHD\n"} {
		require.ErrorIs(t, ticket.ValidateTrackingPrefix(bad), ticket.ErrInvalidTrackingPrefix,
			"%q should be rejected", bad)
	}
}

// TestDefaultTrackingPrefix pins the value itself. Changing it changes what
// every new instance mints, which is a product decision rather than a tidy-up.
func TestDefaultTrackingPrefix(t *testing.T) {
	require.Equal(t, "GHD", ticket.DefaultTrackingPrefix)
	require.NoError(t, ticket.ValidateTrackingPrefix(ticket.DefaultTrackingPrefix),
		"the default must itself be valid, or every fallback produces a malformed number")
}

func TestCanUserUpdate(t *testing.T) {
	now := time.Now()
	recentlyResolved := now.Add(-24 * time.Hour)     // within any reasonable window
	longAgoResolved := now.Add(-30 * 24 * time.Hour) // outside a 7-day window

	statusNew := ticket.Status{Name: ticket.StatusNameNew, Kind: ticket.StatusKindSystem}
	statusResolved := ticket.Status{Name: ticket.StatusNameResolved, Kind: ticket.StatusKindSystem}
	statusClosed := ticket.Status{Name: ticket.StatusNameClosed, Kind: ticket.StatusKindSystem}
	statusCustom := ticket.Status{Name: "In Progress", Kind: ticket.StatusKindCustom}

	myID := uuid.New()
	someoneElseID := uuid.New()
	myTicket := ticket.Ticket{ID: uuid.New(), ReporterUserID: &myID}

	cases := []struct {
		name             string
		t                ticket.Ticket
		u                user.User
		status           ticket.Status
		reopenWindowDays int
		wantErr          bool
	}{
		// Admins always allowed
		{name: "admin/new", t: myTicket, u: user.User{Role: user.RoleAdmin}, status: statusNew, reopenWindowDays: 7, wantErr: false},
		{name: "admin/closed", t: myTicket, u: user.User{Role: user.RoleAdmin}, status: statusClosed, reopenWindowDays: 7, wantErr: false},

		// Staff always allowed
		{name: "staff/new", t: myTicket, u: user.User{Role: user.RoleStaff}, status: statusNew, reopenWindowDays: 7, wantErr: false},
		{name: "staff/closed", t: myTicket, u: user.User{Role: user.RoleStaff}, status: statusClosed, reopenWindowDays: 7, wantErr: false},

		// Users — ownership. Previously unenforced: the doc comment promised
		// "allowed only on their own tickets" while the function compared
		// nothing, and one reporting user could reply on another's ticket.
		{
			name:             "user/not the reporter",
			t:                ticket.Ticket{ReporterUserID: &someoneElseID},
			u:                user.User{ID: myID, Role: user.RoleUser},
			status:           statusNew,
			reopenWindowDays: 7,
			wantErr:          true,
		},
		{
			// A guest ticket has no reporter user, so no signed-in reporting
			// user owns it.
			name:             "user/guest ticket has no owner",
			t:                ticket.Ticket{ReporterUserID: nil},
			u:                user.User{ID: myID, Role: user.RoleUser},
			status:           statusNew,
			reopenWindowDays: 7,
			wantErr:          true,
		},
		{
			// Staff authority does not depend on ownership.
			name:             "staff/not the reporter",
			t:                ticket.Ticket{ReporterUserID: &someoneElseID},
			u:                user.User{ID: myID, Role: user.RoleStaff},
			status:           statusNew,
			reopenWindowDays: 7,
			wantErr:          false,
		},

		// Users — open statuses
		{name: "user/new", t: myTicket, u: user.User{ID: myID, Role: user.RoleUser}, status: statusNew, reopenWindowDays: 7, wantErr: false},
		{name: "user/custom", t: myTicket, u: user.User{ID: myID, Role: user.RoleUser}, status: statusCustom, reopenWindowDays: 7, wantErr: false},

		// Users — Resolved within window
		{
			name:             "user/resolved/within window",
			t:                ticket.Ticket{ReporterUserID: &myID, ResolvedAt: &recentlyResolved},
			u:                user.User{ID: myID, Role: user.RoleUser},
			status:           statusResolved,
			reopenWindowDays: 7,
			wantErr:          false,
		},
		// Users — Resolved outside window
		{
			name:             "user/resolved/outside window",
			t:                ticket.Ticket{ReporterUserID: &myID, ResolvedAt: &longAgoResolved},
			u:                user.User{ID: myID, Role: user.RoleUser},
			status:           statusResolved,
			reopenWindowDays: 7,
			wantErr:          true,
		},
		// Users — Closed
		{name: "user/closed", t: myTicket, u: user.User{ID: myID, Role: user.RoleUser}, status: statusClosed, reopenWindowDays: 7, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ticket.CanUserUpdate(tc.t, tc.u, tc.status, tc.reopenWindowDays)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCanTransitionStatus(t *testing.T) {
	statusClosed := ticket.Status{Name: ticket.StatusNameClosed, Kind: ticket.StatusKindSystem}
	statusResolved := ticket.Status{Name: ticket.StatusNameResolved, Kind: ticket.StatusKindSystem}
	statusCustom := ticket.Status{Name: "In Progress", Kind: ticket.StatusKindCustom}

	cases := []struct {
		name    string
		to      ticket.Status
		role    user.Role
		wantErr bool
	}{
		// Admins can do anything
		{name: "admin → closed", to: statusClosed, role: user.RoleAdmin, wantErr: false},
		{name: "admin → resolved", to: statusResolved, role: user.RoleAdmin, wantErr: false},
		{name: "admin → custom", to: statusCustom, role: user.RoleAdmin, wantErr: false},

		// Staff can go anywhere except Closed
		{name: "staff → resolved", to: statusResolved, role: user.RoleStaff, wantErr: false},
		{name: "staff → custom", to: statusCustom, role: user.RoleStaff, wantErr: false},
		{name: "staff → closed", to: statusClosed, role: user.RoleStaff, wantErr: true},

		// Users cannot set status
		{name: "user → new", to: ticket.Status{Name: ticket.StatusNameNew}, role: user.RoleUser, wantErr: true},
		{name: "user → custom", to: statusCustom, role: user.RoleUser, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ticket.CanTransitionStatus(tc.to, tc.role)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// Priority reaching the database unvalidated failed the CHECK constraint after
// NextSeq had already consumed a tracking number, so a typo cost a 500 and a
// permanent gap in the sequence. Empty is not a typo — it is "unspecified", and
// both callers were defaulting it to medium themselves.
func TestCreate_PriorityValidation(t *testing.T) {
	cases := []struct {
		name     string
		priority ticket.Priority
		wantErr  bool
		want     ticket.Priority
	}{
		{name: "empty defaults to medium", priority: "", want: ticket.PriorityMedium},
		{name: "critical", priority: ticket.PriorityCritical, want: ticket.PriorityCritical},
		{name: "low", priority: ticket.PriorityLow, want: ticket.PriorityLow},
		{name: "unknown word", priority: "urgent", wantErr: true},
		{name: "wrong case", priority: "HIGH", wantErr: true},
		{name: "whitespace", priority: " high", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			reporter := uuid.New()
			got, err := h.svc.Create(context.Background(), ticket.CreateInput{
				Subject:        "Printer offline",
				CategoryID:     uuid.New(),
				Priority:       tc.priority,
				ReporterUserID: &reporter,
			})
			if tc.wantErr {
				require.ErrorIs(t, err, ticket.ErrValidation)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got.Priority)
		})
	}
}
