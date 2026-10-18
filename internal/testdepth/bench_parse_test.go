package testdepth

import (
	"reflect"
	"testing"
)

const benchOutput = `goos: linux
goarch: amd64
pkg: example.com/codec
cpu: AMD EPYC 7763 64-Core Processor
BenchmarkDecode-8        	  921234	      1203 ns/op	     256 B/op	       3 allocs/op
BenchmarkDecode-8        	  918812	      1199.5 ns/op	     256 B/op	       3 allocs/op
BenchmarkEncode/small-16 	 5000000	       240 ns/op	        12.5 MB/s	      0 B/op	       0 allocs/op
BenchmarkBroken-8        	   notanumber
BenchmarkHalf-8          	      10	      abc ns/op
PASS
ok  	example.com/codec	3.012s
`

func TestBenchParse(t *testing.T) {
	t.Parallel()
	got := benchParse(benchOutput)
	want := benchSamples{
		"BenchmarkDecode": {
			"ns/op":     {1203, 1199.5},
			"B/op":      {256, 256},
			"allocs/op": {3, 3},
		},
		"BenchmarkEncode/small": {
			"ns/op":     {240},
			"MB/s":      {12.5},
			"B/op":      {0},
			"allocs/op": {0},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parse:\n got %v\nwant %v", got, want)
	}
	if names := got.benchNames(); !reflect.DeepEqual(names, []string{"BenchmarkDecode", "BenchmarkEncode/small"}) {
		t.Errorf("names = %v", names)
	}
	if order := benchUnitOrder(got["BenchmarkEncode/small"]); !reflect.DeepEqual(order, []string{"ns/op", "B/op", "allocs/op", "MB/s"}) {
		t.Errorf("order = %v", order)
	}
	if order := benchUnitOrder(map[string][]float64{"z/op": nil, "a/op": nil}); !reflect.DeepEqual(order, []string{"a/op", "z/op"}) {
		t.Errorf("custom order = %v", order)
	}
}

func TestBenchFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		unit string
		v    float64
		want string
	}{
		{"ns/op", 0, "0ns"},
		{"ns/op", 12.34, "12.3ns"},
		{"ns/op", 1203, "1.20µs"},
		{"ns/op", 1.48e6, "1.48ms"},
		{"ns/op", 2.5e9, "2.50s"},
		{"ns/op", 123456, "123µs"},
		{"B/op", 512, "512B"},
		{"B/op", 2048, "2.00KiB"},
		{"B/op", 3 << 20, "3.00MiB"},
		{"allocs/op", 3, "3"},
		{"allocs/op", 2.5, "2.50"},
		{"MB/s", 12.5, "12.5 MB/s"},
		{"ns/op", 98765, "98.8µs"},
		{"allocs/op", 4500, "4500"},
	}
	for _, tc := range tests {
		if got := benchFormat(tc.unit, tc.v); got != tc.want {
			t.Errorf("benchFormat(%q, %v) = %q, want %q", tc.unit, tc.v, got, tc.want)
		}
	}
	if benchMetricName("ns/op") != "time/op" || benchMetricName("B/op") != "B/op" {
		t.Error("metric names")
	}
	if got := benchRegex([]string{"BenchmarkA", "BenchmarkB.x"}); got != `^(BenchmarkA|BenchmarkB\.x)$` {
		t.Errorf("regex = %q", got)
	}
}
