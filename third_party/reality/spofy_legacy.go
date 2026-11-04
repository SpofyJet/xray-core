package reality

// [REALITY] SPOFY: helpers for the requireMlkem patch (see SPOFY.md).

// isLegacyX25519Hello reports whether a TLS 1.3 Client Hello is a "legacy" hello that upstream REALITY
// (since 8cdf7bf9) rejects only because it lacks X25519MLKEM768: the hybrid group appears in neither
// supported_groups nor key_share, and there is exactly one well-formed X25519 key share.
// Hellos that offer the hybrid group anywhere are not legacy and stay under upstream's ordering rules.
func isLegacyX25519Hello(ch *clientHelloMsg) bool {
	if ch == nil {
		return false
	}
	for _, c := range ch.supportedCurves {
		if c == X25519MLKEM768 {
			return false
		}
	}
	x25519 := 0
	for _, ks := range ch.keyShares {
		switch ks.group {
		case X25519MLKEM768:
			return false
		case X25519:
			if len(ks.data) != 32 {
				return false
			}
			x25519++
		}
	}
	return x25519 == 1
}
