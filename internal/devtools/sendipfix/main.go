// Command sendipfix sends synthetic IPFIX (or NetFlow v9 with -version 9) to a
// collector, for developing the UI without a router:
//
//	go run ./internal/devtools/sendipfix -addr 127.0.0.1:4739
//
// It sends a MikroTik-shaped Template followed by random Flows between a few Local Hosts
// and Remote IPs. With -no-template it only sends data, to exercise the "0% decoded" path.
package main

import (
	"flag"
	"log"
	"math/rand/v2"
	"net"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4739", "collector UDP address")
	interval := flag.Duration("interval", time.Second, "time between messages")
	count := flag.Int("count", 0, "messages to send (0 = forever)")
	perMessage := flag.Int("flows", 10, "Flows per message")
	noTemplate := flag.Bool("no-template", false, "never send the Template")
	version := flag.Int("version", 10, "10 for IPFIX, 9 for NetFlow v9")
	source := flag.String("source", "", "local address to send from, e.g. 127.0.0.3:0, to simulate another Exporter")
	flag.Parse()
	if *version != 9 && *version != 10 {
		log.Fatal("-version must be 9 or 10")
	}

	dialer := net.Dialer{}
	if *source != "" {
		laddr, err := net.ResolveUDPAddr("udp", *source)
		if err != nil {
			log.Fatal(err)
		}
		dialer.LocalAddr = laddr
	}
	conn, err := dialer.Dial("udp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	locals := [][]byte{{192, 168, 10, 44}, {192, 168, 10, 50}, {10, 0, 20, 14}, {10, 0, 20, 15}}
	// Remote IPs around the world, so GeoIP enrichment has something to show. Earlier ones
	// are picked more often, like real traffic, where a few destinations dominate.
	remotes := [][]byte{{142, 250, 74, 46}, {104, 16, 132, 229}, {151, 101, 1, 140}, {1, 1, 1, 1}, {9, 9, 9, 9},
		{185, 15, 59, 224}, {193, 0, 6, 139}, {81, 2, 69, 142}, {194, 242, 2, 2}, {77, 88, 8, 8},
		{133, 242, 0, 3}, {202, 12, 29, 205}, {200, 160, 4, 6}, {196, 216, 2, 6}, {223, 5, 5, 5}}
	wan := []byte{203, 0, 113, 7}
	start := time.Now()
	boot := start.Add(-time.Minute) // the uptimes below start at a minute
	var seq uint32

	for i := 0; *count == 0 || i < *count; i++ {
		var records [][][]byte
		for j := 0; j < *perMessage; j++ {
			uptime := uint32(time.Since(start).Milliseconds()) + 60_000
			local, remote := locals[rand.IntN(len(locals))], remotes[min(len(remotes)-1, int(rand.ExpFloat64()*3))]
			localPort := ipfixtest.U16(uint16(32768 + rand.IntN(28000)))
			service := ipfixtest.U16([]uint16{443, 53, 80, 123}[rand.IntN(4)])
			proto := ipfixtest.U8([]uint8{6, 17}[rand.IntN(2)])
			times := [][]byte{ipfixtest.U32(uptime - 15_000), ipfixtest.U32(uptime)}
			switch {
			case rand.IntN(8) == 0:
				// Internal: one LAN host to another (file sharing), not NATed.
				peer := locals[rand.IntN(len(locals))]
				if string(peer) == string(local) {
					continue
				}
				records = append(records, append([][]byte{
					local, peer, localPort, ipfixtest.U16(445), ipfixtest.U8(6), ipfixtest.U32(1), ipfixtest.U32(1),
					ipfixtest.U32(uint32(5_000 + rand.IntN(5_000_000))), ipfixtest.U32(uint32(1 + rand.IntN(3000))),
				}, append(times, local, peer)...))
			case rand.IntN(3) == 0:
				// Outbound request: the LAN host is the pre-NAT source, the router's WAN address the post-NAT one.
				records = append(records, append([][]byte{
					local, remote, localPort, service, proto, ipfixtest.U32(1), ipfixtest.U32(3),
					ipfixtest.U32(uint32(500 + rand.IntN(200_000))), ipfixtest.U32(uint32(1 + rand.IntN(200))),
				}, append(times, wan, remote)...))
			default:
				// Download reply, as MikroTik reports it: sent to the WAN address, with the LAN host
				// only in the post-NAT destination.
				records = append(records, append([][]byte{
					remote, wan, service, localPort, proto, ipfixtest.U32(3), ipfixtest.U32(1),
					ipfixtest.U32(uint32(5_000 + rand.IntN(3_000_000))), ipfixtest.U32(uint32(1 + rand.IntN(2000))),
				}, append(times, remote, local)...))
			}
		}
		// RouterOS resends Templates every 20 packets by default.
		withTemplate := !*noTemplate && i%20 == 0
		var packet []byte
		if *version == 9 {
			// NetFlow v9 counts packets, not records.
			msg := ipfixtest.NewNetFlowV9(0, uint32(i), time.Now(), boot)
			if withTemplate {
				msg.TemplateFlowSet(tmpl)
			}
			packet = msg.DataFlowSet(tmpl, records...).Bytes()
		} else {
			msg := ipfixtest.NewMessage(0, seq, time.Now())
			if withTemplate {
				msg.TemplateSet(tmpl)
			}
			packet = msg.DataSet(tmpl, records...).Bytes()
			seq += uint32(len(records))
		}
		if _, err := conn.Write(packet); err != nil {
			log.Fatal(err)
		}
		time.Sleep(*interval)
	}
}
