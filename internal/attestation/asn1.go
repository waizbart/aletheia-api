package attestation

import (
	"encoding/asn1"
	"fmt"
)

// The Android attestation extension mixes ENUMERATED values, implicitly tagged
// SETs and explicitly tagged constructed types in ways encoding/asn1's struct
// tags cannot express. Walking the DER as raw elements is both simpler and more
// tolerant of the shape drift between attestation versions, which is why this
// file exists instead of a pile of struct definitions.

// element is one parsed DER TLV.
type element struct {
	Class      int
	Tag        int
	IsCompound bool
	Bytes      []byte // contents, excluding the tag and length header
	FullBytes  []byte // the complete TLV
}

// parseElement reads the first TLV from der, returning it and the remainder.
func parseElement(der []byte) (element, []byte, error) {
	var raw asn1.RawValue
	rest, err := asn1.Unmarshal(der, &raw)
	if err != nil {
		return element{}, nil, fmt.Errorf("asn1: %w", err)
	}
	return element{
		Class:      raw.Class,
		Tag:        raw.Tag,
		IsCompound: raw.IsCompound,
		Bytes:      raw.Bytes,
		FullBytes:  raw.FullBytes,
	}, rest, nil
}

// parseElements reads every TLV in der, which is typically the contents of a
// SEQUENCE.
func parseElements(der []byte) ([]element, error) {
	var out []element
	rest := der
	for len(rest) > 0 {
		e, remainder, err := parseElement(rest)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
		rest = remainder
	}
	return out, nil
}

// contextTag finds a context-specific element by tag number. Attestation
// authorization lists are sparse: absence is normal and is reported as a clean
// miss rather than an error.
func contextTag(elems []element, tag int) (element, bool) {
	for _, e := range elems {
		if e.Class == asn1.ClassContextSpecific && e.Tag == tag {
			return e, true
		}
	}
	return element{}, false
}

// explicitInner unwraps an explicitly tagged element, whose contents are a
// single nested TLV.
func explicitInner(e element) (element, error) {
	inner, rest, err := parseElement(e.Bytes)
	if err != nil {
		return element{}, fmt.Errorf("asn1: explicit tag %d: %w", e.Tag, err)
	}
	if len(rest) != 0 {
		return element{}, fmt.Errorf("asn1: explicit tag %d has %d trailing bytes", e.Tag, len(rest))
	}
	return inner, nil
}

// asInt decodes an INTEGER or ENUMERATED body. Both are big-endian two's
// complement; only the tag differs, and attestation uses them interchangeably
// across versions.
func asInt(e element) (int, error) {
	if len(e.Bytes) == 0 {
		return 0, fmt.Errorf("asn1: empty integer")
	}
	if len(e.Bytes) > 4 {
		return 0, fmt.Errorf("asn1: integer too wide (%d bytes)", len(e.Bytes))
	}
	v := 0
	if e.Bytes[0]&0x80 != 0 {
		v = -1 // sign-extend
	}
	for _, b := range e.Bytes {
		v = v<<8 | int(b)
	}
	return v, nil
}

// asBool decodes a BOOLEAN body. DER mandates 0xFF for true, but some
// attestation implementations emit any non-zero value, so treat non-zero as
// true rather than rejecting an otherwise valid chain.
func asBool(e element) (bool, error) {
	if len(e.Bytes) != 1 {
		return false, fmt.Errorf("asn1: boolean must be one byte, got %d", len(e.Bytes))
	}
	return e.Bytes[0] != 0, nil
}

// explicitInt unwraps an explicitly tagged INTEGER/ENUMERATED in one step.
func explicitInt(elems []element, tag int) (int, bool, error) {
	e, ok := contextTag(elems, tag)
	if !ok {
		return 0, false, nil
	}
	inner, err := explicitInner(e)
	if err != nil {
		return 0, false, err
	}
	v, err := asInt(inner)
	if err != nil {
		return 0, false, fmt.Errorf("asn1: tag %d: %w", tag, err)
	}
	return v, true, nil
}

// explicitOctets unwraps an explicitly tagged OCTET STRING in one step.
func explicitOctets(elems []element, tag int) ([]byte, bool, error) {
	e, ok := contextTag(elems, tag)
	if !ok {
		return nil, false, nil
	}
	inner, err := explicitInner(e)
	if err != nil {
		return nil, false, err
	}
	if inner.Tag != asn1.TagOctetString {
		return nil, false, fmt.Errorf("asn1: tag %d is not an octet string", tag)
	}
	return inner.Bytes, true, nil
}
