package reality

import (
	"crypto/mlkem"
	"testing"
)

func TestSpofyIsLegacyX25519Hello(t *testing.T) {
	x := keyShare{group: X25519, data: make([]byte, 32)}
	hybrid := keyShare{group: X25519MLKEM768, data: make([]byte, mlkem.EncapsulationKeySize768+32)}
	grease := keyShare{group: CurveID(0x9a9a), data: []byte{0}}

	cases := []struct {
		name string
		ch   *clientHelloMsg
		want bool
	}{
		{"nil", nil, false},
		// Edge 85 / iOS / QQ uTLS fingerprints: GREASE + X25519 only.
		{"edge85-like", &clientHelloMsg{supportedCurves: []CurveID{0x9a9a, X25519, CurveP256}, keyShares: []keyShare{grease, x}}, true},
		{"single x25519", &clientHelloMsg{supportedCurves: []CurveID{X25519}, keyShares: []keyShare{x}}, true},
		{"no key share", &clientHelloMsg{supportedCurves: []CurveID{X25519}}, false},
		{"two x25519 shares", &clientHelloMsg{supportedCurves: []CurveID{X25519}, keyShares: []keyShare{x, x}}, false},
		{"bad x25519 length", &clientHelloMsg{supportedCurves: []CurveID{X25519}, keyShares: []keyShare{{group: X25519, data: make([]byte, 31)}}}, false},
		// Hybrid offered anywhere -> not legacy, upstream rules apply.
		{"hybrid share", &clientHelloMsg{supportedCurves: []CurveID{X25519MLKEM768, X25519}, keyShares: []keyShare{hybrid, x}}, false},
		{"hybrid share after x25519", &clientHelloMsg{supportedCurves: []CurveID{X25519, X25519MLKEM768}, keyShares: []keyShare{x, hybrid}}, false},
		{"hybrid only in supported_groups", &clientHelloMsg{supportedCurves: []CurveID{X25519MLKEM768, X25519}, keyShares: []keyShare{x}}, false},
	}
	for _, c := range cases {
		if got := isLegacyX25519Hello(c.ch); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
