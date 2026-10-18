package testdepth

import (
	"bufio"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// benchSamples maps benchmark name (without the -GOMAXPROCS suffix) to
// metric unit to the values from each run.
type benchSamples map[string]map[string][]float64

var benchProcs = regexp.MustCompile(`-\d+$`)

// benchParse reads `go test -bench` output.
func benchParse(out string) benchSamples {
	s := benchSamples{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || !strings.HasPrefix(f[0], "Benchmark") {
			continue
		}
		if _, err := strconv.ParseInt(f[1], 10, 64); err != nil {
			continue
		}
		name := benchProcs.ReplaceAllString(f[0], "")
		for i := 2; i+1 < len(f); i += 2 {
			v, err := strconv.ParseFloat(f[i], 64)
			if err != nil {
				break
			}
			if s[name] == nil {
				s[name] = map[string][]float64{}
			}
			s[name][f[i+1]] = append(s[name][f[i+1]], v)
		}
	}
	return s
}

// benchNames returns the sorted benchmark names.
func (s benchSamples) benchNames() []string {
	out := make([]string, 0, len(s))
	for n := range s {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// benchUnitOrder sorts the standard metrics first.
func benchUnitOrder(units map[string][]float64) []string {
	rank := map[string]int{"ns/op": 0, "B/op": 1, "allocs/op": 2}
	out := make([]string, 0, len(units))
	for u := range units {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		ri, iok := rank[out[i]]
		rj, jok := rank[out[j]]
		switch {
		case iok && jok:
			return ri < rj
		case iok != jok:
			return iok
		default:
			return out[i] < out[j]
		}
	})
	return out
}

// benchMetricName renders a unit as benchstat names the metric.
func benchMetricName(unit string) string {
	if unit == "ns/op" {
		return "time/op"
	}
	return unit
}

// benchFormat renders a value in human-friendly units.
func benchFormat(unit string, v float64) string {
	switch unit {
	case "ns/op":
		switch {
		case v >= 1e9:
			return benchSig(v/1e9) + "s"
		case v >= 1e6:
			return benchSig(v/1e6) + "ms"
		case v >= 1e3:
			return benchSig(v/1e3) + "µs"
		default:
			return benchSig(v) + "ns"
		}
	case "B/op":
		switch {
		case v >= 1<<20:
			return benchSig(v/(1<<20)) + "MiB"
		case v >= 1<<10:
			return benchSig(v/(1<<10)) + "KiB"
		default:
			return benchSig(v) + "B"
		}
	case "allocs/op":
		if v == math.Trunc(v) {
			return strconv.FormatFloat(v, 'f', 0, 64)
		}
		return benchSig(v)
	default:
		return benchSig(v) + " " + unit
	}
}

// benchSig formats with three significant digits.
func benchSig(v float64) string {
	if v == 0 {
		return "0"
	}
	digits := 2 - int(math.Floor(math.Log10(math.Abs(v))))
	if digits < 0 {
		digits = 0
	}
	return strconv.FormatFloat(v, 'f', digits, 64)
}

// benchRegex builds a -bench pattern matching exactly names.
func benchRegex(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	return fmt.Sprintf("^(%s)$", strings.Join(quoted, "|"))
}
