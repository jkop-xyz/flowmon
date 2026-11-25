package flowstore

import (
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/series"
)

func TestDeviceHealth(t *testing.T) {
	s := open(t)
	boot := t0.Add(-10 * 24 * time.Hour)
	poll := func(at time.Time, boot time.Time, cpu float64, wifi []series.WifiClient) time.Time {
		t.Helper()
		restart, err := s.WriteHealth(ctx, series.Health{Source: "ap", At: at, Boot: boot,
			Metrics: map[string]float64{"cpu": cpu, "temperature:cpu-temperature": 50}, Wifi: wifi})
		if err != nil {
			t.Fatal(err)
		}
		return restart
	}
	phone := series.WifiClient{MAC: "aa:bb:cc:00:00:01", IfIndex: 3, Signal: -61, TxRate: 400_000_000, Connected: time.Hour}
	if r := poll(t0.Add(10*time.Second), boot, 10, []series.WifiClient{phone}); !r.IsZero() {
		t.Error("the first poll is no restart")
	}
	// Jitter in the computed boot time is no restart; a second poll in a minute is dropped.
	if r := poll(t0.Add(70*time.Second), boot.Add(30*time.Second), 20, nil); !r.IsZero() {
		t.Error("jitter taken for a restart")
	}
	poll(t0.Add(80*time.Second), boot, 40, nil)
	// 497 days of uptime wrap the counter: the boot time jumps, but nothing restarted.
	if r := poll(t0.Add(150*time.Second), boot.Add(uptimeWrap), 30, nil); !r.IsZero() {
		t.Error("an uptime wrap taken for a restart")
	}
	restarted := t0.Add(3 * time.Minute)
	if r := poll(t0.Add(4*time.Minute), restarted, 30, nil); !r.Equal(restarted) {
		t.Errorf("restart = %v, want %v", r, restarted)
	}

	pts, err := s.HealthSeries(ctx, "ap", t0, t0.Add(time.Hour))
	if err != nil || len(pts) != 4 || pts[1].Metrics["cpu"] != 20 {
		t.Fatalf("minutes %+v %v", pts, err)
	}
	hours, err := s.HealthSeries(ctx, "ap", t0, t0.Add(48*time.Hour))
	// The hour's mean counts each minute once: 10, 20 (not the second poll's 40), 30, 30.
	if err != nil || len(hours) != 1 || hours[0].Metrics["cpu"] != (10+20+30+30)/4.0 {
		t.Errorf("hours %+v %v", hours, err)
	}
	rs, err := s.Restarts(ctx, "ap", t0, t0.Add(time.Hour))
	if err != nil || len(rs) != 1 || !rs[0].Equal(restarted) {
		t.Errorf("restarts %v %v", rs, err)
	}
	now, err := s.LatestHealth(ctx, "")
	if err != nil || now["ap"].Metrics["cpu"] != 30 || now["ap"].Boot == nil || !now["ap"].Boot.Equal(restarted) {
		t.Errorf("latest %+v %v", now, err)
	}
	// Wi-Fi clients are as last polled: the latest poll had none.
	if c, _ := s.WifiClients(ctx, "ap"); len(c) != 0 {
		t.Errorf("stale clients %+v", c)
	}
	poll(t0.Add(5*time.Minute), restarted, 30, []series.WifiClient{phone})
	if c, _ := s.WifiClients(ctx, "ap"); len(c) != 1 || c[0].MAC != phone.MAC || c[0].ConnectedSeconds != 3600 {
		t.Errorf("clients %+v", c)
	}

	if err := s.applyHealthRetention(ctx, t0.Add(48*time.Hour), Retention{Minute: 24 * time.Hour, Hour: 72 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	if pts, _ := s.HealthSeries(ctx, "ap", t0, t0.Add(time.Hour)); len(pts) != 0 {
		t.Error("minutes past retention kept")
	}
	if hours, _ := s.HealthSeries(ctx, "ap", t0, t0.Add(48*time.Hour)); len(hours) != 1 {
		t.Error("hours within retention dropped")
	}
}
