package domains

import (
	"net/netip"
	"slices"
	"sync"
	"time"
)

// Domain Attribution (ADR 0005): a Flow gets the name its Local Host looked up last,
// within Window before the Flow started, whose answer contained the Flow's Remote IP.
// Window is a day rather than the answer's TTL because devices keep using addresses
// long after their TTL ends.
const Window = 24 * time.Hour

// Skew is how far after a Flow's start a lookup may be logged and still count: the DNS
// server's clock and the Exporter's differ by up to a few seconds.
const Skew = 10 * time.Second

// perAnswer is how many lookups are kept per client and answer address: enough that a
// lookup logged just after a Flow started doesn't hide the one before it.
const perAnswer = 4

// Lookup is a DNS lookup as Attribution needs it.
type Lookup struct {
	Time    time.Time
	Client  netip.Addr
	Name    string
	Answers []netip.Addr
}

type answerKey struct{ client, answer netip.Addr }

type seen struct {
	at   time.Time
	name string
}

// Attributor finds the names Flows' Remote IPs were looked up as. It is safe for
// concurrent use.
type Attributor struct {
	mu     sync.Mutex
	byPair map[answerKey][]seen // newest last
}

// NewAttributor returns an empty Attributor.
func NewAttributor() *Attributor { return &Attributor{byPair: map[answerKey][]seen{}} }

// Add records lookups. They may come in any order, and again.
func (a *Attributor) Add(ls []Lookup) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, l := range ls {
		if l.Name == "" || !l.Client.IsValid() {
			continue
		}
		client := l.Client.Unmap()
		for _, ans := range l.Answers {
			ans = ans.Unmap()
			if !ans.IsValid() || ans.IsUnspecified() {
				continue // a blocked lookup's 0.0.0.0
			}
			k := answerKey{client, ans}
			list := a.byPair[k]
			i, found := slices.BinarySearchFunc(list, l.Time, func(s seen, t time.Time) int { return s.at.Compare(t) })
			if found && list[i].name == l.Name {
				continue
			}
			list = slices.Insert(list, i, seen{l.Time, l.Name})
			if len(list) > perAnswer {
				list = list[len(list)-perAnswer:]
			}
			a.byPair[k] = list
		}
	}
}

// Name returns the name client looked up last before a Flow to remote that started at
// start, or "" when it didn't look it up within Window.
func (a *Attributor) Name(client, remote netip.Addr, start time.Time) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	list := a.byPair[answerKey{client.Unmap(), remote.Unmap()}]
	for i := len(list) - 1; i >= 0; i-- {
		s := list[i]
		if s.at.After(start.Add(Skew)) {
			continue
		}
		if start.Sub(s.at) > Window {
			return ""
		}
		return s.name
	}
	return ""
}

// Prune forgets lookups too old to name a Flow starting at now or later.
func (a *Attributor) Prune(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, list := range a.byPair {
		i := 0
		for i < len(list) && now.Sub(list[i].at) > Window+Skew {
			i++
		}
		if i == len(list) {
			delete(a.byPair, k)
		} else if i > 0 {
			a.byPair[k] = slices.Clone(list[i:])
		}
	}
}

// Size is how many client and answer pairs are kept.
func (a *Attributor) Size() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.byPair)
}
