package main

import (
	"flag"
	"reflect"
	"testing"
)

func TestParsePorts(t *testing.T) {
	for spec, want := range map[string][]portMap{
		"3000":           {{3000, 3000}},
		"8080:3000":      {{8080, 3000}},
		"3000-3002":      {{3000, 3000}, {3001, 3001}, {3002, 3002}},
		"3000,8080:3000": {{3000, 3000}, {8080, 3000}},
		" 3000 , 3001 ":  {{3000, 3000}, {3001, 3001}},
	} {
		got, err := parsePorts(spec)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("parsePorts(%q) = %v, %v; want %v", spec, got, err, want)
		}
	}
	for _, bad := range []string{"", "0", "70000", "abc", "3000:", ":3000", "3005-3000", "1-200", "3000,,3001", "03000", "3000:x"} {
		if got, err := parsePorts(bad); err == nil {
			t.Errorf("parsePorts(%q) = %v; want an error", bad, got)
		}
	}
}

func TestFlagsMayFollowPositionalArguments(t *testing.T) {
	var name *string
	fs, asJSON, err := flags("pair", []string{"calport://x", "--name", "devl", "--json"}, func(fs *flag.FlagSet) {
		name = fs.String("name", "", "")
	})
	if err != nil || !asJSON || *name != "devl" || fs.NArg() != 1 || fs.Arg(0) != "calport://x" {
		t.Fatalf("flags = json %v name %q args %v err %v", asJSON, *name, fs.Args(), err)
	}
	if _, _, err := flags("x", []string{"a", "--bogus"}, nil); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
	fs, _, _ = flags("x", []string{"a", "--", "--json"}, nil)
	if fs.NArg() != 2 || fs.Arg(1) != "--json" {
		t.Fatalf("arguments after -- were parsed as flags: %v", fs.Args())
	}
}
