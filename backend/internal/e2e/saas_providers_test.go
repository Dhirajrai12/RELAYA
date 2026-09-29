package e2e

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"testing"
	"time"
)

// Slack's URL check and Twilio's URL-signed requests, through real ingest.
func TestSaaSProvidersThroughIngest(t *testing.T) {
	e := setup(t)
	s := e.call("POST", "/v1/auth/signup", "", map[string]any{"email": "saas@example.com", "password": "correct-horse-6", "org_name": "SaaS in"}, 201)
	tok := s["token"].(string)
	orgID := e.call("GET", "/v1/me", tok, nil, 200)["orgs"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/orgs/" + orgID
	proj := e.call("POST", base+"/projects", tok, map[string]any{"name": "P"}, 201)["id"].(string)
	post := func(u string, body []byte, h map[string]string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("POST", u, bytes.NewReader(body))
		for k, v := range h {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	// ---- Slack: the signed url_verification is answered, and stores nothing ----
	sl := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "Slack", "provider": "slack", "signing_secret": "slack-secret"}, 201)
	slackSign := func(body string) map[string]string {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		m := hmac.New(sha256.New, []byte("slack-secret"))
		m.Write([]byte("v0:" + ts + ":" + body))
		return map[string]string{"Content-Type": "application/json", "X-Slack-Request-Timestamp": ts, "X-Slack-Signature": "v0=" + hex.EncodeToString(m.Sum(nil))}
	}
	check := `{"token":"x","challenge":"abc123challenge","type":"url_verification"}`
	if st, body := post(sl["ingest_url"].(string), []byte(check), slackSign(check)); st != 200 || body != `{"challenge":"abc123challenge"}` {
		t.Fatalf("url_verification: %d %s", st, body)
	}
	unsigned := map[string]string{"Content-Type": "application/json"}
	if st, _ := post(sl["ingest_url"].(string), []byte(check), unsigned); st != 401 {
		t.Fatalf("unsigned url_verification: %d", st)
	}
	ev := `{"type":"event_callback","event_id":"Ev01","event":{"type":"app_mention","text":"hi"}}`
	if st, _ := post(sl["ingest_url"].(string), []byte(ev), slackSign(ev)); st != 200 {
		t.Fatalf("event: %d", st)
	}
	evs := e.call("GET", base+"/events?webhook_id="+sl["id"].(string)+"&status=received", tok, nil, 200)["data"].([]any)
	if len(evs) != 1 || evs[0].(map[string]any)["type"] != "app_mention" {
		t.Fatalf("slack events: %v", evs)
	}

	// ---- Twilio: signed over the URL Twilio was given ----
	tw := e.call("POST", base+"/webhooks", tok, map[string]any{"project_id": proj, "name": "SMS", "provider": "twilio", "signing_secret": "twilio-token"}, 201)
	u := tw["ingest_url"].(string) + "?region=in"
	form := url.Values{"MessageSid": {"SM1"}, "MessageStatus": {"delivered"}, "To": {"+919876543210"}}
	twSig := func(signedURL string) string {
		keys := make([]string, 0, len(form))
		for k := range form {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		str := signedURL
		for _, k := range keys {
			str += k + form.Get(k)
		}
		m := hmac.New(sha1.New, []byte("twilio-token"))
		m.Write([]byte(str))
		return base64.StdEncoding.EncodeToString(m.Sum(nil))
	}
	h := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "X-Twilio-Signature": twSig(u)}
	if st, body := post(u, []byte(form.Encode()), h); st != 200 {
		t.Fatalf("twilio: %d %s", st, body)
	}
	h["X-Twilio-Signature"] = twSig("https://evil.example/v1/in/x")
	if st, _ := post(u, []byte(form.Encode()), h); st != 401 {
		t.Fatalf("twilio signed for another URL: %d", st)
	}
	tev := e.call("GET", base+"/events?webhook_id="+tw["id"].(string)+"&status=received", tok, nil, 200)["data"].([]any)
	if len(tev) != 1 || tev[0].(map[string]any)["type"] != "message.delivered" {
		t.Fatalf("twilio events: %v", tev)
	}
}
