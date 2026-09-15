package notify

import (
	"net"
	"net/smtp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// net/smtp.SendMail dials with no timeout and sets no deadline, and delivery
// runs on the request goroutine after the ticket has already committed. A relay
// that accepts the connection and then stops responding parked the handler
// until the write timeout dropped the client with no response — the user
// retried and filed a duplicate, while every goroutine that touched email piled
// up until the relay recovered.
func TestSendMailWithTimeout_GivesUpOnASilentServer(t *testing.T) {
	// A listener that accepts and then says nothing — no SMTP banner, ever.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- struct{}{}
		// Hold it open and send nothing.
		time.Sleep(30 * time.Second)
		_ = conn.Close()
	}()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- sendMailWithTimeout(ln.Addr().String(), nil,
			"from@example.com", "to@example.com", []byte("Subject: x\r\n\r\nbody"),
			500*time.Millisecond)
	}()

	select {
	case err := <-done:
		require.Error(t, err, "a server that never replies must produce an error, not a send")
		require.Less(t, time.Since(start), 5*time.Second,
			"it must give up near the timeout, not hang")
	case <-time.After(5 * time.Second):
		t.Fatal("sendMailWithTimeout did not return — this is the hang it exists to prevent")
	}

	select {
	case <-accepted:
	default:
		t.Fatal("the listener never accepted; the test proved nothing")
	}
}

// An unreachable address must fail at the dial rather than block.
func TestSendMailWithTimeout_FailsFastOnARefusedConnection(t *testing.T) {
	// Bind and close to get a port nothing is listening on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	start := time.Now()
	err = sendMailWithTimeout(addr, nil, "from@example.com", "to@example.com",
		[]byte("Subject: x\r\n\r\nbody"), 2*time.Second)

	require.Error(t, err)
	require.Contains(t, err.Error(), "dialing SMTP server")
	require.Less(t, time.Since(start), 2*time.Second)
}

var _ smtp.Auth // the signature takes one; this keeps the import honest
