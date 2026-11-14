package quic

// Spofy regression tests, see SPOFY.md.

import (
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/apernet/quic-go/internal/monotime"
	"github.com/apernet/quic-go/internal/protocol"
	"github.com/apernet/quic-go/internal/wire"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// With DisablePathManager (Hysteria 2 port hopping) a server follows the address of the client
// without path validation. It must only replace the remote address when the address changed:
// replacing it on every received packet allocates on every packet, and takes away the spare room for
// the UDP_SEGMENT control message, so that every send allocates as well.
// A client never follows the address of received packets.
func TestConnectionDisablePathManagerRemoteAddr(t *testing.T) {
	newRemoteAddr := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 1), Port: 1234}

	for _, tc := range []struct {
		name          string
		server        bool
		fromNewAddr   bool
		expectChanges int
	}{
		{name: "server, packets from the known address", server: true, expectChanges: 0},
		{name: "server, packet from a new address", server: true, fromNewAddr: true, expectChanges: 1},
		{name: "client, packets from the known address", expectChanges: 0},
		{name: "client, packet from a new address", fromNewAddr: true, expectChanges: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mockCtrl := gomock.NewController(t)
				unpacker := NewMockUnpacker(mockCtrl)
				conf := &Config{DisablePathMTUDiscovery: true, DisablePathManager: true}
				opts := []testConnectionOpt{
					connectionOptUnpacker(unpacker),
					connectionOptHandshakeConfirmed(),
					connectionOptRTT(time.Second),
				}
				var c *testConnection
				if tc.server {
					c = newServerTestConnection(t, mockCtrl, conf, false, opts...)
				} else {
					c = newClientTestConnection(t, mockCtrl, conf, false, opts...)
				}
				params := &wire.TransportParameters{
					MaxUDPPayloadSize:         1456,
					InitialSourceConnectionID: protocol.ZeroLengthConnectionID,
				}
				if !tc.server {
					params.InitialSourceConnectionID = c.destConnID
					params.OriginalDestinationConnectionID = c.destConnID
					params.ActiveConnectionIDLimit = 1000
				}
				require.NoError(t, c.conn.handleTransportParameters(params))

				errChan := make(chan error, 1)
				go func() { errChan <- c.conn.run() }()

				var changes []net.Addr
				c.sendConn.EXPECT().ChangeRemoteAddr(gomock.Any(), gomock.Any()).Do(
					func(addr net.Addr, _ packetInfo) { changes = append(changes, addr) },
				).AnyTimes()
				c.packer.EXPECT().AppendPacket(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(
					shortHeaderPacket{}, errNothingToPack,
				).AnyTimes()

				// two packets from the address the connection was established with
				from := []net.Addr{c.remoteAddr, c.remoteAddr}
				if tc.fromNewAddr {
					from = append(from, newRemoteAddr)
				}
				for i, addr := range from {
					unpacker.EXPECT().UnpackShortHeader(gomock.Any(), gomock.Any()).Return(
						protocol.PacketNumber(10+i), protocol.PacketNumberLen2, protocol.KeyPhaseZero, []byte{1} /* PING */, nil,
					)
					c.conn.handlePacket(receivedPacket{
						data:       make([]byte, 100),
						buffer:     getPacketBuffer(),
						remoteAddr: addr,
						rcvTime:    monotime.Now(),
					})
					synctest.Wait()
				}

				assert.Len(t, changes, tc.expectChanges, "calls to ChangeRemoteAddr")
				for _, addr := range changes {
					assert.Equal(t, newRemoteAddr, addr)
				}
				// no path validation state is kept when the path manager is disabled
				assert.Nil(t, c.conn.pathManager)

				// test teardown
				c.connRunner.EXPECT().Remove(gomock.Any()).AnyTimes()
				c.conn.destroy(nil)
				synctest.Wait()
				select {
				case err := <-errChan:
					require.NoError(t, err)
				default:
					t.Fatal("should have shut down")
				}
			})
		})
	}
}
