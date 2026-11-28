package mcp

import (
	"strings"
	"testing"
)

// Unassigning a ticket has to be asked for, in every spelling.
//
// Nil means "to nobody" once it reaches Assign, so every argument that fails
// to become an id and is then ignored unassigns the ticket — and the tool
// answers success while taking it off the person working it. An agent sending
// null to mean "leave this alone" did exactly that.
//
// The first attempt at this checked whether the key was PRESENT, and a JSON
// null makes a key present: null and "" walked through the guard that was
// added for them. That is why this test exists at the argument level and
// lists the spellings one by one.
func TestAssignArguments_OnlyAnIdOrAnExplicitClear(t *testing.T) {
	cases := []struct {
		name    string
		args    map[string]any
		wantErr string
	}{
		{"a null user id", map[string]any{"assignee_user_id": nil}, "must be an id"},
		{"an empty user id", map[string]any{"assignee_user_id": ""}, "must be an id"},
		{"a number", map[string]any{"assignee_user_id": float64(123)}, "must be an id"},
		{"a boolean", map[string]any{"assignee_user_id": true}, "must be an id"},
		{"a null group id", map[string]any{"assignee_group_id": nil}, "must be an id"},
		{"an empty group id", map[string]any{"assignee_group_id": ""}, "must be an id"},
		{"nothing at all", map[string]any{}, "clear_assignee"},
		{
			name:    "an id and a clear at once",
			args:    map[string]any{"assignee_user_id": "6f1c…", "clear_assignee": true},
			wantErr: "cannot be combined",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, ok := assignArgumentProblem(tc.args)
			if !ok {
				t.Fatalf("accepted %v, which unassigns the ticket and reports success", tc.args)
			}
			if !strings.Contains(msg, tc.wantErr) {
				t.Errorf("refused for the wrong reason: %q", msg)
			}
		})
	}

	t.Run("an ordinary assignment is accepted", func(t *testing.T) {
		if _, bad := assignArgumentProblem(map[string]any{"assignee_user_id": "6f1c…"}); bad {
			t.Error("a plain assignment was refused")
		}
	})

	t.Run("an explicit clear is accepted", func(t *testing.T) {
		if _, bad := assignArgumentProblem(map[string]any{"clear_assignee": true}); bad {
			t.Error("clear_assignee: true was refused, so nothing can be unassigned")
		}
	})
}
