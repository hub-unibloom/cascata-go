package utils

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// PurifyPgxValue converts pgx-specific types (pgtype.*) to standard Go primitives
// that serialize correctly to JSON. This is the core of the 'Sovereign Purifier'.
func PurifyPgxValue(val interface{}) interface{} {
	if val == nil {
		return nil
	}

	switch v := val.(type) {
	case pgtype.Numeric:
		if !v.Valid { return nil }
		if f, err := v.Float64Value(); err == nil {
			return f.Float64
		}
		return v.Int.String()
	case pgtype.UUID:
		if !v.Valid { return nil }
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", v.Bytes[0:4], v.Bytes[4:6], v.Bytes[6:8], v.Bytes[8:10], v.Bytes[10:16])
	case [16]byte:
		// Handle fixed-size byte array (pgx sometimes returns UUIDs this way)
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", v[0:4], v[4:6], v[6:8], v[8:10], v[10:16])
	case pgtype.Timestamp:
		if !v.Valid { return nil }
		return v.Time
	case pgtype.Timestamptz:
		if !v.Valid { return nil }
		return v.Time
	case pgtype.Date:
		if !v.Valid { return nil }
		return v.Time.Format("2006-01-02")
	case pgtype.Int2:
		if !v.Valid { return nil }
		return v.Int16
	case pgtype.Int4:
		if !v.Valid { return nil }
		return v.Int32
	case pgtype.Int8:
		if !v.Valid { return nil }
		return v.Int64
	case pgtype.Float4:
		if !v.Valid { return nil }
		return v.Float32
	case pgtype.Float8:
		if !v.Valid { return nil }
		return v.Float64
	case pgtype.Bool:
		if !v.Valid { return nil }
		return v.Bool
	case pgtype.Text:
		if !v.Valid { return nil }
		return v.String
	case []byte:
		// [Enterprise Fix] Smart JSON fallback to prevent Base64 leakage or UUID false positives
		// In pgx/v5, JSONB often arrives as raw []byte instead of a dedicated struct.
		if len(v) > 0 && (v[0] == '{' || v[0] == '[') && json.Valid(v) {
			return json.RawMessage(v)
		}
		
		// UUID detection: 16 bytes is standard UUID length
		// Format as proper UUID string: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
		if len(v) == 16 {
			return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
				v[0:4], v[4:6], v[6:8], v[8:10], v[10:16])
		}
		return v
	case []int:
		// Handle UUIDs that arrive as arrays of integers (16 integers = 16 bytes)
		if len(v) == 16 {
			bytes := make([]byte, 16)
			for i, val := range v {
				if val >= 0 && val <= 255 {
					bytes[i] = byte(val)
				}
			}
			return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
				bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
		}
		return v
	case []int64:
		// Handle UUIDs that arrive as arrays of int64
		if len(v) == 16 {
			bytes := make([]byte, 16)
			for i, val := range v {
				if val >= 0 && val <= 255 {
					bytes[i] = byte(val)
				}
			}
			return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
				bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
		}
		return v
	case time.Time:
		return v
	}

	return val
}

// PurifyMap sanitizes a map of pgx results for JSON transmission
func PurifyMap(m map[string]interface{}) map[string]interface{} {
	if m == nil { return nil }
	for k, v := range m {
		m[k] = PurifyPgxValue(v)
	}
	return m
}
