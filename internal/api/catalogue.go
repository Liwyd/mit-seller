package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"

	"github.com/MHBehzadian/nexra-mirzabot/internal/php"
)

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const productCols = "id, code_product, name_product, price_product, Volume_constraint, Location, Service_time, Category"

func (a *API) listProducts(w http.ResponseWriter, r *http.Request) {
	ok(w, rowsMap(a.B.DB.MustQuery("SELECT "+productCols+" FROM product ORDER BY id")))
}

func (a *API) validLocation(loc string) bool {
	return loc == "/all" || a.B.DB.Exists("marzban_panel", "name_panel", loc)
}

func (a *API) createProduct(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	name, loc := f.str("name"), f.str("location")
	vol, days, price, cat := f.str("volume"), f.str("days"), f.str("price"), f.str("category_id")
	switch {
	case name == "":
		fail(w, 400, "name is required")
		return
	case !a.validLocation(loc):
		fail(w, 400, "location must be a panel name or /all")
		return
	case !isDigits(vol) || php.Intval(vol) == 0:
		fail(w, 400, "volume must be a positive number of GB (unlimited is not supported)")
		return
	case !isDigits(days) || php.Intval(days) == 0:
		fail(w, 400, "days must be a positive number (unlimited is not supported)")
		return
	case !isDigits(price) || php.Intval(price) == 0:
		fail(w, 400, "price must be a positive number")
		return
	}
	if cat != "" && !a.B.DB.Exists("category", "id", cat) {
		fail(w, 400, "unknown category")
		return
	}
	d := a.B.DB
	code := randHex(2)
	for d.Exists("product", "code_product", code) {
		code = randHex(3)
	}
	var catArg any
	if cat != "" {
		catArg = cat
	}
	d.Exec("INSERT INTO product (code_product, name_product, price_product, Volume_constraint, Location, Service_time, Category) VALUES (?, ?, ?, ?, ?, ?, ?)",
		code, name, price, vol, loc, days, catArg)
	ok(w, rowMap(d.One("SELECT "+productCols+" FROM product WHERE code_product = ?", code)))
}

func (a *API) updateProduct(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	p := d.One("SELECT * FROM product WHERE id = ?", r.PathValue("id"))
	if p == nil {
		fail(w, 404, "product not found")
		return
	}
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	id := p.S("id")
	oldName, loc := p.S("name_product"), p.S("Location")
	// The Telegram admin menu also rewrote matching invoices; keep doing
	// that so renewals and reports see the new values.
	if f.has("price") {
		v := f.str("price")
		if !isDigits(v) || php.Intval(v) == 0 {
			fail(w, 400, "price must be a positive number")
			return
		}
		d.Exec("UPDATE product SET price_product = ? WHERE id = ?", v, id)
		d.Exec("UPDATE invoice SET price_product = ? WHERE name_product = ? AND Service_location = ?", v, oldName, loc)
	}
	if f.has("volume") {
		v := f.str("volume")
		if !isDigits(v) || php.Intval(v) == 0 {
			fail(w, 400, "volume must be a positive number of GB")
			return
		}
		d.Exec("UPDATE product SET Volume_constraint = ? WHERE id = ?", v, id)
		d.Exec("UPDATE invoice SET Volume = ? WHERE name_product = ? AND Service_location = ?", v, oldName, loc)
	}
	if f.has("days") {
		v := f.str("days")
		if !isDigits(v) || php.Intval(v) == 0 {
			fail(w, 400, "days must be a positive number")
			return
		}
		d.Exec("UPDATE product SET Service_time = ? WHERE id = ?", v, id)
		d.Exec("UPDATE invoice SET Service_time = ? WHERE name_product = ? AND Service_location = ?", v, oldName, loc)
	}
	if f.has("category_id") {
		v := f.str("category_id")
		if v != "" && !d.Exists("category", "id", v) {
			fail(w, 400, "unknown category")
			return
		}
		var arg any
		if v != "" {
			arg = v
		}
		d.Exec("UPDATE product SET Category = ? WHERE id = ?", arg, id)
	}
	if f.has("location") {
		v := f.str("location")
		if !a.validLocation(v) {
			fail(w, 400, "location must be a panel name or /all")
			return
		}
		d.Exec("UPDATE product SET Location = ? WHERE id = ?", v, id)
	}
	if f.has("name") {
		v := f.str("name")
		if v == "" {
			fail(w, 400, "name cannot be empty")
			return
		}
		d.Exec("UPDATE product SET name_product = ? WHERE id = ?", v, id)
		d.Exec("UPDATE invoice SET name_product = ? WHERE name_product = ? AND Service_location = ?", v, oldName, loc)
	}
	ok(w, rowMap(d.One("SELECT "+productCols+" FROM product WHERE id = ?", id)))
}

func (a *API) deleteProduct(w http.ResponseWriter, r *http.Request) {
	res, _ := a.B.DB.Exec("DELETE FROM product WHERE id = ?", r.PathValue("id"))
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "product not found")
		return
	}
	ok(w, nil)
}

func (a *API) listCategories(w http.ResponseWriter, r *http.Request) {
	ok(w, rowsMap(a.B.DB.MustQuery("SELECT id, remark FROM category ORDER BY id")))
}

func (a *API) createCategory(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	name := f.str("name")
	if name == "" {
		fail(w, 400, "name is required")
		return
	}
	res, _ := a.B.DB.Exec("INSERT INTO category (remark) VALUES (?)", name)
	id, _ := res.LastInsertId()
	ok(w, map[string]any{"id": id, "remark": name})
}

func (a *API) updateCategory(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	name := f.str("name")
	if name == "" {
		fail(w, 400, "name is required")
		return
	}
	a.B.DB.Exec("UPDATE category SET remark = ? WHERE id = ?", name, r.PathValue("id"))
	ok(w, nil)
}

func (a *API) deleteCategory(w http.ResponseWriter, r *http.Request) {
	a.B.DB.Exec("DELETE FROM category WHERE id = ?", r.PathValue("id"))
	ok(w, nil)
}

// gift codes (Discount): one-time balance top-ups
var letters = regexp.MustCompile(`^[A-Za-z]+$`)
var alnum = regexp.MustCompile(`^[A-Za-z\d]+$`)

func (a *API) listGiftCodes(w http.ResponseWriter, r *http.Request) {
	d := a.B.DB
	rows := d.MustQuery("SELECT id, code, price FROM Discount ORDER BY id")
	out := rowsMap(rows)
	for _, m := range out {
		m["used"] = d.Count("SELECT COUNT(*) FROM Giftcodeconsumed WHERE code = ?", m["code"])
	}
	ok(w, out)
}

func (a *API) createGiftCode(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	code, price := f.str("code"), f.str("price")
	if !letters.MatchString(code) {
		fail(w, 400, "code must be English letters only")
		return
	}
	if !isDigits(price) {
		fail(w, 400, "price must be a number")
		return
	}
	if a.B.DB.Exists("Discount", "code", code) {
		fail(w, 409, "this code already exists")
		return
	}
	a.B.DB.Exec("INSERT INTO Discount (code, price) VALUES (?, ?)", code, price)
	a.listGiftCodes(w, r)
}

func (a *API) deleteGiftCode(w http.ResponseWriter, r *http.Request) {
	a.B.DB.Exec("DELETE FROM Discount WHERE id = ?", r.PathValue("id"))
	ok(w, nil)
}

// sell discounts (DiscountSell): percentage off a purchase
func (a *API) listDiscounts(w http.ResponseWriter, r *http.Request) {
	ok(w, rowsMap(a.B.DB.MustQuery("SELECT id, codeDiscount, price, limitDiscount, usedDiscount, usefirst FROM DiscountSell ORDER BY id")))
}

func (a *API) createDiscount(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	code, pct, limit := f.str("code"), f.str("percent"), f.str("limit")
	if !alnum.MatchString(code) {
		fail(w, 400, "code must be English letters and digits")
		return
	}
	if !isDigits(pct) || php.Intval(pct) > 100 {
		fail(w, 400, "percent must be 0-100")
		return
	}
	if !isDigits(limit) {
		fail(w, 400, "limit must be a number")
		return
	}
	first := "0"
	if f.boolean("first_purchase_only") {
		first = "1"
	}
	if a.B.DB.Exists("DiscountSell", "codeDiscount", code) {
		fail(w, 409, "this code already exists")
		return
	}
	a.B.DB.Exec("INSERT INTO DiscountSell (codeDiscount, usedDiscount, price, limitDiscount, usefirst) VALUES (?, '0', ?, ?, ?)", code, pct, limit, first)
	a.listDiscounts(w, r)
}

func (a *API) deleteDiscount(w http.ResponseWriter, r *http.Request) {
	a.B.DB.Exec("DELETE FROM DiscountSell WHERE id = ?", r.PathValue("id"))
	ok(w, nil)
}

// tutorials
func (a *API) listHelp(w http.ResponseWriter, r *http.Request) {
	ok(w, rowsMap(a.B.DB.MustQuery("SELECT id, name_os, Description_os, type_Media_os, Media_os FROM help ORDER BY id")))
}

func (a *API) createHelp(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	name, desc := f.str("name"), f.str("description")
	if name == "" || desc == "" {
		fail(w, 400, "name and description are required")
		return
	}
	a.B.DB.Exec("INSERT INTO help (name_os, Media_os, type_Media_os, Description_os) VALUES (?, '', '', ?)", name, desc)
	a.listHelp(w, r)
}

func (a *API) updateHelp(w http.ResponseWriter, r *http.Request) {
	var f fields
	if err := decode(r, &f); err != nil {
		fail(w, 400, err.Error())
		return
	}
	id := r.PathValue("id")
	if f.has("name") && f.str("name") != "" {
		a.B.DB.Exec("UPDATE help SET name_os = ? WHERE id = ?", f.str("name"), id)
	}
	if f.has("description") {
		a.B.DB.Exec("UPDATE help SET Description_os = ? WHERE id = ?", f.str("description"), id)
	}
	if f.boolean("remove_media") {
		a.B.DB.Exec("UPDATE help SET Media_os = '', type_Media_os = '' WHERE id = ?", id)
	}
	a.listHelp(w, r)
}

func (a *API) deleteHelp(w http.ResponseWriter, r *http.Request) {
	a.B.DB.Exec("DELETE FROM help WHERE id = ?", r.PathValue("id"))
	ok(w, nil)
}
