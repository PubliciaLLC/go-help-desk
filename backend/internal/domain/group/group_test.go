package group_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/stretchr/testify/require"
)

func TestGroupScope_IsInScope(t *testing.T) {
	catA := uuid.New()
	catB := uuid.New()
	typeX := uuid.New()
	typeY := uuid.New()

	cases := []struct {
		name       string
		scope      group.GroupScope
		categoryID uuid.UUID
		typeID     *uuid.UUID
		want       bool
	}{
		{
			name:       "whole-category scope matches any type",
			scope:      group.GroupScope{CategoryID: catA, TypeID: nil},
			categoryID: catA,
			typeID:     &typeX,
			want:       true,
		},
		{
			name:       "whole-category scope matches nil type",
			scope:      group.GroupScope{CategoryID: catA, TypeID: nil},
			categoryID: catA,
			typeID:     nil,
			want:       true,
		},
		{
			name:       "specific-type scope matches correct type",
			scope:      group.GroupScope{CategoryID: catA, TypeID: &typeX},
			categoryID: catA,
			typeID:     &typeX,
			want:       true,
		},
		{
			name:       "specific-type scope rejects wrong type",
			scope:      group.GroupScope{CategoryID: catA, TypeID: &typeX},
			categoryID: catA,
			typeID:     &typeY,
			want:       false,
		},
		{
			name:       "specific-type scope rejects nil type",
			scope:      group.GroupScope{CategoryID: catA, TypeID: &typeX},
			categoryID: catA,
			typeID:     nil,
			want:       false,
		},
		{
			name:       "wrong category always fails",
			scope:      group.GroupScope{CategoryID: catA, TypeID: nil},
			categoryID: catB,
			typeID:     &typeX,
			want:       false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.scope.IsInScope(tc.categoryID, tc.typeID))
		})
	}
}
