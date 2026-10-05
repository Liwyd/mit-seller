package php

import (
	"bytes"
	"encoding/json"
	"strconv"
	"unicode/utf16"
)

// KV is one member of a JSON object whose key order matters (PHP arrays keep
// insertion order; Go maps do not).
type KV struct {
	K string
	V any
}

// Object is an ordered JSON object.
type Object []KV

// Set replaces the value of k, or appends it like $a[$k] = $v.
func (o Object) Set(k string, v any) Object {
	for i := range o {
		if o[i].K == k {
			o[i].V = v
			return o
		}
	}
	return append(o, KV{k, v})
}

// DecodeObject is json_decode($s, true) for a top-level object, keeping the
// key order. Anything that is not an object gives nil.
func DecodeObject(s string) Object {
	d := json.NewDecoder(bytes.NewReader([]byte(s)))
	d.UseNumber()
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil
	}
	o := Object{}
	for d.More() {
		kt, err := d.Token()
		if err != nil {
			return nil
		}
		k, _ := kt.(string)
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return nil
		}
		var v any
		vd := json.NewDecoder(bytes.NewReader(raw))
		vd.UseNumber()
		_ = vd.Decode(&v)
		o = o.Set(k, v)
	}
	return o
}

// JSONEncode is json_encode() with PHP's default flags: non-ASCII as \uXXXX
// and "/" as "\/". Object keeps its order; Go maps come out key-sorted.
func JSONEncode(v any) string {
	var b bytes.Buffer
	encodeJSON(&b, v)
	return b.String()
}

func encodeJSON(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case Object:
		b.WriteByte('{')
		for i, kv := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeString(b, kv.K)
			b.WriteByte(':')
			encodeJSON(b, kv.V)
		}
		b.WriteByte('}')
	case string:
		encodeString(b, x)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeJSON(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		raw, _ := json.Marshal(x)
		var o Object
		o = DecodeObject(string(raw))
		encodeJSON(b, o)
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			b.WriteString("null")
			return
		}
		b.Write(raw)
	}
}

func encodeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '/':
			b.WriteString(`\/`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20 || (r >= 0x7f && r < 0x10000):
			if r == 0x7f {
				b.WriteRune(r)
				continue
			}
			b.WriteString(`\u`)
			h := strconv.FormatInt(int64(r), 16)
			for i := len(h); i < 4; i++ {
				b.WriteByte('0')
			}
			b.WriteString(h)
		case r >= 0x10000:
			r1, r2 := utf16.EncodeRune(r)
			for _, u := range []rune{r1, r2} {
				b.WriteString(`\u`)
				b.WriteString(strconv.FormatInt(int64(u), 16))
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}
