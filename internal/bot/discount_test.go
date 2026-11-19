package bot

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/panels"
	"github.com/Liwyd/mit-seller/internal/tg"
)

func TestDiscountedPriceRoundsUpToThousand(t *testing.T) {
	for _, tc := range []struct {
		full, pct, want float64
	}{
		{320676, 20, 257000}, // 256540.8 -> 257,000
		{256541, 0, 256541},  // no discount: the price is left alone
		{300000, 20, 240000}, // already a whole thousand
		{100000, 100, 0},     // free
		{100000, 150, 0},     // a legacy percent above 100 is capped
		{100000, -5, 100000}, // and a negative one ignored
		{1500, 10, 1500},     // 1350 rounds to 2000, but never above the price
		{999, 50, 999},       // same, for a price under 1,000
		{0, 20, 0},
		{50000, 20, 40000},
		{45000, 15, 39000}, // 38250 -> 39,000
		{70000, 30, 49000}, // 70000*0.3 is 20999.999…; must not become 50,000
		{30000, 10, 27000}, // 27000.000000000004 must stay 27,000
	} {
		if got := discountedPrice(tc.full, tc.pct); got != tc.want {
			t.Errorf("discountedPrice(%v, %v) = %v, want %v", tc.full, tc.pct, got, tc.want)
		}
	}
}

func TestLimitReachedKeepsLegacyCodesWorking(t *testing.T) {
	for _, tc := range []struct {
		limit, used string
		want        bool
	}{
		{"5", "4", false},
		{"5", "5", true},
		{"5", "6", true}, // went past the limit: still exhausted (was == only)
		{"1", "1", true},
		{"0", "0", true}, // a code saved half-way (limit 0) stays unusable
		{"۵", "5", true}, // Persian digits are a number
		{"", "0", false}, // live code with an empty limit: unlimited, as before
		{"", "12", false},
		{"نامحدود", "3", false},
	} {
		if got := limitReached(tc.limit, tc.used); got != tc.want {
			t.Errorf("limitReached(%q, %q) = %v, want %v", tc.limit, tc.used, got, tc.want)
		}
	}
}

func TestSellDiscountState(t *testing.T) {
	for in, want := range map[string]string{
		"dis_OFF20_40000": "OFF20",
		"dis_OFF20_":      "OFF20",
		"0":               "",
		"":                "",
		"dis":             "",
		"dis_OFF20":       "",
		"dis__40000":      "",
		"x_OFF20_40000":   "",
	} {
		if got := sellDiscountState(in); got != want {
			t.Errorf("sellDiscountState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormDigits(t *testing.T) {
	if got := normDigits(" ۲۰ "); got != "20" {
		t.Fatalf("got %q", got)
	}
	if got := normDigits("١٠٠"); got != "100" {
		t.Fatalf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// A database that answers by pattern and remembers every statement, so a test
// can prove what a handler did NOT write.

type fakeAnswer struct {
	re   *regexp.Regexp
	cols []string
	rows [][]driver.Value
	n    []int64 // rows affected by successive matching statements
}

type fakeSQL struct {
	mu      sync.Mutex
	answers []*fakeAnswer
	log     []string
}

type fakeStmt struct {
	q    string
	args []driver.Value
}

func (f *fakeSQL) query(cols []string, re string, rows ...[]driver.Value) {
	f.answers = append(f.answers, &fakeAnswer{re: regexp.MustCompile(re), cols: cols, rows: rows})
}

func (f *fakeSQL) exec(re string, n ...int64) {
	f.answers = append(f.answers, &fakeAnswer{re: regexp.MustCompile(re), n: n})
}

func (f *fakeSQL) find(q string) *fakeAnswer {
	for _, a := range f.answers {
		if a.re.MatchString(q) {
			return a
		}
	}
	return nil
}

func (f *fakeSQL) record(q string, args []driver.NamedValue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vals := make([]string, len(args))
	for i, a := range args {
		vals[i] = fmt.Sprint(a.Value)
	}
	f.log = append(f.log, q+" "+strings.Join(vals, "|"))
}

// ran returns the statements matching re.
func (f *fakeSQL) ran(re string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	r := regexp.MustCompile(re)
	for _, l := range f.log {
		if r.MatchString(l) {
			out = append(out, l)
		}
	}
	return out
}

func (f *fakeSQL) Connect(context.Context) (driver.Conn, error) { return &fakeConn{f}, nil }
func (f *fakeSQL) Driver() driver.Driver                        { return nil }

type fakeConn struct{ f *fakeSQL }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, fmt.Errorf("no prepare") }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return nil, fmt.Errorf("no tx") }

func (c *fakeConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.f.record(q, args)
	a := c.f.find(q)
	if a == nil || a.cols == nil {
		return &fakeRows{cols: []string{"x"}}, nil
	}
	return &fakeRows{cols: a.cols, rows: a.rows}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.f.record(q, args)
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	n := int64(1)
	if a := c.f.find(q); a != nil && len(a.n) > 0 {
		n = a.n[0]
		if len(a.n) > 1 {
			a.n = a.n[1:]
		}
	}
	return driver.RowsAffected(n), nil
}

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

// discountHarness is a customer at the pre-invoice of a 50,000 Toman product
// pressing the "pay with discount" button.
func discountHarness(t *testing.T, state, balance string) (*Ctx, *fakeSQL, *[]string) {
	t.Helper()
	f := &fakeSQL{}
	var mu sync.Mutex
	var said []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		said = append(said, p.Text)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	t.Cleanup(server.Close)
	sqldb := sql.OpenDB(f)
	t.Cleanup(func() { sqldb.Close() })
	d := &db.DB{DB: sqldb}
	f.query([]string{"code_product", "name_product", "price_product", "Volume_constraint", "Service_time"},
		`FROM product`, []driver.Value{"p1", "plan", "50000", "10", "30"})
	f.query([]string{"name_panel", "type", "linksubx"}, `FROM .?marzban_panel`, []driver.Value{"panel", "mit", ""})
	c := &Ctx{
		b:      &Bot{DB: d, PM: panels.New(d), TG: tg.New("test", server.URL, "")},
		fromID: "42", datain: "confirmandgetserviceDiscount", setting: db.Row{},
		user: db.Row{
			"Processing_value": {S: "panel"}, "Processing_value_one": {S: "p1"}, "Processing_value_tow": {S: "ali"},
			"Processing_value_four": {S: state}, "Balance": {S: balance},
		},
	}
	return c, f, &said
}

// An old "pay with discount" button pressed once the code was used (state
// reset to "0") must not sell the service for nothing.
func TestStaleDiscountButtonDoesNotSellForFree(t *testing.T) {
	for _, state := range []string{"0", "", "dis", "dis_OFF20"} {
		t.Run(state, func(t *testing.T) {
			c, f, said := discountHarness(t, state, "100000")
			c.payFromBalance()
			if got := f.ran(`(?i)INSERT|Balance|DiscountSell`); len(got) != 0 {
				t.Fatalf("stale button wrote: %v", got)
			}
			if !strings.Contains(strings.Join(*said, "\n"), T("users.Discount.notcode")) {
				t.Fatalf("customer was not told: %v", *said)
			}
		})
	}
}

// The price the state carries is never used: it is worked out again for the
// product being bought now (here 20% off 50,000 = 40,000), so a code applied
// to a cheap product cannot be carried to an expensive one.
func TestDiscountPriceIsRecomputedAtPayment(t *testing.T) {
	c, f, _ := discountHarness(t, "dis_OFF20_1", "10000")
	f.query([]string{"codeDiscount", "price", "limitDiscount", "usedDiscount", "usefirst"},
		`FROM .?DiscountSell`, []driver.Value{"OFF20", "20", "5", "0", "0"})
	c.payFromBalance()
	// not enough balance: the customer is asked for the rest of 40,000
	if got := f.ran(`Processing_value.? = .*30000\|42`); len(got) != 1 {
		t.Fatalf("expected a 30000 top-up request, log:\n%s", strings.Join(f.log, "\n"))
	}
	if got := f.ran(`INSERT IGNORE INTO invoice.*unpaid`); len(got) != 1 {
		t.Fatalf("expected one unpaid invoice, got %v", got)
	}
}

func TestExhaustedCodeIsRefusedAtPayment(t *testing.T) {
	for name, row := range map[string][]driver.Value{
		"limit used up": {"OFF20", "20", "3", "3", "0"},
		"over limit":    {"OFF20", "20", "3", "4", "0"},
	} {
		t.Run(name, func(t *testing.T) {
			c, f, said := discountHarness(t, "dis_OFF20_40000", "100000")
			f.query([]string{"codeDiscount", "price", "limitDiscount", "usedDiscount", "usefirst"}, `FROM .?DiscountSell`, row)
			c.payFromBalance()
			if got := f.ran(`(?i)INSERT|Balance =|SET .?usedDiscount`); len(got) != 0 {
				t.Fatalf("exhausted code still sold: %v", got)
			}
			if !strings.Contains(strings.Join(*said, "\n"), T("users.Discount.erorrlimit")) {
				t.Fatalf("customer was not told: %v", *said)
			}
			if got := f.ran(`Processing_value_four.? = .*0\|42`); len(got) != 1 {
				t.Fatalf("applied code not cleared: %v", f.log)
			}
		})
	}
}

func TestDeletedCodeIsRefusedAtPayment(t *testing.T) {
	c, f, said := discountHarness(t, "dis_GONE_40000", "100000")
	c.payFromBalance()
	if got := f.ran(`(?i)INSERT|Balance =`); len(got) != 0 {
		t.Fatalf("deleted code still sold: %v", got)
	}
	if !strings.Contains(strings.Join(*said, "\n"), T("users.Discount.notcode")) {
		t.Fatalf("customer was not told: %v", *said)
	}
}

func TestBumpDiscountUseRetriesInsteadOfLosingAUse(t *testing.T) {
	f := &fakeSQL{}
	sqldb := sql.OpenDB(f)
	defer sqldb.Close()
	f.query([]string{"usedDiscount"}, `SELECT .*DiscountSell`, []driver.Value{"3"})
	// the first swap loses to a purchase finishing at the same time
	f.exec(`UPDATE DiscountSell SET usedDiscount`, 0, 1)
	bumpDiscountUse(&db.DB{DB: sqldb}, "OFF20")
	got := f.ran(`UPDATE DiscountSell SET usedDiscount`)
	if len(got) != 2 {
		t.Fatalf("want 2 attempts, got %v", got)
	}
	for _, g := range got {
		if !strings.HasSuffix(g, "4|OFF20|3") {
			t.Fatalf("unexpected swap %q", g)
		}
	}
}
