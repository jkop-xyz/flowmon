package iana

import "testing"

func TestLookupWellKnown(t *testing.T) {
	cases := map[uint16]struct {
		name string
		typ  DataType
	}{
		1:   {"octetDeltaCount", Unsigned64},
		8:   {"sourceIPv4Address", IPv4Address},
		27:  {"sourceIPv6Address", IPv6Address},
		152: {"flowStartMilliseconds", DateTimeMilliseconds},
		225: {"postNATSourceIPv4Address", IPv4Address},
		291: {"basicList", BasicList},
	}
	for id, want := range cases {
		e, ok := Lookup(id)
		if !ok || e.Name != want.name || e.Type != want.typ {
			t.Errorf("Lookup(%d) = %+v, %v", id, e, ok)
		}
	}
	if _, ok := Lookup(0); ok {
		t.Error("ID 0 is reserved and must not be in the table")
	}
	if Count() < 450 {
		t.Errorf("only %d elements; the registry has about 490", Count())
	}
}

func TestDataTypeNamesRoundTrip(t *testing.T) {
	for typ := Unsigned8; typ <= SubTemplateMultiList; typ++ {
		parsed, ok := ParseDataType(typ.String())
		if !ok || parsed != typ {
			t.Errorf("%v does not round-trip", typ)
		}
	}
}

func TestEveryElementHasAKnownType(t *testing.T) {
	for id, e := range elements {
		if e.Type == TypeUnknown {
			t.Errorf("element %d (%s) has no type", id, e.Name)
		}
	}
}
