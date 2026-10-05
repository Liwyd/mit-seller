package php

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"testing"
)

// These tests run the same inputs through the real PHP interpreter when it is
// available, so any drift from PHP's behaviour shows up as a failure.

func runPHP(t *testing.T, code string) string {
	t.Helper()
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not installed")
	}
	out, err := exec.Command("php", "-r", code).Output()
	if err != nil {
		t.Fatalf("php failed: %v", err)
	}
	return string(out)
}

var samples = []string{
	"", "0", "1", "-1", "12", "12abc", "abc", "1e3", "1.5", " 42", "42 ", "  -7.9e1x",
	"0123", ".5", "5.", "+3", "9999999999999999999999", "-9999999999999999999999",
	"1,000", "١٢", "0x1A", "12.000", "1e", "e5", "--1", " ", "\n5\n",
}

func TestNumbersMatchPHP(t *testing.T) {
	in, _ := json.Marshal(samples)
	out := runPHP(t, `$a=json_decode('`+string(in)+`',true);$r=[];foreach($a as $s){$r[]=[intval($s),is_numeric($s),ctype_digit($s),(string)floatval($s)];}echo json_encode($r);`)
	var want [][]any
	if err := json.Unmarshal([]byte(out), &want); err != nil {
		t.Fatal(err, out)
	}
	for i, s := range samples {
		if got := Intval(s); float64(got) != want[i][0].(float64) {
			t.Errorf("intval(%q) = %d, php %v", s, got, want[i][0])
		}
		if got := IsNumeric(s); got != want[i][1].(bool) {
			t.Errorf("is_numeric(%q) = %v, php %v", s, got, want[i][1])
		}
		if got := CtypeDigit(s); got != want[i][2].(bool) {
			t.Errorf("ctype_digit(%q) = %v, php %v", s, got, want[i][2])
		}
		if got := FloatToString(Floatval(s)); got != want[i][3].(string) {
			t.Errorf("floatval(%q) = %s, php %v", s, got, want[i][3])
		}
	}
}

func TestLooseEqMatchesPHP(t *testing.T) {
	pairs := [][2]string{{"1", "01"}, {"1", "1.0"}, {"abc", "ABC"}, {"", "0"}, {"10", "1e1"}, {"a", "a"}, {"1 ", "1"}, {"0", "0.0"}, {"", ""}}
	in, _ := json.Marshal(pairs)
	out := runPHP(t, `$a=json_decode('`+string(in)+`',true);$r=[];foreach($a as $p){$r[]=$p[0]==$p[1];}echo json_encode($r);`)
	var want []bool
	json.Unmarshal([]byte(out), &want)
	for i, p := range pairs {
		if got := LooseEq(p[0], p[1]); got != want[i] {
			t.Errorf("%q == %q: got %v php %v", p[0], p[1], got, want[i])
		}
	}
}

func TestNumberFormatAndFloats(t *testing.T) {
	vals := []float64{0, 1, -1, 1234, 1234567.5, 2.5, -2.5, 0.49, 999999.5, 1e15, 1.5e20, 0.1, 1.0 / 3, 123456789012, -0.4, 1.005, 12345.678}
	in, _ := json.Marshal(vals)
	out := runPHP(t, `$a=json_decode('`+string(in)+`',true);$r=[];foreach($a as $v){$v=(float)$v;$r[]=[number_format($v),number_format($v,2),(string)$v,(string)round($v,2)];}echo json_encode($r);`)
	var want [][]string
	if err := json.Unmarshal([]byte(out), &want); err != nil {
		t.Fatal(err, out)
	}
	for i, v := range vals {
		if got := NumberFormat(v, 0); got != want[i][0] {
			t.Errorf("number_format(%v) = %s, php %s", v, got, want[i][0])
		}
		if got := NumberFormat(v, 2); got != want[i][1] {
			t.Errorf("number_format(%v,2) = %s, php %s", v, got, want[i][1])
		}
		if got := FloatToString(v); got != want[i][2] {
			t.Errorf("(string)%v = %s, php %s", v, got, want[i][2])
		}
		if got := FloatToString(Round(v, 2)); got != want[i][3] {
			t.Errorf("round(%v,2) = %s, php %s", v, got, want[i][3])
		}
	}
}

func TestJdateMatchesPHP(t *testing.T) {
	stamps := []int64{0, 1700000000, 1727900000, 1742500000, 1758000000, 1774000000, 1790000000, 1606800000}
	in, _ := json.Marshal(stamps)
	out := runPHP(t, `date_default_timezone_set('Asia/Tehran');include '../../legacy-php/jdf.php';$a=json_decode('`+string(in)+`',true);$r=[];foreach($a as $s){$r[]=[jdate('Y/m/d',$s),jdate('H:i:s',$s),jdate('Y/m/d H:i:s',$s),jdate('Y/m/d h:i:s',$s),date('Y/m/d H:i:s',$s)];}echo json_encode($r);`)
	var want [][]string
	if err := json.Unmarshal([]byte(out), &want); err != nil {
		t.Fatal(err, out)
	}
	for i, s := range stamps {
		got := []string{Jdate("Y/m/d", s), Jdate("H:i:s", s), Jdate("Y/m/d H:i:s", s), Jdate("Y/m/d h:i:s", s), Date("Y/m/d H:i:s", s)}
		for k := range got {
			if got[k] != want[i][k] {
				t.Errorf("ts %d fmt %d: got %s php %s", s, k, got[k], want[i][k])
			}
		}
	}
}

func TestSprintf(t *testing.T) {
	if got := Sprintf("a %s b %s %%", "x", 12.5); got != "a x b 12.5 %" {
		t.Fatal(got)
	}
	if got := Sprintf("%s-%s", "only"); got != "only-" {
		t.Fatal(got)
	}
	if got := Sprintf("%s", int64(5)); got != "5" {
		t.Fatal(got)
	}
	_ = strconv.Itoa
}

func TestJSONEncode(t *testing.T) {
	o := DecodeObject(`{"userid":"7000000001","n":12,"name_panel":"آلمان/1 😀"}`)
	o = o.Set("username", "x").Set("n", "13")
	got := JSONEncode(o)
	// PHP: {"userid":"7000000001","n":"13","name_panel":"<u0622 u0644 u0645 u0627 u0646>\/1 <ud83d ude00>","username":"x"}
	u := func(h string) string { return string(rune(0x5c)) + "u" + h }
	want := `{"userid":"7000000001","n":"13","name_panel":"` + u("0622") + u("0644") + u("0645") + u("0627") + u("0646") + `\/1 ` + u("d83d") + u("de00") + `","username":"x"}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if DecodeObject("0") != nil || DecodeObject("") != nil {
		t.Fatal("non-object should decode to nil")
	}
}
