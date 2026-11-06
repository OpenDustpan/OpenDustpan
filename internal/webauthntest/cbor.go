package webauthntest

import (
	"encoding/binary"
	"math"
)

// The major types of CBOR that the authenticator writes.
const (
	cborUnsigned byte = 0 << 5
	cborNegative byte = 1 << 5
	cborByteText byte = 2 << 5
	cborUTF8Text byte = 3 << 5
	cborMapType  byte = 5 << 5
)

// The limits of the length forms of a CBOR head.
const (
	cborInline    = 24
	cborOneByte   = 0x18
	cborTwoBytes  = 0x19
	cborMaxOne    = 0xff
	cborMaxTwo    = 0xffff
	cborFourBytes = 0x1a
	cborEightByte = 0x1b
	cborPair      = 2
)

// cborHead returns the head of the major type major with the argument n.
func cborHead(major byte, n uint64) []byte {
	switch {
	case n < cborInline:
		return []byte{major | byte(n)}
	case n <= cborMaxOne:
		return []byte{major | cborOneByte, byte(n)}
	case n <= cborMaxTwo:
		return binary.BigEndian.AppendUint16([]byte{major | cborTwoBytes}, uint16(n))
	case n <= math.MaxUint32:
		return binary.BigEndian.AppendUint32([]byte{major | cborFourBytes}, uint32(n))
	default:
		return binary.BigEndian.AppendUint64([]byte{major | cborEightByte}, n)
	}
}

// cborInt returns the CBOR of the small integer n.
func cborInt(n int) []byte {
	if n < 0 {
		return cborHead(cborNegative, uint64(-1-n))
	}

	return cborHead(cborUnsigned, uint64(n))
}

// cborBytes returns the CBOR byte string of b.
func cborBytes(b []byte) []byte {
	return append(cborHead(cborByteText, uint64(len(b))), b...)
}

// cborText returns the CBOR text string of s.
func cborText(s string) []byte {
	return append(cborHead(cborUTF8Text, uint64(len(s))), s...)
}

// cborMap returns the CBOR map of the keys and values kv, in pairs.
func cborMap(kv ...[]byte) []byte {
	out := cborHead(cborMapType, uint64(len(kv)/cborPair))
	for _, item := range kv {
		out = append(out, item...)
	}

	return out
}
