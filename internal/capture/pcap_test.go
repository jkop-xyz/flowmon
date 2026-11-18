package capture

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func TestWritesReadablePcap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.pcap")
	w, err := Open(Options{Path: path, MaxFileBytes: 1 << 20, MaxFiles: 3, MaxDuration: time.Hour}, t0)
	if err != nil {
		t.Fatal(err)
	}
	src := netip.MustParseAddrPort("192.168.10.1:38000")
	dst := netip.MustParseAddrPort("0.0.0.0:4739")
	payload := []byte{0, 10, 0, 16, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if err := w.Write(t0, src, dst, payload); err != nil {
		t.Fatal(err)
	}
	w.Close()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(b) != 0xa1b2c3d4 || binary.LittleEndian.Uint32(b[20:]) != linkTypeRaw {
		t.Fatalf("bad global header %x", b[:24])
	}
	pkt := b[24+16:]
	if pkt[0] != 0x45 || pkt[9] != 17 || len(pkt) != 20+8+len(payload) {
		t.Fatalf("bad IPv4/UDP packet %x", pkt)
	}
	if checksum(pkt[:20], 0) != 0 {
		t.Error("IPv4 header checksum does not verify")
	}
	if binary.BigEndian.Uint16(pkt[20:]) != 38000 || binary.BigEndian.Uint16(pkt[22:]) != 4739 {
		t.Error("wrong UDP ports")
	}
	if string(pkt[28:]) != string(payload) {
		t.Error("payload changed")
	}
}

func TestIPv6PacketChecksum(t *testing.T) {
	pkt := buildPacket(netip.MustParseAddrPort("[2001:db8::1]:5000"), netip.MustParseAddrPort("[2001:db8::2]:4739"), []byte("hello"))
	if pkt[0]>>4 != 6 {
		t.Fatal("expected IPv6")
	}
	var pseudo uint32
	for i := 8; i < 40; i += 2 {
		pseudo += uint32(binary.BigEndian.Uint16(pkt[i:]))
	}
	pseudo += uint32(len(pkt)-40) + 17
	if checksum(pkt[40:], pseudo) != 0 {
		t.Error("UDP checksum does not verify")
	}
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.pcap")
	w, err := Open(Options{Path: path, MaxFileBytes: 200, MaxFiles: 3, MaxDuration: time.Hour}, t0)
	if err != nil {
		t.Fatal(err)
	}
	src := netip.MustParseAddrPort("10.0.0.1:1")
	dst := netip.MustParseAddrPort("10.0.0.2:2")
	for i := 0; i < 20; i++ {
		if err := w.Write(t0, src, dst, make([]byte, 100)); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Errorf("want 3 files (current + 2 rotated), got %d", len(entries))
	}
	for _, e := range entries {
		info, _ := e.Info()
		if info.Size() > 200 {
			t.Errorf("%s is %d bytes, over the limit", e.Name(), info.Size())
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %v; captures are private", e.Name(), info.Mode().Perm())
		}
	}
}

func TestStopsAfterMaxDuration(t *testing.T) {
	w, err := Open(Options{Path: filepath.Join(t.TempDir(), "c.pcap"), MaxDuration: time.Minute}, t0)
	if err != nil {
		t.Fatal(err)
	}
	src := netip.MustParseAddrPort("10.0.0.1:1")
	if err := w.Write(t0.Add(30*time.Second), src, src, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(t0.Add(2*time.Minute), src, src, []byte{1}); !errors.Is(err, ErrStopped) {
		t.Errorf("expected ErrStopped, got %v", err)
	}
}
