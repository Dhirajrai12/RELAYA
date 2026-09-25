package delivery

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader carries Relaya's signature on every forwarded request:
//
//	Relaya-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256(secret, "<t>.<body>")>
//
// Receivers recompute the HMAC over the raw body and compare, and reject
// timestamps older than a few minutes to stop replays.
const SignatureHeader = "Relaya-Signature"

const secretPrefix = "rsec_"

// NewSecret returns a random signing secret for a destination.
func NewSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return secretPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// Sign returns the Relaya-Signature header value for body at time t.
func Sign(secret []byte, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return fmt.Sprintf("t=%s,v1=%s", ts, hex.EncodeToString(m.Sum(nil)))
}

// Verify checks a Relaya-Signature header. Exported for tests and as the
// reference implementation for customers.
func Verify(secret []byte, header string, body []byte, now time.Time, tolerance time.Duration) bool {
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if d := now.Sub(time.Unix(unix, 0)); d > tolerance || d < -tolerance {
		return false
	}
	want := Sign(secret, time.Unix(unix, 0), body)
	return hmac.Equal([]byte(want), []byte("t="+ts+",v1="+sig))
}
