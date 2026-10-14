package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

func TestSessionData_PendingExpired(t *testing.T) {
	userID := uuid.New()
	now := time.Now()

	tests := []struct {
		name    string
		session SessionData
		want    bool
	}{
		{"MFA passed is never pending", SessionData{UserID: userID, MFAPassed: true, PendingUntil: now.Add(-time.Hour)}, false},
		{"no user is the pre-login state, not pending", SessionData{PendingUntil: now.Add(-time.Hour)}, false},
		{"pre-upgrade session with no deadline is expired", SessionData{UserID: userID}, true},
		{"before the deadline", SessionData{UserID: userID, Role: user.RoleAdmin, PendingUntil: now.Add(time.Second)}, false},
		{"after the deadline", SessionData{UserID: userID, Role: user.RoleAdmin, PendingUntil: now.Add(-time.Second)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.session.PendingExpired(now); got != tt.want {
				t.Errorf("PendingExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPendingDeadline(t *testing.T) {
	now := time.Now()
	if got := PendingDeadline(now, false); !got.Equal(now.Add(5 * time.Minute)) {
		t.Errorf("code entry deadline = %v, want 5 minutes out", got.Sub(now))
	}
	if got := PendingDeadline(now, true); !got.Equal(now.Add(15 * time.Minute)) {
		t.Errorf("enrolment deadline = %v, want 15 minutes out", got.Sub(now))
	}
}
