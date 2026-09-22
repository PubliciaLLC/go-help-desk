package antivirus_test

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/antivirus"
)

// fakeClamd speaks enough INSTREAM to be indistinguishable from the real thing
// for these purposes. reply is what it answers once the stream ends; a nil
// reply means it hangs up mid-conversation instead.
func fakeClamd(t *testing.T, reply string, hangUpEarly bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				r := bufio.NewReader(conn)
				cmd, err := r.ReadString(0)
				if err != nil {
					return
				}
				if cmd == "zPING\x00" {
					_, _ = conn.Write([]byte("PONG\x00"))
					return
				}
				if hangUpEarly {
					return // drop the connection mid-stream
				}
				// Drain chunks until the zero-length terminator.
				for {
					var size [4]byte
					if _, err := io.ReadFull(r, size[:]); err != nil {
						return
					}
					n := binary.BigEndian.Uint32(size[:])
					if n == 0 {
						break
					}
					if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
						return
					}
				}
				_, _ = conn.Write([]byte(reply + "\x00"))
			}()
		}
	}()
	return "tcp://" + ln.Addr().String()
}

// The whole point of the package: Clean is reachable only by a completed scan.
// Every other outcome is Unavailable, never Clean.
func TestScan_NeverReportsCleanWithoutScanning(t *testing.T) {
	ctx := context.Background()
	payload := []byte("some file contents")

	t.Run("no scanner configured", func(t *testing.T) {
		got := antivirus.New("").Scan(ctx, payload)
		require.Equal(t, antivirus.Unavailable, got.Verdict,
			"an unconfigured scanner must not vouch for anything")
		require.ErrorIs(t, got.Reason, antivirus.ErrNotConfigured)
	})

	t.Run("nothing listening", func(t *testing.T) {
		got := antivirus.New("tcp://127.0.0.1:1").Scan(ctx, payload)
		require.Equal(t, antivirus.Unavailable, got.Verdict)
		require.Error(t, got.Reason)
	})

	t.Run("daemon hangs up mid-stream", func(t *testing.T) {
		addr := fakeClamd(t, "", true)
		got := antivirus.New(addr).Scan(ctx, payload)
		require.Equal(t, antivirus.Unavailable, got.Verdict,
			"a conversation that failed part-way is not a clean file")
	})

	t.Run("daemon replies with an error", func(t *testing.T) {
		addr := fakeClamd(t, "stream: ERROR size limit exceeded", false)
		got := antivirus.New(addr).Scan(ctx, payload)
		require.Equal(t, antivirus.Unavailable, got.Verdict,
			"a reply we cannot parse is a scan we cannot vouch for")
	})

	t.Run("daemon replies with gibberish", func(t *testing.T) {
		addr := fakeClamd(t, "what", false)
		got := antivirus.New(addr).Scan(ctx, payload)
		require.Equal(t, antivirus.Unavailable, got.Verdict)
	})
}

func TestScan_ReadsARealVerdict(t *testing.T) {
	ctx := context.Background()

	t.Run("clean", func(t *testing.T) {
		addr := fakeClamd(t, "stream: OK", false)
		got := antivirus.New(addr).Scan(ctx, []byte("harmless"))
		require.Equal(t, antivirus.Clean, got.Verdict)
		require.Empty(t, got.Virus)
	})

	t.Run("infected, and names it", func(t *testing.T) {
		addr := fakeClamd(t, "stream: Eicar-Test-Signature FOUND", false)
		got := antivirus.New(addr).Scan(ctx, []byte("nasty"))
		require.Equal(t, antivirus.Infected, got.Verdict)
		require.Equal(t, "Eicar-Test-Signature", got.Virus)
	})

	// Larger than one 4 KB chunk, so the chunking loop is exercised rather
	// than assumed.
	t.Run("a file spanning several chunks", func(t *testing.T) {
		addr := fakeClamd(t, "stream: OK", false)
		big := make([]byte, 40*1024)
		for i := range big {
			big[i] = byte(i)
		}
		got := antivirus.New(addr).Scan(ctx, big)
		require.Equal(t, antivirus.Clean, got.Verdict)
	})
}

// Ping is how the admin UI learns the scanner is degraded without waiting for
// an upload to fail.
func TestPing(t *testing.T) {
	ctx := context.Background()

	t.Run("a live daemon answers", func(t *testing.T) {
		addr := fakeClamd(t, "stream: OK", false)
		require.NoError(t, antivirus.New(addr).Ping(ctx))
	})

	t.Run("nothing listening", func(t *testing.T) {
		require.Error(t, antivirus.New("tcp://127.0.0.1:1").Ping(ctx))
	})

	t.Run("unconfigured", func(t *testing.T) {
		require.ErrorIs(t, antivirus.New("").Ping(ctx), antivirus.ErrNotConfigured)
	})
}

func TestValidPolicy(t *testing.T) {
	for _, ok := range []string{"off", "required", "permissive"} {
		require.True(t, antivirus.ValidPolicy(ok), ok)
	}
	for _, bad := range []string{"", "REQUIRED", "on", "strict", "enabled"} {
		require.False(t, antivirus.ValidPolicy(bad), bad)
	}
}

// A dial that outlives its context must not hang an upload indefinitely.
func TestScan_RespectsAContextThatIsAlreadyDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := antivirus.New("tcp://192.0.2.1:3310").Scan(ctx, []byte("x"))
	require.Equal(t, antivirus.Unavailable, got.Verdict)
}
