package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Liwyd/mit-seller/internal/bot"
	"github.com/Liwyd/mit-seller/internal/db"
)

func catalogueAPI(t *testing.T) (*API, sqlmock.Sqlmock) {
	t.Helper()
	sqldb, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	return &API{B: &bot.Bot{DB: &db.DB{DB: sqldb}}}, mock
}

func TestGiftCodeAcceptsLettersAndDigits(t *testing.T) {
	a, mock := catalogueAPI(t)
	mock.ExpectQuery("SELECT 1 FROM .?Discount").WithArgs("OFF20").WillReturnRows(sqlmock.NewRows([]string{"1"}))
	mock.ExpectExec("INSERT INTO Discount").WithArgs("OFF20", "50000").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT id, code, price FROM Discount").WillReturnRows(sqlmock.NewRows([]string{"id", "code", "price"}))
	w := httptest.NewRecorder()
	a.createGiftCode(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"code":"OFF20","price":"50000"}`)))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCodesRejectAnythingButEnglishLettersAndDigits(t *testing.T) {
	for _, code := range []string{"off-20", "OFF 20", "تخفیف", "OFF۲۰", "", "OFF_20"} {
		a, mock := catalogueAPI(t)
		w := httptest.NewRecorder()
		a.createGiftCode(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"code":"`+code+`","price":"1000"}`)))
		if w.Code != 400 {
			t.Errorf("gift code %q: status %d", code, w.Code)
		}
		w = httptest.NewRecorder()
		a.createDiscount(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"code":"`+code+`","percent":"20","limit":"5"}`)))
		if w.Code != 400 {
			t.Errorf("discount code %q: status %d", code, w.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscountPercentAndLimitBounds(t *testing.T) {
	for _, tc := range []struct{ pct, limit string }{
		{"0", "5"}, {"101", "5"}, {"abc", "5"}, {"20", "0"}, {"20", ""},
	} {
		a, _ := catalogueAPI(t)
		w := httptest.NewRecorder()
		a.createDiscount(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"code":"OFF20","percent":"`+tc.pct+`","limit":"`+tc.limit+`"}`)))
		if w.Code != 400 {
			t.Errorf("percent %q limit %q: status %d", tc.pct, tc.limit, w.Code)
		}
	}
	a, mock := catalogueAPI(t)
	mock.ExpectQuery("SELECT 1 FROM .?DiscountSell").WithArgs("OFF20").WillReturnRows(sqlmock.NewRows([]string{"1"}))
	mock.ExpectExec("INSERT INTO DiscountSell").WithArgs("OFF20", "100", "1", "0").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT id, codeDiscount").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	w := httptest.NewRecorder()
	a.createDiscount(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"code":"OFF20","percent":"100","limit":"1"}`)))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
