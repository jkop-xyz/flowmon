package httpserver

import (
	"context"
	"net/http"
	"net/netip"
	"sort"
	"time"

	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/snmp"
)

// The combined Overview (ADR 0005, priority 3: "why is the internet slow right now?"):
// the WAN's load from Interface Counters, who is using it now, each SNMP device's health
// and its interface problems.

// UsageStore says who is using the internet now. *flowstore.Store implements it.
type UsageStore interface {
	UsageNow(ctx context.Context, from, to time.Time, groups []netip.Prefix, limit int) (flowstore.NowResult, error)
}

// NowWindow is how far back "now" reaches.
const NowWindow = 5 * time.Minute

// recentlyPolled is how old a reading may be and still count as now.
const recentlyPolled = 5 * time.Minute

type wanInterfaceJSON struct {
	Source     string `json:"source"`
	SourceName string `json:"sourceName"`
	IfIndex    uint32 `json:"ifIndex"`
	Name       string `json:"name"`
	Speed      uint64 `json:"speed"`
}

type wanJSON struct {
	Interfaces  []wanInterfaceJSON `json:"interfaces"`
	Points      []wanPoint         `json:"points"`
	StepSeconds int                `json:"stepSeconds"`
	// Now is the latest minute, when it is recent.
	Now *wanPoint `json:"now,omitempty"`
	// Line is the speed WAN load is measured against: the one set in Settings, else the
	// interfaces' speed.
	Line struct {
		DownBps     uint64 `json:"downBps"`
		UpBps       uint64 `json:"upBps"`
		FromSetting bool   `json:"fromSetting"`
	} `json:"line"`
	// Total is what crossed the WAN over the range. Seconds is the polled time it
	// covers: per interface the sum of each bucket's coverage times its length, then the
	// most of any interface, so a second interface does not double it.
	Total struct {
		InBytes  float64 `json:"inBytes"`
		OutBytes float64 `json:"outBytes"`
		Seconds  float64 `json:"seconds"`
	} `json:"total"`
}

type wanPoint struct {
	T time.Time `json:"t"`
	// InBps is download (into the WAN interfaces), OutBps upload.
	InBps  float64 `json:"inBps"`
	OutBps float64 `json:"outBps"`
}

type deviceHealthJSON struct {
	Source  string             `json:"source"`
	Name    string             `json:"name"`
	State   datasource.State   `json:"state"`
	At      *time.Time         `json:"at,omitempty"`
	Metrics map[string]float64 `json:"metrics"`
	// Restarts are those in the last day, newest first.
	Restarts []time.Time `json:"restarts"`
}

type interfaceProblemJSON struct {
	Source     string `json:"source"`
	SourceName string `json:"sourceName"`
	IfIndex    uint32 `json:"ifIndex"`
	Name       string `json:"name"`
	Alias      string `json:"alias,omitempty"`
	// Down is a named interface (one with an alias) that is down.
	Down     bool   `json:"down"`
	Errors   uint64 `json:"errors"`
	Discards uint64 `json:"discards"`
}

func registerOverview(mux *http.ServeMux, opts Options) {
	mux.HandleFunc("GET /api/overview/now", func(w http.ResponseWriter, r *http.Request) {
		if opts.Usage == nil {
			errorJSON(w, http.StatusServiceUnavailable, "flow store not available")
			return
		}
		now := opts.Now()
		res, err := opts.Usage.UsageNow(r.Context(), now.Add(-NowWindow), now, hostGroups(r.Context(), opts), 5)
		if queryError(w, opts.Logger, "usage now", err) {
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("GET /api/overview/network", func(w http.ResponseWriter, r *http.Request) {
		bq, err := bandwidthQuery(r, opts.Now())
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		out := map[string]any{"wan": nil, "devices": []deviceHealthJSON{}, "problems": []interfaceProblemJSON{}}
		is := opts.InterfaceCounters
		if is == nil || opts.Sources == nil {
			writeJSON(w, http.StatusOK, out)
			return
		}
		var srcs []datasource.Status
		for _, s := range opts.Sources.List() {
			if s.Kind == "snmp" && s.Enabled {
				srcs = append(srcs, s)
			}
		}
		sort.Slice(srcs, func(i, j int) bool { return srcs[i].Name < srcs[j].Name })
		wan, err := wanLoad(r.Context(), opts, srcs, bq.From, bq.To)
		if queryError(w, opts.Logger, "wan load", err) {
			return
		}
		if wan != nil {
			out["wan"] = wan
		}
		devs, problems, err := deviceStates(r.Context(), opts, srcs)
		if queryError(w, opts.Logger, "device health", err) {
			return
		}
		out["devices"], out["problems"] = devs, problems
		writeJSON(w, http.StatusOK, out)
	})
}

// wanLoad sums the WAN Interfaces of every Exporter an SNMP source polls; nil when none
// is known.
func wanLoad(ctx context.Context, opts Options, srcs []datasource.Status, from, to time.Time) (*wanJSON, error) {
	if opts.SNMP == nil || opts.SNMP.WAN == nil {
		return nil, nil
	}
	is := opts.InterfaceCounters
	wan := &wanJSON{Interfaces: []wanInterfaceJSON{}, Points: []wanPoint{}, StepSeconds: seriesStep(from, to)}
	byT := map[time.Time]*wanPoint{}
	var now wanPoint
	fresh := 0
	var speed uint64
	for _, src := range srcs {
		e, ok := snmp.ExporterOf(src.Settings)
		if !ok {
			continue
		}
		names := opts.SNMP.WAN(e)
		if len(names) == 0 {
			continue
		}
		ifs, err := is.SNMPInterfaces(ctx, src.ID)
		if err != nil {
			return nil, err
		}
		latest, err := is.InterfacesNow(ctx, src.ID, opts.Now())
		if err != nil {
			return nil, err
		}
		for _, i := range ifs {
			if _, isWAN := names[i.Name]; !isWAN {
				continue
			}
			wan.Interfaces = append(wan.Interfaces, wanInterfaceJSON{src.ID, src.Name, i.IfIndex, i.Name, i.Speed})
			speed += i.Speed
			pts, err := is.InterfaceSeries(ctx, src.ID, i.IfIndex, from, to)
			if err != nil {
				return nil, err
			}
			var covered float64
			for _, p := range pts {
				wan.Total.InBytes += p.InOctets
				wan.Total.OutBytes += p.OutOctets
				covered += p.Coverage * float64(wan.StepSeconds)
				w := byT[p.T]
				if w == nil {
					w = &wanPoint{T: p.T}
					byT[p.T] = w
				}
				w.InBps += p.InBps
				w.OutBps += p.OutBps
			}
			wan.Total.Seconds = max(wan.Total.Seconds, covered)
			if n, ok := latest[i.IfIndex]; ok && opts.Now().Sub(n.At) < recentlyPolled {
				now.InBps += n.InBps
				now.OutBps += n.OutBps
				if n.At.After(now.T) {
					now.T = n.At
				}
				fresh++
			}
		}
	}
	if len(wan.Interfaces) == 0 {
		return nil, nil
	}
	for _, p := range byT {
		wan.Points = append(wan.Points, *p)
	}
	sort.Slice(wan.Points, func(i, j int) bool { return wan.Points[i].T.Before(wan.Points[j].T) })
	if fresh > 0 {
		wan.Now = &now
	}
	wan.Line.DownBps, wan.Line.UpBps = speed, speed
	if opts.Settings != nil {
		if l := opts.Settings.Current().Line; l.Down > 0 || l.Up > 0 {
			wan.Line.DownBps, wan.Line.UpBps, wan.Line.FromSetting = l.Down, l.Up, true
		}
	}
	return wan, nil
}

// deviceStates gives each SNMP device's health and restarts in the last day, and the
// interface problems on any of them: named interfaces that are down, and interfaces
// with errors or discards in the last hour, worst first.
func deviceStates(ctx context.Context, opts Options, srcs []datasource.Status) ([]deviceHealthJSON, []interfaceProblemJSON, error) {
	is := opts.InterfaceCounters
	now := opts.Now()
	latest, err := is.LatestHealth(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	devs, problems := []deviceHealthJSON{}, []interfaceProblemJSON{}
	for _, src := range srcs {
		d := deviceHealthJSON{Source: src.ID, Name: src.Name, State: src.Health.State, Metrics: map[string]float64{}, Restarts: []time.Time{}}
		if h, ok := latest[src.ID]; ok {
			at := h.At
			d.At, d.Metrics = &at, h.Metrics
		}
		rs, err := is.Restarts(ctx, src.ID, now.Add(-24*time.Hour), now.Add(time.Minute))
		if err != nil {
			return nil, nil, err
		}
		for i := len(rs) - 1; i >= 0; i-- {
			d.Restarts = append(d.Restarts, rs[i])
		}
		devs = append(devs, d)

		ifs, err := is.SNMPInterfaces(ctx, src.ID)
		if err != nil {
			return nil, nil, err
		}
		counts, err := is.InterfacesNow(ctx, src.ID, now)
		if err != nil {
			return nil, nil, err
		}
		for _, i := range ifs {
			p := interfaceProblemJSON{Source: src.ID, SourceName: src.Name, IfIndex: i.IfIndex, Name: i.Name, Alias: i.Alias, Down: !i.Up && i.Alias != ""}
			if c, ok := counts[i.IfIndex]; ok {
				p.Errors, p.Discards = c.HourErrors, c.HourDiscards
			}
			if p.Down || p.Errors > 0 || p.Discards > 0 {
				problems = append(problems, p)
			}
		}
	}
	sort.SliceStable(problems, func(i, j int) bool {
		a, b := problems[i], problems[j]
		if a.Down != b.Down {
			return a.Down
		}
		return a.Errors+a.Discards > b.Errors+b.Discards
	})
	return devs, problems, nil
}
