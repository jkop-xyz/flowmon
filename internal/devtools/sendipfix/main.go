// Command sendipfix sends synthetic IPFIX (or NetFlow v9 or v5 with -version) to a
// collector, for developing the UI without a router:
//
//	go run ./internal/devtools/sendipfix -addr 127.0.0.1:4739
//
// It sends a MikroTik-shaped Template followed by random Flows between a few Local Hosts
// and Remote IPs. With -no-template it only sends data, to exercise the "0% decoded" path.
package main

import (
	"encoding/binary"
	"flag"
	"log"
	"math/rand/v2"
	"net"
	"net/netip"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4739", "collector UDP address")
	interval := flag.Duration("interval", time.Second, "time between messages")
	count := flag.Int("count", 0, "messages to send (0 = forever)")
	perMessage := flag.Int("flows", 10, "Flows per message")
	noTemplate := flag.Bool("no-template", false, "never send the Template")
	version := flag.Int("version", 10, "10 for IPFIX, 9 for NetFlow v9, 5 for NetFlow v5")
	biflow := flag.Bool("biflow", false, "IPFIX only: send biflow records (RFC 5103) with reverse counters")
	macs := flag.Bool("macs", false, "add MAC addresses as RouterOS sends them: a fixed one per Local Host, the router's own on replies")
	sampling := flag.Int("sampling", 0, "announce sampling 1 in N packets (an Options record, or the v5 header)")
	source := flag.String("source", "", "local address to send from, e.g. 127.0.0.3:0, to simulate another Exporter")
	flag.Parse()
	if *version != 5 && *version != 9 && *version != 10 {
		log.Fatal("-version must be 5, 9 or 10")
	}
	if *biflow && *version != 10 {
		log.Fatal("-biflow needs IPFIX")
	}
	if *macs && *version == 5 {
		log.Fatal("NetFlow v5 has no MAC addresses")
	}
	if *version == 5 && *perMessage > 30 {
		log.Fatal("a NetFlow v5 packet holds at most 30 Flows")
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
	// MAC addresses, as RouterOS fills them in (measured on a capture): the frame's
	// source and destination before routing, then after. On a reply to a LAN host it
	// names the router's own MAC as the destination even after routing.
	routerLAN, routerWAN, isp := mac(0x48, 1), mac(0x48, 2), mac(0x00, 0x99)
	hostMAC := func(local []byte) []byte { return mac(0x3c, local[3]) }
	withMACs := func(rec [][]byte, src, dst, postSrc, postDst []byte) [][]byte {
		if !*macs {
			return rec
		}
		return append(rec, src, dst, postSrc, postDst)
	}
	if *macs {
		tmpl.ID = 259
		tmpl.Fields = append(tmpl.Fields,
			ipfix.FieldSpec{ID: 56, Length: 6}, // sourceMacAddress
			ipfix.FieldSpec{ID: 80, Length: 6}, // destinationMacAddress
			ipfix.FieldSpec{ID: 81, Length: 6}, // postSourceMacAddress
			ipfix.FieldSpec{ID: 57, Length: 6}) // postDestinationMacAddress
	}
	if *biflow {
		// RFC 5103: the reverse direction's counters, as enterprise 29305 elements.
		tmpl.ID += 4 // 260, or 263 with MACs
		tmpl.Fields = append(tmpl.Fields,
			ipfix.FieldSpec{Enterprise: ipfix.EnterpriseReverse, ID: 1, Length: 4},
			ipfix.FieldSpec{Enterprise: ipfix.EnterpriseReverse, ID: 2, Length: 4},
			ipfix.FieldSpec{ID: 160, Length: 8}) // systemInitTimeMilliseconds, so times are exact
	}
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
				records = append(records, withMACs(append([][]byte{
					local, peer, localPort, ipfixtest.U16(445), ipfixtest.U8(6), ipfixtest.U32(1), ipfixtest.U32(1),
					ipfixtest.U32(uint32(5_000 + rand.IntN(5_000_000))), ipfixtest.U32(uint32(1 + rand.IntN(3000))),
				}, append(times, local, peer)...), hostMAC(local), routerLAN, routerLAN, hostMAC(peer)))
			case rand.IntN(3) == 0:
				// Outbound request: the LAN host is the pre-NAT source, the router's WAN address the post-NAT one.
				records = append(records, withMACs(append([][]byte{
					local, remote, localPort, service, proto, ipfixtest.U32(1), ipfixtest.U32(3),
					ipfixtest.U32(uint32(500 + rand.IntN(200_000))), ipfixtest.U32(uint32(1 + rand.IntN(200))),
				}, append(times, wan, remote)...), hostMAC(local), routerLAN, routerWAN, isp))
			default:
				// Download reply, as MikroTik reports it: sent to the WAN address, with the LAN host
				// only in the post-NAT destination.
				records = append(records, withMACs(append([][]byte{
					remote, wan, service, localPort, proto, ipfixtest.U32(3), ipfixtest.U32(1),
					ipfixtest.U32(uint32(5_000 + rand.IntN(3_000_000))), ipfixtest.U32(uint32(1 + rand.IntN(2000))),
				}, append(times, remote, local)...), isp, routerWAN, routerLAN, routerLAN))
			}
		}
		// RouterOS resends Templates every 20 packets by default.
		withTemplate := !*noTemplate && i%20 == 0
		var packet []byte
		switch *version {
		case 5:
			packet = netflowV5(records, boot, wan, seq, uint16(*sampling))
			seq += uint32(len(records))
		case 9:
			// NetFlow v9 counts packets, not records.
			msg := ipfixtest.NewNetFlowV9(0, uint32(i), time.Now(), boot)
			if withTemplate {
				msg.TemplateFlowSet(tmpl)
				if *sampling > 0 {
					// Scope System (v9 scope type 1) → samplingInterval.
					opts := ipfix.Template{ID: 257, ScopeFieldCount: 1, Fields: []ipfix.FieldSpec{{ID: 1, Length: 4}, {ID: 34, Length: 4}}}
					msg.OptionsTemplateFlowSet(opts).DataFlowSet(opts, [][]byte{ipfixtest.U32(0), ipfixtest.U32(uint32(*sampling))})
				}
			}
			packet = msg.DataFlowSet(tmpl, records...).Bytes()
		default:
			msg := ipfixtest.NewMessage(0, seq, time.Now())
			if withTemplate {
				msg.TemplateSet(tmpl)
				if *sampling > 0 {
					opts := ipfix.Template{ID: 257, ScopeFieldCount: 1, Fields: []ipfix.FieldSpec{{ID: 144, Length: 4}, {ID: 305, Length: 4}, {ID: 306, Length: 4}}}
					msg.OptionsTemplateSet(opts).DataSet(opts, [][]byte{ipfixtest.U32(1), ipfixtest.U32(1), ipfixtest.U32(uint32(*sampling - 1))})
					seq++ // IPFIX sequence numbers count every data record, Options ones too
				}
			}
			if *biflow {
				for j := range records {
					records[j] = append(records[j], ipfixtest.U32(uint32(rand.IntN(2_000_000))), ipfixtest.U32(uint32(1+rand.IntN(1500))),
						ipfixtest.U64(uint64(boot.UnixMilli())))
				}
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

// netflowV5 sends the same Flows as NetFlow v5, which has no NAT fields: the router
// reports a download reply as going to the Local Host, as one exporting from its LAN
// side would.
func netflowV5(records [][][]byte, boot time.Time, wan []byte, seq uint32, sampling uint16) []byte {
	m := &ipfixtest.NetFlowV5{Sequence: seq, ExportTime: time.Now(), Boot: boot}
	if sampling > 0 {
		m.Sampling = 0x4000 | sampling&0x3FFF // mode 1: deterministic
	}
	u16 := func(b []byte) uint16 { return binary.BigEndian.Uint16(b) }
	u32 := func(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
	for _, r := range records {
		dst := r[1]
		if string(dst) == string(wan) {
			dst = r[12] // the post-NAT destination: the Local Host
		}
		m.Records = append(m.Records, ipfixtest.NetFlowV5Record{
			Src: netip.AddrFrom4([4]byte(r[0])), Dst: netip.AddrFrom4([4]byte(dst)),
			SrcPort: u16(r[2]), DstPort: u16(r[3]), Protocol: r[4][0],
			Input: uint16(u32(r[5])), Output: uint16(u32(r[6])), Octets: u32(r[7]), Packets: u32(r[8]),
			First: boot.Add(time.Duration(u32(r[9])) * time.Millisecond), Last: boot.Add(time.Duration(u32(r[10])) * time.Millisecond),
		})
	}
	return m.Bytes()
}

// mac makes a sample MAC address: a vendor's first byte (0x3c, Google's 3c:5a:b4, for
// hosts; 0x48, MikroTik's 48:a9:8a, for the router) and a last byte that tells them apart.
func mac(vendor, last byte) []byte {
	switch vendor {
	case 0x3c:
		return []byte{0x3c, 0x5a, 0xb4, 0x00, 0x00, last}
	case 0x48:
		return []byte{0x48, 0xa9, 0x8a, 0x00, 0x00, last}
	}
	return []byte{0x00, 0x00, 0x0c, 0x00, 0x00, last}
}
