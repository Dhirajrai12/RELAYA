package mask

import (
	"net/http"
	"strings"
	"testing"
)

func TestJSONRedactsNestedSensitiveKeys(t *testing.T) {
	in := []byte(`{"id":"pay_1","amount":50000,"card":{"card_number":"4111","cvv":"123","last4":"1111"},
		"notes":[{"access_token":"abc"}],"customer":{"pan":"ABCDE1234F","panel":"x"}}`)
	out := string(JSON(in))

	for _, leaked := range []string{"4111", `"123"`, "abc", "ABCDE1234F"} {
		if strings.Contains(out, leaked) {
			t.Errorf("leaked %s in %s", leaked, out)
		}
	}
	for _, kept := range []string{"pay_1", "50000", "1111", `"panel":"x"`} {
		if !strings.Contains(out, kept) {
			t.Errorf("lost %s in %s", kept, out)
		}
	}
}

func TestJSONLeavesNonJSONAlone(t *testing.T) {
	in := []byte("a=1&password=2")
	if string(JSON(in)) != string(in) {
		t.Fatal("non-JSON body changed")
	}
}

func TestHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer x")
	h.Set("Cookie", "s=1")
	h.Set("X-Api-Key", "k")
	h.Set("X-Razorpay-Signature", "sig")
	h.Set("Content-Type", "application/json")

	out := Headers(h)
	if _, ok := out["authorization"]; ok {
		t.Error("authorization stored")
	}
	if _, ok := out["cookie"]; ok {
		t.Error("cookie stored")
	}
	if out["x-api-key"] != Redacted {
		t.Errorf("x-api-key = %q", out["x-api-key"])
	}
	if out["x-razorpay-signature"] != "sig" {
		t.Error("signature header should be kept as evidence")
	}
	if out["content-type"] != "application/json" {
		t.Error("content-type lost")
	}
}
