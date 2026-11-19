package httpserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

// FlowStoreInfo is what the Flow Store reports about itself, and its snapshots.
type FlowStoreInfo interface {
	Disk(ctx context.Context) (flowstore.Disk, error)
	Memory(ctx context.Context) (flowstore.Memory, error)
	CapStatus() flowstore.CapStatus
	RawDays(from time.Time) []time.Time
	Snapshot(ctx context.Context) (*flowstore.Snapshot, error)
}

// FlowStoreOptions wires the Flow Store card, backups and the disk cap warning.
type FlowStoreOptions struct {
	Store FlowStoreInfo
	// Path is the Flow Store's file, for the restore instructions.
	Path string
	// MaxBytes is FLOWMON_FLOW_STORE_MAX_GB; 0 means no cap.
	MaxBytes int64
	// BackupToken, when set, lets "Authorization: Bearer <token>" download backups
	// without a session, for scheduled backups.
	BackupToken string
}

type capJSON struct {
	Checked  *time.Time `json:"checked,omitempty"`
	Over     bool       `json:"over"`
	LastDrop *time.Time `json:"lastDrop,omitempty"`
	Dropped  []string   `json:"dropped"`
}

type flowStoreJSON struct {
	Path        string     `json:"path"`
	UsedBytes   int64      `json:"usedBytes"`
	FileBytes   int64      `json:"fileBytes"`
	MaxBytes    int64      `json:"maxBytes"`
	Cap         *capJSON   `json:"cap,omitempty"`
	MemoryBytes int64      `json:"memoryBytes"`
	MemoryLimit string     `json:"memoryLimit"`
	Threads     int        `json:"threads"`
	RawDays     int        `json:"rawDays"`
	OldestRaw   *time.Time `json:"oldestRawDay,omitempty"`
	BackupToken bool       `json:"backupToken"`
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// flowStoreStatus is the banner's part of /api/status: whether the Flow Store is over
// its cap. Nil without a cap.
type flowStoreStatusJSON struct {
	UsedBytes int64 `json:"usedBytes"`
	MaxBytes  int64 `json:"maxBytes"`
	OverCap   bool  `json:"overCap"`
}

func flowStoreStatus(ctx context.Context, opts Options) *flowStoreStatusJSON {
	fo := opts.FlowStore
	if fo == nil || fo.Store == nil || fo.MaxBytes == 0 {
		return nil
	}
	d, _ := fo.Store.Disk(ctx)
	return &flowStoreStatusJSON{UsedBytes: d.Used, MaxBytes: fo.MaxBytes, OverCap: fo.Store.CapStatus().Over}
}

func registerFlowStore(public, mux *http.ServeMux, opts Options) {
	fo := opts.FlowStore
	if fo == nil || fo.Store == nil {
		return
	}
	mux.HandleFunc("GET /api/flow-store", func(w http.ResponseWriter, r *http.Request) {
		d, err := fo.Store.Disk(r.Context())
		if queryError(w, opts.Logger, "flow store disk", err) {
			return
		}
		m, err := fo.Store.Memory(r.Context())
		if queryError(w, opts.Logger, "flow store memory", err) {
			return
		}
		out := flowStoreJSON{Path: fo.Path, UsedBytes: d.Used, FileBytes: d.File, MaxBytes: fo.MaxBytes,
			MemoryBytes: m.Used, MemoryLimit: m.Limit, Threads: m.Threads, BackupToken: fo.BackupToken != ""}
		if fo.MaxBytes > 0 {
			c := fo.Store.CapStatus()
			out.Cap = &capJSON{Checked: optionalTime(c.Checked), Over: c.Over, LastDrop: optionalTime(c.LastDrop), Dropped: c.Dropped}
			if out.Cap.Dropped == nil {
				out.Cap.Dropped = []string{}
			}
		}
		days := fo.Store.RawDays(time.Time{})
		out.RawDays = len(days)
		if len(days) > 0 {
			out.OldestRaw = &days[0]
		}
		writeJSON(w, http.StatusOK, out)
	})

	session := requireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveBackup(w, r, opts, currentUser(r.Context()))
	}), opts)
	public.Handle("GET /api/flow-store/backup", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, isBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if fo.BackupToken == "" || !isBearer {
			session.ServeHTTP(w, r)
			return
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(fo.BackupToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="flowmon backup"`)
			errorJSON(w, http.StatusUnauthorized, "wrong backup token")
			return
		}
		serveBackup(w, r, opts, "FLOWMON_BACKUP_TOKEN")
	}))
}

// serveBackup takes a snapshot of the Flow Store and sends it as a download; by says
// who asked, for the log.
func serveBackup(w http.ResponseWriter, r *http.Request, opts Options, by string) {
	start := time.Now()
	sn, err := opts.FlowStore.Store.Snapshot(r.Context())
	if errors.Is(err, flowstore.ErrSnapshotRunning) {
		errorJSON(w, http.StatusConflict, "a backup is already being taken; try again when it's done")
		return
	}
	if err != nil {
		opts.Logger.Error("Flow Store backup", "err", err, "by", by)
		errorJSON(w, http.StatusInternalServerError, "taking the backup failed; the log says why")
		return
	}
	defer sn.Close()
	// A large backup takes longer to send than the server's write timeout allows.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	name := "flowmon-flows-" + opts.Now().UTC().Format("20060102-150405") + ".duckdb"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(sn.Size, 10))
	w.Header().Set("Cache-Control", "no-store")
	n, err := io.Copy(w, sn)
	if err != nil {
		opts.Logger.Warn("Flow Store backup not fully sent", "err", err, "bytes", n, "by", by)
		return
	}
	opts.Logger.Info("Flow Store backup sent", "bytes", n, "took", time.Since(start).Round(time.Millisecond), "by", by)
}
