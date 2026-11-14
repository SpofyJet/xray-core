package quic

// Spofy regression tests, see SPOFY.md.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"testing"
	"testing/synctest"
	"time"

	"github.com/apernet/quic-go/internal/monotime"
	"github.com/apernet/quic-go/internal/protocol"
	"github.com/apernet/quic-go/internal/wire"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// A Write that is blocked behind an already queued frame must fail when the stream is reset,
// and the stream must complete once the RESET_STREAM frame is acknowledged.
// Before the fix the blocked Write of up to one packet of data returned success, left its data
// queued on the reset stream forever, and the stream never completed.
func TestSendStreamResetDuringBlockedSmallWrite(t *testing.T) {
	const streamID protocol.StreamID = 4
	for _, tc := range []struct {
		name  string
		reset func(*SendStream)
		want  *StreamError
	}{
		{
			name: "STOP_SENDING from the peer",
			reset: func(s *SendStream) {
				s.handleStopSendingFrame(&wire.StopSendingFrame{StreamID: streamID, ErrorCode: 7})
			},
			want: &StreamError{StreamID: streamID, ErrorCode: 7, Remote: true},
		},
		{
			name:  "local CancelWrite",
			reset: func(s *SendStream) { s.CancelWrite(7) },
			want:  &StreamError{StreamID: streamID, ErrorCode: 7, Remote: false},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mockCtrl := gomock.NewController(t)
				mockSender := NewMockStreamSender(mockCtrl)
				fc := newTestStreamFlowControllerWithSendWindow(streamID, protocol.MaxByteCount)
				str := newSendStream(context.Background(), streamID, mockSender, fc, false)

				// 1000 bytes are copied into the next frame; the connection has not sent it yet
				mockSender.EXPECT().onHasStreamData(streamID, str).AnyTimes()
				n, err := str.Write(make([]byte, 1000))
				require.NoError(t, err)
				require.Equal(t, 1000, n)

				// the next 1000 bytes don't fit into that frame: Write blocks
				type result struct {
					n   int
					err error
				}
				done := make(chan result, 1)
				go func() {
					n, err := str.Write(make([]byte, 1000))
					done <- result{n, err}
				}()
				synctest.Wait()
				select {
				case <-done:
					t.Fatal("Write should be blocked")
				default:
				}

				mockSender.EXPECT().onHasStreamControlFrame(streamID, str)
				tc.reset(str)
				synctest.Wait()

				select {
				case r := <-done:
					require.ErrorIs(t, r.err, tc.want)
					require.Zero(t, r.n)
				default:
					t.Fatal("Write should have returned")
				}

				// no data stays queued on the reset stream
				f, _, _ := str.popStreamFrame(protocol.MaxByteCount, protocol.Version1)
				require.Nil(t, f.Frame)

				// acknowledging the RESET_STREAM frame completes the stream
				cf, ok, _ := str.getControlFrame(monotime.Now())
				require.True(t, ok)
				var completed bool
				mockSender.EXPECT().onStreamCompleted(streamID).Do(func(protocol.StreamID) { completed = true }).MaxTimes(1)
				cf.Handler.OnAcked(cf.Frame)
				require.True(t, completed, "the send stream never completes: it leaks until the connection is closed")
			})
		})
	}
}

func spofyTestTLSConfigs(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	server = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:   []string{"spofy-test"},
	}
	client = &tls.Config{InsecureSkipVerify: true, ServerName: "localhost", NextProtos: []string{"spofy-test"}}
	return server, client
}

// What a proxy user sees: every stream the peer resets while the server is blocked in a small Write
// used to cost one stream credit for the rest of the connection. With MaxIncomingStreams = 4 the
// client could not open a fifth stream after four such resets (in production the limit is 1024 per
// Hysteria connection: new TCP connections through the tunnel hang until the client reconnects).
func TestSpofyStreamCreditSurvivesResetDuringBlockedWrite(t *testing.T) {
	const (
		maxStreams = 4
		rounds     = 40
		chunk      = 1000
	)
	serverTLS, clientTLS := spofyTestTLSConfigs(t)

	serverTr := &Transport{Conn: newUDPConnLocalhost(t)}
	defer serverTr.Close()
	ln, err := serverTr.Listen(serverTLS, &Config{MaxIncomingStreams: maxStreams})
	require.NoError(t, err)
	defer ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	blocked := make(chan struct{}, rounds) // the handler is about to block in Write
	handlersDone := make(chan struct{})
	go func() {
		defer close(handlersDone)
		conn, err := ln.Accept(ctx)
		if err != nil {
			return
		}
		for {
			str, err := conn.AcceptStream(ctx)
			if err != nil {
				return
			}
			go func() {
				// the way a relay handles a stream: copy until an error, then tear the stream down
				if _, err := io.ReadFull(str, make([]byte, 1)); err != nil {
					return
				}
				for i := 0; ; i++ {
					// The first two writes fit into the client's 1500 byte stream window and the frame
					// buffer. The third one blocks behind the 500 bytes that are still queued.
					if i == 2 {
						blocked <- struct{}{}
					}
					if _, err := str.Write(make([]byte, chunk)); err != nil {
						break
					}
				}
				str.CancelRead(0)
				str.CancelWrite(0)
			}()
		}
	}()

	clientTr := &Transport{Conn: newUDPConnLocalhost(t)}
	defer clientTr.Close()
	// a small stream window: the server gets flow control blocked after the first packets
	conn, err := clientTr.Dial(ctx, serverTr.Conn.LocalAddr(), clientTLS, &Config{
		InitialStreamReceiveWindow: 1500,
		MaxStreamReceiveWindow:     1500,
	})
	require.NoError(t, err)

	for i := range rounds {
		openCtx, openCancel := context.WithTimeout(ctx, 5*time.Second)
		str, err := conn.OpenStreamSync(openCtx)
		openCancel()
		require.NoErrorf(t, err, "cannot open stream %d: the server leaked its %d stream credits", i+1, maxStreams)
		_, err = str.Write([]byte{1})
		require.NoError(t, err)
		select {
		case <-blocked:
		case <-ctx.Done():
			t.Fatal("timeout waiting for the server handler")
		}
		time.Sleep(scaleDuration(20 * time.Millisecond)) // let the handler block in Write
		// the user closes the page: the client gives up on the stream without reading it
		str.CancelRead(0)
		str.CancelWrite(0)
	}

	conn.CloseWithError(0, "")
	select {
	case <-handlersDone:
	case <-ctx.Done():
		t.Fatal("timeout waiting for the server to shut down")
	}
}
