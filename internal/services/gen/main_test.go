package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	csv := `Service Name,Port Number,Transport Protocol,Description,Assignee
,0,tcp,Reserved,
http,80,tcp,World Wide Web HTTP,
www,80,tcp,World Wide Web HTTP,
http,80,udp,"World Wide
 Web HTTP",
https,443,sctp,HTTPS,
x11,6000-6063,tcp,X Window System,
,1234,tcp,Unassigned,
domain,53,udp,Domain Name Server,
odd,53,ddp,Not a transport here,
`
	rows, err := Parse(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Write(&b, rows); err != nil {
		t.Fatal(err)
	}
	want := "6\t80\thttp\tWorld Wide Web HTTP\n17\t53\tdomain\tDomain Name Server\n17\t80\thttp\tWorld Wide Web HTTP\n132\t443\thttps\tHTTPS\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
	if _, err := Parse(strings.NewReader("Name,Port\n")); err == nil {
		t.Error("a CSV without the expected columns was accepted")
	}
}
