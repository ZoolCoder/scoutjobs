package store

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Boards quote salaries in their own currency and period. Store.Currency, when set, converts
// every new job to yearly figures in that currency so the pipeline sorts and filters on one scale.
// Rates are rough yearly averages against EUR; exactness matters less than comparability.

var eurRates = map[string]float64{
	"EUR": 1, "USD": 1.08, "GBP": 0.85, "CHF": 0.95, "CAD": 1.47, "AUD": 1.65, "NZD": 1.78,
	"SEK": 11.3, "NOK": 11.6, "DKK": 7.46, "PLN": 4.3, "CZK": 25, "HUF": 400, "RON": 5,
	"INR": 90, "JPY": 160, "BRL": 6, "SGD": 1.45, "ZAR": 19.5, "MXN": 20, "TRY": 35, "ILS": 4,
}

var currencySymbols = map[string]string{"€": "EUR", "$": "USD", "£": "GBP", "₹": "INR", "¥": "JPY", "zł": "PLN", "kr": "SEK", "R$": "BRL", "CA$": "CAD", "A$": "AUD", "US$": "USD"}

var currencyRe = regexp.MustCompile(`US\$|CA\$|A\$|R\$|[€$£₹¥]|zł|kr|\b[A-Z]{3}\b`)

var salaryRe = regexp.MustCompile(`(?i)(US\$|CA\$|A\$|R\$|[€$£₹¥]|zł|kr|\b[A-Z]{3}\b)?\s*(\d{1,3}(?:[,.]\d{3})+|\d+(?:\.\d+)?)\s*(k)?`)

// ParseSalary reads a free-text range such as "$80k - $120k", "60.000 – 80.000 EUR" or
// "£45,000 per year". It returns zeros when nothing looks like money.
func ParseSalary(text string) (minV, maxV int64, currency string) {
	var nums []int64
	for _, m := range salaryRe.FindAllStringSubmatch(text, -1) {
		raw := strings.NewReplacer(",", "", ".", "").Replace(m[2])
		if strings.Contains(m[2], ".") && !strings.Contains(m[2], ",") && len(m[2])-strings.LastIndex(m[2], ".") != 4 {
			raw = strings.Split(m[2], ".")[0] // 7.5k
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		if m[3] != "" {
			n *= 1000
		}
		if n < 10 {
			continue
		}
		if cur := currencyCode(m[1]); cur != "" && currency == "" {
			currency = cur
		}
		nums = append(nums, n)
	}
	if currency == "" {
		// "60.000 - 80.000 EUR": code after the numbers
		for _, m := range currencyRe.FindAllString(text, -1) {
			if currency = currencyCode(m); currency != "" {
				break
			}
		}
	}
	if len(nums) == 0 || currency == "" {
		return 0, 0, ""
	}
	minV, maxV = nums[0], nums[0]
	for _, n := range nums[1:] {
		if n < minV {
			minV = n
		}
		if n > maxV {
			maxV = n
		}
	}
	return minV, maxV, currency
}

func currencyCode(tok string) string {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return ""
	}
	if c, ok := currencySymbols[tok]; ok {
		return c
	}
	up := strings.ToUpper(tok)
	if _, ok := eurRates[up]; ok {
		return up
	}
	return ""
}

// NormalizeSalary rewrites j's salary as a yearly figure in target. Hourly and monthly
// quotes are recognised by size. The original quote is kept in the notes.
func NormalizeSalary(j *Job, target string) {
	target = strings.ToUpper(strings.TrimSpace(target))
	from := strings.ToUpper(strings.TrimSpace(j.Currency))
	if target == "" || j.SalaryMax == 0 || from == "" {
		return
	}
	period := "yearly"
	factor := 1.0
	switch {
	case j.SalaryMax < 400:
		period, factor = "hourly", 2080
	case j.SalaryMax < 15000:
		period, factor = "monthly", 12
	}
	rate := 1.0
	if from != target {
		src, okSrc := eurRates[from]
		dst, okDst := eurRates[target]
		if !okSrc || !okDst {
			return
		}
		rate = dst / src
	}
	if period == "yearly" && rate == 1 {
		return
	}
	orig := fmt.Sprintf("%d-%d %s %s", j.SalaryMin, j.SalaryMax, from, period)
	if j.SalaryMin == j.SalaryMax || j.SalaryMin == 0 {
		orig = fmt.Sprintf("%d %s %s", j.SalaryMax, from, period)
	}
	j.SalaryMin = round(float64(j.SalaryMin) * factor * rate)
	j.SalaryMax = round(float64(j.SalaryMax) * factor * rate)
	j.Currency = target
	note := "salary quoted as " + orig
	if j.Notes != "" {
		note = j.Notes + "\n" + note
	}
	j.Notes = note
}

func round(v float64) int64 {
	if v < 1000 {
		return int64(v + 0.5)
	}
	return int64(v/100+0.5) * 100
}

// KnownCurrency reports whether NormalizeSalary can convert to or from code.
func KnownCurrency(code string) bool {
	_, ok := eurRates[strings.ToUpper(strings.TrimSpace(code))]
	return ok
}
