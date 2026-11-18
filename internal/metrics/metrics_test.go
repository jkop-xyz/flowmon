package metrics

import (
	"bytes"
	"math"
	"testing"
)

func TestWriter(t *testing.T) {
	var b bytes.Buffer
	w := NewWriter(&b)
	w.Counter("flowmon_x_total", "Things\nseen.", 3, "exporter", "192.168.88.1", "kind", `a"b\c`)
	w.Counter("flowmon_x_total", "Things\nseen.", 4.5, "exporter", "10.0.0.1", "kind", "z")
	w.Gauge("flowmon_up", "Up.", 1)
	w.Gauge("flowmon_inf", "Inf.", math.Inf(1))
	w.Gauge("flowmon_big", "Big.", 4194304)
	want := `# HELP flowmon_x_total Things\nseen.
# TYPE flowmon_x_total counter
flowmon_x_total{exporter="192.168.88.1",kind="a\"b\\c"} 3
flowmon_x_total{exporter="10.0.0.1",kind="z"} 4.5
# HELP flowmon_up Up.
# TYPE flowmon_up gauge
flowmon_up 1
# HELP flowmon_inf Inf.
# TYPE flowmon_inf gauge
flowmon_inf +Inf
# HELP flowmon_big Big.
# TYPE flowmon_big gauge
flowmon_big 4194304
`
	if b.String() != want || w.Err() != nil {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}
