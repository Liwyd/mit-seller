package bot

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Liwyd/mit-seller/internal/db"
	"github.com/Liwyd/mit-seller/internal/panels"
	"github.com/Liwyd/mit-seller/internal/tg"
)

// paymentHarness isolates the database and captures everything the bot says
// on Telegram while a payment is being processed.
func paymentHarness(t *testing.T) (*Bot, sqlmock.Sqlmock, func(), *[]string) {
	t.Helper()
	sqldb, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	d := &db.DB{DB: sqldb}
	var messages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&p)
		messages = append(messages, p.Text)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	b := &Bot{
		DB:  d,
		PM:  panels.New(d),
		TG:  tg.New("test", server.URL, ""),
		Log: log.New(io.Discard, "", 0),
	}
	return b, mock, func() {
		server.Close()
		sqldb.Close()
	}, &messages
}

// A receipt may only be tied to a purchase when the name it carries really is
// one of the buyer's unpaid invoices. Every other value of Processing_value_one
// (a product code, a service that already exists, nothing at all) has to become
// a plain top-up, because DirectPayment would otherwise look for an invoice
// that is not there and return without moving the customer's money.
func TestPurchaseTagOnlyNamesAnUnpaidInvoice(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tow   string
		one   string
		found bool
		want  string
	}{
		{"purchase waiting for payment", "getconfigafterpay", "hasoon", true, "getconfigafterpay|hasoon"},
		{"stale state naming a product code", "getconfigafterpay", "c3c4", false, "0|0"},
		{"stale state naming a service already active", "getconfigafterpay", "7017242mary", false, "0|0"},
		{"plain top up", "0", "0", false, "0|0"},
		{"purchase state carrying no name", "getconfigafterpay", "", false, "0|0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqldb, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqldb.Close()
			d := &db.DB{DB: sqldb}
			if tc.tow == "getconfigafterpay" && tc.one != "" {
				rows := sqlmock.NewRows([]string{"id_invoice"})
				if tc.found {
					rows.AddRow("8bad3b94")
				}
				mock.ExpectQuery("SELECT .* FROM invoice WHERE username").WithArgs(tc.one, "42").WillReturnRows(rows)
			}
			user := db.Row{"Processing_value_tow": {S: tc.tow}, "Processing_value_one": {S: tc.one}}
			if got := purchaseTag(d, user, "42", tc.one); got != tc.want {
				t.Errorf("purchaseTag = %q, want %q", got, tc.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// An invoice bound to a panel row that no longer exists must not be handed to
// the panel layer: the empty panel name came back as a bare "Panel Not Found",
// the customer was charged an error for it, and nothing was recorded about why
// the receipt could not be delivered. The receipt now stays waiting with the
// reason written on it, and nothing is sent or created.
func TestDirectPaymentMissingPanelStaysWaiting(t *testing.T) {
	b, mock, done, messages := paymentHarness(t)
	defer done()
	const (
		order   = "e93effdcd8"
		buyer   = "8089425940"
		unpaid  = "8089425940_1b06"
		missing = "💸 اقتصادی نامحدود | تک کاربر 💸"
	)

	mock.ExpectQuery(`SELECT \* FROM ` + "`setting`" + ` LIMIT 1`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("1"))
	mock.ExpectQuery(`SELECT \* FROM ` + "`Payment_report`" + ` WHERE ` + "`id_order`").
		WithArgs(order).
		WillReturnRows(sqlmock.NewRows([]string{"id_order", "id_user", "price", "payment_Status", "Payment_Method", "invoice"}).
			AddRow(order, buyer, "385000", "waiting", "cart to cart", "getconfigafterpay|"+unpaid))
	mock.ExpectQuery(`SELECT \* FROM ` + "`user`" + ` WHERE ` + "`id`").
		WithArgs(buyer).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username"}).AddRow(buyer, "Someone"))
	mock.ExpectQuery("SELECT .* FROM invoice WHERE username").
		WithArgs(unpaid, buyer).
		WillReturnRows(sqlmock.NewRows([]string{"id_invoice", "username", "Service_location"}).
			AddRow("d42925a1", unpaid, missing))
	mock.ExpectQuery(`SELECT \* FROM ` + "`marzban_panel`" + ` WHERE ` + "`name_panel`").
		WithArgs(missing).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name_panel"}))
	mock.ExpectExec(`UPDATE `+"`Payment_report`"+` SET `+"`dec_not_confirmed`").
		WithArgs("پنل سرویس پیدا نشد: "+missing, order).
		WillReturnResult(sqlmock.NewResult(0, 1))

	c := &Ctx{b: b, fromID: "0", user: db.Row{}}
	if applied := b.DirectPayment(order, c); applied {
		t.Fatal("DirectPayment reported success for an invoice bound to a missing panel")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if len(*messages) != 0 {
		t.Fatalf("nothing may be sent or created for a missing panel, got %v", *messages)
	}
}

// The other half of the same defect: a receipt that cannot be tied to an
// unpaid invoice has to be credited as the top-up it now is, instead of
// leaving the money undelivered while the payment is marked paid.
func TestReceiptWithNoPendingInvoiceIsCreditedAsTopUp(t *testing.T) {
	b, mock, done, messages := paymentHarness(t)
	defer done()
	const (
		order = "d18eceee10"
		buyer = "415378105"
	)

	// the buyer's state still carries the product code of an earlier click,
	// and no invoice of theirs is waiting to be created
	mock.ExpectQuery("SELECT .* FROM invoice WHERE username").WithArgs("c3c4", buyer).
		WillReturnRows(sqlmock.NewRows([]string{"id_invoice"}))
	c := &Ctx{b: b, fromID: buyer, user: db.Row{
		"Processing_value_tow": {S: "getconfigafterpay"},
		"Processing_value_one": {S: "c3c4"},
	}}
	tag := c.invoiceTag()
	if tag != "0|0" {
		t.Fatalf("invoiceTag = %q, want 0|0", tag)
	}

	mock.ExpectQuery(`SELECT \* FROM ` + "`setting`" + ` LIMIT 1`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("1"))
	mock.ExpectQuery(`SELECT \* FROM ` + "`Payment_report`" + ` WHERE ` + "`id_order`").
		WithArgs(order).
		WillReturnRows(sqlmock.NewRows([]string{"id_order", "id_user", "price", "payment_Status", "Payment_Method", "invoice"}).
			AddRow(order, buyer, "180000", "waiting", "cart to cart", tag))
	mock.ExpectQuery(`SELECT \* FROM ` + "`user`" + ` WHERE ` + "`id`").
		WithArgs(buyer).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "Balance"}).AddRow(buyer, "Mmdhz", "0"))
	mock.ExpectExec(`UPDATE user SET Balance = Balance \+ \? WHERE id = \?`).
		WithArgs(int64(180000), buyer).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE `+"`Payment_report`"+` SET `+"`payment_Status`").
		WithArgs("paid", order).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if applied := b.DirectPayment(order, c); !applied {
		t.Fatal("DirectPayment did not credit a receipt that is a plain top-up")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if len(*messages) != 1 {
		t.Fatalf("the customer should be told the balance was charged, got %v", *messages)
	}
	if !strings.Contains((*messages)[0], order) {
		t.Fatalf("confirmation %q does not name order %s", (*messages)[0], order)
	}
}
