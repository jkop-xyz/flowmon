// Package iana holds the IANA IPFIX Information Element registry.
//
// The table is generated from a pinned copy of IANA's CSV
// (ipfix-information-elements.csv, checked in next to this file). To update it,
// download a new CSV over the old one and run `go generate`.
package iana

//go:generate go run ./gen -in ipfix-information-elements.csv -out elements_gen.go

import "fmt"

// DataType is an IPFIX abstract data type (RFC 7012 §3.1 and later additions).
type DataType uint8

// Abstract data types.
const (
	TypeUnknown DataType = iota
	Unsigned8
	Unsigned16
	Unsigned32
	Unsigned64
	Unsigned256
	Signed8
	Signed16
	Signed32
	Signed64
	Float32
	Float64
	Boolean
	MACAddress
	OctetArray
	String
	DateTimeSeconds
	DateTimeMilliseconds
	DateTimeMicroseconds
	DateTimeNanoseconds
	IPv4Address
	IPv6Address
	BasicList
	SubTemplateList
	SubTemplateMultiList
)

var dataTypeNames = map[DataType]string{
	TypeUnknown:          "unknown",
	Unsigned8:            "unsigned8",
	Unsigned16:           "unsigned16",
	Unsigned32:           "unsigned32",
	Unsigned64:           "unsigned64",
	Unsigned256:          "unsigned256",
	Signed8:              "signed8",
	Signed16:             "signed16",
	Signed32:             "signed32",
	Signed64:             "signed64",
	Float32:              "float32",
	Float64:              "float64",
	Boolean:              "boolean",
	MACAddress:           "macAddress",
	OctetArray:           "octetArray",
	String:               "string",
	DateTimeSeconds:      "dateTimeSeconds",
	DateTimeMilliseconds: "dateTimeMilliseconds",
	DateTimeMicroseconds: "dateTimeMicroseconds",
	DateTimeNanoseconds:  "dateTimeNanoseconds",
	IPv4Address:          "ipv4Address",
	IPv6Address:          "ipv6Address",
	BasicList:            "basicList",
	SubTemplateList:      "subTemplateList",
	SubTemplateMultiList: "subTemplateMultiList",
}

func (t DataType) String() string {
	if name, ok := dataTypeNames[t]; ok {
		return name
	}
	return fmt.Sprintf("DataType(%d)", uint8(t))
}

// ParseDataType maps a registry name such as "unsigned64" to its DataType.
func ParseDataType(name string) (DataType, bool) {
	for t, n := range dataTypeNames {
		if n == name && t != TypeUnknown {
			return t, true
		}
	}
	return TypeUnknown, false
}

// NativeLength is the full encoded length of a fixed-size type, or 0 for types whose
// length varies (octetArray, string, lists) or is not fixed (unknown).
func (t DataType) NativeLength() int {
	switch t {
	case Unsigned8, Signed8, Boolean:
		return 1
	case Unsigned16, Signed16:
		return 2
	case Unsigned32, Signed32, Float32, DateTimeSeconds, IPv4Address:
		return 4
	case Unsigned64, Signed64, Float64, DateTimeMilliseconds, DateTimeMicroseconds, DateTimeNanoseconds:
		return 8
	case MACAddress:
		return 6
	case IPv6Address:
		return 16
	case Unsigned256:
		return 32
	}
	return 0
}

// AllowsReducedSize reports whether RFC 7011 §6.2 reduced-size encoding applies:
// the exporter may send fewer bytes than NativeLength.
func (t DataType) AllowsReducedSize() bool {
	switch t {
	case Unsigned16, Unsigned32, Unsigned64, Unsigned256, Signed16, Signed32, Signed64, Float64:
		return true
	}
	return false
}

// Element is one registered Information Element.
type Element struct {
	ID         uint16
	Name       string
	Type       DataType
	Semantics  string
	Units      string
	Deprecated bool
}

// Lookup returns the IANA-registered element with the given ID.
func Lookup(id uint16) (Element, bool) {
	e, ok := elements[id]
	return e, ok
}

// Count is the number of registered elements in the table.
func Count() int { return len(elements) }
