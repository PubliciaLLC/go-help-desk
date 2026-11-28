package user_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #451: the password reset hashes the new password where it is chosen, so the
// statement that spends the link can write it. It must hash the way every other
// path does: at the service's cost.
func TestHashPassword_HashesAtTheServicesCost(t *testing.T) {
	svc := user.NewService(newFakeUserStore(), user.WithBcryptCost(7))

	hash, err := svc.HashPassword("a-long-enough-password")

	require.NoError(t, err)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("a-long-enough-password")))
	cost, err := bcrypt.Cost([]byte(hash))
	require.NoError(t, err)
	require.Equal(t, 7, cost)
}

// A password bcrypt cannot take is an error, not a truncated hash.
func TestHashPassword_RefusesWhatBcryptRefuses(t *testing.T) {
	svc := user.NewService(newFakeUserStore(), user.WithBcryptCost(6))
	_, err := svc.HashPassword(string(make([]byte, user.MaxPasswordLength+1)))
	require.Error(t, err)
}
