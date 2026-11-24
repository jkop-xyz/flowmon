// Command fakeadguard serves a fake AdGuard Home whose query log fills with made-up
// lookups from a few devices, for trying Flowmon's AdGuard Home Data Source without one:
//
//	go run ./internal/devtools/fakeadguard -addr 127.0.0.1:3001
//
// Add it in Flowmon as http://127.0.0.1:3001 with user admin and password admin. With
// -router, every lookup comes from that address, as when clients use the router as
// their DNS server.
package main

import (
	"flag"
	"log"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/jkop-xyz/flowmon/internal/adguard/adguardtest"
)

type site struct {
	name    string
	answers []string
	blocked bool
}

// sites answer with the Remote IPs internal/devtools/sendipfix sends Flows to, and the
// clients are its Local Hosts, so the two together exercise Domain Attribution.
var sites = []site{
	{name: "www.google.com", answers: []string{"A 142.250.74.46"}},
	{name: "www.youtube.com", answers: []string{"CNAME youtube-ui.l.google.com", "A 142.250.74.46"}},
	{name: "www.cloudflare.com", answers: []string{"A 104.16.132.229"}},
	{name: "www.reddit.com", answers: []string{"CNAME reddit.map.fastly.net", "A 151.101.1.140"}},
	{name: "one.one.one.one", answers: []string{"A 1.1.1.1"}},
	{name: "dns.quad9.net", answers: []string{"A 9.9.9.9"}},
	{name: "www.bbc.co.uk", answers: []string{"CNAME www.bbc.net.uk", "A 151.101.1.140"}},
	{name: "connectivity-check.ubuntu.com", answers: []string{"AAAA 2620:2d:4000:1::22"}},
	{name: "ads.doubleclick.net", blocked: true},
	{name: "telemetry.example-tv.com", blocked: true},
}

var clients = []string{"192.168.10.44", "192.168.10.50", "10.0.20.14", "10.0.20.15"}

func main() {
	addr := flag.String("addr", "127.0.0.1:3001", "address to listen on")
	user := flag.String("user", "admin", "user name")
	pass := flag.String("password", "admin", "password")
	rate := flag.Duration("every", 500*time.Millisecond, "time between lookups")
	router := flag.String("router", "", "send every lookup from this address")
	history := flag.Duration("history", 2*time.Hour, "made-up history to start with")
	flag.Parse()

	fake := adguardtest.New(*user, *pass)
	add := func(at time.Time) {
		s := sites[rand.IntN(len(sites))]
		client := clients[rand.IntN(len(clients))]
		if *router != "" {
			client = *router
		}
		qtype := "A"
		if len(s.answers) > 0 && s.answers[len(s.answers)-1][:4] == "AAAA" {
			qtype = "AAAA"
		}
		e := adguardtest.Query(at, client, s.name, qtype, s.answers...)
		if s.blocked {
			e = adguardtest.Blocked(e, "||"+s.name+"^")
		}
		fake.Add(e)
	}
	start := time.Now().Add(-*history)
	for t := start; t.Before(time.Now()); t = t.Add(*rate * 10) {
		add(t)
	}
	go func() {
		for now := range time.Tick(*rate) {
			add(now)
		}
	}()
	log.Printf("fake AdGuard Home on http://%s (user %q)", *addr, *user)
	log.Fatal(http.ListenAndServe(*addr, fake))
}
