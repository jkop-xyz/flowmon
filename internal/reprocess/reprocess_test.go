package reprocess

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

type fakeStore struct{ release chan struct{} }

func (f fakeStore) Reprocess(_ context.Context, _ netip.Addr, from time.Time, classify func(*flow.Flow), progress func(time.Time, int)) (int, time.Time, error) {
	<-f.release
	fl := flow.Flow{}
	classify(&fl)
	progress(from, 1)
	return 1, from.Truncate(24 * time.Hour), nil
}
func (fakeStore) Compact(context.Context) error { return nil }

func TestRunner(t *testing.T) {
	release := make(chan struct{})
	classified := 0
	r := New(fakeStore{release}, func(*flow.Flow) { classified++ }, nil)
	exp := netip.MustParseAddr("192.168.10.1")
	from := time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)
	if err := r.Start(exp, from); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(exp, from); err != ErrBusy {
		t.Errorf("second job: %v", err)
	}
	if !r.Status().Running {
		t.Error("should be running")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for r.Status().Running && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	st := r.Status()
	if st.Running || st.Flows != 1 || st.DaysDone != 1 || !st.From.Equal(from.Truncate(24*time.Hour)) || classified != 1 || st.Error != "" {
		t.Errorf("status %+v, classified %d", st, classified)
	}
}
