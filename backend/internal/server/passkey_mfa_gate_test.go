package server

import (
	"testing"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #307 item 3: completing TOTP enrolment satisfies this login's MFA
// challenge (handleMFAEnrollConfirm flips MFAPassed), but completing passkey
// registration did not — so somebody compelled to enrol who registered a
// passkey was still refused by RequireMFA until they separately ran the
// sign-in ceremony against the key they had just proven they held.
//
// This can't be driven through the real HTTP route without a genuine
// WebAuthn ceremony, which this suite has no infrastructure to perform (see
// #307's "could not verify" list) — the bug was never in the cryptography,
// only in the handler forgetting to touch the session once the ceremony and
// the store write had both already succeeded. So the fix is verified at the
// point it actually lives: the session mutation itself.
func TestMarkPasskeyRegistrationSatisfiesMFA(t *testing.T) {
	uid := uuid.New()

	t.Run("flips MFAPassed for a session that owed a factor", func(t *testing.T) {
		sd := auth.SessionData{UserID: uid, Role: user.RoleAdmin, MFAPassed: false}
		got := markPasskeyRegistrationSatisfiesMFA(sd)

		if !got.MFAPassed {
			t.Fatal("MFAPassed is still false after registration completed")
		}
		if got.UserID != uid || got.Role != user.RoleAdmin {
			t.Errorf("identity fields were disturbed: got UserID=%s Role=%s", got.UserID, got.Role)
		}
	})

	t.Run("is a no-op for a session that already held the flag", func(t *testing.T) {
		// The only other way past requireFactorOrFirstEnrolment: an
		// already-protected account's owner, already MFA-passed,
		// registering a replacement key.
		sd := auth.SessionData{UserID: uid, Role: user.RoleStaff, MFAPassed: true}
		got := markPasskeyRegistrationSatisfiesMFA(sd)

		if !got.MFAPassed {
			t.Fatal("an already-satisfied session must not become unsatisfied")
		}
	})

	t.Run("does not touch unrelated pending session state", func(t *testing.T) {
		sd := auth.SessionData{
			UserID:           uid,
			MFAPassed:        false,
			PendingMFASecret: "leave-me-alone",
			OIDCState:        "leave-me-alone-too",
		}
		got := markPasskeyRegistrationSatisfiesMFA(sd)

		if got.PendingMFASecret != "leave-me-alone" || got.OIDCState != "leave-me-alone-too" {
			t.Errorf("unrelated session fields were clobbered: %+v", got)
		}
	})
}
