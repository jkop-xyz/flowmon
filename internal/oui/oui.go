// Package oui names the organization a MAC address was assigned to, from the IEEE
// Registration Authority's registries (MA-L, MA-M and MA-S), and tells randomized
// MAC addresses apart.
package oui

// To update oui.tsv.gz, download oui.csv, mam.csv and oui36.csv from
// https://standards-oui.ieee.org/ here, then run go generate. The CSVs aren't committed.
//
//go:generate go run ./gen -out oui.tsv.gz oui.csv mam.csv oui36.csv

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"net"
	"strconv"
	"strings"
	"sync"
)

//go:embed oui.tsv.gz
var table []byte

// subdivided is the name the MA-L registry gives a block IEEE split into MA-M and MA-S
// blocks; a MAC in one whose smaller block isn't listed has no known vendor.
const subdivided = "IEEE Registration Authority"

var (
	load sync.Once
	// blocks maps a prefix to its organization, keyed by prefix length in bits (24, 28
	// or 36) and the prefix's value.
	blocks map[blockKey]string
)

type blockKey struct {
	bits  uint8
	value uint64
}

func loadTable() {
	blocks = make(map[blockKey]string, 60000)
	zr, err := gzip.NewReader(bytes.NewReader(table))
	if err != nil {
		panic("oui: embedded table: " + err.Error())
	}
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		prefix, name, ok := strings.Cut(sc.Text(), "\t")
		v, err := strconv.ParseUint(prefix, 16, 64)
		if !ok || err != nil {
			continue
		}
		blocks[blockKey{uint8(len(prefix) * 4), v}] = name
	}
	if err := sc.Err(); err != nil {
		panic("oui: embedded table: " + err.Error())
	}
}

// Private reports whether mac is locally administered: a randomized ("private") address
// such as phones and laptops use, or one a VM or container set. It has no vendor.
func Private(mac net.HardwareAddr) bool {
	return len(mac) > 0 && mac[0]&0x02 != 0
}

// Vendor returns the organization mac was assigned to, or "" when it is unknown,
// locally administered, a group (multicast) address, or not 6 bytes long.
func Vendor(mac net.HardwareAddr) string {
	if len(mac) != 6 || mac[0]&0x03 != 0 {
		return ""
	}
	load.Do(loadTable)
	var v uint64
	for _, b := range mac {
		v = v<<8 | uint64(b)
	}
	for _, bits := range []uint8{36, 28, 24} {
		if name, ok := blocks[blockKey{bits, v >> (48 - bits)}]; ok && name != subdivided {
			return name
		}
	}
	return ""
}
