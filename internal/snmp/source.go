package snmp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/series"
)

// SourceInterval is how often an SNMP Data Source polls every interface.
const SourceInterval = time.Minute

// SourceOptions configure the SNMP Data Source kind.
type SourceOptions struct {
	Writer series.Writer
	// Dial opens a device's agent; Dial with gosnmp when nil. Tests replace it.
	Dial     func(c Config) (TableReader, error)
	Interval time.Duration
	Now      func() time.Time
	Logger   *slog.Logger
}

// SourceKind is the SNMP Data Source kind (ADR 0005): any device, polled every minute
// into Interface Counters.
type SourceKind struct{ opts SourceOptions }

// NewSourceKind returns the kind.
func NewSourceKind(opts SourceOptions) *SourceKind {
	if opts.Dial == nil {
		opts.Dial = dialTable
	}
	if opts.Interval == 0 {
		opts.Interval = SourceInterval
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &SourceKind{opts: opts}
}

func dialTable(c Config) (TableReader, error) {
	a, err := Dial(c, netip.Addr{})
	if err != nil {
		return nil, err
	}
	t, ok := a.(TableReader)
	if !ok {
		a.Close()
		return nil, errors.New("snmp: this agent can't read interface tables")
	}
	return t, nil
}

var (
	authOptions = []string{"", "MD5", "SHA", "SHA224", "SHA256", "SHA384", "SHA512"}
	privOptions = []string{"", "DES", "AES", "AES192", "AES256"}
)

// Info describes the kind.
func (k *SourceKind) Info() datasource.KindInfo {
	return datasource.KindInfo{
		Name:  "snmp",
		Title: "SNMP device",
		Description: "Polls every interface of a router, switch, access point or NAS over SNMP each minute: traffic, " +
			"errors, discards, speed and whether it is up. Kept as minute and hour Interface Counters for the Rollup " +
			"retentions on the Settings page. Allow SNMP only from Flowmon's address on the device.",
		Fields: []datasource.Field{
			{Key: "address", Label: "Address", Type: datasource.FieldText, Required: true,
				Help: "The device's address or name, with :port if it isn't 161."},
			{Key: "version", Label: "Version", Type: datasource.FieldSelect, Options: []string{"v3", "v2c"}, Default: "v3"},
			{Key: "community", Label: "Community (SNMPv2c)", Type: datasource.FieldSecret},
			{Key: "user", Label: "User (SNMPv3)", Type: datasource.FieldText, Help: "On RouterOS, the community's name."},
			{Key: "auth_protocol", Label: "Authentication", Type: datasource.FieldSelect, Options: authOptions},
			{Key: "auth_password", Label: "Authentication password", Type: datasource.FieldSecret},
			{Key: "priv_protocol", Label: "Privacy (encryption)", Type: datasource.FieldSelect, Options: privOptions},
			{Key: "priv_password", Label: "Privacy password", Type: datasource.FieldSecret},
		},
	}
}

// ConfigOf turns a source's settings into an agent Config.
func ConfigOf(s datasource.Settings) Config {
	return Config{Enabled: true, Version: s["version"], Target: s["address"], Community: s["community"], User: s["user"],
		AuthProtocol: s["auth_protocol"], AuthPassword: s["auth_password"], PrivProtocol: s["priv_protocol"], PrivPassword: s["priv_password"]}
}

// Validate checks the SNMP settings.
func (k *SourceKind) Validate(s datasource.Settings) error {
	c := ConfigOf(s)
	if c.Version == "v2c" && c.Community == "" {
		return &datasource.InvalidError{Field: "community", Msg: "SNMPv2c needs a community."}
	}
	switch err := c.Validate(); {
	case errors.Is(err, ErrV3User):
		return &datasource.InvalidError{Field: "user", Msg: "SNMPv3 needs a user."}
	case errors.Is(err, ErrPriv):
		return &datasource.InvalidError{Field: "priv_protocol", Msg: "Privacy needs authentication."}
	case errors.Is(err, ErrTarget):
		return &datasource.InvalidError{Field: "address", Msg: "The address must be host or host:port."}
	case err != nil:
		return err
	}
	return nil
}

// Test reads the interface table once.
func (k *SourceKind) Test(ctx context.Context, s datasource.Settings) (string, error) {
	a, err := k.opts.Dial(ConfigOf(s))
	if err != nil {
		return "", err
	}
	defer a.Close()
	t, err := a.Table(ctx)
	if err != nil {
		return "", err
	}
	up := 0
	for _, r := range t.Rows {
		if r.Up {
			up++
		}
	}
	return fmt.Sprintf("Connected: %d interfaces, %d up. The device has been running for %s.", len(t.Rows), up, roundUptime(t.Uptime)), nil
}

func roundUptime(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return d.Round(time.Minute).String()
	}
}

// Run polls every interface until ctx ends.
func (k *SourceKind) Run(ctx context.Context, src datasource.Source, r datasource.Reporter) {
	c := ConfigOf(src.Settings)
	var prev *Poll
	t := time.NewTicker(k.opts.Interval)
	defer t.Stop()
	for {
		cur, err := k.poll(ctx, c)
		switch {
		case err != nil && ctx.Err() != nil:
			return
		case err != nil:
			r.Failed(err)
		default:
			var samples []series.InterfaceSample
			if prev != nil {
				samples = Deltas(src.ID, *prev, cur)
			}
			if err := k.opts.Writer.WriteInterfaces(ctx, cur.At, Interfaces(src.ID, cur.Table), samples); err != nil {
				r.Failed(fmt.Errorf("storing Interface Counters: %w", err))
			} else {
				var warn []string
				if prev != nil && cur.Table.Uptime < prev.Table.Uptime {
					warn = append(warn, fmt.Sprintf("The device restarted at about %s: that minute has no counters.", cur.At.Add(-cur.Table.Uptime).UTC().Format(time.RFC3339)))
				}
				r.Warn(warn)
				r.Succeeded(fmt.Sprintf("%d interfaces", len(cur.Table.Rows)))
			}
			prev = &cur
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (k *SourceKind) poll(ctx context.Context, c Config) (Poll, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	a, err := k.opts.Dial(c)
	if err != nil {
		return Poll{}, err
	}
	defer a.Close()
	t, err := a.Table(ctx)
	if err != nil {
		return Poll{}, err
	}
	return Poll{At: k.opts.Now(), Table: t}, nil
}

var _ datasource.Kind = (*SourceKind)(nil)
