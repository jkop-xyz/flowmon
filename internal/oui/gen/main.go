// Command gen turns the IEEE Registration Authority's MAC address block registries
// (oui.csv for MA-L, mam.csv for MA-M and oui36.csv for MA-S, from
// https://standards-oui.ieee.org/) into oui.tsv.gz: one line per block, its prefix in
// upper-case hex (6, 7 or 9 digits) and its organization's short name.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strings"
)

// prefixLen is the hex digits of each registry's prefix.
var prefixLen = map[string]int{"MA-L": 6, "MA-M": 7, "MA-S": 9}

// Row is one assigned block.
type Row struct {
	Prefix string
	Name   string
}

// Parse reads one registry CSV.
func Parse(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}
	for _, need := range []string{"Registry", "Assignment", "Organization Name"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("CSV has no %q column", need)
		}
	}
	var rows []Row
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		field := func(name string) string {
			if i := col[name]; i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		n, ok := prefixLen[field("Registry")]
		prefix := strings.ToUpper(field("Assignment"))
		name := ShortName(field("Organization Name"))
		if !ok || len(prefix) != n || !isHex(prefix) || name == "" || name == "Private" {
			continue
		}
		rows = append(rows, Row{Prefix: prefix, Name: name})
	}
	return rows, nil
}

// legalForms are endings dropped from organization names, so a Device reads "Apple"
// or "TP-Link" rather than "Apple, Inc." or "TP-LINK TECHNOLOGIES CO.,LTD.". Longer
// forms first.
var legalForms = []string{
	"technologies co., ltd", "technology co., ltd", "technologies co.,ltd", "technology co.,ltd",
	"co., ltd", "co.,ltd", "co ltd", "co. ltd", "pty ltd", "pvt ltd", "limited", "ltd", "inc", "incorporated",
	"llc", "l.l.c", "gmbh & co. kg", "gmbh", "corporation", "corp", "co", "ag", "s.a", "sa", "s.p.a", "spa",
	"b.v", "bv", "a/s", "ab", "oy", "plc", "s.r.l", "srl", "s.l", "sas", "kg", "kk", "k.k",
}

// ShortName trims an organization's legal form and punctuation from its end, and
// collapses spaces.
func ShortName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	for changed := true; changed; {
		changed = false
		trimmed := strings.TrimRight(name, " .,")
		lower := strings.ToLower(trimmed)
		for _, f := range legalForms {
			if rest, ok := strings.CutSuffix(lower, f); ok && rest != "" && (strings.HasSuffix(rest, " ") || strings.HasSuffix(rest, ",")) {
				trimmed = trimmed[:len(rest)]
				break
			}
		}
		trimmed = strings.TrimRight(trimmed, " .,")
		if trimmed != name && trimmed != "" {
			name, changed = trimmed, true
		}
	}
	return name
}

func isHex(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789ABCDEF", c) {
			return false
		}
	}
	return true
}

// Write writes rows, sorted by prefix, as tab-separated prefix and name, gzipped.
func Write(w io.Writer, rows []Row) error {
	rows = slices.Clone(rows)
	slices.SortFunc(rows, func(a, b Row) int { return strings.Compare(a.Prefix, b.Prefix) })
	rows = slices.CompactFunc(rows, func(a, b Row) bool { return a.Prefix == b.Prefix })
	zw := gzip.NewWriter(w) // no name or time in the header, so output is reproducible
	bw := bufio.NewWriter(zw)
	for _, r := range rows {
		fmt.Fprintf(bw, "%s\t%s\n", r.Prefix, strings.ReplaceAll(r.Name, "\t", " "))
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return zw.Close()
}

func main() {
	out := flag.String("out", "oui.tsv.gz", "output file")
	flag.Parse()
	if flag.NArg() == 0 {
		log.Fatal("usage: gen [-out oui.tsv.gz] oui.csv mam.csv oui36.csv")
	}
	var rows []Row
	for _, in := range flag.Args() {
		f, err := os.Open(in)
		if err != nil {
			log.Fatal(err)
		}
		r, err := Parse(f)
		f.Close()
		if err != nil {
			log.Fatalf("%s: %v", in, err)
		}
		rows = append(rows, r...)
	}
	o, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := Write(o, rows); err != nil {
		log.Fatal(err)
	}
	if err := o.Close(); err != nil {
		log.Fatal(err)
	}
}
