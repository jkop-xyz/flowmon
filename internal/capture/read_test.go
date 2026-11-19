package capture

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadWhatWasWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.pcap")
	w, err := Open(Options{Path: path, MaxFileBytes: 1 << 20, MaxFiles: 1, MaxDuration: time.Hour}, t0)
	if err != nil {
		t.Fatal(err)
	}
	in := []Datagram{
		{Time: t0, Src: netip.MustParseAddrPort("192.168.10.1:38000"), Dst: netip.MustParseAddrPort("192.168.10.5:2055"), Payload: []byte{0, 9, 1, 2}},
		{Time: t0.Add(1500 * time.Microsecond), Src: netip.MustParseAddrPort("[fd00::1]:5000"), Dst: netip.MustParseAddrPort("[fd00::5]:4739"), Payload: []byte{0, 10, 3}},
	}
	for _, d := range in {
		if err := w.Write(d.Time, d.Src, d.Dst, d.Payload); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	f, _ := os.Open(path)
	defer f.Close()
	got, err := Read(f)
	if err != nil || len(got) != 2 {
		t.Fatalf("%d datagrams, %v", len(got), err)
	}
	for i := range in {
		if !got[i].Time.Equal(in[i].Time) || got[i].Src != in[i].Src || got[i].Dst != in[i].Dst || !bytes.Equal(got[i].Payload, in[i].Payload) {
			t.Errorf("datagram %d: %+v, want %+v", i, got[i], in[i])
		}
	}
}

func TestReadEthernetCapture(t *testing.T) {
	// A tcpdump capture: big-endian, Ethernet with a VLAN tag, and a non-UDP packet.
	pkt := buildPacket(netip.MustParseAddrPort("10.0.0.1:1000"), netip.MustParseAddrPort("10.0.0.2:2055"), []byte("flow"))
	eth := append(make([]byte, 12), 0x81, 0x00, 0, 10, 0x08, 0x00)
	frames := [][]byte{append(append([]byte{}, eth...), pkt...), append(make([]byte, 12), 0x08, 0x06, 1, 2, 3)}
	var b bytes.Buffer
	hdr := make([]byte, 24)
	binary.BigEndian.PutUint32(hdr, 0xa1b2c3d4)
	binary.BigEndian.PutUint32(hdr[16:], 65535)
	binary.BigEndian.PutUint32(hdr[20:], linkTypeEthernet)
	b.Write(hdr)
	for _, fr := range frames {
		rec := make([]byte, 16)
		binary.BigEndian.PutUint32(rec, uint32(t0.Unix()))
		binary.BigEndian.PutUint32(rec[8:], uint32(len(fr)))
		binary.BigEndian.PutUint32(rec[12:], uint32(len(fr)))
		b.Write(rec)
		b.Write(fr)
	}
	got, err := Read(&b)
	if err != nil || len(got) != 1 || string(got[0].Payload) != "flow" || got[0].Dst.Port() != 2055 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestReadRejectsPcapng(t *testing.T) {
	if _, err := Read(bytes.NewReader([]byte{0x0a, 0x0d, 0x0d, 0x0a, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})); err == nil {
		t.Error("pcapng read as pcap")
	}
}
