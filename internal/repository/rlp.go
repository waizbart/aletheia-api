package repository

import "math/big"

// Minimal RLP encoder, enough to build and sign an EIP-155 legacy transaction.
//
// Pulling in a full Ethereum client for this would add a very large dependency
// tree to encode six integers and two byte strings. RLP's rules fit in this
// file, and the transaction shape the anchor worker sends never varies.

// rlpString encodes a byte string.
func rlpString(b []byte) []byte {
	if len(b) == 1 && b[0] < 0x80 {
		return b
	}
	return append(rlpLengthPrefix(len(b), 0x80), b...)
}

// rlpList encodes an already-encoded sequence of items as a list.
func rlpList(items ...[]byte) []byte {
	var payload []byte
	for _, item := range items {
		payload = append(payload, item...)
	}
	return append(rlpLengthPrefix(len(payload), 0xc0), payload...)
}

// rlpLengthPrefix builds the header for a payload of the given length. offset
// is 0x80 for strings and 0xc0 for lists.
func rlpLengthPrefix(length, offset int) []byte {
	if length <= 55 {
		return []byte{byte(offset + length)}
	}
	size := bigEndianBytes(uint64(length))
	return append([]byte{byte(offset + 55 + len(size))}, size...)
}

// rlpUint encodes an integer. RLP has no zero: it is the empty string, which is
// why a zero-value transfer and an unset field encode identically.
func rlpUint(v uint64) []byte {
	return rlpString(bigEndianBytes(v))
}

// rlpBig encodes an arbitrary-precision integer, used for gas price and the
// signature components.
func rlpBig(v *big.Int) []byte {
	if v == nil || v.Sign() == 0 {
		return rlpString(nil)
	}
	return rlpString(v.Bytes())
}

// bigEndianBytes renders v with no leading zero bytes. Zero renders empty.
func bigEndianBytes(v uint64) []byte {
	if v == 0 {
		return nil
	}
	var buf [8]byte
	for i := 7; i >= 0; i-- {
		buf[i] = byte(v)
		v >>= 8
	}
	start := 0
	for start < 8 && buf[start] == 0 {
		start++
	}
	return buf[start:]
}
