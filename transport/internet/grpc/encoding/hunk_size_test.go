package encoding_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/proxy"
	. "github.com/xtls/xray-core/transport/internet/grpc/encoding"
)

type fakeHunks struct {
	hunks [][]byte
	multi bool
}

func (f *fakeHunks) Context() context.Context  { return context.Background() }
func (f *fakeHunks) Send(*Hunk) error          { return nil }
func (f *fakeHunks) SendMsg(interface{}) error { return nil }
func (f *fakeHunks) RecvMsg(interface{}) error { return nil }
func (f *fakeHunks) CloseSend() error          { return nil }
func (f *fakeHunks) next() ([]byte, error) {
	if len(f.hunks) == 0 {
		return nil, io.EOF
	}
	h := f.hunks[0]
	f.hunks = f.hunks[1:]
	return h, nil
}

func (f *fakeHunks) Recv() (*Hunk, error) {
	h, err := f.next()
	if err != nil {
		return nil, err
	}
	return &Hunk{Data: h}, nil
}

type fakeMulti struct{ fakeHunks }

func (f *fakeMulti) Send(*MultiHunk) error { return nil }
func (f *fakeMulti) Recv() (*MultiHunk, error) {
	h, err := f.next()
	if err != nil {
		return nil, err
	}
	return &MultiHunk{Data: [][]byte{h}}, nil
}

func clone(hs [][]byte) [][]byte {
	var out [][]byte
	for _, h := range hs {
		out = append(out, append([]byte(nil), h...))
	}
	return out
}

func payload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func drain(t *testing.T, r buf.Reader) []byte {
	var out bytes.Buffer
	uuid := make([]byte, 16)
	for {
		mb, err := r.ReadMultiBuffer()
		for _, b := range mb {
			if b.Len() > buf.Size {
				t.Fatalf("buffer of %d bytes, larger than buf.Size", b.Len())
			}
		}
		for _, b := range proxy.ReshapeMultiBuffer(context.Background(), mb) {
			out.Write(b.Bytes())
			proxy.XtlsPadding(b, 0, &uuid, true, context.Background(), []uint32{900, 500, 900, 256}).Release()
		}
		if err != nil {
			return out.Bytes()
		}
	}
}

func TestHunkReaderBufferSizeForVision(t *testing.T) {
	for _, size := range []int{4096, 8192, 12288, 16384, 32768} {
		var hunks [][]byte
		var want []byte
		for i := 0; i < 8; i++ {
			p := payload(size)
			p[0] = byte(i)
			hunks = append(hunks, p)
			want = append(want, p...)
		}
		got := drain(t, NewHunkReadWriter(&fakeHunks{hunks: clone(hunks)}, func() {}))
		if !bytes.Equal(got, want) {
			t.Fatalf("hunk %d: got %d bytes, want %d", size, len(got), len(want))
		}
		got = drain(t, NewMultiHunkReadWriter(&fakeMulti{fakeHunks{hunks: clone(hunks)}}, func() {}))
		if !bytes.Equal(got, want) {
			t.Fatalf("multi hunk %d: got %d bytes, want %d", size, len(got), len(want))
		}
	}
}
