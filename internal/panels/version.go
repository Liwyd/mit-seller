package panels

import (
	"strconv"
	"strings"
	"unicode"
)

// VersionCompare is PHP's version_compare($a, $b) returning -1, 0 or 1.
func VersionCompare(a, b string) int {
	pa, pb := canonVersion(a), canonVersion(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y string
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if c := cmpPart(x, y); c != 0 {
			return c
		}
	}
	return 0
}

func canonVersion(v string) []string {
	var parts []string
	var cur strings.Builder
	kind := 0 // 1 digit, 2 alpha
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, cur.String())
			cur.Reset()
		}
	}
	for _, r := range v {
		switch {
		case unicode.IsDigit(r):
			if kind == 2 {
				flush()
			}
			kind = 1
			cur.WriteRune(r)
		case r == '.' || r == '-' || r == '_' || r == '+':
			flush()
			kind = 0
		default:
			if kind == 1 {
				flush()
			}
			kind = 2
			cur.WriteRune(r)
		}
	}
	flush()
	return parts
}

func specialOrder(s string) int {
	switch strings.ToLower(s) {
	case "dev":
		return 0
	case "alpha", "a":
		return 1
	case "beta", "b":
		return 2
	case "rc", "c":
		return 3
	case "#":
		return 4
	case "pl", "p":
		return 5
	}
	return -1
}

func cmpPart(x, y string) int {
	if x == "" && y == "" {
		return 0
	}
	// a missing part is lower than a number and compares like "#" otherwise
	if x == "" {
		if _, err := strconv.Atoi(y); err == nil {
			return -1
		}
		x = "#"
	}
	if y == "" {
		if _, err := strconv.Atoi(x); err == nil {
			return 1
		}
		y = "#"
	}
	xn, xerr := strconv.Atoi(x)
	yn, yerr := strconv.Atoi(y)
	switch {
	case xerr == nil && yerr == nil:
		switch {
		case xn < yn:
			return -1
		case xn > yn:
			return 1
		}
		return 0
	case xerr == nil:
		return cmpInt(specialOrder("#"), specialOrder(y))
	case yerr == nil:
		return cmpInt(specialOrder(x), specialOrder("#"))
	}
	return cmpInt(specialOrder(x), specialOrder(y))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
