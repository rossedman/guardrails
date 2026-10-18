package testdepth

import (
	"math"
	"sort"
)

// benchExactLimit is the largest sample size per side for which the exact
// Mann-Whitney U distribution is used (when there are no ties).
const benchExactLimit = 20

// benchMannWhitney returns the two-sided p-value of the Mann-Whitney U test
// for samples x and y. It uses the exact distribution for small samples
// without ties, and the normal approximation with tie and continuity
// corrections otherwise.
func benchMannWhitney(x, y []float64) float64 {
	n1, n2 := len(x), len(y)
	if n1 == 0 || n2 == 0 {
		return 1
	}
	type obs struct {
		v     float64
		first bool
	}
	all := make([]obs, 0, n1+n2)
	for _, v := range x {
		all = append(all, obs{v, true})
	}
	for _, v := range y {
		all = append(all, obs{v, false})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v < all[j].v })

	// Average ranks for ties; tieTerm accumulates sum(t^3 - t).
	var r1, tieTerm float64
	ties := false
	for i := 0; i < len(all); {
		j := i
		for j+1 < len(all) && all[j+1].v == all[i].v {
			j++
		}
		rank := float64(i+j)/2 + 1
		if t := float64(j - i + 1); t > 1 {
			ties = true
			tieTerm += t*t*t - t
		}
		for k := i; k <= j; k++ {
			if all[k].first {
				r1 += rank
			}
		}
		i = j + 1
	}
	u := r1 - float64(n1*(n1+1))/2

	if !ties && n1 <= benchExactLimit && n2 <= benchExactLimit {
		return benchExactP(n1, n2, int(math.Round(u)))
	}

	nf1, nf2 := float64(n1), float64(n2)
	n := nf1 + nf2
	mean := nf1 * nf2 / 2
	variance := nf1 * nf2 / 12 * ((n + 1) - tieTerm/(n*(n-1)))
	if variance <= 0 {
		return 1
	}
	d := math.Abs(u-mean) - 0.5
	if d < 0 {
		d = 0
	}
	z := d / math.Sqrt(variance)
	return math.Min(1, math.Erfc(z/math.Sqrt2))
}

// benchExactP is the exact two-sided p-value for U = u with sample sizes
// n1 and n2: 2 * min(P(U <= u), P(U >= u)), capped at 1.
func benchExactP(n1, n2, u int) float64 {
	counts := benchUCounts(n1, n2)
	var total, le, ge float64
	for k, c := range counts {
		total += c
		if k <= u {
			le += c
		}
		if k >= u {
			ge += c
		}
	}
	return math.Min(1, 2*math.Min(le, ge)/total)
}

// benchUCounts returns, for each U from 0 to n1*n2, the number of
// orderings of n1 x-values and n2 y-values that produce it, via the
// recurrence f(m, n, u) = f(m-1, n, u-n) + f(m, n-1, u).
func benchUCounts(n1, n2 int) []float64 {
	maxU := n1 * n2
	// prev[n][u] holds f(m-1, n, u); cur holds f(m, n, u).
	prev := make([][]float64, n2+1)
	for n := range prev {
		prev[n] = make([]float64, maxU+1)
		prev[n][0] = 1 // f(0, n, 0) = 1
	}
	for m := 1; m <= n1; m++ {
		cur := make([][]float64, n2+1)
		for n := range cur {
			cur[n] = make([]float64, maxU+1)
		}
		cur[0][0] = 1 // f(m, 0, 0) = 1
		for n := 1; n <= n2; n++ {
			for u := 0; u <= m*n; u++ {
				v := cur[n-1][u]
				if u-n >= 0 {
					v += prev[n][u-n]
				}
				cur[n][u] = v
			}
		}
		prev = cur
	}
	return prev[n2]
}

// benchMedian returns the median of xs (0 for none).
func benchMedian(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}
