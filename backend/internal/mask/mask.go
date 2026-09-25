// Package mask hides sensitive values in headers and JSON payloads before they
// are stored or shown in the Explorer.
package mask

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

const Redacted = "[REDACTED]"

// dropHeaders are never stored: they carry credentials for our own gateway or the caller.
var dropHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
}

// sensitiveKeyParts mark a JSON key or header as sensitive when its lowercased
// name contains any of them.
var sensitiveKeyParts = []string{
	"password", "passwd", "secret", "token", "api_key", "apikey",
	"authorization", "cvv", "cvc", "card_number", "aadhaar",
}

// sensitiveExact are matched against the whole key only, to avoid false
// positives such as "panel" or "pincode".
var sensitiveExact = map[string]bool{"pan": true, "pin": true, "otp": true}

func isSensitive(key string) bool {
	k := strings.ReplaceAll(strings.ToLower(key), "-", "_")
	if sensitiveExact[k] {
		return true
	}
	for _, p := range sensitiveKeyParts {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}

// Headers returns a flattened copy of h with credential headers dropped and
// sensitive values redacted. Signature headers are kept: they are needed as
// evidence and are useless without the signing secret.
func Headers(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for name, vals := range h {
		lower := strings.ToLower(name)
		if dropHeaders[lower] {
			continue
		}
		v := strings.Join(vals, ", ")
		if isSensitive(lower) && !strings.Contains(lower, "signature") && !strings.Contains(lower, "hmac") {
			v = Redacted
		}
		out[lower] = v
	}
	return out
}

// JSON redacts values of sensitive keys at any depth. Non-JSON input is returned unchanged.
func JSON(body []byte) []byte {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return body
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return body
	}
	out, err := json.Marshal(walk(v))
	if err != nil {
		return body
	}
	return out
}

func walk(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if isSensitive(k) {
				if val != nil {
					t[k] = Redacted
				}
				continue
			}
			t[k] = walk(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = walk(t[i])
		}
		return t
	default:
		return v
	}
}
