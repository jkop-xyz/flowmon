// Command sendipfix sends synthetic IPFIX to a collector, for developing the UI without
// a router: go run ./internal/devtools/sendipfix -addr 127.0.0.1:4739
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
	source := flag.String("source", "", "local address to send from, e.g. 127.0.0.3:0, to simulate another Exporter")
	flag.Parse()

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
	remotes := [][]byte{{1, 1, 1, 1}, {142, 250, 74, 46}, {151, 101, 1, 140}, {104, 16, 132, 229}, {9, 9, 9, 9}}
	wan := []byte{203, 0, 113, 7}
	start := time.Now()
	var seq uint32

	for i := 0; *count == 0 || i < *count; i++ {
		msg := ipfixtest.NewMessage(0, seq, time.Now())
		// RouterOS resends Templates every 20 packets by default.
		if !*noTemplate && i%20 == 0 {
			msg.TemplateSet(tmpl)
		}
		var records [][][]byte
		for j := 0; j < *perMessage; j++ {
			uptime := uint32(time.Since(start).Milliseconds()) + 60_000
			local, remote := locals[rand.IntN(len(locals))], remotes[rand.IntN(len(remotes))]
			localPort := ipfixtest.U16(uint16(32768 + rand.IntN(28000)))
			service := ipfixtest.U16([]uint16{443, 53, 80, 123}[rand.IntN(4)])
			proto := ipfixtest.U8([]uint8{6, 17}[rand.IntN(2)])
			times := [][]byte{ipfixtest.U32(uptime - 15_000), ipfixtest.U32(uptime)}
			if rand.IntN(3) == 0 {
				// Outbound request: the LAN host is the pre-NAT source, the router's WAN address the post-NAT one.
				records = append(records, append([][]byte{
					local, remote, localPort, service, proto, ipfixtest.U32(1), ipfixtest.U32(3),
					ipfixtest.U32(uint32(500 + rand.IntN(200_000))), ipfixtest.U32(uint32(1 + rand.IntN(200))),
				}, append(times, wan, remote)...))
			} else {
				// Download reply, as MikroTik reports it: sent to the WAN address, with the LAN host
				// only in the post-NAT destination.
				records = append(records, append([][]byte{
					remote, wan, service, localPort, proto, ipfixtest.U32(3), ipfixtest.U32(1),
					ipfixtest.U32(uint32(5_000 + rand.IntN(3_000_000))), ipfixtest.U32(uint32(1 + rand.IntN(2000))),
				}, append(times, remote, local)...))
			}
		}
		msg.DataSet(tmpl, records...)
		seq += uint32(len(records))
		if _, err := conn.Write(msg.Bytes()); err != nil {
			log.Fatal(err)
		}
		time.Sleep(*interval)
	}
}
