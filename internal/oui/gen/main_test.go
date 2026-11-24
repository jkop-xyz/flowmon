package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

func TestParseAndWrite(t *testing.T) {
	csv := "\ufeffRegistry,Assignment,Organization Name,Organization Address\n" +
		"MA-L,F0D2F1,\"Amazon Technologies Inc.\",\"P.O Box 8102 Reno NV US 89507 \"\n" +
		"MA-L,286FB9,\"Nokia Shanghai Bell Co., Ltd.\",somewhere\n" +
		"MA-L,00000C,\"Cisco Systems, Inc\",somewhere\n" +
		"MA-M,70B3D51,Some Sensor GmbH,somewhere\n" +
		"MA-S,70B3D5123,\"Tiny Things, LLC\",somewhere\n" +
		"MA-L,ABCDEF,Private,\n" +
		"MA-L,12345,Too Short Inc,somewhere\n" +
		"CID,1A2B3C,Not a MAC block,somewhere\n"
	rows, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Write(&b, rows); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(&b)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	want := "00000C\tCisco Systems\n286FB9\tNokia Shanghai Bell\n70B3D51\tSome Sensor\n70B3D5123\tTiny Things\nF0D2F1\tAmazon Technologies\n"
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if _, err := Parse(strings.NewReader("Registry,Assignment\n")); err == nil {
		t.Error("a CSV without the expected columns was accepted")
	}
}

func TestShortName(t *testing.T) {
	for in, want := range map[string]string{
		"Apple, Inc.":                                           "Apple",
		"TP-LINK TECHNOLOGIES CO.,LTD.":                         "TP-LINK",
		"Routerboard.com":                                       "Routerboard.com",
		"Raspberry Pi Trading Ltd":                              "Raspberry Pi Trading",
		"Samsung Electronics Co.,Ltd":                           "Samsung Electronics",
		"Hon Hai Precision Ind. Co.,Ltd.":                       "Hon Hai Precision Ind",
		"AVM Audiovisuelles Marketing und Computersysteme GmbH": "AVM Audiovisuelles Marketing und Computersysteme",
		"Sonos, Inc.":                                           "Sonos",
		"Intel Corporate":                                       "Intel Corporate",
		"Espressif Inc.":                                        "Espressif",
		"Inc":                                                   "Inc",
		"  Ubiquiti   Inc  ":                                    "Ubiquiti",
	} {
		if got := ShortName(in); got != want {
			t.Errorf("ShortName(%q) = %q, want %q", in, got, want)
		}
	}
}
