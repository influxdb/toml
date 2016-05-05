package toml

import (
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"time"

	"go/ast"

	"github.com/naoina/go-stringutil"
)

const (
	tagOmitempty = "omitempty"
	tagDocName   = "doc"
	tagSkip      = "-"
	indentStr    = "    "
)

// Marshal returns the TOML encoding of v.
//
// Struct values encode as TOML. Each exported struct field becomes a field of
// the TOML structure unless
//   - the field's tag is "-", or
//   - the field is empty and its tag specifies the "omitempty" option.
// The "toml" key in the struct field's tag value is the key name, followed by
// an optional comma and options. Examples:
//
//   // Field is ignored by this package.
//   Field int `toml:"-"`
//
//   // Field appears in TOML as key "myName".
//   Field int `toml:"myName"`
//
//   // Field appears in TOML as key "myName" and the field is omitted from the
//   // result of encoding if its value is empty.
//   Field int `toml:"myName,omitempty"`
//
//   // Field appears in TOML as key "field", but the field is skipped if
//   // empty.
//   // Note the leading comma.
//   Field int `toml:",omitempty"`
func Marshal(v interface{}) ([]byte, error) {
	return marshal(nil, "", "", reflect.ValueOf(v), false)
}

// A Encoder writes TOML to an output stream.
type Encoder struct {
	w io.Writer
}

// NewEncoder returns a new Encoder that writes to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{
		w: w,
	}
}

// Encode writes the TOML of v to the stream.
// See the documentation for Marshal for details about the conversion of Go values to TOML.
func (e *Encoder) Encode(v interface{}) error {
	b, err := Marshal(v)
	if err != nil {
		return err
	}
	_, err = e.w.Write(b)
	return err
}

// Marshaler is the interface implemented by objects that can marshal themselves into valid TOML.
type Marshaler interface {
	MarshalTOML() ([]byte, error)
}

func marshal(buf []byte, indent, prefix string, rv reflect.Value, inArray bool) ([]byte, error) {
	rt := rv.Type()
	for rt.Kind() == reflect.Ptr {
		rv = rv.Elem()
		rt = rv.Type()
	}

	tableBuf := make([]byte, 0)
	valueBuf := make([]byte, 0)

	for i := 0; i < rv.NumField(); i++ {
		ft := rt.Field(i)
		if ft.Anonymous {
			return marshal(buf, indent, prefix, rv.Field(i), inArray)
		}
		if !ast.IsExported(ft.Name) {
			continue
		}
		colName, rest := extractTag(rt.Field(i).Tag.Get(fieldTagName))
		docStr := rt.Field(i).Tag.Get(tagDocName)

		if colName == tagSkip {
			continue
		}
		if colName == "" {
			colName = stringutil.ToSnakeCase(ft.Name)
		}
		fv := rv.Field(i)
		switch rest {
		case tagOmitempty:
			if fv.Interface() == reflect.Zero(ft.Type).Interface() {
				continue
			}
		}
		var err error
		switch fv.Kind() {
		case reflect.Struct, reflect.Map, reflect.Slice:
			if tableBuf, err = encodeValue(tableBuf, indent, prefix, colName, fv, inArray, docStr); err != nil {
				return nil, err
			}
		default:
			if valueBuf, err = encodeValue(valueBuf, indent, prefix, colName, fv, inArray, docStr); err != nil {
				return nil, err
			}
		}
	}
	return append(append(buf, valueBuf...), tableBuf...), nil
}

func encodeValue(buf []byte, indent, prefix, name string, fv reflect.Value, inArray bool, doc string) ([]byte, error) {
	switch t := fv.Interface().(type) {
	case Marshaler:
		b, err := t.MarshalTOML()
		if err != nil {
			return nil, err
		}
		return appendNewline(appendDocInline(append(appendKey(appendIndent(buf, indent), name, inArray), b...), doc), inArray), nil
	case time.Time:
		return appendNewline(appendDocInline(encodeTime(appendKey(appendIndent(buf, indent), name, inArray), t), doc), inArray), nil
	}
	switch fv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return appendNewline(appendDocInline(encodeInt(appendKey(appendIndent(buf, indent), name, inArray), fv.Int()), doc), inArray), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return appendNewline(appendDocInline(encodeUint(appendKey(appendIndent(buf, indent), name, inArray), fv.Uint()), doc), inArray), nil
	case reflect.Float32, reflect.Float64:
		return appendNewline(appendDocInline(encodeFloat(appendKey(appendIndent(buf, indent), name, inArray), fv.Float()), doc), inArray), nil
	case reflect.Bool:
		return appendNewline(appendDocInline(encodeBool(appendKey(appendIndent(buf, indent), name, inArray), fv.Bool()), doc), inArray), nil
	case reflect.String:
		return appendNewline(appendDocInline(encodeString(appendKey(appendIndent(buf, indent), name, inArray), fv.String()), doc), inArray), nil
	case reflect.Slice, reflect.Array:
		if doc != "" {
			buf = appendNewline(appendDoc(appendIndent(buf, indent), doc), false)
		}
		ft := fv.Type().Elem()
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			name := tableName(prefix, name)
			var err error
			for i := 0; i < fv.Len(); i++ {
				if buf, err = marshal(append(append(append(appendIndent(buf, indent), '[', '['), name...), ']', ']', '\n'), indent+indentStr, name, fv.Index(i), false); err != nil {
					return nil, err
				}
			}
			return buf, nil
		}
		buf = append(appendKey(appendIndent(buf, indent), name, inArray), '[')
		var err error
		for i := 0; i < fv.Len(); i++ {
			if i != 0 {
				buf = append(buf, ',', ' ')
			}
			if buf, err = encodeValue(buf, "", prefix, name, fv.Index(i), true, ""); err != nil {
				return nil, err
			}
		}
		return appendNewline(append(buf, ']'), inArray), nil
	case reflect.Struct:
		name := tableName(prefix, name)
		if doc != "" {
			buf = appendNewline(appendDoc(appendIndent(buf, indent), doc), false)
		}
		return marshal(append(append(append(appendIndent(buf, indent), '['), name...), ']', '\n'), indent+indentStr, name, fv, inArray)
	case reflect.Interface:
		var err error
		if buf, err = encodeInterface(appendKey(appendIndent(buf, indent), name, inArray), fv.Interface()); err != nil {
			return nil, err
		}
		return appendNewline(buf, inArray), nil
	case reflect.Ptr:
		newElem := fv.Elem()
		if newElem.IsValid() {
			return encodeValue(buf, indent, prefix, name, newElem, inArray, doc)
		} else {
			return encodeValue(buf, indent, prefix, name, reflect.New(fv.Type().Elem()), inArray, doc)
		}
	case reflect.Map:
		name := tableName(prefix, name)
		if doc != "" {
			buf = appendNewline(appendDoc(appendIndent(buf, indent), doc), false)
		}
		buf = append(append(append(appendIndent(buf, indent), '['), name...), ']', '\n')

		keys := fv.MapKeys()
		sortedKeys := make([]string, 0, len(keys))
		for _, key := range keys {
			var keyStr string
			switch k := key.Interface().(type) {
			case fmt.Stringer:
				keyStr = k.String()
			case string:
				keyStr = k
			}
			sortedKeys = append(sortedKeys, keyStr)
		}
		sort.Strings(sortedKeys)

		var err error
		for _, key := range sortedKeys {
			buf, err = encodeValue(buf, indent+indentStr, name, key, fv.MapIndex(reflect.ValueOf(key)), inArray, "")
			if err != nil {
				return nil, err
			}
		}
		return buf, nil
	}
	return nil, fmt.Errorf("toml: marshal: unsupported type %v", fv.Kind())
}

func appendDocInline(buf []byte, doc string) []byte {
	if doc != "" {
		return append(append(buf, ' ', '#', ' '), doc...)
	} else {
		return buf
	}
}

func appendDoc(buf []byte, doc string) []byte {
	return append(buf, appendDocInline(nil, doc)[1:]...)
}

func appendKey(buf []byte, key string, inArray bool) []byte {
	if !inArray {
		return append(append(buf, key...), ' ', '=', ' ')
	}
	return buf
}

func appendIndent(buf []byte, indent string) []byte {
	return append(buf, indent...)
}

func appendNewline(buf []byte, inArray bool) []byte {
	if !inArray {
		return append(buf, '\n')
	}
	return buf
}

func encodeInterface(buf []byte, v interface{}) ([]byte, error) {
	switch v := v.(type) {
	case int:
		return encodeInt(buf, int64(v)), nil
	case int8:
		return encodeInt(buf, int64(v)), nil
	case int16:
		return encodeInt(buf, int64(v)), nil
	case int32:
		return encodeInt(buf, int64(v)), nil
	case int64:
		return encodeInt(buf, v), nil
	case uint:
		return encodeUint(buf, uint64(v)), nil
	case uint8:
		return encodeUint(buf, uint64(v)), nil
	case uint16:
		return encodeUint(buf, uint64(v)), nil
	case uint32:
		return encodeUint(buf, uint64(v)), nil
	case uint64:
		return encodeUint(buf, v), nil
	case float32:
		return encodeFloat(buf, float64(v)), nil
	case float64:
		return encodeFloat(buf, v), nil
	case bool:
		return encodeBool(buf, v), nil
	case string:
		return encodeString(buf, v), nil
	}
	return nil, fmt.Errorf("toml: marshal: unable to detect a type of value `%v'", v)
}

func encodeInt(buf []byte, i int64) []byte {
	return strconv.AppendInt(buf, i, 10)
}

func encodeUint(buf []byte, u uint64) []byte {
	return strconv.AppendUint(buf, u, 10)
}

func encodeFloat(buf []byte, f float64) []byte {
	return strconv.AppendFloat(buf, f, 'e', -1, 64)
}

func encodeBool(buf []byte, b bool) []byte {
	return strconv.AppendBool(buf, b)
}

func encodeString(buf []byte, s string) []byte {
	return strconv.AppendQuote(buf, s)
}

func encodeTime(buf []byte, t time.Time) []byte {
	return append(buf, t.Format(time.RFC3339Nano)...)
}
