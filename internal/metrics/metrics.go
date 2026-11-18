// Package metrics writes the Prometheus text exposition format (version 0.0.4). Flowmon's
// metrics are a snapshot of counters it already keeps, so a small writer is all it
// needs.
package metrics

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// ContentType is the exposition format's media type.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Writer writes metrics. All samples of one metric must be written together.
type Writer struct {
	w    io.Writer
	last string
	err  error
}

// NewWriter returns a Writer to w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Err is the first write error.
func (w *Writer) Err() error { return w.err }

// Counter writes one sample of a counter.
func (w *Writer) Counter(name, help string, value float64, labels ...string) {
	w.sample(name, "counter", help, value, labels)
}

// Gauge writes one sample of a gauge.
func (w *Writer) Gauge(name, help string, value float64, labels ...string) {
	w.sample(name, "gauge", help, value, labels)
}

// sample writes HELP and TYPE before a metric's first sample. labels are name, value pairs.
func (w *Writer) sample(name, kind, help string, value float64, labels []string) {
	if w.err != nil {
		return
	}
	var b strings.Builder
	if name != w.last {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(help), name, kind)
		w.last = name
	}
	b.WriteString(name)
	if len(labels) > 0 {
		b.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%s=\"%s\"", labels[i], escapeLabel(labels[i+1]))
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(formatValue(value))
	b.WriteByte('\n')
	_, w.err = io.WriteString(w.w, b.String())
}

func formatValue(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatFloat(v, 'f', -1, 64) // whole numbers in full: 4194304, not 4.194304e+06
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

var (
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	labelEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
)

func escapeHelp(s string) string  { return helpEscaper.Replace(s) }
func escapeLabel(s string) string { return labelEscaper.Replace(s) }
