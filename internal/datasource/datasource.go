// Package datasource runs Data Sources: configured integrations other than Exporters,
// such as an AdGuard Home instance (ADR 0005). A Kind is one integration type; a Source
// is one configured integration of a kind. There can be any number of Sources of a kind.
//
// Sources are stored in the Flow Store with their secret fields sealed by secret.key, so
// collection never depends on the App Store (ADR 0001). Sources given by environment
// variables are not stored and can't be changed in the UI.
package datasource

import (
	"context"
	"time"
)

// Kind is one integration type. Registering a Kind with the Manager is all it takes to
// configure, run and show Sources of it: the UI draws its form from Info().Fields.
type Kind interface {
	Info() KindInfo
	// Validate checks settings beyond what Fields say (required fields and types are
	// checked before). Its error is shown to the user as is.
	Validate(s Settings) error
	// Test connects once with s and says what it found, for the Sources page's "Test".
	Test(ctx context.Context, s Settings) (string, error)
	// Run collects for src until ctx ends, reporting each attempt through r. If it
	// returns before ctx ends, the Manager starts it again after RestartDelay.
	Run(ctx context.Context, src Source, r Reporter)
}

// KindInfo describes a Kind.
type KindInfo struct {
	// Name identifies the kind in storage, environment variables and the API: "adguard".
	Name string `json:"name"`
	// Title is the kind's name for people: "AdGuard Home".
	Title string `json:"title"`
	// Description says what a Source of this kind collects, and what is kept for how long.
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
}

// FieldType is how a setting is entered and checked.
type FieldType string

// Field types. Secret fields are sealed at rest and never sent back to the UI.
const (
	FieldText   FieldType = "text"
	FieldURL    FieldType = "url"
	FieldSecret FieldType = "secret"
	FieldBool   FieldType = "bool"
	// FieldSelect is one of Field.Options.
	FieldSelect FieldType = "select"
)

// Field is one setting of a Kind.
type Field struct {
	// Key names the setting in Settings, and in environment variables upper-cased:
	// key "skip_tls_verify" is FLOWMON_SOURCE_<ID>_SKIP_TLS_VERIFY.
	Key      string    `json:"key"`
	Label    string    `json:"label"`
	Help     string    `json:"help,omitempty"`
	Type     FieldType `json:"type"`
	Required bool      `json:"required,omitempty"`
	// Default fills an empty setting.
	Default string `json:"default,omitempty"`
	// Options are a FieldSelect's choices; "" may be one, meaning none.
	Options []string `json:"options,omitempty"`
}

// Settings are a Source's settings by Field key. Booleans are "true" or "false".
type Settings map[string]string

// Bool reads a boolean setting.
func (s Settings) Bool(key string) bool { return s[key] == "true" }

// Source is one configured integration.
type Source struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Name     string    `json:"name"`
	Enabled  bool      `json:"enabled"`
	Settings Settings  `json:"settings"`
	Created  time.Time `json:"created"`
}

// Reporter is how a running Source reports its health.
type Reporter interface {
	// Succeeded records a successful attempt. detail says what it did: "312 new queries".
	Succeeded(detail string)
	// Failed records a failed attempt.
	Failed(err error)
	// Warn replaces the Source's warnings: problems that don't stop collection, such as
	// "nearly all queries come from the router". nil clears them.
	Warn(warnings []string)
}

// State is a Source's health in one word.
type State string

// States.
const (
	StateDisabled State = "disabled"
	// StateStarting is an enabled Source that hasn't reported yet.
	StateStarting State = "starting"
	StateOK       State = "ok"
	StateFailing  State = "failing"
)

// Health is how a Source is doing.
type Health struct {
	State State `json:"state"`
	// Since is when State began.
	Since       time.Time  `json:"since"`
	LastSuccess *time.Time `json:"lastSuccess,omitempty"`
	Detail      string     `json:"detail,omitempty"`
	LastError   *time.Time `json:"lastError,omitempty"`
	Error       string     `json:"error,omitempty"`
	Warnings    []string   `json:"warnings,omitempty"`
}
