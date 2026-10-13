package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

func TestSessionData_PendingExpired(t *testing.T) {
	userId := uuid.New()
	now := time.Now()

	tests := []struct {
		name     string
		session  SessionData
		checkAt  time.Time
		expected bool
	}{
		{
			name: "not expired when MFAPassed is true",
			session: SessionData{
				UserID:       userId,
				MFAPassed:    true,
				PendingSince: now.Add(-10 * time.Minute),
			},
			checkAt:  now,
			expected: false,
		},
		{
			name: "not expired when UserID is nil",
			session: SessionData{
				UserID:       uuid.Nil,
				MFAPassed:    false,
				PendingSince: now.Add(-10 * time.Minute),
			},
			checkAt:  now,
			expected: false,
		},
		{
			name: "not expired when no PendingSince (test mock or pre-upgrade session)",
			session: SessionData{
				UserID:       userId,
				MFAPassed:    false,
				PendingSince: time.Time{},
			},
			checkAt:  now,
			expected: false,
		},
		{
			name: "not expired when within deadline",
			session: SessionData{
				UserID:       userId,
				Role:         user.RoleAdmin,
				MFAPassed:    false,
				PendingSince: now.Add(-4*time.Minute - 59*time.Second),
			},
			checkAt:  now,
			expected: false,
		},
		{
			name: "expired when past deadline",
			session: SessionData{
				UserID:       userId,
				Role:         user.RoleAdmin,
				MFAPassed:    false,
				PendingSince: now.Add(-5*time.Minute - 1*time.Second),
			},
			checkAt:  now,
			expected: true,
		},
		{
			name: "expired at exactly deadline plus epsilon",
			session: SessionData{
				UserID:       userId,
				MFAPassed:    false,
				PendingSince: now.Add(-5 * time.Minute),
			},
			checkAt:  now.Add(1 * time.Microsecond),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.session.PendingExpired(tt.checkAt)
			if got != tt.expected {
				t.Errorf("PendingExpired() = %v, want %v", got, tt.expected)
			}
		})
	}
}
