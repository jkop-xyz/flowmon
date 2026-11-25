// Package adguardtest is a fake AdGuard Home: the parts of its HTTP API Flowmon reads
// (/control/status, /control/querylog and /control/querylog/config), over an in-memory
// query log. Tests use it, and so does cmd/fakeadguard for trying Flowmon by hand.
package adguardtest

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Entry is one query log entry, in AdGuard Home's JSON shape.
type Entry struct {
	Time     string `json:"time"`
	Client   string `json:"client"`
	Question struct {
		Class string `json:"class"`
		Name  string `json:"name"`
		Type  string `json:"type"`
	} `json:"question"`
	Answer      []Answer `json:"answer,omitempty"`
	Status      string   `json:"status"`
	Reason      string   `json:"reason"`
	Rules       []Rule   `json:"rules"`
	Upstream    string   `json:"upstream"`
	ElapsedMs   string   `json:"elapsedMs"`
	Cached      bool     `json:"cached"`
	ClientProto string   `json:"client_proto"`
	ClientInfo  struct {
		Name string `json:"name"`
	} `json:"client_info"`
}

// Answer is one answer record.
type Answer struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   int    `json:"ttl"`
}

// Rule is a filtering rule that matched.
type Rule struct {
	FilterListID int64  `json:"filter_list_id"`
	Text         string `json:"text"`
}

// Query builds an Entry. answers are "A 1.2.3.4", "AAAA ::1" or "CNAME other.example".
func Query(t time.Time, client, name, qtype string, answers ...string) Entry {
	e := Entry{Time: t.Format(time.RFC3339Nano), Client: client, Status: "NOERROR", Reason: "NotFilteredNotFound",
		Upstream: "https://dns10.quad9.net:443/dns-query", ElapsedMs: "12.5", Rules: []Rule{}}
	e.Question.Class, e.Question.Name, e.Question.Type = "IN", name, qtype
	for _, a := range answers {
		for i := 0; i < len(a); i++ {
			if a[i] == ' ' {
				e.Answer = append(e.Answer, Answer{Type: a[:i], Value: a[i+1:], TTL: 300})
				break
			}
		}
	}
	return e
}

// Blocked marks e as blocked by rule.
func Blocked(e Entry, rule string) Entry {
	e.Reason, e.Answer = "FilteredBlackList", []Answer{{Type: "A", Value: "0.0.0.0", TTL: 10}}
	e.Rules = []Rule{{FilterListID: 1, Text: rule}}
	return e
}

// Server is a fake AdGuard Home. Its zero value is not usable; use New.
type Server struct {
	User, Password string
	Version        string
	// Keep is how long the log is kept, reported by /control/querylog/config.
	Keep time.Duration
	// LogOff turns the query log off.
	LogOff bool

	mu      sync.Mutex
	entries []Entry // in the order they were logged
	// Requests counts /control/querylog requests.
	Requests int
}

// New returns a fake with no entries.
func New(user, password string) *Server {
	return &Server{User: user, Password: password, Version: "v0.107.57", Keep: 90 * 24 * time.Hour}
}

// Add logs entries as if each was answered at once; they may come in any order.
func (s *Server) Add(es ...Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, es...)
	sort.SliceStable(s.entries, func(i, j int) bool { return s.time(s.entries[i]).Before(s.time(s.entries[j])) })
}

// Answer logs entries in the order given, after everything logged so far. AdGuard
// Home logs a lookup when it is answered, with the time it was asked, so a slow
// lookup is logged after newer ones.
func (s *Server) Answer(es ...Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, es...)
}

// DropBefore removes entries older than t, as AdGuard Home's log rotation does.
func (s *Server) DropBefore(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = slices.DeleteFunc(s.entries, func(e Entry) bool { return s.time(e).Before(t) })
}

func (s *Server) time(e Entry) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, e.Time)
	return t
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if u, p, ok := r.BasicAuth(); s.User != "" && (!ok || u != s.User || p != s.Password) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/control/status":
		writeJSON(w, map[string]any{"version": s.Version, "running": true, "dns_addresses": []string{"127.0.0.1"},
			"dns_port": 53, "http_port": 3000, "protection_enabled": true, "protection_disabled_duration": 0, "language": "en"})
	case "/control/querylog/config":
		writeJSON(w, map[string]any{"enabled": !s.LogOff, "interval": float64(s.Keep.Milliseconds()), "anonymize_client_ip": false, "ignored": []string{}})
	case "/control/querylog":
		s.queryLog(w, r)
	default:
		http.NotFound(w, r)
	}
}

// queryLog answers like AdGuard Home: last logged first, older than older_than, at
// most limit, with "oldest" the time of the page's last entry.
func (s *Server) queryLog(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests++
	limit := 500
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	var olderThan time.Time
	if ot := r.URL.Query().Get("older_than"); ot != "" {
		t, err := time.Parse(time.RFC3339Nano, ot)
		if err != nil {
			http.Error(w, "bad older_than", http.StatusBadRequest)
			return
		}
		olderThan = t
	}
	page := []Entry{}
	for i := len(s.entries) - 1; i >= 0 && len(page) < limit; i-- {
		if olderThan.IsZero() || s.time(s.entries[i]).Before(olderThan) {
			page = append(page, s.entries[i])
		}
	}
	oldest := ""
	if len(page) > 0 {
		oldest = page[len(page)-1].Time
	}
	writeJSON(w, map[string]any{"oldest": oldest, "data": page})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
