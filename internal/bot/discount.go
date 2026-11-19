package bot

import (
	"math"
	"strconv"
	"strings"

	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/php"
)

// Percentage discount codes (DiscountSell): the rules every place that prices
// or counts a discounted purchase shares, so the invoice the customer is shown,
// the amount taken from their balance and the commission paid on it agree.

// digitFolder turns Persian and Arabic-Indic digits into ASCII ones.
var digitFolder = strings.NewReplacer(
	"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9",
	"٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9",
)

// normDigits is what an admin typed, with Persian digits made ASCII and the
// surrounding spaces gone, so "۲۰ " is read as "20".
func normDigits(s string) string { return strings.TrimSpace(digitFolder.Replace(s)) }

// plainInt reads a whole non-negative number written with ASCII digits only.
func plainInt(s string) (int64, bool) {
	if !php.CtypeDigit(s) || len(s) > 15 {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// discountRoundTo is the step a discounted price is rounded up to (Toman).
const discountRoundTo = 1000

// discountedPrice is full minus pct percent, rounded up to the next 1,000
// Toman, but never above the undiscounted price and never below zero.
func discountedPrice(full, pct float64) float64 {
	if full <= 0 {
		return 0
	}
	if pct < 0 || math.IsNaN(pct) {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	// whole Toman first, so float noise like 257000.0000001 cannot push the
	// price up a whole step
	d := math.Round(full - pct/100*full)
	d = math.Ceil(d/discountRoundTo) * discountRoundTo
	if d > full {
		d = full
	}
	if d < 0 {
		d = 0
	}
	return d
}

// limitReached reports whether a code has been used up. A limit written as a
// plain number is a ceiling (used >= limit). Anything else — codes saved
// before the limit was validated, such as an empty one — keeps the old
// comparison, under which such a code never ran out.
func limitReached(limit, used string) bool {
	if l, ok := plainInt(normDigits(limit)); ok {
		u, _ := plainInt(normDigits(used))
		return u >= l
	}
	return php.LooseEq(limit, used)
}

// sellDiscountState is the Processing_value_four a customer carries once a
// code was accepted for their invoice: "dis_<code>_<price>". It returns the
// code, or "" when the state holds no applied code.
func sellDiscountState(v string) string {
	parts := strings.Split(v, "_")
	if len(parts) < 3 || parts[0] != "dis" || parts[1] == "" {
		return ""
	}
	return parts[1]
}

// checkSellDiscount validates code for this customer buying product p, right
// now, and returns the price they pay with it. errKey is the text to show
// when the code cannot be used.
func (c *Ctx) checkSellDiscount(code string, p db.Row) (price float64, errKey string) {
	d := c.db()
	if code == "" || p == nil {
		return 0, "users.Discount.notcode"
	}
	sd := d.Select("DiscountSell", "*", "codeDiscount", code)
	if sd == nil {
		return 0, "users.Discount.notcode"
	}
	if limitReached(sd.S("limitDiscount"), sd.S("usedDiscount")) {
		return 0, "users.Discount.erorrlimit"
	}
	if php.LooseEq(sd.S("usefirst"), "1") && d.SelectCount("invoice", "id_user", c.fromID) != 0 {
		return 0, "users.Discount.firstdiscount"
	}
	return discountedPrice(p.F("price_product"), sd.F("price")), ""
}

// bumpDiscountUse counts one more use of a code. It compares and swaps the
// stored count instead of reading and writing it back, so two purchases
// finishing together cannot lose a use, and it does no SQL arithmetic on the
// varchar column (the server runs in strict mode).
func bumpDiscountUse(d *db.DB, code string) {
	for i := 0; i < 5; i++ {
		sd := d.Select("DiscountSell", "usedDiscount", "codeDiscount", code)
		if sd == nil {
			return
		}
		old := sd.S("usedDiscount")
		res, err := d.Exec("UPDATE DiscountSell SET usedDiscount = ? WHERE codeDiscount = ? AND usedDiscount = ?",
			strconv.FormatInt(php.Intval(normDigits(old))+1, 10), code, old)
		if err != nil {
			return
		}
		if n, e := res.RowsAffected(); e == nil && n > 0 {
			return
		}
	}
}
