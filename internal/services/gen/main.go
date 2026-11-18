// Command gen turns IANA's Service Name and Transport Protocol Port Number Registry
// (service-names-port-numbers.csv, from
// https://www.iana.org/assignments/service-names-port-numbers/) into services.tsv: one
// line per protocol and port, with the first Service Name the registry lists for it and
// its description. Port ranges and rows without a name are left out.
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
)

// protocols are the transport protocols the registry assigns ports for.
var protocols = map[string]uint8{"tcp": 6, "udp": 17, "dccp": 33, "sctp": 132}

// Row is one protocol and port's Service Name.
type Row struct {
	Protocol    uint8
	Port        uint16
	Name        string
	Description string
}

// Parse reads the registry CSV.
func Parse(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	for _, need := range []string{"Service Name", "Port Number", "Transport Protocol", "Description"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("CSV has no %q column", need)
		}
	}
	type key struct {
		proto uint8
		port  uint16
	}
	seen := map[key]bool{}
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
		name := field("Service Name")
		proto, ok := protocols[field("Transport Protocol")]
		port, err := strconv.ParseUint(field("Port Number"), 10, 16)
		if name == "" || !ok || err != nil || port == 0 {
			continue // unnamed, another protocol, a range, or reserved
		}
		k := key{proto, uint16(port)}
		if seen[k] {
			continue // the first name listed is the usual one (http before www)
		}
		seen[k] = true
		desc := strings.Join(strings.Fields(field("Description")), " ")
		rows = append(rows, Row{Protocol: proto, Port: uint16(port), Name: name, Description: desc})
	}
	slices.SortFunc(rows, func(a, b Row) int {
		if a.Protocol != b.Protocol {
			return int(a.Protocol) - int(b.Protocol)
		}
		return int(a.Port) - int(b.Port)
	})
	return rows, nil
}

// Write writes rows as tab-separated protocol, port, name and description.
func Write(w io.Writer, rows []Row) error {
	bw := bufio.NewWriter(w)
	for _, r := range rows {
		fmt.Fprintf(bw, "%d\t%d\t%s\t%s\n", r.Protocol, r.Port, r.Name, strings.ReplaceAll(r.Description, "\t", " "))
	}
	return bw.Flush()
}

func main() {
	in := flag.String("in", "service-names-port-numbers.csv", "registry CSV")
	out := flag.String("out", "services.tsv", "output file")
	flag.Parse()
	f, err := os.Open(*in)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	rows, err := Parse(f)
	if err != nil {
		log.Fatal(err)
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
