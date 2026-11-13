package hysteria

import (
	"testing"
	"time"
)

func TestRetryGate(t *testing.T) {
	t0 := time.Unix(1000, 0)
	g := &retryGate{rate: 50, burst: 200, tokens: 200, last: t0}
	for i := 0; i < 200; i++ {
		if g.verifyAt(t0) {
			t.Fatalf("connection %d inside the burst must not need Retry", i)
		}
	}
	if !g.verifyAt(t0) {
		t.Fatal("past the burst the client must pass Retry")
	}
	// 100 ms later the bucket holds 5 tokens again
	t1 := t0.Add(100 * time.Millisecond)
	for i := 0; i < 5; i++ {
		if g.verifyAt(t1) {
			t.Fatalf("refilled token %d must admit without Retry", i)
		}
	}
	if !g.verifyAt(t1) {
		t.Fatal("refill must be limited by the rate")
	}
	// a long quiet period never stores more than the burst
	t2 := t1.Add(time.Hour)
	n := 0
	for !g.verifyAt(t2) {
		n++
	}
	if n != 200 {
		t.Fatalf("after idle the gate admitted %d, want 200", n)
	}
	// a clock step backwards must not add or remove tokens
	if !g.verifyAt(t0) {
		t.Fatal("clock going back must not refill")
	}
}
