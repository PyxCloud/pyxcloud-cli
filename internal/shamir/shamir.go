package shamir

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// GF(256) lookup tables with primitive polynomial 0x11D (x^8+x^4+x^3+x^2+1).
// Matches the frontend shamir.js implementation exactly.
var expTable [256]byte
var logTable [256]byte

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		expTable[i] = byte(x)
		logTable[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
}

func add(a, b byte) byte { return a ^ b }

func mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return expTable[(int(logTable[a])+int(logTable[b]))%255]
}

func div(a, b byte) byte {
	if a == 0 {
		return 0
	}
	return expTable[(int(logTable[a])-int(logTable[b])+255)%255]
}

// Combine reconstructs a secret from two Shamir shares.
// Shares are in format "id-hexdata" (e.g. "1-abcdef...", "2-012345...").
// Returns the secret as a hex string.
func Combine(share1, share2 string) (string, error) {
	id0, bytes0, err := parseShare(share1)
	if err != nil {
		return "", fmt.Errorf("parse share 1: %w", err)
	}
	id1, bytes1, err := parseShare(share2)
	if err != nil {
		return "", fmt.Errorf("parse share 2: %w", err)
	}

	if len(bytes0) != len(bytes1) {
		return "", fmt.Errorf("share length mismatch: %d vs %d", len(bytes0), len(bytes1))
	}

	x0 := byte(id0)
	x1 := byte(id1)

	denominator := add(x0, x1)
	l0 := div(x1, denominator) // L0(0) = x1 / (x0 + x1)
	l1 := div(x0, denominator) // L1(0) = x0 / (x0 + x1)

	secret := make([]byte, len(bytes0))
	for i := range bytes0 {
		term0 := mul(bytes0[i], l0)
		term1 := mul(bytes1[i], l1)
		secret[i] = add(term0, term1)
	}

	return hex.EncodeToString(secret), nil
}

// parseShare splits "id-hexdata" into the numeric id and byte slice.
func parseShare(s string) (int, []byte, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, nil, fmt.Errorf("invalid share format (expected id-hexdata): %q", s)
	}
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, nil, fmt.Errorf("invalid share id: %w", err)
	}
	data, err := hex.DecodeString(parts[1])
	if err != nil {
		return 0, nil, fmt.Errorf("invalid hex data: %w", err)
	}
	return id, data, nil
}

// Hex2Str converts a hex string to a regular string.
// Backwards compatible with the legacy secrets.js hex2str format:
// reads 4-hex-char blocks from the end, each representing a Unicode code point.
func Hex2Str(h string) string {
	var sb strings.Builder
	for i := len(h); i >= 4; i -= 4 {
		code, err := strconv.ParseInt(h[i-4:i], 16, 32)
		if err != nil {
			break
		}
		sb.WriteRune(rune(code))
	}
	return sb.String()
}
