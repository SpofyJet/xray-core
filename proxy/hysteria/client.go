package hysteria

import (
	"context"
	go_errors "errors"
	"io"
	"math/rand"

	"github.com/apernet/quic-go"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/hysteria"
	"github.com/xtls/xray-core/transport/internet/stat"
)

type Client struct {
	server        *protocol.ServerSpec
	policyManager policy.Manager
}

func NewClient(ctx context.Context, config *ClientConfig) (*Client, error) {
	v := core.MustFromContext(ctx)
	p := v.GetFeature(policy.ManagerType()).(policy.Manager)

	streamSettings := session.StreamSettingsFromContext(ctx).(*internet.MemoryStreamConfig)
	if _, ok := streamSettings.ProtocolSettings.(*hysteria.Config); !ok {
		return nil, errors.New("not hysteria transport")
	}
	if config.Server == nil {
		return nil, errors.New(`no target server found`)
	}
	server, err := protocol.NewServerSpecFromPB(config.Server)
	if err != nil {
		return nil, errors.New("failed to get server spec").Base(err)
	}

	return &Client{
		server:        server,
		policyManager: p,
	}, nil
}

func (c *Client) Process(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
	outbounds := session.OutboundsFromContext(ctx)
	ob := outbounds[len(outbounds)-1]
	if !ob.Target.IsValid() {
		return errors.New("target not specified")
	}
	ob.Name = "hysteria"
	ob.CanSpliceCopy = 3
	target := ob.Target

	conn, err := dialer.Dial(hysteria.ContextWithDatagram(ctx, target.Network == net.Network_UDP), c.server.Destination)
	if err != nil {
		return errors.New("failed to find an available destination").Base(err)
	}
	defer conn.Close()
	errors.LogInfo(ctx, "tunneling request to ", target, " via ", target.Network, ":", c.server.Destination.NetAddr())

	var newCtx context.Context
	var newCancel context.CancelFunc
	if session.TimeoutOnlyFromContext(ctx) {
		newCtx, newCancel = context.WithCancel(context.Background())
	}

	sessionPolicy := c.policyManager.ForLevel(0)
	ctx, cancel := context.WithCancel(ctx)
	timer := signal.CancelAfterInactivity(ctx, func() {
		cancel()
		if newCancel != nil {
			newCancel()
		}
	}, sessionPolicy.Timeouts.ConnectionIdle)

	if newCtx != nil {
		ctx = newCtx
	}

	if target.Network == net.Network_TCP {
		requestDone := func() error {
			defer timer.SetTimeout(sessionPolicy.Timeouts.DownlinkOnly)
			bufferedWriter := buf.NewBufferedWriter(buf.NewWriter(conn))
			err := WriteTCPRequest(bufferedWriter, target.NetAddr())
			if err != nil {
				return errors.New("failed to write request").Base(err)
			}
			if err := bufferedWriter.SetBuffered(false); err != nil {
				return err
			}
			return buf.Copy(link.Reader, bufferedWriter, buf.UpdateActivity(timer))
		}

		responseDone := func() error {
			defer timer.SetTimeout(sessionPolicy.Timeouts.UplinkOnly)
			ok, msg, err := ReadTCPResponse(conn)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New(msg)
			}
			return buf.Copy(buf.NewReader(conn), link.Writer, buf.UpdateActivity(timer))
		}

		responseDoneAndCloseWriter := task.OnSuccess(responseDone, task.Close(link.Writer))
		if err := task.Run(ctx, requestDone, responseDoneAndCloseWriter); err != nil {
			return errors.New("connection ends").Base(err)
		}

		return nil
	}

	if target.Network == net.Network_UDP {
		iConn := stat.TryUnwrapStatsConn(conn)
		_, ok := iConn.(*hysteria.InterConn)
		if !ok {
			return errors.New("udp requires hysteria udp transport")
		}

		requestDone := func() error {
			defer timer.SetTimeout(sessionPolicy.Timeouts.DownlinkOnly)

			writer := &UDPWriter{
				writer: conn,
				addr:   target.NetAddr(),
			}

			if err := buf.Copy(link.Reader, writer, buf.UpdateActivity(timer)); err != nil {
				return errors.New("failed to transport all UDP request").Base(err)
			}

			return nil
		}

		responseDone := func() error {
			defer timer.SetTimeout(sessionPolicy.Timeouts.UplinkOnly)

			reader := newUDPReader(conn)

			if err := buf.Copy(reader, link.Writer, buf.UpdateActivity(timer)); err != nil {
				return errors.New("failed to transport all UDP response").Base(err)
			}

			return nil
		}

		responseDoneAndCloseWriter := task.OnSuccess(responseDone, task.Close(link.Writer))
		if err := task.Run(ctx, requestDone, responseDoneAndCloseWriter); err != nil {
			return errors.New("connection ends").Base(err)
		}

		return nil
	}

	return nil
}

func init() {
	common.Must(common.RegisterConfig((*ClientConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return NewClient(ctx, config.(*ClientConfig))
	}))
}

type UDPWriter struct {
	writer io.Writer
	addr   string
	buf    [buf.Size]byte

	// Spofy: address string of the last packet destination, so that a flow to one destination does not
	// format it again for every packet (freedom hands over a new Destination with every packet).
	lastDest net.Destination
	lastAddr string
}

func (w *UDPWriter) packetAddr(d *net.Destination) string {
	if w.lastAddr == "" || d.Port != w.lastDest.Port || d.Network != w.lastDest.Network || d.Address != w.lastDest.Address {
		w.lastDest = *d
		w.lastAddr = d.NetAddr()
	}
	return w.lastAddr
}

func (w *UDPWriter) SendMessage(msg *UDPMessage) error {
	msgN := msg.Serialize(w.buf[:])
	if msgN < 0 {
		return nil
	}
	_, err := w.writer.Write(w.buf[:msgN])
	return err
}

func (w *UDPWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	for i, b := range mb {
		addr := w.addr
		if b.UDP != nil {
			addr = w.packetAddr(b.UDP)
		}

		msg := &UDPMessage{
			SessionID: 0,
			PacketID:  0,
			FragID:    0,
			FragCount: 1,
			Addr:      addr,
			Data:      b.Bytes(),
		}

		if err := w.SendMessage(msg); err != nil {
			// errTooLarge lives on the heap (its address goes to errors.As): only on the error path
			var errTooLarge *quic.DatagramTooLargeError
			if !go_errors.As(err, &errTooLarge) {
				buf.ReleaseMulti(mb[i:])
				return err
			}
			msg.PacketID = uint16(rand.Intn(0xFFFF)) + 1
			fMsgs := FragUDPMessage(msg, int(errTooLarge.MaxDatagramPayloadSize))
			for _, fMsg := range fMsgs {
				err := w.SendMessage(&fMsg)
				if err != nil {
					buf.ReleaseMulti(mb[i:])
					return err
				}
			}
		}

		b.Release()
	}

	return nil
}

// packetReader is implemented by hysteria.InterConn: ReadPacket hands over the received datagram itself, which
// Read would copy.
type packetReader interface {
	ReadPacket() ([]byte, error)
}

type UDPReader struct {
	reader   io.Reader
	df       *Defragger
	firstBuf *buf.Buffer

	// Spofy (per-packet overhead, after Hysteria 2.14): datagrams taken without a copy, the message parsed
	// into msg, and the destination of the last address reused while the address does not change.
	packets  packetReader
	counter  stats.Counter
	msg      UDPMessage
	lastAddr string
	lastDest net.Destination
}

func newUDPReader(conn io.Reader) *UDPReader {
	r := &UDPReader{reader: conn, df: &Defragger{}}
	inner := conn
	var counter stats.Counter
	if c, ok := conn.(*stat.CounterConnection); ok {
		// keep counting the inbound/outbound traffic the wrapper counts in Read
		inner, counter = c.Connection, c.ReadCounter
	}
	if p, ok := inner.(packetReader); ok {
		r.packets, r.counter = p, counter
	}
	return r
}

func (r *UDPReader) readPacket() ([]byte, error) {
	if r.packets != nil {
		p, err := r.packets.ReadPacket()
		if err != nil {
			return nil, err
		}
		if r.counter != nil {
			r.counter.Add(int64(len(p)))
		}
		return p, nil
	}
	p := make([]byte, 1500)
	n, err := r.reader.Read(p)
	if err != nil {
		return nil, err
	}
	return p[:n], nil
}

func (r *UDPReader) ReadFrom(p []byte) (n int, addr *net.Destination, err error) {
	for {
		packet, err := r.readPacket()
		if err != nil {
			return 0, nil, err
		}

		if parseUDPMessage(packet, &r.msg, r.lastAddr) != nil {
			continue
		}

		msg := &r.msg
		if msg.FragCount > 1 {
			// the defragger keeps fragments until the whole packet is there, r.msg is reused
			frag := new(UDPMessage)
			*frag = r.msg
			msg = frag
		}
		dfMsg := r.df.Feed(msg)
		if dfMsg == nil {
			continue
		}

		if dfMsg.Addr != r.lastAddr || r.lastAddr == "" {
			dest, err := net.ParseDestination("udp:" + dfMsg.Addr)
			if err != nil {
				continue
			}
			r.lastAddr, r.lastDest = dfMsg.Addr, dest
		}

		if len(p) < len(dfMsg.Data) {
			continue
		}

		dest := r.lastDest
		return copy(p, dfMsg.Data), &dest, nil
	}
}

func (r *UDPReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	if r.firstBuf != nil {
		mb := buf.MultiBuffer{r.firstBuf}
		r.firstBuf = nil
		return mb, nil
	}
	b := buf.New()
	b.Resize(0, buf.Size)
	n, addr, err := r.ReadFrom(b.Bytes())
	if err != nil {
		b.Release()
		return nil, err
	}
	b.Resize(0, int32(n))
	b.UDP = addr
	return buf.MultiBuffer{b}, nil
}
