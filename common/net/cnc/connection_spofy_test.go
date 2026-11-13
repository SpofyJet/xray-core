package cnc_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"testing"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net/cnc"
)

// chunkRecorder is a plain io.Writer (not a buf.Writer, not a syscall.Conn), like the gRPC
// HunkReaderWriter: it records every Write as a separate chunk and keeps its own copy.
type chunkRecorder struct {
	chunks [][]byte
	failAt int // fail on this Write call (1-based), 0 = never
}

func (r *chunkRecorder) Write(b []byte) (int, error) {
	if r.failAt > 0 && len(r.chunks)+1 == r.failAt {
		return 0, errors.New("boom")
	}
	r.chunks = append(r.chunks, append([]byte(nil), b...))
	return len(b), nil
}

// reference: what Connection.Write did before the direct path (MergeBytes + SequentialWriter).
func referenceChunks(b []byte) [][]byte {
	r := &chunkRecorder{}
	mb := buf.MergeBytes(make(buf.MultiBuffer, 0, len(b)/buf.Size+1), b)
	w := &buf.SequentialWriter{Writer: r}
	if err := w.WriteMultiBuffer(mb); err != nil {
		panic(err)
	}
	return r.chunks
}

func TestSpofyDirectWriteKeepsChunking(t *testing.T) {
	for _, size := range []int{0, 1, 100, buf.Size - 1, buf.Size, buf.Size + 1, 3 * buf.Size, 3*buf.Size + 17, 100000} {
		payload := make([]byte, size)
		rand.Read(payload)

		rec := &chunkRecorder{}
		conn := cnc.NewConnection(cnc.ConnectionInput(rec))
		n, err := conn.Write(payload)
		if err != nil || n != size {
			t.Fatalf("size %d: n=%d err=%v", size, n, err)
		}
		want := referenceChunks(payload)
		if len(rec.chunks) != len(want) {
			t.Fatalf("size %d: %d chunks, want %d", size, len(rec.chunks), len(want))
		}
		for i := range want {
			if !bytes.Equal(rec.chunks[i], want[i]) {
				t.Fatalf("size %d: chunk %d differs", size, i)
			}
		}
		if !bytes.Equal(bytes.Join(rec.chunks, nil), payload) {
			t.Fatalf("size %d: payload differs", size)
		}
	}
}

func TestSpofyDirectWriteErrorAndClose(t *testing.T) {
	rec := &chunkRecorder{failAt: 2}
	conn := cnc.NewConnection(cnc.ConnectionInput(rec))
	n, err := conn.Write(make([]byte, 3*buf.Size))
	if err == nil || n != buf.Size {
		t.Fatalf("n=%d err=%v, want n=%d and an error", n, err, buf.Size)
	}

	conn = cnc.NewConnection(cnc.ConnectionInput(&chunkRecorder{}), cnc.ConnectionOutput(bytes.NewReader(nil)))
	conn.Close()
	if _, err := conn.Write([]byte("x")); err != io.ErrClosedPipe {
		t.Fatalf("write after close: %v", err)
	}
}

// A buf.Writer input takes ownership of buffers, so Write must still copy for it.
type mbRecorder struct{ got buf.MultiBuffer }

func (r *mbRecorder) WriteMultiBuffer(mb buf.MultiBuffer) error {
	r.got = append(r.got, mb...)
	return nil
}
func (r *mbRecorder) Write(b []byte) (int, error) { panic("must not be called") }

func TestSpofyMultiBufferInputStillCopies(t *testing.T) {
	rec := &mbRecorder{}
	conn := cnc.NewConnection(cnc.ConnectionInput(rec))
	payload := []byte("hello world")
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	payload[0] = 'X' // caller reuses its slice
	if got := rec.got.String(); got != "hello world" {
		t.Fatalf("got %q", got)
	}
	buf.ReleaseMulti(rec.got)
}

func BenchmarkSpofyConnectionWrite8K(b *testing.B) {
	conn := cnc.NewConnection(cnc.ConnectionInput(io.Discard))
	payload := make([]byte, buf.Size)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		conn.Write(payload)
	}
}

// The path Connection.Write took before: copy into a pooled buffer, then write it out.
func BenchmarkSpofyOldCopyPath8K(b *testing.B) {
	w := &buf.SequentialWriter{Writer: io.Discard}
	payload := make([]byte, buf.Size)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mb := buf.MergeBytes(make(buf.MultiBuffer, 0, 2), payload)
		w.WriteMultiBuffer(mb)
	}
}
