// Package axi implements the Agent eXperience Interface conventions: TOON
// output, strict flag validation, structured errors and session hooks.
package axi

import (
	"strconv"
	"strings"
)

// Doc is an ordered TOON document. Field order is the schema an agent reads, so
// it is preserved exactly as written.
type Doc struct {
	keys   []string
	values []any
	// raw holds output that is already encoded, such as a reply relayed from
	// another process.
	raw   string
	isRaw bool
}

// Raw wraps output that is already TOON, so it passes through unchanged.
func Raw(encoded string) *Doc {
	return &Doc{raw: strings.TrimRight(encoded, "\n"), isRaw: true}
}

// Row is one record of a tabular array. Every row of an array must carry the
// same fields in the same order.
type Row = *Doc

func NewDoc() *Doc { return &Doc{} }

// Set appends a field. Supported values: string, int, uint, bool, []string,
// []*Doc (tabular array) and *Doc (nested document).
func (d *Doc) Set(key string, value any) *Doc {
	d.keys = append(d.keys, key)
	d.values = append(d.values, value)
	return d
}

func (d *Doc) Len() int { return len(d.keys) }

// Encode renders the document as TOON without a trailing newline.
func (d *Doc) Encode() string {
	if d.isRaw {
		return d.raw
	}
	var b strings.Builder
	d.encode(&b, 0)
	return strings.TrimRight(b.String(), "\n")
}

func (d *Doc) encode(b *strings.Builder, indent int) {
	pad := strings.Repeat("  ", indent)
	for i, key := range d.keys {
		switch value := d.values[i].(type) {
		case *Doc:
			b.WriteString(pad + encodeKey(key) + ":\n")
			value.encode(b, indent+1)
		case []string:
			b.WriteString(pad + encodeKey(key) + "[" + strconv.Itoa(len(value)) + "]: ")
			parts := make([]string, len(value))
			for j, item := range value {
				parts[j] = encodeScalar(item)
			}
			b.WriteString(strings.Join(parts, ",") + "\n")
		case []*Doc:
			writeTable(b, pad, key, value)
		default:
			b.WriteString(pad + encodeKey(key) + ": " + encodeValue(value) + "\n")
		}
	}
}

func writeTable(b *strings.Builder, pad, key string, rows []*Doc) {
	if len(rows) == 0 {
		b.WriteString(pad + encodeKey(key) + "[0]{}:\n")
		return
	}
	fields := rows[0].keys
	header := make([]string, len(fields))
	for i, field := range fields {
		header[i] = encodeKey(field)
	}
	b.WriteString(pad + encodeKey(key) + "[" + strconv.Itoa(len(rows)) + "]{" + strings.Join(header, ",") + "}:\n")
	for _, row := range rows {
		cells := make([]string, len(row.values))
		for i, value := range row.values {
			cells[i] = encodeValue(value)
		}
		b.WriteString(pad + "  " + strings.Join(cells, ",") + "\n")
	}
}

func encodeValue(value any) string {
	switch v := value.(type) {
	case string:
		return encodeScalar(v)
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	default:
		return encodeScalar("")
	}
}

func encodeKey(key string) string {
	for i, r := range key {
		alpha := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		digit := r >= '0' && r <= '9'
		if !alpha && !(digit && i > 0) {
			return quote(key)
		}
	}
	if key == "" {
		return quote(key)
	}
	return key
}

// encodeScalar quotes anything a TOON reader could mistake for another type or
// for a delimiter. Snowflake ids are strings and must survive as strings.
func encodeScalar(s string) string {
	if s == "" || s == "true" || s == "false" || s == "null" {
		return quote(s)
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return quote(s)
	}
	if strings.ContainsAny(s, ",:\"\n\r\t[]{}") {
		return quote(s)
	}
	if strings.TrimSpace(s) != s {
		return quote(s)
	}
	return s
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
