package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/jkop-xyz/flowmon/internal/appstore"
)

const (
	overviewLayoutPref = "overview-layout"
	maxLayoutSections  = 32
)

var layoutIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

type layoutSection struct {
	ID     string `json:"id"`
	Hidden bool   `json:"hidden"`
}

// overviewLayout is a user's saved Overview. Sections is nil when none is saved.
// The server doesn't know which ids the Overview has; the web side ignores unknown ones.
type overviewLayout struct {
	Sections []layoutSection `json:"sections"`
}

func (l overviewLayout) validate() error {
	if n := len(l.Sections); n < 1 || n > maxLayoutSections {
		return fmt.Errorf("sections must list 1 to %d sections", maxLayoutSections)
	}
	seen := map[string]bool{}
	for _, s := range l.Sections {
		if !layoutIDPattern.MatchString(s.ID) {
			return errors.New("a section id is a lowercase letter, then lowercase letters, digits or dashes, at most 32 characters")
		}
		if seen[s.ID] {
			return fmt.Errorf("section %q is listed twice", s.ID)
		}
		seen[s.ID] = true
	}
	return nil
}

// registerOverviewLayout adds the logged-in user's own Overview layout. Every handler
// acts on the session's user only.
func registerOverviewLayout(mux *http.ServeMux, opts Options) {
	// own finds the session's user and the App Store, answering the request itself when it can't.
	own := func(w http.ResponseWriter, r *http.Request) (*appstore.Store, string, bool) {
		u, ok := r.Context().Value(userKey{}).(appstore.User)
		if !ok {
			errorJSON(w, http.StatusUnauthorized, "unauthenticated")
			return nil, "", false
		}
		var st *appstore.Store
		if opts.AppStore != nil && opts.AppStore.Health().Available {
			st = opts.AppStore.Get()
		}
		if st == nil {
			errorJSON(w, http.StatusServiceUnavailable, "the App Store is unavailable, so the layout can't be read or saved")
			return nil, "", false
		}
		return st, u.ID, true
	}
	fail := func(w http.ResponseWriter, what string, err error) {
		opts.Logger.Error("overview layout", "what", what, "err", err)
		errorJSON(w, http.StatusInternalServerError, what+" failed")
	}

	mux.HandleFunc("GET /api/me/overview-layout", func(w http.ResponseWriter, r *http.Request) {
		st, uid, ok := own(w, r)
		if !ok {
			return
		}
		raw, err := st.UserPreference(r.Context(), uid, overviewLayoutPref)
		if errors.Is(err, appstore.ErrNotFound) {
			writeJSON(w, http.StatusOK, overviewLayout{})
			return
		}
		if err != nil {
			fail(w, "reading the layout", err)
			return
		}
		var l overviewLayout
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			fail(w, "reading the layout", err)
			return
		}
		writeJSON(w, http.StatusOK, l)
	})
	mux.HandleFunc("PUT /api/me/overview-layout", func(w http.ResponseWriter, r *http.Request) {
		st, uid, ok := own(w, r)
		if !ok {
			return
		}
		var l overviewLayout
		if !readJSON(w, r, &l) {
			return
		}
		if err := l.validate(); err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		raw, _ := json.Marshal(l)
		if err := st.SetUserPreference(r.Context(), uid, overviewLayoutPref, string(raw), opts.Now()); err != nil {
			fail(w, "saving the layout", err)
			return
		}
		writeJSON(w, http.StatusOK, l)
	})
	mux.HandleFunc("DELETE /api/me/overview-layout", func(w http.ResponseWriter, r *http.Request) {
		st, uid, ok := own(w, r)
		if !ok {
			return
		}
		if err := st.DeleteUserPreference(r.Context(), uid, overviewLayoutPref); err != nil {
			fail(w, "resetting the layout", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
