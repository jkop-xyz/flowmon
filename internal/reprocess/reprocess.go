// Package reprocess runs re-classification of stored Flows in the background, one job
// at a time, after an Exporter's Interface Roles or Classification Mode change.
package reprocess

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// Store is the part of the Flow Store a job needs.
type Store interface {
	Reprocess(ctx context.Context, exporter netip.Addr, from time.Time, classify func(*flow.Flow), progress func(day time.Time, flows int)) (int, time.Time, error)
	Compact(ctx context.Context) error
}

// Status is the current or last job.
type Status struct {
	Running    bool      `json:"running"`
	Exporter   string    `json:"exporter,omitempty"`
	From       time.Time `json:"from,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	DaysDone   int       `json:"daysDone"`
	Flows      int       `json:"flows"`
	Error      string    `json:"error,omitempty"`
}

// Runner is safe for concurrent use.
type Runner struct {
	store    Store
	classify func(*flow.Flow)
	logger   *slog.Logger

	mu     sync.Mutex
	status Status
}

// New returns a Runner. classify re-classifies one Flow with the current settings.
func New(store Store, classify func(*flow.Flow), logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{store: store, classify: classify, logger: logger}
}

// ErrBusy is returned while another job runs.
var ErrBusy = errors.New("reprocess: a job is already running")

// Start reprocesses an Exporter's raw Flows from from on, in the background.
func (r *Runner) Start(exporter netip.Addr, from time.Time) error {
	r.mu.Lock()
	if r.status.Running {
		r.mu.Unlock()
		return ErrBusy
	}
	r.status = Status{Running: true, Exporter: exporter.String(), From: from.UTC(), StartedAt: time.Now().UTC()}
	r.mu.Unlock()

	go func() {
		ctx := context.Background()
		n, window, err := r.store.Reprocess(ctx, exporter, from, r.classify, func(time.Time, int) {
			r.mu.Lock()
			r.status.DaysDone++
			r.mu.Unlock()
		})
		if err == nil {
			err = r.store.Compact(ctx)
		}
		r.mu.Lock()
		r.status.Running, r.status.Flows, r.status.FinishedAt = false, n, time.Now().UTC()
		if !window.IsZero() {
			r.status.From = window
		}
		if err != nil {
			r.status.Error = err.Error()
			r.logger.Error("reprocessing failed", "exporter", exporter.String(), "err", err)
		} else {
			r.logger.Info("reprocessing finished", "exporter", exporter.String(), "flows", n, "from", window)
		}
		r.mu.Unlock()
	}()
	return nil
}

// Status returns the current or last job.
func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}
