package collector

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

// blockingSink holds the reader on the first records until released.
type blockingSink struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (b *blockingSink) Records(netip.Addr, []ipfix.Record) {
	b.once.Do(func() {
		close(b.started)
		<-b.release
	})
}

func TestKernelDropsAreCounted(t *testing.T) {
	sink := &blockingSink{started: make(chan struct{}), release: make(chan struct{})}
	c := New(Options{Sink: sink, ReceiveBuffer: 4096})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.ListenUDP(ctx, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	l := c.Listeners()[0]
	if !l.DropsCounted || l.GrantedBuffer == 0 || l.GrantedBuffer > 64<<10 {
		t.Fatalf("listener %+v", l)
	}
	conn, err := net.Dial("udp", l.Address[:len(l.Address)-len("/udp")])
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	msg := mikrotikMessage(0, true)
	if _, err := conn.Write(msg); err != nil {
		t.Fatal(err)
	}
	<-sink.started
	// The reader is stuck; these overflow the small buffer.
	for range 2000 {
		_, _ = conn.Write(msg)
	}
	close(sink.release)
	deadline := time.Now().Add(5 * time.Second)
	for c.Listeners()[0].KernelDrops == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no kernel drops counted: %+v", c.Listeners()[0])
		}
		_, _ = conn.Write(msg)
		time.Sleep(10 * time.Millisecond)
	}
	if st := c.Status(); st.LastAccepted == nil || st.LastAcceptedFrom != "127.0.0.1" {
		t.Errorf("last accepted %v %q", st.LastAccepted, st.LastAcceptedFrom)
	}
}
