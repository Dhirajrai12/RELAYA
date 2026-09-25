package contract

import (
	"fmt"
	"strings"
	"testing"
)

// payment builds a Razorpay-like payment.captured payload.
func payment(id int, status string, amount string, extra string) []byte {
	return []byte(fmt.Sprintf(`{"event":"payment.captured","payload":{"payment":{"entity":{
		"id":"pay_%d","amount":%s,"currency":"INR","status":%q,"captured":true,
		"notes":[{"k":"order","v":"o_%d"}],"card":null%s}}}}`, id, amount, status, id, extra))
}

func learn(t *testing.T, n int) *Schema {
	t.Helper()
	s := NewSchema()
	statuses := []string{"captured", "authorized", "captured", "failed", "captured"}
	for i := 0; i < n; i++ {
		o, ok := Observe(payment(i, statuses[i%len(statuses)], "50000", ""))
		if !ok {
			t.Fatal("observe failed")
		}
		s.Merge(o)
	}
	return s
}

func check(t *testing.T, s *Schema, critical []string, payload []byte) ([]Finding, Severity) {
	t.Helper()
	o, ok := Observe(payload)
	if !ok {
		t.Fatal("observe failed")
	}
	crit := map[string]bool{}
	for _, c := range critical {
		crit[c] = true
	}
	return Check(s, crit, o)
}

func find(fs []Finding, kind, path string) *Finding {
	for i := range fs {
		if fs[i].Kind == kind && fs[i].Path == path {
			return &fs[i]
		}
	}
	return nil
}

const amount = "payload.payment.entity.amount"
const status = "payload.payment.entity.status"

func TestLearnShape(t *testing.T) {
	s := learn(t, 10)
	if s.Samples != 10 {
		t.Fatalf("samples %d", s.Samples)
	}
	a := s.Fields[amount]
	if a == nil || !s.Required(a) || strings.Join(a.Types, ",") != "integer" {
		t.Fatalf("amount: %+v", a)
	}
	st := s.Fields[status]
	if !st.isEnum() || strings.Join(st.Enum, ",") != "authorized,captured,failed" {
		t.Fatalf("status enum: %+v", st)
	}
	if id := s.Fields["payload.payment.entity.id"]; id.isEnum() {
		t.Fatal("unique ids must not become an enum")
	}
	if s.Fields["payload.payment.entity.notes[].k"] == nil || s.Fields["payload.payment.entity.card"].Types[0] != TNull {
		t.Fatal("arrays/null not learned")
	}
	if s.Fingerprint() != learn(t, 10).Fingerprint() {
		t.Fatal("fingerprint not stable")
	}
}

func TestSameShapeIsOK(t *testing.T) {
	s := learn(t, 10)
	fs, sev := check(t, s, []string{amount}, payment(99, "captured", "120000", ""))
	if sev != OK || len(fs) != 0 {
		t.Fatalf("got %v %+v", sev, fs)
	}
}

func TestClassification(t *testing.T) {
	s := learn(t, 10)
	crit := []string{amount, status}

	// New optional field: compatible.
	fs, sev := check(t, s, crit, payment(1, "captured", "1", `,"utm_source":"x"`))
	if sev != Compatible || find(fs, KindNewField, "payload.payment.entity.utm_source") == nil {
		t.Fatalf("new field: %v %+v", sev, fs)
	}

	// New enum value: suspicious even on a critical field (blueprint rule).
	fs, sev = check(t, s, crit, payment(1, "partially_paid", "1", ""))
	if f := find(fs, KindNewEnumValue, status); sev != Suspicious || f == nil || f.Actual != "partially_paid" {
		t.Fatalf("enum: %v %+v", sev, fs)
	}

	// Integer -> decimal: widened, suspicious.
	fs, sev = check(t, s, crit, payment(1, "captured", "500.5", ""))
	if sev != Suspicious || find(fs, KindTypeWidened, amount) == nil {
		t.Fatalf("widened: %v %+v", sev, fs)
	}

	// Critical field retyped (amount becomes a string): breaking.
	fs, sev = check(t, s, crit, payment(1, "captured", `"500"`, ""))
	if f := find(fs, KindTypeChanged, amount); sev != Breaking || f == nil || f.Expected != "integer" || f.Actual != "string" {
		t.Fatalf("retyped: %v %+v", sev, fs)
	}

	// Same change on a non-critical field: only suspicious.
	fs, sev = check(t, s, nil, payment(1, "captured", `"500"`, ""))
	if sev != Suspicious {
		t.Fatalf("non-critical retype: %v %+v", sev, fs)
	}

	// Critical field null: breaking.
	fs, sev = check(t, s, crit, payment(1, "captured", "null", ""))
	if sev != Breaking || find(fs, KindNullValue, amount) == nil {
		t.Fatalf("null: %v %+v", sev, fs)
	}
}

func TestRenamedCriticalFieldIsBreaking(t *testing.T) {
	s := learn(t, 10)
	// "amount" renamed to "amount_paise": old path missing (breaking), new path compatible.
	renamed := []byte(`{"event":"payment.captured","payload":{"payment":{"entity":{
		"id":"pay_1","amount_paise":50000,"currency":"INR","status":"captured","captured":true,
		"notes":[{"k":"order","v":"o_1"}],"card":null}}}}`)
	fs, sev := check(t, s, []string{amount}, renamed)
	if sev != Breaking || find(fs, KindMissingField, amount) == nil || find(fs, KindNewField, "payload.payment.entity.amount_paise") == nil {
		t.Fatalf("renamed: %v %+v", sev, fs)
	}
}

func TestMissingObjectReportedOnceAndCriticalInside(t *testing.T) {
	s := learn(t, 10)
	// The whole entity object disappears: one finding at the top, breaking because amount is inside.
	fs, sev := check(t, s, []string{amount}, []byte(`{"event":"payment.captured","payload":{"payment":{}}}`))
	f := find(fs, KindMissingField, "payload.payment.entity")
	if sev != Breaking || f == nil || f.Nested < 5 {
		t.Fatalf("missing object: %v %+v", sev, fs)
	}
	if find(fs, KindMissingField, amount) != nil {
		t.Fatal("children of a missing object must not be reported separately")
	}
}

func TestEmptyArrayDoesNotMissChildren(t *testing.T) {
	s := learn(t, 10)
	p := []byte(strings.Replace(string(payment(1, "captured", "1", "")), `[{"k":"order","v":"o_1"}]`, `[]`, 1))
	fs, sev := check(t, s, nil, p)
	if sev != OK {
		t.Fatalf("empty array should be fine: %v %+v", sev, fs)
	}
}

func TestObserveRejectsNonObjects(t *testing.T) {
	for _, in := range []string{`[1,2]`, `"x"`, `not json`, ``} {
		if _, ok := Observe([]byte(in)); ok {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestLimits(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < maxPaths+50; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"k%d":1`, i)
	}
	b.WriteString("}")
	o, _ := Observe([]byte(b.String()))
	if len(o.Paths) > maxPaths || !o.Truncated {
		t.Fatalf("paths %d truncated %v", len(o.Paths), o.Truncated)
	}
}
