package httpx

import (
	"bytes"
	"encoding"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/shopspring/decimal"
)

// shopspring's JSON form drops trailing zeros ("1000" for 1000.00), while the API
// reference promises decimal strings such as "2500.00". Data and List run every
// response through normalize, which writes each decimal at its own scale: money
// at 2 places, quantities at 3, costs at 4, as scanned from the numeric column
// or produced by money.Money, money.Qty and money.Cost.

var (
	decimalType     = reflect.TypeOf(decimal.Decimal{})
	nullDecimalType = reflect.TypeOf(decimal.NullDecimal{})
	marshalerType   = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshaler   = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

// fixed is a decimal rendered as a JSON string at its own scale.
type fixed decimal.Decimal

func (f fixed) MarshalJSON() ([]byte, error) {
	d := decimal.Decimal(f)
	s := d.String()
	if exp := d.Exponent(); exp < 0 {
		s = d.StringFixed(-exp)
	}
	return json.Marshal(s)
}

// object keeps struct fields in their declared order.
type object struct {
	keys []string
	vals []any
}

func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := json.Marshal(o.vals[i])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func normalize(v any) any {
	if v == nil {
		return nil
	}
	return norm(reflect.ValueOf(v))
}

func norm(rv reflect.Value) any {
	if !rv.IsValid() {
		return nil
	}
	t := rv.Type()
	switch {
	case t == decimalType:
		return fixed(rv.Interface().(decimal.Decimal))
	case t == nullDecimalType:
		nd := rv.Interface().(decimal.NullDecimal)
		if !nd.Valid {
			return nil
		}
		return fixed(nd.Decimal)
	}
	switch rv.Kind() {
	case reflect.Interface, reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		// A pointer type inherits its element's value-receiver methods, so *Decimal would
		// look custom-marshalled; decimals are handled before that check.
		if et := rv.Elem().Type(); rv.Kind() == reflect.Pointer && et != decimalType && et != nullDecimalType && customJSON(t) {
			return rv.Interface()
		}
		return norm(rv.Elem())
	}
	if customJSON(t) {
		return rv.Interface()
	}
	switch rv.Kind() {
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return rv.Interface()
		}
		if rv.IsNil() {
			return nil
		}
		out := make(map[string]any, rv.Len())
		it := rv.MapRange()
		for it.Next() {
			out[it.Key().String()] = norm(it.Value())
		}
		return out
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return rv.Interface()
		}
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil
		}
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = norm(rv.Index(i))
		}
		return out
	case reflect.Struct:
		o := object{}
		addFields(&o, rv)
		return o
	}
	return rv.Interface()
}

func customJSON(t reflect.Type) bool {
	return t.Implements(marshalerType) || t.Implements(textMarshaler) ||
		(t.Kind() != reflect.Pointer && (reflect.PointerTo(t).Implements(marshalerType) || reflect.PointerTo(t).Implements(textMarshaler)))
}

// addFields appends a struct's JSON fields using encoding/json's naming,
// omitempty and embedding rules.
func addFields(o *object, rv reflect.Value) {
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		fv := rv.Field(i)
		if f.Anonymous && name == "" {
			inner := fv
			if inner.Kind() == reflect.Pointer {
				if inner.IsNil() {
					continue
				}
				inner = inner.Elem()
			}
			if inner.Kind() == reflect.Struct && !customJSON(inner.Type()) && inner.Type() != decimalType {
				addFields(o, inner)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if strings.Contains(","+opts+",", ",omitempty,") && isEmpty(fv) {
			continue
		}
		o.keys = append(o.keys, name)
		o.vals = append(o.vals, norm(fv))
	}
}

func isEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return false
}
