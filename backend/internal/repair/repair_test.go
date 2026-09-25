package repair

import (
	"encoding/json"
	"strings"
	"testing"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func apply(t *testing.T, payload string, ops ...Op) (string, Result) {
	t.Helper()
	if err := Validate(ops); err != nil {
		t.Fatal(err)
	}
	res, err := Apply([]byte(payload), "pay", []Rule{{ID: "r1", Name: "fix", Ops: ops}})
	if err != nil {
		t.Fatal(err)
	}
	return string(res.Body), res
}

func TestConvert(t *testing.T) {
	cases := []struct {
		in, typ, want string
	}{
		{`{"amount":"100"}`, TypeInteger, `{"amount":100}`},
		{`{"amount":" 100.0 "}`, TypeInteger, `{"amount":100}`},
		{`{"amount":"12.5"}`, TypeInteger, `{"amount":"12.5"}`}, // would lose data: unchanged
		{`{"amount":"12.50"}`, TypeNumber, `{"amount":12.50}`},  // exact text kept
		{`{"amount":"+5"}`, TypeNumber, `{"amount":5}`},
		{`{"amount":"abc"}`, TypeNumber, `{"amount":"abc"}`},
		{`{"amount":100}`, TypeString, `{"amount":"100"}`},
		{`{"amount":1.0}`, TypeInteger, `{"amount":1}`},
		{`{"ok":"Yes"}`, TypeBoolean, `{"ok":true}`},
		{`{"ok":0}`, TypeBoolean, `{"ok":false}`},
		{`{"amount":null}`, TypeInteger, `{"amount":null}`},
		{`{"other":1}`, TypeInteger, `{"other":1}`},
	}
	for _, c := range cases {
		field := "amount"
		if strings.Contains(c.in, `"ok"`) {
			field = "ok"
		}
		if got, _ := apply(t, c.in, Op{Op: OpConvert, Path: field, Type: c.typ}); got != c.want {
			t.Errorf("convert %s to %s: got %s, want %s", c.in, c.typ, got, c.want)
		}
	}
}

func TestUnchangedPayloadKeepsExactBytes(t *testing.T) {
	in := `{ "b": 1,   "a": "x<y" }`
	got, res := apply(t, in, Op{Op: OpConvert, Path: "b", Type: TypeInteger})
	if got != in || res.Changed || len(res.Applied) != 0 {
		t.Fatalf("got %q changed=%v", got, res.Changed)
	}
}

func TestKeyOrderAndHTMLKept(t *testing.T) {
	got, res := apply(t, `{"z":"1","a":{"y":2,"b":"<b>&"},"m":[1,2]}`, Op{Op: OpConvert, Path: "z", Type: TypeInteger})
	if got != `{"z":1,"a":{"y":2,"b":"<b>&"},"m":[1,2]}` || !res.Changed || res.Applied[0] != "r1" {
		t.Fatalf("got %s", got)
	}
}

func TestNestedAndLists(t *testing.T) {
	in := `{"order":{"items":[{"price":"10","qty":"2"},{"price":"5.5"},"odd",{"price":7}]},"tags":["1","x"]}`
	got, res := apply(t, in,
		Op{Op: OpConvert, Path: "order.items[].price", Type: TypeNumber},
		Op{Op: OpConvert, Path: "order.items[].qty", Type: TypeInteger},
		Op{Op: OpConvert, Path: "tags[]", Type: TypeInteger},
	)
	want := `{"order":{"items":[{"price":10,"qty":2},{"price":5.5},"odd",{"price":7}]},"tags":[1,"x"]}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if res.Counts[0][0] != 2 || res.Counts[0][1] != 1 || res.Counts[0][2] != 1 {
		t.Fatalf("counts %v", res.Counts)
	}
}

func TestRename(t *testing.T) {
	got, _ := apply(t, `{"payment":{"amount_paise":500,"id":"p1"}}`, Op{Op: OpRename, From: "payment.amount_paise", To: "payment.amount"})
	if got != `{"payment":{"id":"p1","amount":500}}` {
		t.Fatal(got)
	}
	// never overwrites a real value; fills a null
	got, _ = apply(t, `{"a":1,"b":2}`, Op{Op: OpRename, From: "a", To: "b"})
	if got != `{"a":1,"b":2}` {
		t.Fatal("overwrote:", got)
	}
	got, _ = apply(t, `{"a":1,"b":null}`, Op{Op: OpRename, From: "a", To: "b"})
	if got != `{"b":1}` {
		t.Fatal(got)
	}
	// inside list elements, creating intermediates
	got, _ = apply(t, `{"items":[{"sku_code":"A"},{"x":1}]}`, Op{Op: OpRename, From: "items[].sku_code", To: "items[].product.sku"})
	if got != `{"items":[{"product":{"sku":"A"}},{"x":1}]}` {
		t.Fatal(got)
	}
}

func TestSetDefaultRemoveMap(t *testing.T) {
	got, _ := apply(t, `{"status":"SUCCESS","currency":null,"debug":{"x":1}}`,
		Op{Op: OpDefault, Path: "currency", Value: raw(`"INR"`)},
		Op{Op: OpDefault, Path: "country", Value: raw(`"IN"`)},
		Op{Op: OpSet, Path: "meta.source", Value: raw(`"relaya"`)},
		Op{Op: OpRemove, Path: "debug"},
		Op{Op: OpMap, Path: "status", Values: map[string]json.RawMessage{"SUCCESS": raw(`"captured"`), "FAIL": raw(`"failed"`)}},
	)
	want := `{"status":"captured","currency":"INR","country":"IN","meta":{"source":"relaya"}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	got, _ = apply(t, `{"currency":"USD"}`, Op{Op: OpDefault, Path: "currency", Value: raw(`"INR"`)})
	if got != `{"currency":"USD"}` {
		t.Fatal("default overwrote a value:", got)
	}
}

func TestLeavesNonObjectsInTheWayAlone(t *testing.T) {
	got, res := apply(t, `{"a":"text"}`, Op{Op: OpSet, Path: "a.b", Value: raw(`1`)})
	if got != `{"a":"text"}` || res.Changed {
		t.Fatal(got)
	}
}

func TestRulesByEventTypeAndOrder(t *testing.T) {
	rules := []Rule{
		{ID: "only-refund", Name: "a", EventType: "refund", Ops: []Op{{Op: OpSet, Path: "x", Value: raw(`1`)}}},
		{ID: "rename", Name: "b", Ops: []Op{{Op: OpRename, From: "amt", To: "amount"}}},
		{ID: "convert", Name: "c", Ops: []Op{{Op: OpConvert, Path: "amount", Type: TypeInteger}}}, // sees the renamed field
	}
	res, err := Apply([]byte(`{"amt":"9"}`), "pay", rules)
	if err != nil || string(res.Body) != `{"amount":9}` || strings.Join(res.Applied, ",") != "rename,convert" {
		t.Fatal(string(res.Body), res.Applied, err)
	}
}

func TestNotJSONUntouched(t *testing.T) {
	for _, in := range []string{`amount=100`, `[1,2]`, `"x"`, `{"a":1} trailing`} {
		res, err := Apply([]byte(in), "", []Rule{{ID: "r", Ops: []Op{{Op: OpSet, Path: "a", Value: raw(`2`)}}}})
		if err != nil || res.Changed || string(res.Body) != in {
			t.Fatalf("%s: %v %s", in, err, res.Body)
		}
	}
}

func TestValidate(t *testing.T) {
	bad := [][]Op{
		nil,
		{{Op: "explode", Path: "a"}},
		{{Op: OpConvert, Path: "a", Type: "date"}},
		{{Op: OpConvert, Path: "", Type: TypeString}},
		{{Op: OpConvert, Path: "a..b", Type: TypeString}},
		{{Op: OpConvert, Path: "a[0]", Type: TypeString}},
		{{Op: OpSet, Path: "a", Value: raw(`INR`)}},
		{{Op: OpSet, Path: "a"}},
		{{Op: OpSet, Path: "items[]", Value: raw(`1`)}},
		{{Op: OpRename, From: "a", To: "a"}},
		{{Op: OpRename, From: "items[].a", To: "b"}},
		{{Op: OpMap, Path: "s"}},
	}
	for _, ops := range bad {
		if err := Validate(ops); err == nil {
			t.Errorf("expected %+v to be rejected", ops)
		}
	}
	ok := []Op{{Op: OpConvert, Path: "items[]", Type: TypeString}, {Op: OpRename, From: "items[].a", To: "items[].b.c"}}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
}
