// Package repair applies repair rules: small, ordered edits to a provider's
// JSON payload (convert a type, rename a field, fill a default, map a value…)
// so a customer's endpoint keeps receiving the shape it expects while the
// provider fixes their side. The stored original payload is never changed.
package repair

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Operations.
const (
	OpConvert = "convert" // path, type: string | number | integer | boolean
	OpRename  = "rename"  // from, to
	OpSet     = "set"     // path, value (always)
	OpDefault = "default" // path, value (only when missing or null)
	OpRemove  = "remove"  // path
	OpMap     = "map"     // path, values: {"old": new, …} for string values
)

// Types a value can be converted to.
const (
	TypeString  = "string"
	TypeNumber  = "number"
	TypeInteger = "integer"
	TypeBoolean = "boolean"
)

// Limits keep rules small and cheap to apply on the delivery path.
const (
	MaxOps       = 20
	maxPathLen   = 300
	maxValueLen  = 4 << 10
	maxMapValues = 100
)

// Op is one edit. Paths use the contract notation: "payload.amount", "items[].price".
type Op struct {
	Op     string                     `json:"op"`
	Path   string                     `json:"path,omitempty"`
	Type   string                     `json:"type,omitempty"`
	From   string                     `json:"from,omitempty"`
	To     string                     `json:"to,omitempty"`
	Value  json.RawMessage            `json:"value,omitempty"`
	Values map[string]json.RawMessage `json:"values,omitempty"`
}

// Rule is a named list of ops for one webhook (and optionally one event type).
type Rule struct {
	ID        string
	Name      string
	EventType string // "" = every event type
	Ops       []Op
}

// Applies reports whether the rule is meant for events of this type.
func (r Rule) Applies(eventType string) bool { return r.EventType == "" || r.EventType == eventType }

// Validate checks ops before they are saved.
func Validate(ops []Op) error {
	if len(ops) == 0 {
		return fmt.Errorf("add at least one change")
	}
	if len(ops) > MaxOps {
		return fmt.Errorf("a rule can have at most %d changes", MaxOps)
	}
	for i, op := range ops {
		if err := op.validate(); err != nil {
			return fmt.Errorf("change %d: %w", i+1, err)
		}
	}
	return nil
}

func (op Op) validate() error {
	switch op.Op {
	case OpConvert:
		switch op.Type {
		case TypeString, TypeNumber, TypeInteger, TypeBoolean:
		default:
			return fmt.Errorf("convert to string, number, integer or boolean")
		}
		return validPath(op.Path, true)
	case OpRename:
		if err := validPath(op.From, false); err != nil {
			return fmt.Errorf("from: %w", err)
		}
		if err := validPath(op.To, false); err != nil {
			return fmt.Errorf("to: %w", err)
		}
		if op.From == op.To {
			return fmt.Errorf("from and to are the same field")
		}
		if scopeOf(op.From) != scopeOf(op.To) {
			return fmt.Errorf("from and to must be inside the same list (same [] part)")
		}
		return nil
	case OpSet, OpDefault:
		if err := validPath(op.Path, false); err != nil {
			return err
		}
		if len(op.Value) == 0 {
			return fmt.Errorf("a value is required")
		}
		if len(op.Value) > maxValueLen {
			return fmt.Errorf("the value is too large")
		}
		if !json.Valid(op.Value) {
			return fmt.Errorf("the value must be valid JSON (put text in quotes)")
		}
		return nil
	case OpRemove:
		return validPath(op.Path, false)
	case OpMap:
		if err := validPath(op.Path, true); err != nil {
			return err
		}
		if len(op.Values) == 0 {
			return fmt.Errorf("add at least one value to map")
		}
		if len(op.Values) > maxMapValues {
			return fmt.Errorf("at most %d values can be mapped", maxMapValues)
		}
		for k, v := range op.Values {
			if len(v) == 0 || len(v) > maxValueLen || !json.Valid(v) {
				return fmt.Errorf("the replacement for %q must be valid JSON (put text in quotes)", k)
			}
		}
		return nil
	case "":
		return fmt.Errorf("choose what the change does")
	}
	return fmt.Errorf("unknown change %q", op.Op)
}

// validPath checks the "a.b[].c" notation. elementsOK allows a path ending in
// "[]", which targets every element of a list (convert and map only).
func validPath(p string, elementsOK bool) error {
	if p == "" {
		return fmt.Errorf("a field path is required")
	}
	if len(p) > maxPathLen {
		return fmt.Errorf("the field path is too long")
	}
	segs := strings.Split(p, ".")
	for i, s := range segs {
		key := strings.TrimSuffix(s, "[]")
		if key == "" || strings.Contains(key, "[") || strings.Contains(key, "]") {
			return fmt.Errorf("%q is not a valid field path (use a.b or items[].price)", p)
		}
		if i == len(segs)-1 && key != s && !elementsOK {
			return fmt.Errorf("%q must name a field, not the items of a list", p)
		}
	}
	return nil
}

// scopeOf is the path up to and including its last "[]" segment ("" when none).
func scopeOf(p string) string {
	segs := strings.Split(p, ".")
	for i := len(segs) - 1; i >= 0; i-- {
		if strings.HasSuffix(segs[i], "[]") {
			return strings.Join(segs[:i+1], ".")
		}
	}
	return ""
}

// Result of applying rules to one payload.
type Result struct {
	Body    []byte   // the repaired payload, or the original when nothing changed
	Changed bool     // whether any rule changed anything
	Applied []string // IDs of the rules that changed something, in order
	// Counts[i][j] is how many values op j of rule i changed.
	Counts [][]int
}

// Apply runs the rules that match eventType, in order. Payloads that aren't a
// JSON object are returned unchanged. The original bytes are returned
// untouched when no op changes anything.
func Apply(payload []byte, eventType string, rules []Rule) (Result, error) {
	res := Result{Body: payload, Counts: make([][]int, len(rules))}
	root, err := decode(payload)
	if err != nil {
		return res, nil // not JSON: nothing to repair
	}
	obj, ok := root.(*object)
	if !ok {
		return res, nil
	}
	for i, r := range rules {
		res.Counts[i] = make([]int, len(r.Ops))
		if !r.Applies(eventType) {
			continue
		}
		ruleChanged := false
		for j, op := range r.Ops {
			n, err := op.apply(obj)
			if err != nil {
				return Result{Body: payload, Counts: res.Counts}, fmt.Errorf("rule %q, change %d: %w", r.Name, j+1, err)
			}
			res.Counts[i][j] = n
			if n > 0 {
				ruleChanged = true
			}
		}
		if ruleChanged {
			res.Applied = append(res.Applied, r.ID)
		}
	}
	if len(res.Applied) == 0 {
		return res, nil
	}
	out, err := encode(obj)
	if err != nil {
		return Result{Body: payload, Counts: res.Counts}, err
	}
	res.Body, res.Changed = out, true
	return res, nil
}

// apply runs one op and returns how many values it changed.
func (op Op) apply(root *object) (int, error) {
	switch op.Op {
	case OpRename:
		n := 0
		for _, scope := range scopes(root, op.From) {
			from := relative(op.From)
			parent, key, ok := walk(scope, from, false)
			if !ok {
				continue
			}
			v, exists := parent.get(key)
			if !exists {
				continue
			}
			tParent, tKey, ok := walk(scope, relative(op.To), true)
			if !ok {
				continue
			}
			if cur, has := tParent.get(tKey); has && cur != nil {
				continue // never overwrite a real value
			}
			parent.del(key)
			tParent.set(tKey, v)
			n++
		}
		return n, nil
	case OpSet, OpDefault:
		val, err := fromRaw(op.Value)
		if err != nil {
			return 0, err
		}
		n := 0
		for _, scope := range scopes(root, op.Path) {
			parent, key, ok := walk(scope, relative(op.Path), true)
			if !ok {
				continue
			}
			cur, has := parent.get(key)
			if op.Op == OpDefault && has && cur != nil {
				continue
			}
			if has && sameJSON(cur, val) {
				continue
			}
			parent.set(key, clone(val))
			n++
		}
		return n, nil
	case OpRemove:
		n := 0
		for _, scope := range scopes(root, op.Path) {
			if parent, key, ok := walk(scope, relative(op.Path), false); ok && parent.del(key) {
				n++
			}
		}
		return n, nil
	case OpConvert, OpMap:
		var mapped map[string]any
		if op.Op == OpMap {
			mapped = map[string]any{}
			for k, raw := range op.Values {
				v, err := fromRaw(raw)
				if err != nil {
					return 0, err
				}
				mapped[k] = v
			}
		}
		change := func(v any) (any, bool) {
			if op.Op == OpConvert {
				return convert(v, op.Type)
			}
			s, ok := v.(string)
			if !ok {
				return v, false
			}
			nv, ok := mapped[s]
			if !ok || sameJSON(v, nv) {
				return v, false
			}
			return clone(nv), true
		}
		n := 0
		if strings.HasSuffix(op.Path, "[]") { // every element of a list
			for _, arr := range lists(root, op.Path) {
				for i, v := range arr {
					if nv, ok := change(v); ok {
						arr[i] = nv
						n++
					}
				}
			}
			return n, nil
		}
		for _, scope := range scopes(root, op.Path) {
			parent, key, ok := walk(scope, relative(op.Path), false)
			if !ok {
				continue
			}
			v, has := parent.get(key)
			if !has {
				continue
			}
			if nv, ok := change(v); ok {
				parent.set(key, nv)
				n++
			}
		}
		return n, nil
	}
	return 0, fmt.Errorf("unknown change %q", op.Op)
}

// scopes returns the objects a path's relative part is resolved against: the
// root, or every object element of the list(s) named by the path's "[]" part.
func scopes(root *object, path string) []*object {
	scope := scopeOf(path)
	if scope == "" {
		return []*object{root}
	}
	var out []*object
	for _, arr := range lists(root, scope) {
		for _, v := range arr {
			if o, ok := v.(*object); ok {
				out = append(out, o)
			}
		}
	}
	return out
}

// lists resolves a path ending in "[]" to the list(s) it names.
func lists(root *object, path string) [][]any {
	cur := []*object{root}
	segs := strings.Split(path, ".")
	var out [][]any
	for i, s := range segs {
		key, isList := strings.CutSuffix(s, "[]")
		var next []*object
		for _, o := range cur {
			v, ok := o.get(key)
			if !ok {
				continue
			}
			if !isList {
				if child, ok := v.(*object); ok {
					next = append(next, child)
				}
				continue
			}
			arr, ok := v.([]any)
			if !ok {
				continue
			}
			if i == len(segs)-1 {
				out = append(out, arr)
				continue
			}
			for _, e := range arr {
				if child, ok := e.(*object); ok {
					next = append(next, child)
				}
			}
		}
		cur = next
	}
	return out
}

// relative is the part of a path after its last "[]" segment.
func relative(path string) string {
	scope := scopeOf(path)
	if scope == "" {
		return path
	}
	return strings.TrimPrefix(path[len(scope):], ".")
}

// walk finds the object holding the last key of a plain dotted path. With
// create, missing intermediate objects are added.
func walk(o *object, path string, create bool) (*object, string, bool) {
	segs := strings.Split(path, ".")
	for _, s := range segs[:len(segs)-1] {
		v, ok := o.get(s)
		if !ok || v == nil {
			if !create {
				return nil, "", false
			}
			child := newObject()
			o.set(s, child)
			o = child
			continue
		}
		child, ok := v.(*object)
		if !ok {
			return nil, "", false // a non-object is in the way: leave it alone
		}
		o = child
	}
	return o, segs[len(segs)-1], true
}

// convert changes a scalar's type; it reports false when the value is already
// that type, is null, or can't be converted without losing information.
func convert(v any, to string) (any, bool) {
	switch to {
	case TypeString:
		switch t := v.(type) {
		case json.Number:
			return string(t), true
		case bool:
			return strconv.FormatBool(t), true
		}
	case TypeNumber, TypeInteger:
		var num json.Number
		switch t := v.(type) {
		case string:
			s := strings.TrimSpace(t)
			f, err := strconv.ParseFloat(s, 64)
			if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
				return v, false
			}
			num = canonicalNumber(s, f)
		case json.Number:
			if to == TypeNumber {
				return v, false
			}
			num = t
		case bool:
			if t {
				num = "1"
			} else {
				num = "0"
			}
		default:
			return v, false
		}
		if to == TypeInteger {
			f, err := num.Float64()
			if err != nil || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
				return v, false // 12.5 is not an integer: leave it for a human
			}
			if _, err := num.Int64(); err != nil {
				num = json.Number(strconv.FormatInt(int64(f), 10))
			}
		}
		if n, ok := v.(json.Number); ok && n == num {
			return v, false
		}
		return num, true
	case TypeBoolean:
		switch t := v.(type) {
		case string:
			switch strings.ToLower(strings.TrimSpace(t)) {
			case "true", "1", "yes", "y", "on":
				return true, true
			case "false", "0", "no", "n", "off":
				return false, true
			}
		case json.Number:
			switch string(t) {
			case "1":
				return true, true
			case "0":
				return false, true
			}
		}
	}
	return v, false
}

// canonicalNumber keeps the text when it is already valid JSON ("100", "1.5",
// "2e3") and reformats otherwise ("+5", ".5", "5.").
func canonicalNumber(s string, f float64) json.Number {
	if json.Valid([]byte(s)) && !strings.ContainsAny(s, "\"tfn") {
		return json.Number(s)
	}
	return json.Number(strconv.FormatFloat(f, 'f', -1, 64))
}

func sameJSON(a, b any) bool {
	x, err1 := encode(a)
	y, err2 := encode(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}

func clone(v any) any {
	switch t := v.(type) {
	case *object:
		c := newObject()
		for _, k := range t.keys {
			c.set(k, clone(t.vals[k]))
		}
		return c
	case []any:
		c := make([]any, len(t))
		for i, x := range t {
			c[i] = clone(x)
		}
		return c
	}
	return v
}
