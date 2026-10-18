package query

import (
	"strings"
	"testing"
)

func TestLoadAndPlan(t *testing.T) {
	qs, err := Load([]string{" α β ", "α β"}, nil, strings.NewReader("# comment\nγ\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 2 {
		t.Fatalf("%q", qs)
	}
	vs, err := Plan([]string{"a   b"}, "conservative", 2)
	if err != nil || len(vs) != 2 || vs[1].Original != "a   b" || vs[1].Variant != "a b" {
		t.Fatalf("%+v %v", vs, err)
	}
}
