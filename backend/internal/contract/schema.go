// Package contract learns the shape of each event type's JSON payload and
// checks new events against it, classifying differences as compatible,
// suspicious or breaking.
package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// Limits keep a pathological payload (huge maps keyed by IDs) from blowing up a contract.
const (
	maxDepth      = 10
	maxPaths      = 1000
	maxEnumValues = 10
	maxEnumLen    = 64
	minEnumSeen   = 5 // a field needs this many samples before its values count as an enum
)

// JSON types as recorded in a schema.
const (
	TString  = "string"
	TInteger = "integer"
	TNumber  = "number"
	TBoolean = "boolean"
	TNull    = "null"
	TObject  = "object"
	TArray   = "array"
)

// Field is what we know about one path, e.g. "payload.payment.entity.amount".
// Array elements use "[]": "items[].sku".
type Field struct {
	Types        []string `json:"types"`                   // sorted
	Seen         int      `json:"seen"`                    // samples that contained this path
	Enum         []string `json:"enum,omitempty"`          // distinct string values, while few
	EnumOverflow bool     `json:"enum_overflow,omitempty"` // too many/long values: not an enum
}

// Schema is a learned shape. A field is required when Seen == Samples.
type Schema struct {
	Samples   int               `json:"samples"`
	Fields    map[string]*Field `json:"fields"`
	Truncated bool              `json:"truncated,omitempty"` // hit maxPaths/maxDepth
}

func NewSchema() *Schema { return &Schema{Fields: map[string]*Field{}} }

// Required reports whether a field was present in every sample.
func (s *Schema) Required(f *Field) bool { return s.Samples > 0 && f.Seen >= s.Samples }

// isEnum reports whether f's values form a closed set worth checking. Values
// must repeat (on average at least twice each), so unique values such as IDs
// never count as an enum even while there are only a few samples.
func (f *Field) isEnum() bool {
	return !f.EnumOverflow && len(f.Enum) > 0 && f.Seen >= minEnumSeen &&
		len(f.Enum)*2 <= f.Seen && hasType(f.Types, TString)
}

// ---- observation ------------------------------------------------------------------------

// Obs is one event's flattened payload: path -> the types and string values seen there.
type Obs struct {
	Paths     map[string]*obsField
	Truncated bool
}

type obsField struct {
	types  map[string]bool
	values map[string]bool
	long   bool // a string value exceeded maxEnumLen
}

// Observe flattens a JSON object payload. ok is false for non-objects/invalid JSON.
func Observe(payload []byte) (o Obs, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return o, false
	}
	root, isObj := v.(map[string]any)
	if !isObj {
		return o, false
	}
	o.Paths = map[string]*obsField{}
	o.walkObject("", root, 1)
	return o, true
}

func (o *Obs) add(path, typ string) *obsField {
	f := o.Paths[path]
	if f == nil {
		if len(o.Paths) >= maxPaths {
			o.Truncated = true
			return nil
		}
		f = &obsField{types: map[string]bool{}, values: map[string]bool{}}
		o.Paths[path] = f
	}
	f.types[typ] = true
	return f
}

func (o *Obs) walkObject(prefix string, m map[string]any, depth int) {
	if depth > maxDepth {
		o.Truncated = true
		return
	}
	for k, v := range m {
		if len(k) > 200 {
			continue
		}
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		o.walk(p, v, depth)
	}
}

func (o *Obs) walk(path string, v any, depth int) {
	switch t := v.(type) {
	case map[string]any:
		if o.add(path, TObject) != nil {
			o.walkObject(path, t, depth+1)
		}
	case []any:
		if o.add(path, TArray) != nil && depth < maxDepth {
			for _, el := range t {
				o.walk(path+"[]", el, depth+1)
			}
		}
	case string:
		if f := o.add(path, TString); f != nil {
			if len(t) > maxEnumLen {
				f.long = true
			} else {
				f.values[t] = true
			}
		}
	case json.Number:
		if strings.ContainsAny(t.String(), ".eE") {
			o.add(path, TNumber)
		} else {
			o.add(path, TInteger)
		}
	case bool:
		o.add(path, TBoolean)
	case nil:
		o.add(path, TNull)
	}
}

// ---- learning -----------------------------------------------------------------------------

// Merge folds one observation into the schema.
func (s *Schema) Merge(o Obs) {
	s.Samples++
	if o.Truncated {
		s.Truncated = true
	}
	for p, of := range o.Paths {
		f := s.Fields[p]
		if f == nil {
			if len(s.Fields) >= maxPaths {
				s.Truncated = true
				continue
			}
			f = &Field{}
			s.Fields[p] = f
		}
		f.Seen++
		for t := range of.types {
			if !hasType(f.Types, t) {
				f.Types = append(f.Types, t)
				sort.Strings(f.Types)
			}
		}
		if of.types[TString] && !f.EnumOverflow {
			if of.long {
				f.EnumOverflow = true
			}
			for v := range of.values {
				if !contains(f.Enum, v) {
					f.Enum = append(f.Enum, v)
				}
			}
			if f.EnumOverflow || len(f.Enum) > maxEnumValues {
				f.EnumOverflow, f.Enum = true, nil
			} else {
				sort.Strings(f.Enum)
			}
		}
	}
}

// Fingerprint is a stable hash of the schema's structure (paths, types,
// required-ness and enums), so identical shapes get identical fingerprints.
func (s *Schema) Fingerprint() string {
	paths := make([]string, 0, len(s.Fields))
	for p := range s.Fields {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		f := s.Fields[p]
		req := "o"
		if s.Required(f) {
			req = "r"
		}
		enum := ""
		if f.isEnum() {
			enum = strings.Join(f.Enum, "|")
		}
		h.Write([]byte(p + "\x00" + strings.Join(f.Types, ",") + "\x00" + req + "\x00" + enum + "\n"))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))[:16]
}

// ---- checking ---------------------------------------------------------------------------------

// Severity of a finding, from harmless to incident-worthy.
type Severity int

const (
	OK Severity = iota
	Compatible
	Suspicious
	Breaking
)

func (s Severity) String() string {
	return [...]string{"ok", "compatible", "suspicious", "breaking"}[s]
}

// Finding kinds.
const (
	KindMissingField = "missing_field"
	KindTypeChanged  = "type_changed"
	KindTypeWidened  = "type_widened"
	KindNullValue    = "null_value"
	KindNewEnumValue = "new_enum_value"
	KindNewField     = "new_field"
)

// Finding is one difference between an event and the contract.
type Finding struct {
	Severity Severity
	Kind     string
	Path     string
	Expected string
	Actual   string
	Nested   int // for missing objects: how many required fields inside went missing too
}

// Check compares an observation with a contract version. critical lists paths
// whose loss or retyping is breaking. Returns findings (new fields included, as
// Compatible) and the overall severity.
func Check(v *Schema, critical map[string]bool, o Obs) ([]Finding, Severity) {
	var out []Finding
	worst := OK
	emit := func(f Finding) {
		out = append(out, f)
		if f.Severity > worst {
			worst = f.Severity
		}
	}

	// Missing required fields. Report only the outermost missing path (a missing
	// object implies its children are missing), breaking if any of them is critical.
	missing := map[string]bool{}
	for p, f := range v.Fields {
		if !v.Required(f) || o.Paths[p] != nil {
			continue
		}
		// Fields inside an array only count when this event's array had elements.
		if arr := nearestArray(p); arr != "" && o.Paths[arr] == nil {
			continue
		}
		missing[p] = true
	}
	for _, p := range sortedKeys(missing) {
		if ancestorIn(p, missing) {
			continue
		}
		f := Finding{Kind: KindMissingField, Path: p, Severity: Suspicious, Expected: typesLabel(v.Fields[p].Types), Actual: "missing"}
		for q := range missing {
			if q == p || strings.HasPrefix(q, p+".") || strings.HasPrefix(q, p+"[]") {
				if q != p {
					f.Nested++
				}
				if critical[q] {
					f.Severity = Breaking
				}
			}
		}
		emit(f)
	}

	// Type, null and enum changes on known paths; new paths.
	for _, p := range sortedKeys(o.Paths) {
		of := o.Paths[p]
		f := v.Fields[p]
		if f == nil {
			if !ancestorNew(p, v) { // report the new parent once, not every child
				emit(Finding{Kind: KindNewField, Path: p, Severity: Compatible, Actual: typesLabel(keys(of.types))})
			}
			continue
		}
		for _, t := range sortedKeys(of.types) {
			if hasType(f.Types, t) {
				continue
			}
			switch {
			case t == TInteger && hasType(f.Types, TNumber):
				// integers are valid numbers
			case t == TNumber && hasType(f.Types, TInteger):
				emit(Finding{Kind: KindTypeWidened, Path: p, Severity: Suspicious, Expected: typesLabel(f.Types), Actual: t})
			case t == TNull:
				emit(Finding{Kind: KindNullValue, Path: p, Severity: severityFor(critical[p]), Expected: typesLabel(f.Types), Actual: TNull})
			default:
				emit(Finding{Kind: KindTypeChanged, Path: p, Severity: severityFor(critical[p]), Expected: typesLabel(f.Types), Actual: t})
			}
		}
		if f.isEnum() && of.types[TString] {
			for _, val := range sortedKeys(of.values) {
				if !contains(f.Enum, val) {
					emit(Finding{Kind: KindNewEnumValue, Path: p, Severity: Suspicious, Expected: strings.Join(f.Enum, " | "), Actual: val})
				}
			}
		}
	}
	return out, worst
}

func severityFor(critical bool) Severity {
	if critical {
		return Breaking
	}
	return Suspicious
}

// nearestArray returns the closest "x[]" prefix of p, or "" if p isn't inside an array.
func nearestArray(p string) string {
	i := strings.LastIndex(p, "[]")
	if i < 0 {
		return ""
	}
	return p[:i+2]
}

// parents returns the ancestor paths of p, nearest first: "a.b[].c" -> "a.b[]", "a.b", "a".
func parents(p string) []string {
	var out []string
	for {
		i := strings.LastIndexAny(p, ".[")
		if i <= 0 {
			return out
		}
		p = p[:i]
		out = append(out, p)
	}
}

func ancestorIn(p string, set map[string]bool) bool {
	for _, a := range parents(p) {
		if set[a] {
			return true
		}
	}
	return false
}

// ancestorNew reports whether p's nearest parent is also unknown to the schema,
// in which case the parent is reported as the new field instead of p.
func ancestorNew(p string, v *Schema) bool {
	ps := parents(p)
	return len(ps) > 0 && v.Fields[ps[0]] == nil
}

func typesLabel(t []string) string { return strings.Join(t, " | ") }

func hasType(ts []string, t string) bool { return contains(ts, t) }

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func keys(m map[string]bool) []string { return sortedKeys(m) }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// IsEnum reports whether f's string values currently form a checked enum.
func IsEnum(f *Field) bool { return f.isEnum() }
