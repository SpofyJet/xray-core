package cnc

import (
	"io"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/signal/done"
)

type ConnectionOption func(*Connection)

func ConnectionLocalAddr(a net.Addr) ConnectionOption {
	return func(c *Connection) {
		c.local = a
	}
}

func ConnectionRemoteAddr(a net.Addr) ConnectionOption {
	return func(c *Connection) {
		c.remote = a
	}
}

func ConnectionInput(writer io.Writer) ConnectionOption {
	return func(c *Connection) {
		c.writer = buf.NewWriter(writer)
		// Spofy: a plain io.Writer is driven one buffer at a time (buf.SequentialWriter) and
		// must not keep the bytes after Write returns, so Connection.Write can pass the
		// caller's bytes to it as they are instead of copying them into pooled buffers first.
		if _, ok := c.writer.(*buf.SequentialWriter); ok {
			c.direct = writer
		}
	}
}

func ConnectionInputMulti(writer buf.Writer) ConnectionOption {
	return func(c *Connection) {
		c.writer = writer
	}
}

func ConnectionOutput(reader io.Reader) ConnectionOption {
	return func(c *Connection) {
		c.reader = &buf.BufferedReader{Reader: buf.NewReader(reader)}
	}
}

func ConnectionOutputMulti(reader buf.Reader) ConnectionOption {
	return func(c *Connection) {
		c.reader = &buf.BufferedReader{Reader: reader}
	}
}

func ConnectionOutputMultiUDP(reader buf.Reader) ConnectionOption {
	return func(c *Connection) {
		c.reader = &buf.BufferedReader{
			Reader:   reader,
			Splitter: buf.SplitFirstBytes,
		}
	}
}

func ConnectionOnClose(n io.Closer) ConnectionOption {
	return func(c *Connection) {
		c.onClose = n
	}
}

func NewConnection(opts ...ConnectionOption) net.Conn {
	c := &Connection{
		done: done.New(),
		local: &net.TCPAddr{
			IP:   []byte{0, 0, 0, 0},
			Port: 0,
		},
		remote: &net.TCPAddr{
			IP:   []byte{0, 0, 0, 0},
			Port: 0,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

type Connection struct {
	reader  *buf.BufferedReader
	writer  buf.Writer
	direct  io.Writer // set by ConnectionInput when writer is a buf.SequentialWriter around it
	done    *done.Instance
	onClose io.Closer
	local   net.Addr
	remote  net.Addr
}

func (c *Connection) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

// ReadMultiBuffer implements buf.Reader.
func (c *Connection) ReadMultiBuffer() (buf.MultiBuffer, error) {
	return c.reader.ReadMultiBuffer()
}

// Write implements net.Conn.Write().
func (c *Connection) Write(b []byte) (int, error) {
	if c.done.Done() {
		return 0, io.ErrClosedPipe
	}

	if c.direct != nil {
		// Same chunks as buf.MergeBytes + buf.SequentialWriter produce (one Write per
		// buf.Size bytes), without the copy. With inbound stats enabled every payload buffer
		// of a gRPC stream comes through here (stat.CounterConnection.Write).
		written := 0
		for len(b) > 0 {
			n := len(b)
			if n > buf.Size {
				n = buf.Size
			}
			if _, err := c.direct.Write(b[:n]); err != nil {
				return written, err
			}
			written += n
			b = b[n:]
		}
		return written, nil
	}

	l := len(b)
	mb := make(buf.MultiBuffer, 0, l/buf.Size+1)
	mb = buf.MergeBytes(mb, b)
	return l, c.writer.WriteMultiBuffer(mb)
}

func (c *Connection) WriteMultiBuffer(mb buf.MultiBuffer) error {
	if c.done.Done() {
		buf.ReleaseMulti(mb)
		return io.ErrClosedPipe
	}

	return c.writer.WriteMultiBuffer(mb)
}

// Close implements net.Conn.Close().
func (c *Connection) Close() error {
	common.Must(c.done.Close())
	common.Interrupt(c.reader)
	common.Close(c.writer)
	if c.onClose != nil {
		return c.onClose.Close()
	}

	return nil
}

// LocalAddr implements net.Conn.LocalAddr().
func (c *Connection) LocalAddr() net.Addr {
	return c.local
}

// RemoteAddr implements net.Conn.RemoteAddr().
func (c *Connection) RemoteAddr() net.Addr {
	return c.remote
}

// SetDeadline implements net.Conn.SetDeadline().
func (c *Connection) SetDeadline(t time.Time) error {
	return nil
}

// SetReadDeadline implements net.Conn.SetReadDeadline().
func (c *Connection) SetReadDeadline(t time.Time) error {
	return nil
}

// SetWriteDeadline implements net.Conn.SetWriteDeadline().
func (c *Connection) SetWriteDeadline(t time.Time) error {
	return nil
}
