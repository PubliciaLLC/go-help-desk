package ticket_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/stretchr/testify/require"
)

func TestGenerateTrackingNumber(t *testing.T) {
	cases := []struct {
		year int
		seq  int64
		want ticket.TrackingNumber
	}{
		{2024, 1, "OHD-2024-000001"},
		{2024, 999999, "OHD-2024-999999"},
		{2025, 42, "OHD-2025-000042"},
	}
	for _, tc := range cases {
		got := ticket.GenerateTrackingNumber(tc.year, tc.seq)
		require.Equal(t, tc.want, got)
	}
}

func TestCanUserUpdate(t *testing.T) {
	now := time.Now()
	recentlyResolved := now.Add(-24 * time.Hour)    // within any reasonable window
	longAgoResolved := now.Add(-30 * 24 * time.Hour) // outside a 7-day window

	statusNew := ticket.Status{Name: ticket.StatusNameNew, Kind: ticket.StatusKindSystem}
	statusResolved := ticket.Status{Name: ticket.StatusNameResolved, Kind: ticket.StatusKindSystem}
	statusClosed := ticket.Status{Name: ticket.StatusNameClosed, Kind: ticket.StatusKindSystem}
	statusCustom := ticket.Status{Name: "In Progress", Kind: ticket.StatusKindCustom}

	myID := uuid.New()
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

		// Users — open statuses
		{name: "user/new", t: myTicket, u: user.User{Role: user.RoleUser}, status: statusNew, reopenWindowDays: 7, wantErr: false},
		{name: "user/custom", t: myTicket, u: user.User{Role: user.RoleUser}, status: statusCustom, reopenWindowDays: 7, wantErr: false},

		// Users — Resolved within window
		{
			name:             "user/resolved/within window",
			t:                ticket.Ticket{ReporterUserID: &myID, ResolvedAt: &recentlyResolved},
			u:                user.User{Role: user.RoleUser},
			status:           statusResolved,
			reopenWindowDays: 7,
			wantErr:          false,
		},
		// Users — Resolved outside window
		{
			name:             "user/resolved/outside window",
			t:                ticket.Ticket{ReporterUserID: &myID, ResolvedAt: &longAgoResolved},
			u:                user.User{Role: user.RoleUser},
			status:           statusResolved,
			reopenWindowDays: 7,
			wantErr:          true,
		},
		// Users — Closed
		{name: "user/closed", t: myTicket, u: user.User{Role: user.RoleUser}, status: statusClosed, reopenWindowDays: 7, wantErr: true},
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
