// Package php reproduces the handful of PHP built-ins whose exact behaviour the
// original bot relied on (number parsing, loose comparison, number_format,
// float-to-string, sprintf with %s, jdate). The bot's data was written by PHP
// and existing users see texts formatted by PHP, so these follow PHP 8 rather
// than Go's own conventions.
package php

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// Tehran is the timezone the PHP bot pinned with date_default_timezone_set.
var Tehran = mustLoad("Asia/Tehran")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		// Iran has had a fixed +03:30 offset since DST was abolished in 2022.
		return time.FixedZone("IRST", 3*3600+1800)
	}
	return loc
}

const phpSpace = " \t\n\r\v\f"

// numericPrefix returns the longest leading numeric part of s (after leading
// whitespace), whether it looks like a float, and whether the whole string is
// numeric in the is_numeric() sense (trailing whitespace allowed).
func numericPrefix(s string) (num string, isFloat bool, whole bool) {
	i := 0
	for i < len(s) && strings.IndexByte(phpSpace, s[i]) >= 0 {
		i++
	}
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		j := i + 1
		frac := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
			frac++
		}
		if digits > 0 || frac > 0 {
			i = j
			digits += frac
			isFloat = true
		}
	}
	if digits == 0 {
		return "", false, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		exp := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
			exp++
		}
		if exp > 0 {
			i = j
			isFloat = true
		}
	}
	num = s[start:i]
	rest := s[i:]
	whole = strings.Trim(rest, phpSpace) == ""
	return num, isFloat, whole
}

// IsNumeric mirrors is_numeric() for strings.
func IsNumeric(s string) bool {
	_, _, whole := numericPrefix(s)
	return whole
}

// Intval mirrors intval()/(int) for strings.
func Intval(s string) int64 {
	num, isFloat, _ := numericPrefix(s)
	if num == "" {
		return 0
	}
	if isFloat {
		f, _ := strconv.ParseFloat(num, 64)
		return FloatToInt(f)
	}
	v, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		if strings.HasPrefix(num, "-") {
			return math.MinInt64
		}
		return math.MaxInt64
	}
	return v
}

// FloatToInt is PHP's (int) cast of a float.
func FloatToInt(f float64) int64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	if f >= 9.2233720368547758e18 || f <= -9.2233720368547758e18 {
		return 0
	}
	return int64(f)
}

// Floatval mirrors floatval() for strings.
func Floatval(s string) float64 {
	num, _, _ := numericPrefix(s)
	if num == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(num, 64)
	return f
}

// CtypeDigit mirrors ctype_digit() for a string argument.
func CtypeDigit(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// LooseEq is PHP 8 `==` between two strings: numeric strings compare as numbers.
func LooseEq(a, b string) bool {
	if a == b {
		return true
	}
	if IsNumeric(a) && IsNumeric(b) {
		return Floatval(a) == Floatval(b)
	}
	return false
}

// Num converts a PHP-ish scalar (as stored in the DB or typed by a user) to a
// number for arithmetic, the way PHP's arithmetic operators would.
func Num(s string) float64 { return Floatval(s) }

// Round is PHP's round() (half away from zero) with precision.
func Round(f float64, precision int) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return f
	}
	p := math.Pow(10, float64(precision))
	v := f * p
	// pre-round to 15 significant digits like PHP's php_round_helper does, so
	// values such as 1.005 round the way PHP users expect.
	pre, err := strconv.ParseFloat(strconv.FormatFloat(v, 'g', 15, 64), 64)
	if err == nil {
		v = pre
	}
	r := math.Round(v)
	return r / p
}

// NumberFormat is number_format($f, $decimals) with "," and ".".
func NumberFormat(f float64, decimals int) string {
	if decimals < 0 {
		decimals = 0
	}
	f = Round(f, decimals)
	neg := f < 0
	f = math.Abs(f)
	s := strconv.FormatFloat(f, 'f', decimals, 64)
	intPart, frac := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart, frac = s[:dot], s[dot+1:]
	}
	var b strings.Builder
	n := len(intPart)
	for i, c := range intPart {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if decimals > 0 {
		out += "." + frac
	}
	if neg && strings.Trim(out, "0.,") != "" {
		out = "-" + out
	}
	return out
}

// NumberFormatS formats a value that PHP would have held as a string.
func NumberFormatS(s string) string { return NumberFormat(Floatval(s), 0) }

// FloatToString is how PHP 8 converts a float to string (precision = 14,
// i.e. printf("%.14G") with PHP's own exponent spelling).
func FloatToString(f float64) string {
	if math.IsNaN(f) {
		return "NAN"
	}
	if math.IsInf(f, 1) {
		return "INF"
	}
	if math.IsInf(f, -1) {
		return "-INF"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0"
		}
		return "0"
	}
	const precision = 14
	e := strconv.FormatFloat(f, 'e', precision-1, 64)
	idx := strings.IndexByte(e, 'e')
	mant, expS := e[:idx], e[idx+1:]
	exp, _ := strconv.Atoi(expS)
	if exp < -4 || exp >= precision {
		if strings.Contains(mant, ".") {
			mant = strings.TrimRight(strings.TrimRight(mant, "0"), ".")
		}
		if !strings.Contains(mant, ".") {
			mant += ".0"
		}
		sign := "+"
		if exp < 0 {
			sign = "-"
			exp = -exp
		}
		return mant + "E" + sign + strconv.Itoa(exp)
	}
	out := strconv.FormatFloat(f, 'f', precision-1-exp, 64)
	if strings.Contains(out, ".") {
		out = strings.TrimRight(strings.TrimRight(out, "0"), ".")
	}
	return out
}

// NumStr prints the result of PHP arithmetic: integers without a fraction,
// everything else the PHP float way.
func NumStr(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 9.2e18 {
		return strconv.FormatInt(int64(f), 10)
	}
	return FloatToString(f)
}

// ToString converts a Go value the way PHP's string conversion would.
func ToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return FloatToString(x)
	case bool:
		if x {
			return "1"
		}
		return ""
	case interface{ String() string }:
		return x.String()
	}
	return ""
}

// Sprintf implements the subset of PHP sprintf the texts use (%s, %d, %%).
// Missing arguments render as empty instead of aborting the request.
func Sprintf(format string, args ...any) string {
	var b strings.Builder
	ai := 0
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' || i+1 >= len(format) {
			b.WriteByte(c)
			continue
		}
		n := format[i+1]
		switch n {
		case '%':
			b.WriteByte('%')
			i++
		case 's', 'd':
			var v any
			if ai < len(args) {
				v = args[ai]
			}
			ai++
			if n == 'd' {
				b.WriteString(strconv.FormatInt(Intval(ToString(v)), 10))
			} else {
				b.WriteString(ToString(v))
			}
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

var faDigits = []string{"۰", "۱", "۲", "۳", "۴", "۵", "۶", "۷", "۸", "۹"}

// TrNumFa converts ASCII digits to Persian ones (jdf's tr_num($s, 'fa', '.')).
func TrNumFa(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteString(faDigits[r-'0'])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// GregorianToJalali is jdf's gregorian_to_jalali.
func GregorianToJalali(gy, gm, gd int) (int, int, int) {
	gdm := []int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	gy2 := gy
	if gm > 2 {
		gy2 = gy + 1
	}
	days := 355666 + (365 * gy) + ((gy2 + 3) / 4) - ((gy2 + 99) / 100) + ((gy2 + 399) / 400) + gd + gdm[gm-1]
	jy := -1595 + (33 * (days / 12053))
	days %= 12053
	jy += 4 * (days / 1461)
	days %= 1461
	if days > 365 {
		jy += (days - 1) / 365
		days = (days - 1) % 365
	}
	var jm, jd int
	if days < 186 {
		jm = 1 + days/31
		jd = 1 + days%31
	} else {
		jm = 7 + (days-186)/30
		jd = 1 + (days-186)%30
	}
	return jy, jm, jd
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// Jdate is jdf's jdate($format, $ts) for the format letters the bot uses,
// with Persian digits as jdate produces by default.
func Jdate(format string, ts int64) string {
	t := time.Unix(ts, 0).In(Tehran)
	jy, jm, jd := GregorianToJalali(t.Year(), int(t.Month()), t.Day())
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c == '\\' && i+1 < len(format) {
			i++
			b.WriteByte(format[i])
			continue
		}
		switch c {
		case 'Y':
			b.WriteString(strconv.Itoa(jy))
		case 'y':
			s := strconv.Itoa(jy)
			if len(s) >= 4 {
				s = s[2:4]
			}
			b.WriteString(s)
		case 'm':
			b.WriteString(pad2(jm))
		case 'n':
			b.WriteString(strconv.Itoa(jm))
		case 'd':
			b.WriteString(pad2(jd))
		case 'j':
			b.WriteString(strconv.Itoa(jd))
		case 'H':
			b.WriteString(pad2(t.Hour()))
		case 'G':
			b.WriteString(strconv.Itoa(t.Hour()))
		case 'h':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			b.WriteString(pad2(h))
		case 'g':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			b.WriteString(strconv.Itoa(h))
		case 'i':
			b.WriteString(pad2(t.Minute()))
		case 's':
			b.WriteString(pad2(t.Second()))
		case 'a':
			if t.Hour() < 12 {
				b.WriteString("ق.ظ")
			} else {
				b.WriteString("ب.ظ")
			}
		case 'U':
			b.WriteString(strconv.FormatInt(ts, 10))
		default:
			b.WriteByte(c)
		}
	}
	return TrNumFa(b.String())
}

// JdateNow is jdate($format) for the current time.
func JdateNow(format string) string { return Jdate(format, time.Now().Unix()) }

// Date is PHP date() for the handful of formats the bot writes into the DB.
func Date(format string, ts int64) string {
	t := time.Unix(ts, 0).In(Tehran)
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		switch format[i] {
		case 'Y':
			b.WriteString(strconv.Itoa(t.Year()))
		case 'm':
			b.WriteString(pad2(int(t.Month())))
		case 'd':
			b.WriteString(pad2(t.Day()))
		case 'H':
			b.WriteString(pad2(t.Hour()))
		case 'i':
			b.WriteString(pad2(t.Minute()))
		case 's':
			b.WriteString(pad2(t.Second()))
		case 'c':
			b.WriteString(t.Format("2006-01-02T15:04:05-07:00"))
		default:
			b.WriteByte(format[i])
		}
	}
	return b.String()
}

// DateNow is date($format).
func DateNow(format string) string { return Date(format, time.Now().Unix()) }

// Strtotime parses the date strings the bot and the panels produce
// ("2006/01/02 15:04:05", "2006-01-02 15:04:05", RFC3339 with or without
// zone/fraction). Returns ok=false where PHP's strtotime would return false.
func Strtotime(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	layouts := []string{
		"2006/01/02 15:04:05",
		"2006-01-02 15:04:05",
		"2006/1/2 15:04:05",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02",
		"2006/01/02",
	}
	for _, l := range layouts {
		var t time.Time
		var err error
		if strings.Contains(l, "Z07") {
			t, err = time.Parse(l, s)
		} else {
			t, err = time.ParseInLocation(l, s, Tehran)
		}
		if err == nil {
			return t.Unix(), true
		}
	}
	if IsNumeric(s) && strings.HasPrefix(s, "@") {
		return Intval(s[1:]), true
	}
	return 0, false
}

// PlusDaysUnix is strtotime("+N days") (also used for hours via PlusHoursUnix).
func PlusDaysUnix(n string) int64 {
	return time.Now().In(Tehran).AddDate(0, 0, int(Intval(n))).Unix()
}

// PlusHoursUnix is strtotime("+N hours").
func PlusHoursUnix(n string) int64 {
	return time.Now().Add(time.Duration(Intval(n)) * time.Hour).Unix()
}
