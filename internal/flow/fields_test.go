package flow

import (
	"testing"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

func TestDescribeFields(t *testing.T) {
	enc := EncodeFields([]ipfix.Field{
		{Spec: ipfix.FieldSpec{ID: 8, Length: 4}, Raw: []byte{192, 168, 10, 44}},
		{Spec: ipfix.FieldSpec{ID: 1, Length: 4}, Raw: []byte{0, 0, 1, 0}},
		{Spec: ipfix.FieldSpec{ID: 8, Length: 3}, Raw: []byte{1, 2, 3}},
		{Spec: ipfix.FieldSpec{Enterprise: 14988, ID: 1, Length: 2}, Raw: []byte{0xab, 0xcd}},
	})
	got, err := DescribeFields(enc)
	if err != nil {
		t.Fatal(err)
	}
	want := []FieldView{
		{ID: 8, Name: "sourceIPv4Address", Known: true, Type: "ipv4Address", Value: "192.168.10.44", Raw: "c0a80a2c"},
		{ID: 1, Name: "octetDeltaCount", Known: true, Type: "unsigned64", Value: "256", Raw: "00000100"},
		{ID: 8, Name: "sourceIPv4Address", Known: true, Type: "ipv4Address", Value: "010203", Raw: "010203"},
		{Enterprise: 14988, ID: 1, Name: "enterprise14988.ie1", Value: "abcd", Raw: "abcd"},
	}
	for i, w := range want {
		g := got[i]
		if i == 2 {
			if g.Error == "" {
				t.Errorf("a 3-byte IPv4 address has no error")
			}
			g.Error = ""
		}
		if g != w {
			t.Errorf("field %d: %+v, want %+v", i, g, w)
		}
	}
	if _, err := DescribeFields([]byte{5}); err == nil {
		t.Error("corrupt encoding accepted")
	}
}
