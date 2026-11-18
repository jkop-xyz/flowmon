package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestGeneratedFileIsCurrent fails when elements_gen.go doesn't match the pinned CSV,
// for example after the CSV was updated without running `go generate`.
func TestGeneratedFileIsCurrent(t *testing.T) {
	f, err := os.Open("../ipfix-information-elements.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Generate(rows)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../elements_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("elements_gen.go is out of date; run `go generate ./internal/ipfix/iana`")
	}
}

func TestUnknownDataTypeFails(t *testing.T) {
	csv := "ElementID,Name,Abstract Data Type,Data Type Semantics,Status,Units\n" +
		"999,futureThing,quantumBit,default,current,\n"
	if _, err := Parse(strings.NewReader(csv)); err == nil || !strings.Contains(err.Error(), "quantumBit") {
		t.Fatalf("expected an unknown-type error, got %v", err)
	}
}

func TestSkipsRangesAndReserved(t *testing.T) {
	csv := "ElementID,Name,Abstract Data Type,Data Type Semantics,Status,Units\n" +
		"0,Reserved,,,,\n" +
		"1,octetDeltaCount,unsigned64,deltaCounter,current,octets\n" +
		"105-127,Assigned for NetFlow v9 compatibility,,,,\n"
	rows, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "octetDeltaCount" || rows[0].TypeConst != "Unsigned64" {
		t.Fatalf("rows %+v", rows)
	}
}
