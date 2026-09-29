package provider

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Signing is the other half of Verify: it builds a request the way each provider
// sends it, so the event simulator can test a webhook end to end with its own
// secret. Every provider's Sign must produce a request its Verify accepts.

// Outgoing is a request as a provider would send it.
type Outgoing struct {
	Header http.Header
	Body   []byte
}

// ErrCannotSign means Relaya can't sign for this provider with the stored secret
// (a Standard Webhooks whpk_ public key: signing needs the sender's private key).
var ErrCannotSign = errors.New("this webhook's secret is a public key; only the sender can sign for it")

// Sign returns body as provider name would send it at now. eventType goes where
// the provider puts it outside the body (Shopify, GitHub); deliveryID is the
// provider's delivery ID for header-based deduplication. Without a secret the
// request is built unsigned, as for a webhook that doesn't check signatures.
func Sign(name string, body []byte, c Config, now time.Time, eventType, deliveryID string) (Outgoing, error) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	secret := c.Secret
	signed := len(secret) > 0
	hexMAC := func() string { return hex.EncodeToString(hmacSHA256(secret, body)) }

	switch name {
	case "razorpay":
		h.Set("X-Razorpay-Event-Id", deliveryID)
		if signed {
			h.Set("X-Razorpay-Signature", hexMAC())
		}
	case "stripe":
		if signed {
			ts := strconv.FormatInt(now.Unix(), 10)
			h.Set("Stripe-Signature", "t="+ts+",v1="+hex.EncodeToString(hmacSHA256(secret, append([]byte(ts+"."), body...))))
		}
	case "shopify":
		h.Set("X-Shopify-Topic", eventType)
		h.Set("X-Shopify-Event-Id", deliveryID)
		h.Set("X-Shopify-Webhook-Id", deliveryID)
		h.Set("X-Shopify-Triggered-At", now.UTC().Format(time.RFC3339Nano))
		if signed {
			h.Set("X-Shopify-Hmac-Sha256", base64.StdEncoding.EncodeToString(hmacSHA256(secret, body)))
		}
	case "github":
		h.Set("X-GitHub-Event", eventType)
		h.Set("X-GitHub-Delivery", deliveryID)
		if signed {
			h.Set("X-Hub-Signature-256", "sha256="+hexMAC())
		}
	case "jira":
		h.Set("X-Atlassian-Webhook-Identifier", deliveryID)
		if signed {
			h.Set("X-Hub-Signature", "sha256="+hexMAC())
		}
	case "standardwebhooks":
		ts := strconv.FormatInt(now.Unix(), 10)
		h.Set("webhook-id", deliveryID)
		h.Set("webhook-timestamp", ts)
		if signed {
			s := strings.TrimSpace(string(secret))
			if strings.HasPrefix(s, "whpk_") {
				return Outgoing{}, ErrCannotSign
			}
			key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, "whsec_"))
			if err != nil {
				key = secret
			}
			mac := hmacSHA256(key, append([]byte(deliveryID+"."+ts+"."), body...))
			h.Set("webhook-signature", "v1,"+base64.StdEncoding.EncodeToString(mac))
		}
	case "cashfree":
		ts := strconv.FormatInt(now.UnixMilli(), 10)
		h.Set("X-Webhook-Timestamp", ts)
		h.Set("X-Idempotency-Key", deliveryID)
		h.Set("X-Webhook-Version", "2025-01-01")
		if signed {
			h.Set("X-Webhook-Signature", base64.StdEncoding.EncodeToString(hmacSHA256(secret, append([]byte(ts), body...))))
		}
	case "payu":
		// PayU posts a form and signs it with a hash field inside the body.
		h.Set("Content-Type", "application/x-www-form-urlencoded")
		q, err := url.ParseQuery(string(body))
		if err != nil {
			return Outgoing{}, errors.New("a PayU payload is form-encoded: key=value&…")
		}
		q.Del("hash")
		if signed {
			parts := []string{strings.TrimSpace(string(secret)), q.Get("status")}
			for i := 10; i >= 1; i-- {
				parts = append(parts, q.Get("udf"+strconv.Itoa(i)))
			}
			parts = append(parts, q.Get("email"), q.Get("firstname"), q.Get("productinfo"), q.Get("amount"), q.Get("txnid"), q.Get("key"))
			s := strings.Join(parts, "|")
			if ac := q.Get("additional_charges"); ac != "" {
				s = ac + "|" + s
			}
			sum := sha512.Sum512([]byte(s))
			q.Set("hash", hex.EncodeToString(sum[:]))
		}
		body = []byte(q.Encode())
	case "phonepe":
		if signed {
			sum := sha256.Sum256([]byte(strings.TrimSpace(string(secret))))
			h.Set("Authorization", "SHA256 "+hex.EncodeToString(sum[:]))
		}
	default: // generic
		h.Set("X-Event-Id", deliveryID)
		if eventType != "" {
			h.Set("X-Event-Type", eventType)
		}
		if signed {
			header := c.SignatureHeader
			if header == "" {
				header = "X-Signature"
			}
			h.Set(header, hexMAC())
		}
	}
	return Outgoing{Header: h, Body: body}, nil
}
