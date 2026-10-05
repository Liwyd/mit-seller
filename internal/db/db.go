// Package db talks to the bot's MySQL database. The schema is exactly the one
// the PHP bot created, so an existing database is used as-is.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/Liwyd/mit-seller/internal/php"
)

// Val is one column value as PHP would have received it.
type Val struct {
	S    string
	Null bool
}

// Row is an associative row ($row['col']). A nil Row is PHP's `false`
// (no such row).
type Row map[string]Val

// S returns the column as a string ("" for NULL or a missing row/column).
func (r Row) S(col string) string {
	if r == nil {
		return ""
	}
	return r[col].S
}

// I returns intval($row[col]).
func (r Row) I(col string) int64 { return php.Intval(r.S(col)) }

// F returns the column as a PHP number.
func (r Row) F(col string) float64 { return php.Floatval(r.S(col)) }

// IsNull reports PHP's `$row[col] === null` (also true for a missing row).
func (r Row) IsNull(col string) bool {
	if r == nil {
		return true
	}
	v, ok := r[col]
	return !ok || v.Null
}

// Has reports whether the row exists (PHP: $row !== false).
func (r Row) Has() bool { return r != nil }

// Clone copies the row so callers can modify it like a PHP array copy.
func (r Row) Clone() Row {
	if r == nil {
		return nil
	}
	c := make(Row, len(r))
	for k, v := range r {
		c[k] = v
	}
	return c
}

// Set overwrites a column in this in-memory copy.
func (r Row) Set(col, v string) {
	if r != nil {
		r[col] = Val{S: v}
	}
}

type DB struct {
	*sql.DB
}

func Open(dsn string) (*DB, error) {
	d, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(20)
	d.SetMaxIdleConns(5)
	d.SetConnMaxLifetime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.PingContext(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return &DB{d}, nil
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ident guards the table/column names that the PHP helpers interpolated into
// SQL. Every caller passes constants, this is defence in depth.
func ident(s string) string {
	if !identRe.MatchString(s) {
		panic("db: bad identifier " + s)
	}
	return "`" + s + "`"
}

func fieldList(field string) string {
	if field == "*" {
		return "*"
	}
	parts := strings.Split(field, ",")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "SUM(") || strings.HasPrefix(p, "COUNT(") {
			parts[i] = p
			continue
		}
		parts[i] = ident(p)
	}
	return strings.Join(parts, ",")
}

// Arg converts a Go value into what PDO would bind for it.
func Arg(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return x
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		return php.FloatToString(x)
	case bool:
		if x {
			return "1"
		}
		return ""
	case Val:
		if x.Null {
			return nil
		}
		return x.S
	}
	return fmt.Sprint(v)
}

func args(vs []any) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = Arg(v)
	}
	return out
}

func scanRows(rows *sql.Rows) ([]Row, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := make(Row, len(cols))
		for i, c := range cols {
			r[c] = Val{S: vals[i].String, Null: !vals[i].Valid}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Query runs a statement and returns all rows.
func (d *DB) Query(q string, a ...any) ([]Row, error) {
	rows, err := d.DB.Query(q, args(a)...)
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}

// MustQuery is Query that logs failures and returns no rows, matching how the
// PHP bot carried on after a failed statement.
func (d *DB) MustQuery(q string, a ...any) []Row {
	rs, err := d.Query(q, a...)
	if err != nil {
		logErr(q, err)
	}
	return rs
}

// One returns the first row or nil.
func (d *DB) One(q string, a ...any) Row {
	rs := d.MustQuery(q, a...)
	if len(rs) == 0 {
		return nil
	}
	return rs[0]
}

// Exec runs a statement, logging failures.
func (d *DB) Exec(q string, a ...any) (sql.Result, error) {
	res, err := d.DB.Exec(q, args(a)...)
	if err != nil {
		logErr(q, err)
	}
	return res, err
}

// Scalar returns the first column of the first row as a string.
func (d *DB) Scalar(q string, a ...any) string {
	r := d.One(q, a...)
	for _, v := range r {
		return v.S
	}
	return ""
}

// ScalarRow returns the first column of the first row preserving column order.
func (d *DB) Count(q string, a ...any) int64 {
	var n int64
	if err := d.DB.QueryRow(q, args(a)...).Scan(&n); err != nil {
		logErr(q, err)
		return 0
	}
	return n
}

// Select is the PHP select($table,$field,$whereField,$whereValue,"select").
func (d *DB) Select(table, field, whereField string, whereValue any) Row {
	q := "SELECT " + fieldList(field) + " FROM " + ident(table)
	var a []any
	if whereField != "" {
		q += " WHERE " + ident(whereField) + " = ?"
		a = append(a, whereValue)
	}
	return d.One(q+" LIMIT 1", a...)
}

// SelectAll is select(...,"fetchAll").
func (d *DB) SelectAll(table, field, whereField string, whereValue any) []Row {
	q := "SELECT " + fieldList(field) + " FROM " + ident(table)
	var a []any
	if whereField != "" {
		q += " WHERE " + ident(whereField) + " = ?"
		a = append(a, whereValue)
	}
	return d.MustQuery(q, a...)
}

// SelectCount is select(...,"count").
func (d *DB) SelectCount(table, whereField string, whereValue any) int64 {
	q := "SELECT COUNT(*) FROM " + ident(table)
	var a []any
	if whereField != "" {
		q += " WHERE " + ident(whereField) + " = ?"
		a = append(a, whereValue)
	}
	return d.Count(q, a...)
}

// Column is select(...,"FETCH_COLUMN").
func (d *DB) Column(table, field string) []string {
	rs := d.MustQuery("SELECT " + ident(field) + " FROM " + ident(table))
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.S(field))
	}
	return out
}

// Exists reports whether table has a row with col = v.
func (d *DB) Exists(table, col string, v any) bool {
	return d.One("SELECT 1 FROM "+ident(table)+" WHERE "+ident(col)+" = ? LIMIT 1", v) != nil
}

// Update is the PHP update($table,$field,$newValue,$whereField,$whereValue).
func (d *DB) Update(table, field string, value any, whereField string, whereValue any) {
	if whereField != "" {
		d.Exec("UPDATE "+ident(table)+" SET "+ident(field)+" = ? WHERE "+ident(whereField)+" = ?", value, whereValue)
		return
	}
	d.Exec("UPDATE "+ident(table)+" SET "+ident(field)+" = ?", value)
}

// Step is step($step,$from_id).
func (d *DB) Step(step string, id any) {
	d.Exec("UPDATE user SET step = ? WHERE id = ?", step, id)
}

// Setting returns the single setting row.
func (d *DB) Setting() Row { return d.Select("setting", "*", "", nil) }

// PaySetting returns PaySetting.ValuePay for name.
func (d *DB) PaySetting(name string) string {
	return d.Select("PaySetting", "ValuePay", "NamePay", name).S("ValuePay")
}

// AdminIDs is select("admin","id_admin",...,"FETCH_COLUMN").
func (d *DB) AdminIDs() []string { return d.Column("admin", "id_admin") }

// IsAdmin is in_array($id, $admin_ids).
func (d *DB) IsAdmin(id string) bool {
	for _, a := range d.AdminIDs() {
		if php.LooseEq(a, id) {
			return true
		}
	}
	return false
}

// KV is the bot's own settings table (not used by the PHP bot).
func (d *DB) KV(key string) string {
	return d.Scalar("SELECT v FROM mit_kv WHERE k = ?", key)
}

func (d *DB) KVOk(key string) (string, bool) {
	r := d.One("SELECT v FROM mit_kv WHERE k = ?", key)
	if r == nil {
		return "", false
	}
	return r.S("v"), true
}

func (d *DB) SetKV(key, value string) {
	d.Exec("INSERT INTO mit_kv (k, v) VALUES (?, ?) ON DUPLICATE KEY UPDATE v = VALUES(v)", key, value)
}

// Logger is replaced by main to route DB errors into the bot log.
var Logger = func(format string, a ...any) { fmt.Printf(format+"\n", a...) }

func logErr(q string, err error) {
	if len(q) > 300 {
		q = q[:300] + "..."
	}
	Logger("db error: %v | %s", err, q)
}
