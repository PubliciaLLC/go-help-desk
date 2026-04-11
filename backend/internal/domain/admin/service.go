package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// Service provides typed access to the settings table.
type Service struct{ store Store }

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

// GetBool returns a boolean setting value.
func (s *Service) GetBool(ctx context.Context, key string) (bool, error) {
	raw, err := s.store.Get(ctx, key)
	if err != nil {
		return false, fmt.Errorf("getting setting %q: %w", key, err)
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, fmt.Errorf("parsing setting %q as bool: %w", key, err)
	}
	return v, nil
}

// GetInt returns an integer setting value.
func (s *Service) GetInt(ctx context.Context, key string) (int, error) {
	raw, err := s.store.Get(ctx, key)
	if err != nil {
		return 0, fmt.Errorf("getting setting %q: %w", key, err)
	}
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, fmt.Errorf("parsing setting %q as int: %w", key, err)
	}
	return v, nil
}

// GetString returns a string setting value.
func (s *Service) GetString(ctx context.Context, key string) (string, error) {
	raw, err := s.store.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("getting setting %q: %w", key, err)
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("parsing setting %q as string: %w", key, err)
	}
	return v, nil
}

// SetBool persists a boolean setting.
func (s *Service) SetBool(ctx context.Context, key string, v bool) error {
	b := []byte(strconv.FormatBool(v))
	return s.store.Set(ctx, key, b)
}

// SetInt persists an integer setting.
func (s *Service) SetInt(ctx context.Context, key string, v int) error {
	b := []byte(strconv.Itoa(v))
	return s.store.Set(ctx, key, b)
}

// SetString persists a string setting.
func (s *Service) SetString(ctx context.Context, key string, v string) error {
	b, _ := json.Marshal(v)
	return s.store.Set(ctx, key, b)
}

// ReopenWindowDays returns the configured reopen window, defaulting to 7.
func (s *Service) ReopenWindowDays(ctx context.Context) int {
	v, err := s.GetInt(ctx, KeyReopenWindowDays)
	if err != nil {
		return 7
	}
	return v
}

// SAMLEnabled returns whether SAML authentication is enabled.
func (s *Service) SAMLEnabled(ctx context.Context) bool {
	v, _ := s.GetBool(ctx, KeySAMLEnabled)
	return v
}

// GuestSubmissionEnabled returns whether unauthenticated ticket submission is allowed.
func (s *Service) GuestSubmissionEnabled(ctx context.Context) bool {
	v, _ := s.GetBool(ctx, KeyGuestSubmissionEnabled)
	return v
}

// SLAEnabled returns whether SLA tracking is active.
func (s *Service) SLAEnabled(ctx context.Context) bool {
	v, _ := s.GetBool(ctx, KeySLAEnabled)
	return v
}

// MFAEnabled returns whether MFA is available.
func (s *Service) MFAEnabled(ctx context.Context) bool {
	v, _ := s.GetBool(ctx, KeyMFAEnabled)
	return v
}

// ReopenTargetStatusName returns the name of the status tickets are moved to
// when reopened, defaulting to "New".
func (s *Service) ReopenTargetStatusName(ctx context.Context) string {
	v, err := s.GetString(ctx, KeyReopenTargetStatusName)
	if err != nil {
		return "New"
	}
	return v
}

// ListAll returns all settings as raw JSON map.
func (s *Service) ListAll(ctx context.Context) (map[string][]byte, error) {
	return s.store.List(ctx)
}

// SetRaw persists a raw JSON value for the given key.
func (s *Service) SetRaw(ctx context.Context, key string, value []byte) error {
	return s.store.Set(ctx, key, value)
}
