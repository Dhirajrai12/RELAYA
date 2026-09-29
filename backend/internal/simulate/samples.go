// Package simulate builds sample webhook events, signs them the way each
// provider does with a webhook's own secret, and runs them through ingest, so
// developers can test their integration without a real payment or order.
package simulate

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

// Sample is one ready-to-send event for a provider.
type Sample struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	// Payload is the body to send: JSON, or form-encoded for PayU.
	Payload string `json:"payload"`
}

type template struct {
	typ, desc string
	build     func(now time.Time) any // JSON value; a url.Values for form bodies
}

func id(prefix string, n int) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[k.Int64()]
	}
	return prefix + string(b)
}

func digits(n int) string {
	b := make([]byte, n)
	for i := range b {
		k, _ := rand.Int(rand.Reader, big.NewInt(10))
		b[i] = byte('1' + k.Int64()%9)
	}
	return string(b)
}

func razorpayPayment(now time.Time, status string) map[string]any {
	p := map[string]any{
		"id": id("pay_", 14), "entity": "payment", "amount": 49900, "currency": "INR", "status": status,
		"order_id": id("order_", 14), "method": "upi", "vpa": "customer@okaxis", "email": "customer@example.com",
		"contact": "+919876543210", "captured": status == "captured", "created_at": now.Unix(),
		"notes": map[string]string{"plan": "pro-monthly"},
	}
	if status == "failed" {
		p["error_code"], p["error_description"], p["error_reason"] = "BAD_REQUEST_ERROR", "Payment was cancelled by the customer.", "payment_cancelled"
	}
	return p
}

var templates = map[string][]template{
	"razorpay": {
		{"payment.captured", "A payment was captured", func(now time.Time) any {
			return map[string]any{"entity": "event", "account_id": "acc_" + digits(14), "event": "payment.captured", "contains": []string{"payment"},
				"payload": map[string]any{"payment": map[string]any{"entity": razorpayPayment(now, "captured")}}, "created_at": now.Unix()}
		}},
		{"payment.failed", "A payment failed", func(now time.Time) any {
			return map[string]any{"entity": "event", "account_id": "acc_" + digits(14), "event": "payment.failed", "contains": []string{"payment"},
				"payload": map[string]any{"payment": map[string]any{"entity": razorpayPayment(now, "failed")}}, "created_at": now.Unix()}
		}},
		{"refund.processed", "A refund was processed", func(now time.Time) any {
			return map[string]any{"entity": "event", "account_id": "acc_" + digits(14), "event": "refund.processed", "contains": []string{"refund", "payment"},
				"payload": map[string]any{"refund": map[string]any{"entity": map[string]any{"id": id("rfnd_", 14), "entity": "refund", "amount": 49900,
					"currency": "INR", "payment_id": id("pay_", 14), "status": "processed", "speed_processed": "normal", "created_at": now.Unix()}}},
				"created_at": now.Unix()}
		}},
		{"order.paid", "An order was paid in full", func(now time.Time) any {
			return map[string]any{"entity": "event", "account_id": "acc_" + digits(14), "event": "order.paid", "contains": []string{"payment", "order"},
				"payload": map[string]any{
					"payment": map[string]any{"entity": razorpayPayment(now, "captured")},
					"order": map[string]any{"entity": map[string]any{"id": id("order_", 14), "entity": "order", "amount": 49900, "amount_paid": 49900,
						"amount_due": 0, "currency": "INR", "receipt": "rcpt_" + digits(6), "status": "paid", "attempts": 1, "created_at": now.Unix()}},
				}, "created_at": now.Unix()}
		}},
	},
	"stripe": {
		{"payment_intent.succeeded", "A payment succeeded", func(now time.Time) any {
			return stripeEvent(now, "payment_intent.succeeded", map[string]any{"id": id("pi_", 24), "object": "payment_intent", "amount": 2000,
				"amount_received": 2000, "currency": "usd", "status": "succeeded", "customer": id("cus_", 14), "payment_method": id("pm_", 24),
				"metadata": map[string]string{"order_id": "ord_" + digits(6)}, "created": now.Unix()})
		}},
		{"charge.refunded", "A charge was refunded", func(now time.Time) any {
			return stripeEvent(now, "charge.refunded", map[string]any{"id": id("ch_", 24), "object": "charge", "amount": 2000, "amount_refunded": 2000,
				"currency": "usd", "refunded": true, "status": "succeeded", "payment_intent": id("pi_", 24), "created": now.Unix()})
		}},
		{"checkout.session.completed", "A Checkout session was completed", func(now time.Time) any {
			return stripeEvent(now, "checkout.session.completed", map[string]any{"id": id("cs_test_", 24), "object": "checkout.session",
				"amount_total": 4900, "currency": "usd", "customer_details": map[string]string{"email": "customer@example.com", "name": "Asha Rao"},
				"mode": "subscription", "payment_status": "paid", "status": "complete", "subscription": id("sub_", 14)})
		}},
		{"invoice.paid", "A subscription invoice was paid", func(now time.Time) any {
			return stripeEvent(now, "invoice.paid", map[string]any{"id": id("in_", 24), "object": "invoice", "amount_paid": 4900, "currency": "usd",
				"customer": id("cus_", 14), "subscription": id("sub_", 14), "status": "paid", "billing_reason": "subscription_cycle"})
		}},
	},
	"shopify": {
		{"orders/create", "An order was placed", func(now time.Time) any { return shopifyOrder(now, "pending") }},
		{"orders/paid", "An order was paid", func(now time.Time) any { return shopifyOrder(now, "paid") }},
		{"refunds/create", "A refund was created", func(now time.Time) any {
			return map[string]any{"id": digits(13), "order_id": digits(13), "created_at": now.Format(time.RFC3339), "note": "Damaged in transit",
				"refund_line_items": []any{map[string]any{"line_item_id": digits(13), "quantity": 1, "subtotal": "1499.00"}},
				"transactions":      []any{map[string]any{"id": digits(13), "kind": "refund", "status": "success", "amount": "1499.00", "currency": "INR"}}}
		}},
	},
	"github": {
		{"push", "Commits were pushed", func(now time.Time) any {
			sha := strings.ToLower(id("", 40))
			return map[string]any{"ref": "refs/heads/main", "before": strings.ToLower(id("", 40)), "after": sha,
				"repository": map[string]any{"id": digits(9), "full_name": "acme/checkout", "private": true},
				"pusher":     map[string]string{"name": "asha", "email": "asha@example.com"},
				"head_commit": map[string]any{"id": sha, "message": "Fix rounding in totals", "timestamp": now.Format(time.RFC3339),
					"author": map[string]string{"name": "Asha Rao", "email": "asha@example.com"}}}
		}},
		{"pull_request", "A pull request was opened", func(now time.Time) any {
			return map[string]any{"action": "opened", "number": 42, "pull_request": map[string]any{"id": digits(10), "number": 42,
				"title": "Retry failed payouts", "state": "open", "user": map[string]string{"login": "asha"}, "created_at": now.Format(time.RFC3339),
				"head": map[string]string{"ref": "retry-payouts"}, "base": map[string]string{"ref": "main"}},
				"repository": map[string]any{"id": digits(9), "full_name": "acme/checkout"}}
		}},
		{"issues", "An issue was opened", func(now time.Time) any {
			return map[string]any{"action": "opened", "issue": map[string]any{"id": digits(10), "number": 7, "title": "Checkout times out on UPI",
				"state": "open", "user": map[string]string{"login": "asha"}, "created_at": now.Format(time.RFC3339)},
				"repository": map[string]any{"id": digits(9), "full_name": "acme/checkout"}}
		}},
	},
	"jira": {
		{"jira:issue_created", "An issue was created", func(now time.Time) any { return jiraIssue(now, "jira:issue_created", "issue_created") }},
		{"jira:issue_updated", "An issue was updated", func(now time.Time) any { return jiraIssue(now, "jira:issue_updated", "issue_generic") }},
		{"comment_created", "A comment was added", func(now time.Time) any {
			m := jiraIssue(now, "comment_created", "")
			m["comment"] = map[string]any{"id": digits(5), "body": "Reproduced on Android as well.", "created": now.Format("2006-01-02T15:04:05.000-0700"),
				"author": map[string]string{"displayName": "Asha Rao"}}
			return m
		}},
	},
	"cashfree": {
		{"PAYMENT_SUCCESS_WEBHOOK", "A payment succeeded", func(now time.Time) any { return cashfreePayment(now, "SUCCESS") }},
		{"PAYMENT_FAILED_WEBHOOK", "A payment failed", func(now time.Time) any { return cashfreePayment(now, "FAILED") }},
		{"REFUND_STATUS_WEBHOOK", "A refund changed status", func(now time.Time) any {
			return map[string]any{"type": "REFUND_STATUS_WEBHOOK", "event_time": now.Format(time.RFC3339), "data": map[string]any{"refund": map[string]any{
				"cf_refund_id": digits(8), "refund_id": "refund_" + digits(6), "order_id": "order_" + digits(8), "refund_amount": 499.0,
				"refund_currency": "INR", "refund_status": "SUCCESS", "refund_speed": map[string]string{"processed": "STANDARD"}}}}
		}},
	},
	"payu": {
		{"payment.success", "A payment succeeded", func(now time.Time) any { return payuForm("success") }},
		{"payment.failure", "A payment failed", func(now time.Time) any { return payuForm("failure") }},
	},
	"phonepe": {
		{"checkout.order.completed", "An order was paid", func(now time.Time) any { return phonepeOrder(now, "checkout.order.completed", "COMPLETED") }},
		{"checkout.order.failed", "An order failed", func(now time.Time) any { return phonepeOrder(now, "checkout.order.failed", "FAILED") }},
		{"pg.refund.completed", "A refund completed", func(now time.Time) any {
			return map[string]any{"event": "pg.refund.completed", "payload": map[string]any{"refundId": id("OMR", 18), "originalMerchantOrderId": "order_" + digits(8),
				"amount": 49900, "state": "COMPLETED", "timestamp": now.UnixMilli()}}
		}},
	},
	"standardwebhooks": {
		{"invoice.paid", "An invoice was paid", func(now time.Time) any {
			return map[string]any{"type": "invoice.paid", "timestamp": now.Format(time.RFC3339),
				"data": map[string]any{"invoice_id": id("in_", 14), "amount": 4900, "currency": "usd", "customer": "cus_" + digits(8)}}
		}},
		{"user.created", "A user signed up", func(now time.Time) any {
			return map[string]any{"type": "user.created", "timestamp": now.Format(time.RFC3339),
				"data": map[string]any{"id": id("user_", 16), "email": "asha@example.com", "name": "Asha Rao"}}
		}},
	},
	"generic": {
		{"order.created", "An order was created", func(now time.Time) any {
			return map[string]any{"id": id("evt_", 16), "type": "order.created", "created_at": now.Format(time.RFC3339),
				"data": map[string]any{"order_id": "ord_" + digits(6), "amount": 1499.00, "currency": "INR", "customer": map[string]string{"email": "asha@example.com"}}}
		}},
		{"order.cancelled", "An order was cancelled", func(now time.Time) any {
			return map[string]any{"id": id("evt_", 16), "type": "order.cancelled", "created_at": now.Format(time.RFC3339),
				"data": map[string]any{"order_id": "ord_" + digits(6), "reason": "customer_request"}}
		}},
	},
}

func stripeEvent(now time.Time, typ string, obj map[string]any) map[string]any {
	return map[string]any{"id": id("evt_", 24), "object": "event", "api_version": "2025-09-30", "created": now.Unix(), "type": typ,
		"livemode": false, "pending_webhooks": 1, "data": map[string]any{"object": obj}}
}

func shopifyOrder(now time.Time, financial string) map[string]any {
	return map[string]any{"id": digits(13), "name": "#" + digits(4), "email": "customer@example.com", "created_at": now.Format(time.RFC3339),
		"currency": "INR", "total_price": "1499.00", "subtotal_price": "1499.00", "financial_status": financial, "fulfillment_status": nil,
		"line_items": []any{map[string]any{"id": digits(13), "title": "Cotton T-shirt", "quantity": 1, "price": "1499.00", "sku": "TS-BLK-M"}},
		"customer":   map[string]any{"id": digits(13), "first_name": "Asha", "last_name": "Rao"}}
}

func jiraIssue(now time.Time, event, typeName string) map[string]any {
	n := digits(3)
	m := map[string]any{"timestamp": now.UnixMilli(), "webhookEvent": event,
		"user": map[string]string{"accountId": id("", 24), "displayName": "Asha Rao"},
		"issue": map[string]any{"id": digits(5), "key": "OPS-" + n, "fields": map[string]any{
			"summary": "Checkout times out on UPI", "status": map[string]string{"name": "To Do"}, "issuetype": map[string]string{"name": "Bug"},
			"priority": map[string]string{"name": "High"}, "project": map[string]string{"key": "OPS", "name": "Operations"},
			"created": now.Format("2006-01-02T15:04:05.000-0700"), "updated": now.Format("2006-01-02T15:04:05.000-0700")}}}
	if typeName != "" {
		m["issue_event_type_name"] = typeName
	}
	return m
}

func cashfreePayment(now time.Time, status string) map[string]any {
	return map[string]any{"type": map[string]string{"SUCCESS": "PAYMENT_SUCCESS_WEBHOOK", "FAILED": "PAYMENT_FAILED_WEBHOOK"}[status],
		"event_time": now.Format(time.RFC3339), "data": map[string]any{
			"order":            map[string]any{"order_id": "order_" + digits(8), "order_amount": 499.0, "order_currency": "INR"},
			"payment":          map[string]any{"cf_payment_id": digits(10), "payment_status": status, "payment_amount": 499.0, "payment_currency": "INR", "payment_group": "upi"},
			"customer_details": map[string]string{"customer_email": "customer@example.com", "customer_phone": "9876543210"}}}
}

func payuForm(status string) url.Values {
	return url.Values{"mihpayid": {digits(12)}, "status": {status}, "txnid": {"txn" + digits(8)}, "amount": {"499.00"},
		"productinfo": {"Pro plan"}, "firstname": {"Asha"}, "email": {"asha@example.com"}, "phone": {"9876543210"},
		"key": {"merchantKey"}, "mode": {"UPI"}, "bank_ref_num": {digits(12)}}
}

func phonepeOrder(now time.Time, event, state string) map[string]any {
	return map[string]any{"event": event, "payload": map[string]any{"orderId": id("OMO", 18), "merchantId": "MERCHANTUAT",
		"merchantOrderId": "order_" + digits(8), "state": state, "amount": 49900, "expireAt": now.Add(20 * time.Minute).UnixMilli(),
		"paymentDetails": []any{map[string]any{"paymentMode": "UPI_QR", "transactionId": id("OM", 20), "timestamp": now.UnixMilli(), "amount": 49900, "state": state}}}}
}

// Samples returns fresh samples for a provider (with new IDs each call).
func Samples(provider string, now time.Time) []Sample {
	ts, ok := templates[provider]
	if !ok {
		ts = templates["generic"]
	}
	out := make([]Sample, 0, len(ts))
	for _, t := range ts {
		var payload string
		switch v := t.build(now).(type) {
		case url.Values:
			payload = v.Encode()
		default:
			b, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				panic(fmt.Sprintf("sample %s: %v", t.typ, err))
			}
			payload = string(b)
		}
		out = append(out, Sample{Type: t.typ, Description: t.desc, Payload: payload})
	}
	return out
}
