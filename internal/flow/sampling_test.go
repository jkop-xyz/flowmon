package flow

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

func optionsRecord(fields ...ipfix.Field) ipfix.Record {
	return ipfix.Record{ScopeFieldCount: 1, ExportTime: exportT, Fields: append([]ipfix.Field{f(144, ipfixtest.U32(1))}, fields...)}
}

func float64Bytes(v float64) []byte { return binary.BigEndian.AppendUint64(nil, math.Float64bits(v)) }

func TestSamplingRateInTheRecord(t *testing.T) {
	for _, c := range []struct {
		name   string
		fields []ipfix.Field
		want   uint32
	}{
		{"NetFlow v5/v9 samplingInterval", []ipfix.Field{f(ieSamplingInterval, ipfixtest.U32(100))}, 100},
		{"interval 0 says nothing", []ipfix.Field{f(ieSamplingInterval, ipfixtest.U16(0))}, 0},
		{"samplerRandomInterval", []ipfix.Field{f(ieSamplerRandomInterval, ipfixtest.U32(512))}, 512},
		{"1 packet, then skip 9", []ipfix.Field{f(ieSamplingPacketInterval, ipfixtest.U32(1)), f(ieSamplingPacketSpace, ipfixtest.U32(9))}, 10},
		{"10 out of 1000", []ipfix.Field{f(ieSamplingSize, ipfixtest.U32(10)), f(ieSamplingPopulation, ipfixtest.U32(1000))}, 100},
		{"probability 0.25", []ipfix.Field{{Spec: ipfix.FieldSpec{ID: ieSamplingProbability, Length: 8}, Raw: float64Bytes(0.25)}}, 4},
		{"an absurd rate is capped", []ipfix.Field{f(ieSamplingInterval, ipfixtest.U32(math.MaxUint32))}, MaxSamplingRate},
		{"nothing", nil, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := NewNormalizer().Normalize(exp, record(c.fields...), recvT)
			if err != nil || got.SamplingRate != c.want {
				t.Errorf("rate %d, want %d (%v)", got.SamplingRate, c.want, err)
			}
		})
	}
}

func TestSamplingRateFromOptions(t *testing.T) {
	n := NewNormalizer()
	// The Observation Domain samples 1 in 64; sampler 7 samples 1 in 1000.
	for _, o := range []ipfix.Record{
		optionsRecord(f(ieSamplingInterval, ipfixtest.U32(64))),
		optionsRecord(f(ieSamplerID, ipfixtest.U8(7)), f(ieSamplerRandomInterval, ipfixtest.U32(1000))),
	} {
		if _, err := n.Normalize(exp, o, recvT); err != ErrOptionsRecord {
			t.Fatal(err)
		}
	}
	// The second announcement is the domain's newest, so it is the default now.
	if got, _ := n.Normalize(exp, record(f(ieSamplerID, ipfixtest.U8(7))), recvT); got.SamplingRate != 1000 {
		t.Errorf("sampler 7: %d", got.SamplingRate)
	}
	if got, _ := n.Normalize(exp, record(), recvT); got.SamplingRate != 1000 {
		t.Errorf("domain default: %d", got.SamplingRate)
	}
	// Another Exporter's announcements don't apply.
	other := exp.Next()
	if got, _ := n.Normalize(other, record(), recvT); got.SamplingRate != 0 {
		t.Errorf("another Exporter: %d", got.SamplingRate)
	}
}

func TestApplySampling(t *testing.T) {
	for _, c := range []struct {
		name             string
		announced, man   uint32
		wantRate         uint32
		wantBytes, wantP uint64
	}{
		{"announced", 100, 0, 100, 150_000, 300},
		{"announced wins over the manual rate", 100, 10, 100, 150_000, 300},
		{"manual when nothing is announced", 0, 10, 10, 15_000, 30},
		{"announced as not sampled: the manual rate doesn't apply", 1, 10, 0, 1500, 3},
		{"not sampled", 0, 0, 0, 1500, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			fl := Flow{Bytes: 1500, Packets: 3, SamplingRate: c.announced}
			if got := fl.ApplySampling(c.man); got != c.wantRate || fl.SamplingRate != c.wantRate || fl.Bytes != c.wantBytes || fl.Packets != c.wantP {
				t.Errorf("rate %d, flow %+v", got, fl)
			}
		})
	}
}
