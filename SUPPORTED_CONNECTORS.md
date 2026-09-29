# Supported Webhook Connectors

Relaya supports 18+ webhook providers with built-in signature verification, event deduplication, and type extraction.

## Payment Processors & PGs

### Stripe
- **Signature Header**: `Stripe-Signature`
- **Format**: `t=<unix>,v1=<hex HMAC-SHA256(t + "." + body)>`
- **Event ID**: `id` field in body
- **Event Type**: `type` field
- **Setup**: Get webhook signing secret from Stripe Dashboard → Developers → Webhooks

### Razorpay
- **Signature Header**: `X-Razorpay-Signature`
- **Format**: Hex HMAC-SHA256 of body
- **Event ID**: `X-Razorpay-Event-Id` header
- **Event Type**: `event` field
- **Setup**: Get webhook secret from Razorpay Dashboard → Settings → Webhooks

### Shopify
- **Signature Header**: `X-Shopify-Hmac-Sha256`
- **Format**: Base64 HMAC-SHA256 of body
- **Event ID**: `X-Shopify-Event-Id` or `X-Shopify-Webhook-Id` header
- **Event Type**: `X-Shopify-Topic` header
- **Setup**: Get API credentials from Shopify Admin → Settings → API and access scopes

### Cashfree
- **Signature Header**: `X-Webhook-Signature`
- **Format**: Base64 HMAC-SHA256 of `timestamp + body`
- **Timestamp Header**: `X-Webhook-Timestamp` (milliseconds)
- **Event ID**: `X-Idempotency-Key` header
- **Event Type**: `type` field
- **Setup**: Get merchant PG secret key from Cashfree Dashboard → Settings

### PayU
- **Signature Field**: `hash` in body (form-encoded or JSON)
- **Format**: SHA512 hex hash of specific field order
- **Event ID**: `mihpayid` + `:` + `status`
- **Event Type**: `payment.` + lowercase status
- **Setup**: Configure in PayU Dashboard → Settings → Webhooks

### PhonePe
- **Authorization Header**: SHA256 of `username:password`
- **Format**: Hex SHA256
- **Event ID**: `orderId` or `refundId` + `:` + `state`
- **Event Type**: `event` field
- **Setup**: Get credentials from PhonePe Dashboard → Settings

### Square
- **Signature Header**: `X-Square-Hmac-SHA256`
- **Format**: Base64 HMAC-SHA256 of body
- **Event ID**: `data.object.id`
- **Event Type**: `type` field
- **Setup**: Get signing key from Square Dashboard → Developer → Webhooks

### Brex
- **Signature Header**: `X-Brex-Signature`
- **Format**: Hex HMAC-SHA256 of body
- **Event ID**: `id` field
- **Event Type**: `type` field
- **Setup**: Get signing key from Brex Dashboard → Settings → Webhooks

## Developer Platforms

### GitHub
- **Signature Header**: `X-Hub-Signature-256`
- **Format**: `sha256=` + hex HMAC-SHA256 of body
- **Event ID**: `X-GitHub-Delivery` header
- **Event Type**: `X-GitHub-Event` header
- **Setup**: Repository → Settings → Webhooks → Add webhook, copy secret

### Jira Cloud
- **Signature Header**: `X-Hub-Signature`
- **Format**: `sha256=` + hex HMAC-SHA256 of body
- **Event ID**: `X-Atlassian-Webhook-Identifier` header
- **Event Type**: `webhookEvent` field (e.g., `jira:issue_created`)
- **Setup**: Jira Admin → System → Webhooks → Create → Set secret

### Notion
- **Signature Header**: `X-Notion-Signature`
- **Format**: Hex HMAC-SHA256 of `"Notion:" + body`
- **Event ID**: `id` field
- **Event Type**: `type` field
- **Setup**: Notion → Settings → Integrations → Create integration → Copy secret

## Communication & Marketing

### SendGrid (Twilio Email)
- **Signature Header**: `X-Twilio-Email-Event-Webhook-Signature`
- **Format**: Base64 HMAC-SHA256 of `timestamp + body`
- **Timestamp Header**: `X-Twilio-Email-Event-Webhook-Timestamp`
- **Event ID**: `email_to` + `:` + `timestamp` + `:` + `event`
- **Event Type**: `event` field (e.g., `sent`, `bounce`, `click`)
- **Setup**: SendGrid Dashboard → Settings → Mail Send → Event Webhook

### Slack
- **Signature Header**: `X-Slack-Signature`
- **Timestamp Header**: `X-Slack-Request-Timestamp`
- **Format**: `v0=` + hex HMAC-SHA256 of `v0:timestamp:body`
- **Event ID**: `envelope_id` field
- **Event Type**: `type` field
- **Setup**: Slack App → Event Subscriptions → Enable events → Copy signing secret

### HubSpot
- **Signature Header**: `X-HubSpot-Signature`
- **Format**: Hex HMAC-SHA256 of body
- **Event ID**: First attempt `id` in attempts array
- **Event Type**: `subscription.subscriptionType` field
- **Setup**: HubSpot → Settings → Webhooks → Copy API Key

## Communication Platforms

### Twilio
- **Signature Header**: `X-Twilio-Signature`
- **Format**: Base64 HMAC-SHA1 of `URL + sorted form fields`
- **Event ID**: `MessageSid` field
- **Event Type**: `MessageStatus` field
- **Setup**: Twilio Console → Phone Numbers → Configure → Webhook URL

### Segment
- **Authorization Header**: Base64 HMAC-SHA1 (first 20 bytes) of body
- **Event ID**: `messageId` field
- **Event Type**: `Content-Type` header
- **Setup**: Segment → Settings → Workspace Settings → API Tokens

## Generic Webhook Support

### Generic Provider
Supports any webhook using these patterns:

**Signature Verification Options**:
- Header name: `X-Signature` (default) or custom `SignatureHeader` config
- Format: `sha256=` + hex HMAC-SHA256 (optional prefix)

**Event ID Detection** (in order):
1. `Idempotency-Key` header
2. `X-Idempotency-Key` header
3. `X-Event-Id` header
4. `X-Request-Id` header
5. `X-Delivery-Id` header
6. `id` field in JSON body
7. SHA256 hash of body (fallback)

**Event Type Detection** (in order):
1. `X-Event-Type` header
2. `type` field in JSON body
3. `event` field in JSON body
4. `event_type` field in JSON body
5. `topic` field in JSON body

## Standard Webhooks (Svix-compatible)

### Standard Webhooks / Svix
- **Headers**: `webhook-id`, `webhook-timestamp`, `webhook-signature`
- **Fallback**: `svix-id`, `svix-timestamp`, `svix-signature`
- **Format**: Space-separated signatures with version prefix
  - `v1,<base64 HMAC-SHA256>` - HMAC verification
  - `v1a,<base64 Ed25519>` - Ed25519 public key verification
- **Message**: `<id>.<timestamp>.<body>`
- **Secrets**:
  - `whsec_...` - HMAC secret (base64 encoded)
  - `whpk_...` - Ed25519 public key (base64 encoded)
- **Event ID**: `webhook-id` header
- **Event Type**: `type`, `event_type`, or `event` field
- **Setup**: [Standard Webhooks](https://www.standardwebhooks.com/) or [Svix](https://svix.com/) platform

---

## Adding a New Connector

To add a new webhook provider:

1. **Implement the Provider interface** in `/backend/internal/provider/provider.go`:
   ```go
   type myProvider struct{}
   
   func (myProvider) Name() string { return "myprovider" }
   func (myProvider) Verify(r Request, c Config) SignatureResult { /* ... */ }
   func (myProvider) DedupKey(r Request) string { /* ... */ }
   func (myProvider) EventType(r Request) string { /* ... */ }
   ```

2. **Register it** in `init()`:
   ```go
   register(myProvider{})
   ```

3. **Test signature verification** in `/backend/internal/provider/provider_test.go`

4. **Update this documentation** with setup instructions

---

## Signature Verification Flow

```
Webhook Received
    ↓
Look up provider by name
    ↓
Get webhook secret from config
    ↓
Call provider.Verify(request, config)
    ├─ If secret not set → SigNotConfigured
    ├─ If signature header missing → SigMissing
    ├─ If signature invalid → SigInvalid
    └─ If signature valid → SigValid
    ↓
Store SigResult on event
    ↓
Return to client (200 OK regardless)
```

---

## Event Deduplication

Each provider defines how to extract a unique key for the event:

- **Option 1**: Use provider's event/delivery ID (if available in header or body)
- **Option 2**: Use timestamp + event type (if provider sends them)
- **Option 3**: Fall back to SHA256 hash of the entire body

This prevents duplicate processing if a provider retries sending the same webhook.

---

## Configuration

In the dashboard, when creating an integration:

```
Provider: [Dropdown of all 18+ providers]
Name: "My Stripe Webhook"
Secret: [Paste signing secret from provider dashboard]
Webhook URL: [Auto-generated token URL]
```

Relaya automatically:
- ✅ Validates signatures using the selected provider
- ✅ Extracts event IDs for deduplication
- ✅ Extracts event types for filtering
- ✅ Handles retries with exponential backoff
- ✅ Logs all delivery attempts

---

## Common Webhook Patterns

### HMAC-SHA256 (Most Common)
Signature = hex or base64 HMAC-SHA256(secret, body)

**Examples**: Stripe, Razorpay, GitHub, Brex, HubSpot, Square, SendGrid

### HMAC-SHA1
Signature = base64 HMAC-SHA1 of body

**Examples**: Segment, Shopify (older)

### Timestamp + Body
Signature includes request timestamp for replay protection

**Examples**: Stripe, Slack, Cashfree, SendGrid, Standard Webhooks

### Ed25519 Public Key
Uses public key cryptography instead of shared secret

**Examples**: Standard Webhooks (v1a)

### Custom Formats
Each has unique signing scheme

**Examples**: PayU (form-encoded fields), PhonePe (Authorization header)

---

## Testing Webhooks

Use Relaya's webhook testing feature:

1. Create integration
2. Click "Send Test Webhook"
3. Review received payload
4. Check signature verification status
5. Monitor delivery attempt

Or test with curl:
```bash
curl -X POST https://yourdomain.com/v1/in/webhook-token \
  -H "X-Signature: sha256=$(echo -n "body" | openssl dgst -sha256 -hmac "secret" -hex | cut -d' ' -f2)" \
  -H "Content-Type: application/json" \
  -d '{"event": "test"}'
```
