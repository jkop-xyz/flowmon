package oui

import (
	"net"
	"testing"
)

func mac(s string) net.HardwareAddr {
	m, err := net.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return m
}

func TestVendor(t *testing.T) {
	for in, want := range map[string]string{
		"48:a9:8a:01:02:03": "Routerboard.com", // MA-L
		"b8:27:eb:aa:bb:cc": "Raspberry Pi Foundation",
		"f0:d2:f1:00:00:01": "Amazon Technologies",
		"e4:95:6e:00:00:00": "SMC Networks", // an MA-M block in a subdivided MA-L block
		"de:ad:be:ef:00:01": "",             // locally administered
		"01:00:5e:00:00:fb": "",             // multicast
		"00:00:00:00:00:00": "XEROX",
	} {
		if got := Vendor(mac(in)); got != want {
			t.Errorf("Vendor(%s) = %q, want %q", in, got, want)
		}
	}
	if Vendor(net.HardwareAddr{1, 2, 3}) != "" {
		t.Error("a 3-byte address got a vendor")
	}
}

func TestSubdividedBlockWithoutItsSmallBlockIsUnknown(t *testing.T) {
	load.Do(loadTable)
	for k, name := range blocks {
		if k.bits != 24 || name != subdivided {
			continue
		}
		for nibble := uint64(0); nibble < 16; nibble++ {
			if _, ok := blocks[blockKey{28, k.value<<4 | nibble}]; ok {
				continue
			}
			m := net.HardwareAddr{byte(k.value >> 16), byte(k.value >> 8), byte(k.value), byte(nibble << 4), 0, 1}
			if got := Vendor(m); got != "" {
				t.Errorf("Vendor(%s) = %q; want unknown, not the block IEEE subdivided", m, got)
			}
			return
		}
	}
	t.Skip("every subdivided block is fully assigned")
}

func TestLongestBlockWins(t *testing.T) {
	load.Do(loadTable)
	var ma28, ma36 int
	for k, name := range blocks {
		switch {
		case k.bits == 28 && ma28 == 0:
			m := net.HardwareAddr{byte(k.value >> 20), byte(k.value >> 12), byte(k.value >> 4), byte(k.value<<4) | 0x0a, 0x12, 0x34}
			if got := Vendor(m); got != name {
				t.Errorf("MA-M %07X: Vendor(%s) = %q, want %q", k.value, m, got, name)
			}
			ma28++
		case k.bits == 36 && ma36 == 0:
			m := net.HardwareAddr{byte(k.value >> 28), byte(k.value >> 20), byte(k.value >> 12), byte(k.value >> 4), byte(k.value<<4) | 0x0b, 0x77}
			if got := Vendor(m); got != name {
				t.Errorf("MA-S %09X: Vendor(%s) = %q, want %q", k.value, m, got, name)
			}
			ma36++
		}
	}
	if ma28 == 0 || ma36 == 0 {
		t.Errorf("the table has %d MA-M and %d MA-S blocks checked; want both", ma28, ma36)
	}
}

func TestPrivate(t *testing.T) {
	for in, want := range map[string]bool{
		"48:a9:8a:01:02:03": false,
		"da:a1:19:00:00:01": true,
		"02:42:ac:11:00:02": true, // Docker
		"6e:00:00:00:00:00": true,
	} {
		if got := Private(mac(in)); got != want {
			t.Errorf("Private(%s) = %v, want %v", in, got, want)
		}
	}
}
