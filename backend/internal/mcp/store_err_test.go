package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A validation refusal reaches the caller; anything else does not.
//
// This exists because the first version of storeErr called itself on the
// validation branch. Go turns a runaway stack into a fatal runtime error, not
// a panic, so the recoverer could not catch it: any staff MCP caller sending
// an empty subject took the whole server down, repeatably. An LLM-driven
// client sends an empty subject by accident.
//
// The test is a plain call, no server, because that is all it takes — and
// nothing in the MCP suite drove a validation failure through this function,
// which is exactly why it shipped.
func TestStoreErr_DoesNotCallItself(t *testing.T) {
	t.Run("a validation refusal is passed to the caller", func(t *testing.T) {
		err := fmt.Errorf("%w: subject is required", ticket.ErrValidation)
		res, callErr := storeErr(context.Background(), "create ticket", err)
		if callErr != nil {
			t.Fatalf("unexpected error: %v", callErr)
		}
		if !strings.Contains(resultText(t, res), "subject is required") {
			t.Errorf("the caller cannot see what was wrong with their request: %v", res)
		}
	})

	t.Run("anything else is summarised", func(t *testing.T) {
		err := errors.New(`violates foreign key constraint "tickets_reporter_user_id_fkey" (SQLSTATE 23503)`)
		res, callErr := storeErr(context.Background(), "create ticket", err)
		if callErr != nil {
			t.Fatalf("unexpected error: %v", callErr)
		}
		text := resultText(t, res)
		if strings.Contains(text, "constraint") || strings.Contains(text, "SQLSTATE") {
			t.Errorf("the database's own words reached the caller: %s", text)
		}
		if !strings.Contains(text, "create ticket") {
			t.Errorf("the caller is not told which operation failed: %s", text)
		}
	})
}
