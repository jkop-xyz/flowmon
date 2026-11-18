// Package capture writes received flow-export datagrams to pcap files for debugging
// (FLOWMON_DEBUG_CAPTURE). Captures hold private metadata about who talked to whom,
// so capture is off by default, limited in size and run time, and rotated.
package capture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sync"
	"time"
)

// Options bound a capture.
type Options struct {
	// Path is the capture file. Rotated files get .1, .2, … suffixes.
	Path string
	// MaxFileBytes rotates the file before it would grow past this size.
	MaxFileBytes int64
	// MaxFiles is how many files are kept, including the current one.
	MaxFiles int
	// MaxDuration stops capturing this long after Open.
	MaxDuration time.Duration
}

// Writer appends datagrams to a pcap file with synthetic IP and UDP headers, so
// Wireshark and tshark decode them as IPFIX/NetFlow. It is safe for concurrent use.
type Writer struct {
	mu      sync.Mutex
	opts    Options
	f       *os.File
	size    int64
	stopAt  time.Time
	stopped bool
}

// linkTypeRaw is LINKTYPE_RAW: each packet starts with an IPv4 or IPv6 header.
const linkTypeRaw = 101

const maxSnapLen = 65535

// Open starts a capture at now.
func Open(opts Options, now time.Time) (*Writer, error) {
	if opts.Path == "" {
		return nil, errors.New("capture: no path")
	}
	if opts.MaxFiles < 1 {
		opts.MaxFiles = 1
	}
	w := &Writer{opts: opts, stopAt: now.Add(opts.MaxDuration)}
	if err := w.openFile(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) openFile() error {
	f, err := os.OpenFile(w.opts.Path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	hdr := make([]byte, 24)
	binary.LittleEndian.PutUint32(hdr[0:], 0xa1b2c3d4) // microsecond timestamps
	binary.LittleEndian.PutUint16(hdr[4:], 2)
	binary.LittleEndian.PutUint16(hdr[6:], 4)
	binary.LittleEndian.PutUint32(hdr[16:], maxSnapLen)
	binary.LittleEndian.PutUint32(hdr[20:], linkTypeRaw)
	if _, err := f.Write(hdr); err != nil {
		f.Close()
		return fmt.Errorf("capture: %w", err)
	}
	w.f, w.size = f, int64(len(hdr))
	return nil
}

// ErrStopped is returned once the capture has reached its time limit or failed.
var ErrStopped = errors.New("capture: stopped")

// Write records one datagram received at ts from src on the collector socket dst.
func (w *Writer) Write(ts time.Time, src, dst netip.AddrPort, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return ErrStopped
	}
	if !ts.Before(w.stopAt) {
		w.stopLocked()
		return ErrStopped
	}

	pkt := buildPacket(src, dst, payload)
	if len(pkt) > maxSnapLen {
		pkt = pkt[:maxSnapLen]
	}
	rec := make([]byte, 16, 16+len(pkt))
	binary.LittleEndian.PutUint32(rec[0:], uint32(ts.Unix()))
	binary.LittleEndian.PutUint32(rec[4:], uint32(ts.Nanosecond()/1000))
	binary.LittleEndian.PutUint32(rec[8:], uint32(len(pkt)))
	binary.LittleEndian.PutUint32(rec[12:], uint32(len(pkt)))
	rec = append(rec, pkt...)

	if w.opts.MaxFileBytes > 0 && w.size+int64(len(rec)) > w.opts.MaxFileBytes {
		if err := w.rotateLocked(); err != nil {
			w.stopLocked()
			return err
		}
	}
	if _, err := w.f.Write(rec); err != nil {
		w.stopLocked()
		return fmt.Errorf("capture: %w", err)
	}
	w.size += int64(len(rec))
	return nil
}

func (w *Writer) rotateLocked() error {
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	w.f = nil
	if w.opts.MaxFiles == 1 {
		return w.openFile()
	}
	for i := w.opts.MaxFiles - 1; i >= 1; i-- {
		from := w.opts.Path
		if i > 1 {
			from = fmt.Sprintf("%s.%d", w.opts.Path, i-1)
		}
		if err := os.Rename(from, fmt.Sprintf("%s.%d", w.opts.Path, i)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("capture: %w", err)
		}
	}
	return w.openFile()
}

func (w *Writer) stopLocked() {
	w.stopped = true
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
}

// Close stops the capture.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
	return nil
}

// buildPacket wraps payload in an IPv4 or IPv6 header and a UDP header.
func buildPacket(src, dst netip.AddrPort, payload []byte) []byte {
	sa, da := src.Addr().Unmap(), dst.Addr().Unmap()
	if !da.IsValid() || da.IsUnspecified() {
		// A wildcard listener doesn't know its own address; use a placeholder of the same family.
		if sa.Is4() {
			da = netip.IPv4Unspecified()
		} else {
			da = netip.IPv6Unspecified()
		}
	}
	if sa.Is4() != da.Is4() {
		sa, da = netip.AddrFrom16(sa.As16()), netip.AddrFrom16(da.As16())
	}

	udpLen := 8 + len(payload)
	udp := make([]byte, 8, udpLen)
	binary.BigEndian.PutUint16(udp[0:], src.Port())
	binary.BigEndian.PutUint16(udp[2:], dst.Port())
	binary.BigEndian.PutUint16(udp[4:], uint16(udpLen))
	udp = append(udp, payload...)

	if sa.Is4() {
		ip := make([]byte, 20, 20+udpLen)
		ip[0] = 0x45
		binary.BigEndian.PutUint16(ip[2:], uint16(20+udpLen))
		ip[8] = 64 // TTL
		ip[9] = 17 // UDP
		s4, d4 := sa.As4(), da.As4()
		copy(ip[12:16], s4[:])
		copy(ip[16:20], d4[:])
		binary.BigEndian.PutUint16(ip[10:], checksum(ip, 0))
		// The IPv4 UDP checksum is optional; zero means "not computed".
		return append(ip, udp...)
	}

	ip := make([]byte, 40, 40+udpLen)
	ip[0] = 0x60
	binary.BigEndian.PutUint16(ip[4:], uint16(udpLen))
	ip[6] = 17 // UDP
	ip[7] = 64 // hop limit
	s16, d16 := sa.As16(), da.As16()
	copy(ip[8:24], s16[:])
	copy(ip[24:40], d16[:])
	// IPv6 requires the UDP checksum, over a pseudo-header of the addresses, length and protocol.
	var pseudo uint32
	for i := 8; i < 40; i += 2 {
		pseudo += uint32(binary.BigEndian.Uint16(ip[i:]))
	}
	pseudo += uint32(udpLen) + 17
	sum := checksum(udp, pseudo)
	if sum == 0 {
		sum = 0xFFFF
	}
	binary.BigEndian.PutUint16(udp[6:], sum)
	return append(ip, udp...)
}

func checksum(b []byte, initial uint32) uint16 {
	sum := initial
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i:]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xFFFF + sum>>16
	}
	return ^uint16(sum)
}
