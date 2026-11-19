package flow

import (
	"math"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

// Elements that say how an Exporter samples packets. A data record may carry them
// itself; more often an Options record announces them for the whole Observation Domain,
// or for one sampler that data records then name by ID.
const (
	ieSamplingInterval       = 34  // NetFlow v5/v9: 1 in N packets
	ieSamplerID              = 48  // which sampler (NetFlow v9 FLOW_SAMPLER_ID)
	ieSamplerRandomInterval  = 50  // 1 in N, chosen at random
	ieSelectorID             = 302 // which selector (PSAMP, RFC 5477)
	ieSamplingPacketInterval = 305 // select this many packets...
	ieSamplingPacketSpace    = 306 // ...then skip this many
	ieSamplingSize           = 309 // select this many packets...
	ieSamplingPopulation     = 310 // ...out of this many
	ieSamplingProbability    = 311 // each packet with this probability
)

// MaxSamplingRate bounds a sampling rate, so a garbage value can't multiply counts into
// nonsense.
const MaxSamplingRate = 1 << 20

// recordSamplingRate is the sampling rate a record states, 0 if it states none. A rate
// of 1 means every packet: "not sampled", stated.
func recordSamplingRate(rec ipfix.Record) uint32 {
	clamp := func(r float64) uint32 {
		if r < 1 || math.IsNaN(r) {
			return 0
		}
		return uint32(min(math.Round(r), MaxSamplingRate))
	}
	if n, ok := uintFieldOK(rec, ieSamplingPacketInterval); ok && n > 0 {
		space, _ := uintFieldOK(rec, ieSamplingPacketSpace)
		return clamp(float64(n+space) / float64(n))
	}
	if size, ok := uintFieldOK(rec, ieSamplingSize); ok && size > 0 {
		if pop, ok := uintFieldOK(rec, ieSamplingPopulation); ok {
			return clamp(float64(pop) / float64(size))
		}
	}
	if f, ok := rec.Get(ieSamplingProbability); ok {
		if v, err := f.Value(); err == nil && v.Kind == ipfix.KindFloat && v.Float > 0 {
			return clamp(1 / v.Float)
		}
	}
	for _, id := range []uint16{ieSamplerRandomInterval, ieSamplingInterval} {
		// NetFlow v5 and v9 exporters send 0 when sampling isn't configured, which says
		// nothing either way, so a rate set by hand still applies.
		if n, ok := uintFieldOK(rec, id); ok && n > 0 {
			return clamp(float64(n))
		}
	}
	return 0
}

// samplerID is the sampler or selector a record names, if any.
func samplerID(rec ipfix.Record) (uint64, bool) {
	for _, id := range []uint16{ieSelectorID, ieSamplerID} {
		if v, ok := uintFieldOK(rec, id); ok {
			return v, true
		}
	}
	return 0, false
}

type samplerKey struct {
	domain domainKey
	id     uint64
}

// samplingState remembers the rates Options records announced, per Observation Domain
// and per sampler. It is guarded by the Normalizer's mutex.
type samplingState struct {
	domains  map[domainKey]uint32
	samplers map[samplerKey]uint32
}

func newSamplingState() samplingState {
	return samplingState{domains: map[domainKey]uint32{}, samplers: map[samplerKey]uint32{}}
}

// learn takes the sampling rate an Options record announces, if it does.
func (s samplingState) learn(key domainKey, rec ipfix.Record) {
	rate := recordSamplingRate(rec)
	if rate == 0 {
		return
	}
	if _, known := s.domains[key]; known || len(s.domains) < maxDomains {
		s.domains[key] = rate
	}
	if id, ok := samplerID(rec); ok {
		k := samplerKey{key, id}
		if _, known := s.samplers[k]; known || len(s.samplers) < maxDomains {
			s.samplers[k] = rate
		}
	}
}

// rate is a data record's sampling rate: what it states itself, else what was announced
// for the sampler it names, else for its Observation Domain. 0: nothing announced; 1:
// announced as not sampled.
func (s samplingState) rate(key domainKey, rec ipfix.Record) uint32 {
	rate := recordSamplingRate(rec)
	if rate == 0 {
		if id, ok := samplerID(rec); ok {
			rate = s.samplers[samplerKey{key, id}]
		}
	}
	if rate == 0 {
		rate = s.domains[key]
	}
	return rate
}

// ApplySampling scales a sampled Flow's counts up to estimates of the real traffic.
// manual is the rate set for the Exporter by hand, used when the Exporter announced
// none (SamplingRate 0); an announced rate, including 1 for "not sampled", wins. It
// returns the rate applied, 0 if the Flow isn't sampled.
func (f *Flow) ApplySampling(manual uint32) uint32 {
	if f.SamplingRate == 0 && manual > 1 {
		f.SamplingRate = min(manual, MaxSamplingRate)
	}
	if f.SamplingRate <= 1 {
		f.SamplingRate = 0
		return 0
	}
	f.Bytes *= uint64(f.SamplingRate)
	f.Packets *= uint64(f.SamplingRate)
	return f.SamplingRate
}
