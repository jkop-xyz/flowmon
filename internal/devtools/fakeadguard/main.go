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

var sites = []site{
	{name: "www.example.com", answers: []string{"CNAME edge.example.net", "A 93.184.215.14"}},
	{name: "api.github.com", answers: []string{"A 140.82.121.6"}},
	{name: "www.youtube.com", answers: []string{"CNAME youtube-ui.l.google.com", "A 142.250.185.78", "A 142.250.185.110"}},
	{name: "rr3---sn-4g5e6nsz.googlevideo.com", answers: []string{"A 173.194.160.8"}},
	{name: "time.apple.com", answers: []string{"A 17.253.53.207"}},
	{name: "www.bbc.co.uk", answers: []string{"CNAME www.bbc.net.uk", "A 151.101.0.81"}},
	{name: "update.googleapis.com", answers: []string{"A 142.250.186.35"}},
	{name: "connectivity-check.ubuntu.com", answers: []string{"AAAA 2620:2d:4000:1::22"}},
	{name: "ads.doubleclick.net", blocked: true},
	{name: "telemetry.example-tv.com", blocked: true},
}

var clients = []string{"192.168.10.44", "192.168.10.45", "192.168.10.46", "fd00::44"}

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
