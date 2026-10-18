package testdepth

import (
	"math"
	"testing"
)

func TestBenchMannWhitney(t *testing.T) {
	t.Parallel()
	seq := func(from, n int) []float64 {
		out := make([]float64, n)
		for i := range out {
			out[i] = float64(from + i)
		}
		return out
	}
	tests := []struct {
		name string
		x, y []float64
		want float64
		tol  float64
	}{
		// Exact distribution for n=3/3: U=3, P(U<=3)=7/20, so p=0.7.
		{"exact interleaved", []float64{1, 3, 5}, []float64{2, 4, 6}, 0.7, 1e-12},
		// Fully separated samples: p = 2/C(n1+n2, n1).
		{"separated 5/5", seq(1, 5), seq(6, 5), 2.0 / 252, 1e-12},
		{"separated 6/6", seq(1, 6), seq(7, 6), 2.0 / 924, 1e-12},
		{"separated reversed", seq(7, 6), seq(1, 6), 2.0 / 924, 1e-12},
		{"identical", []float64{1, 1, 1}, []float64{1, 1, 1}, 1, 0},
		{"empty", nil, []float64{1}, 1, 0},
		// Normal approximation with continuity correction (n=21 per side,
		// above the exact limit): U=0, mean=220.5, var=21*21*43/12.
		{"normal separated 21/21", seq(1, 21), seq(22, 21), 3.125399998400882e-08, 1e-15},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := benchMannWhitney(tc.x, tc.y); math.Abs(got-tc.want) > tc.tol {
				t.Errorf("p = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("ties use the normal approximation", func(t *testing.T) {
		t.Parallel()
		p := benchMannWhitney([]float64{1, 1, 2, 2, 3, 3}, []float64{3, 3, 4, 4, 5, 5})
		if p <= 0 || p >= benchSignificance {
			t.Errorf("tied but clearly shifted samples: p = %v", p)
		}
	})
	t.Run("small difference is not significant", func(t *testing.T) {
		t.Parallel()
		if p := benchMannWhitney([]float64{10, 12, 11, 13, 9, 10}, []float64{11, 10, 12, 13, 10, 11}); p < 0.3 {
			t.Errorf("p = %v, want large", p)
		}
	})
}

func TestBenchUCountsSumToBinomial(t *testing.T) {
	t.Parallel()
	for _, n := range [][2]int{{1, 1}, {3, 3}, {4, 6}, {6, 6}, {10, 7}} {
		total := 0.0
		for _, c := range benchUCounts(n[0], n[1]) {
			total += c
		}
		want := 1.0
		for i := 1; i <= n[0]; i++ {
			want = want * float64(n[1]+i) / float64(i)
		}
		if math.Abs(total-want) > 1e-6 {
			t.Errorf("%v: total %v, want C=%v", n, total, want)
		}
	}
}

func TestBenchMedian(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   []float64
		want float64
	}{{nil, 0}, {[]float64{3, 1, 2}, 2}, {[]float64{4, 1, 3, 2}, 2.5}} {
		if got := benchMedian(tc.in); got != tc.want {
			t.Errorf("median(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
