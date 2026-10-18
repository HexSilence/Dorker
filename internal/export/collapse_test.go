package export

import (
	"testing"

	"github.com/HexSilence/dorker/internal/model"
)

func TestCollapseModesAndRepresentative(t *testing.T) {
	urls := []string{
		"https://saba.ye/en", "https://www.saba.ye/en/category15.htm", "https://www.saba.ye/fr/", "https://www.saba.ye/ar/category1.htm",
		"https://yemenmobile.com.ye/en/homepage", "https://yemenmobile.com.ye/", "https://yemenmobile.com.ye/en",
		"https://post.ye/", "https://post.ye/web/", "https://post.ye/post/main/", "https://mail.post.ye/",
	}
	results := make([]model.Result, len(urls))
	for i, u := range urls {
		results[i] = model.Result{NormalizedURL: u}
	}
	tests := []struct {
		mode  CollapseMode
		count int
	}{{CollapseNone, 11}, {CollapseHost, 5}, {CollapseDomain, 3}}
	for _, tc := range tests {
		got, err := Collapse(results, tc.mode)
		if err != nil || len(got) != tc.count {
			t.Fatalf("%s: count=%d err=%v", tc.mode, len(got), err)
		}
	}
	domain, _ := Collapse(results, CollapseDomain)
	want := map[string]bool{"https://saba.ye/en": true, "https://yemenmobile.com.ye/": true, "https://mail.post.ye/": true}
	for _, r := range domain {
		if !want[r.NormalizedURL] {
			t.Errorf("unexpected representative %s", r.NormalizedURL)
		}
	}
}

func TestCollapseUsesPublicSuffixList(t *testing.T) {
	results := []model.Result{{NormalizedURL: "https://www.example.co.uk/a"}, {NormalizedURL: "https://mail.example.co.uk/"}, {NormalizedURL: "https://other.co.uk/"}}
	got, err := Collapse(results, CollapseDomain)
	if err != nil || len(got) != 2 {
		t.Fatalf("count=%d err=%v", len(got), err)
	}
}

func TestRepresentativeRankingIsOrderIndependent(t *testing.T) {
	urls := []string{"http://example.com/", "https://example.com/a?x=1", "https://example.com/a", "https://example.com/"}
	for reverse := false; ; reverse = true {
		var in []model.Result
		for i := range urls {
			j := i
			if reverse {
				j = len(urls) - 1 - i
			}
			in = append(in, model.Result{NormalizedURL: urls[j]})
		}
		got, _ := Collapse(in, CollapseHost)
		if len(got) != 1 || got[0].NormalizedURL != "https://example.com/" {
			t.Fatalf("representative=%v", got)
		}
		if reverse {
			break
		}
	}
}
