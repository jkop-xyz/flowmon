package collector

import (
	"testing"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

func TestNATEventTemplatesGetOneInfoFinding(t *testing.T) {
	// RouterOS's NAT event Template: no counters, so no "no byte counter" warning.
	nat := &ipfix.Template{ID: 260, Fields: []ipfix.FieldSpec{
		{ID: 323, Length: 8}, {ID: 4, Length: 1}, {ID: 8, Length: 4}, {ID: 12, Length: 4},
		{ID: 7, Length: 2}, {ID: 11, Length: 2}, {ID: 225, Length: 4}, {ID: 226, Length: 4},
		{ID: 227, Length: 2}, {ID: 228, Length: 2}, {ID: 230, Length: 1},
	}}
	if fs := AssessTemplate(nat); len(fs) != 1 || fs[0].Code != "nat_events" || fs[0].Severity != SeverityInfo {
		t.Errorf("%+v", fs)
	}
	// A flow Template that also carries natEvent is assessed as a flow Template.
	flowish := &ipfix.Template{ID: 261, Fields: append(append([]ipfix.FieldSpec{}, nat.Fields...), ipfix.FieldSpec{ID: 1, Length: 8})}
	for _, f := range AssessTemplate(flowish) {
		if f.Code == "nat_events" {
			t.Errorf("flow Template got %+v", f)
		}
	}
}
