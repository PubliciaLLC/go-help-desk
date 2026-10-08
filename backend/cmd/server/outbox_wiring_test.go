package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

type nopSender struct{}

func (nopSender) Dispatch(context.Context, notification.Event) error { return nil }

// #361: the channel list main.go queues for passes the startup check. Drop
// "verification" from outboxChannels and this fails.
func TestOutboxChannels_PassTheStartupCheck(t *testing.T) {
	senders := map[string]notification.Dispatcher{}
	for _, ch := range outboxChannels() {
		senders[ch] = nopSender{}
	}
	require.NoError(t, checkOutboxWiring(outboxChannels(), senders))
}

func TestCheckOutboxWiring(t *testing.T) {
	all := map[string]notification.Dispatcher{"email": nopSender{}, "webhook": nopSender{}, "verification": nopSender{}}
	cases := []struct {
		name    string
		queued  []string
		senders map[string]notification.Dispatcher
		wantErr bool
	}{
		{"every channel queued and sent", []string{"email", "webhook", "verification"}, all, false},
		{"verification not queued", []string{"email", "webhook"}, all, true},
		{"verification queued with no sender", []string{"email", "webhook", "verification"},
			map[string]notification.Dispatcher{"email": nopSender{}, "webhook": nopSender{}}, true},
		{"another channel queued with no sender", []string{"email", "verification"},
			map[string]notification.Dispatcher{"verification": nopSender{}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkOutboxWiring(tc.queued, tc.senders)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
