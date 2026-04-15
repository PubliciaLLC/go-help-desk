package notify_test

import (
	"context"
	"errors"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
	"github.com/stretchr/testify/require"
)

type countingDispatcher struct {
	calls int
	err   error
}

func (d *countingDispatcher) Dispatch(_ context.Context, _ notification.Event) error {
	d.calls++
	return d.err
}

func TestMulti_NoDispatchers(t *testing.T) {
	m := notify.NewMulti()
	err := m.Dispatch(context.Background(), notification.Event{})
	require.NoError(t, err)
}

func TestMulti_AllDispatchersCalled(t *testing.T) {
	d1 := &countingDispatcher{}
	d2 := &countingDispatcher{}
	m := notify.NewMulti(d1, d2)

	require.NoError(t, m.Dispatch(context.Background(), notification.Event{}))
	require.Equal(t, 1, d1.calls)
	require.Equal(t, 1, d2.calls)
}

func TestMulti_FirstErrorReturned(t *testing.T) {
	sentinel := errors.New("dispatch failed")
	d1 := &countingDispatcher{err: sentinel}
	d2 := &countingDispatcher{}
	m := notify.NewMulti(d1, d2)

	err := m.Dispatch(context.Background(), notification.Event{})
	require.ErrorIs(t, err, sentinel)
	// Both dispatchers must still be called.
	require.Equal(t, 1, d1.calls)
	require.Equal(t, 1, d2.calls)
}
