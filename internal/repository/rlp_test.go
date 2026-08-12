package repository

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

// The vectors below come from the RLP specification. They are the ground truth
// for this encoder: a transaction encoded even slightly wrong is rejected by
// the node, or worse, signs a payload that does not match what is broadcast.
func TestRLPString_SpecVectors(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{name: "empty string", input: nil, want: "80"},
		{name: "single low byte is itself", input: []byte{0x0f}, want: "0f"},
		{name: "single byte at the boundary", input: []byte{0x7f}, want: "7f"},
		{name: "0x80 needs a header", input: []byte{0x80}, want: "8180"},
		{name: "short string", input: []byte("dog"), want: "83646f67"},
		{
			name:  "55 bytes is still short form",
			input: bytes.Repeat([]byte{0x61}, 55),
			want:  "b7" + strings.Repeat("61", 55),
		},
		{
			name:  "56 bytes switches to long form",
			input: bytes.Repeat([]byte{0x61}, 56),
			want:  "b838" + strings.Repeat("61", 56),
		},
		{
			name:  "1024 bytes uses a two-byte length",
			input: bytes.Repeat([]byte{0x61}, 1024),
			want:  "b90400" + strings.Repeat("61", 1024),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hex.EncodeToString(rlpString(tt.input)); got != tt.want {
				t.Errorf("rlpString = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRLPList_SpecVectors(t *testing.T) {
	tests := []struct {
		name  string
		items [][]byte
		want  string
	}{
		{name: "empty list", want: "c0"},
		{
			name:  "list of two strings",
			items: [][]byte{rlpString([]byte("cat")), rlpString([]byte("dog"))},
			want:  "c88363617483646f67",
		},
		{
			name:  "long list switches to the extended header",
			items: [][]byte{rlpString(bytes.Repeat([]byte{0x61}, 56))},
			want:  "f83a" + "b838" + strings.Repeat("61", 56),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hex.EncodeToString(rlpList(tt.items...)); got != tt.want {
				t.Errorf("rlpList = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRLPUint(t *testing.T) {
	tests := []struct {
		name  string
		input uint64
		want  string
	}{
		// RLP has no zero: it is the empty string. Encoding it as 0x00 would
		// produce a different transaction hash than every other client.
		{name: "zero is the empty string", input: 0, want: "80"},
		{name: "fifteen", input: 15, want: "0f"},
		{name: "one twenty eight needs a header", input: 128, want: "8180"},
		{name: "1024", input: 1024, want: "820400"},
		{name: "max uint64", input: ^uint64(0), want: "88ffffffffffffffff"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hex.EncodeToString(rlpUint(tt.input)); got != tt.want {
				t.Errorf("rlpUint(%d) = %s, want %s", tt.input, got, tt.want)
			}
		})
	}
}

func TestRLPBig(t *testing.T) {
	tests := []struct {
		name  string
		input *big.Int
		want  string
	}{
		{name: "nil is the empty string", input: nil, want: "80"},
		{name: "zero is the empty string", input: big.NewInt(0), want: "80"},
		{name: "small value", input: big.NewInt(15), want: "0f"},
		{name: "multi-byte value", input: big.NewInt(1024), want: "820400"},
		{
			name:  "value wider than uint64",
			input: new(big.Int).Lsh(big.NewInt(1), 72),
			want:  "8a01000000000000000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hex.EncodeToString(rlpBig(tt.input)); got != tt.want {
				t.Errorf("rlpBig = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestBigEndianBytes_HasNoLeadingZeros(t *testing.T) {
	if got := bigEndianBytes(0); got != nil {
		t.Errorf("bigEndianBytes(0) = %v, want nil", got)
	}
	if got := hex.EncodeToString(bigEndianBytes(1)); got != "01" {
		t.Errorf("bigEndianBytes(1) = %s, want 01", got)
	}
	if got := hex.EncodeToString(bigEndianBytes(256)); got != "0100" {
		t.Errorf("bigEndianBytes(256) = %s, want 0100", got)
	}
}
