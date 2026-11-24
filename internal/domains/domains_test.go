package domains

import "testing"

func TestRegistrable(t *testing.T) {
	for in, want := range map[string]string{
		"www.example.com":      "example.com",
		"example.com":          "example.com",
		"WWW.Example.COM.":     "example.com",
		"www.news.bbc.co.uk":   "bbc.co.uk",
		"co.uk":                "co.uk",
		"jakob.github.io":      "jakob.github.io",
		"cdn.jakob.github.io":  "jakob.github.io",
		"github.io":            "github.io",
		"s3.amazonaws.com":     "s3.amazonaws.com",
		"a.b.example.xn--p1ai": "example.xn--p1ai",
		"www.пример.рф":        "xn--e1afmkfd.xn--p1ai",
		"router.lan":           "router.lan",
		"lan":                  "lan",
		"_dmarc.example.org":   "example.org",
		"":                     "",
		"time.apple.com.":      "apple.com",
		"e1.whatsapp.net":      "whatsapp.net",
	} {
		if got := Registrable(in); got != want {
			t.Errorf("Registrable(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"WWW.Example.com.": "www.example.com",
		" example.com ":    "example.com",
		"bücher.de":        "xn--bcher-kva.de",
		"xn--bcher-kva.de": "xn--bcher-kva.de",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
