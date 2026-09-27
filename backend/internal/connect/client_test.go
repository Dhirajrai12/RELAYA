package connect

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestZohoTokenURLFollowsOnlyZohoDataCentres(t *testing.T) {
	p, _ := Get("zoho")
	for server, want := range map[string]string{
		"":                              "https://accounts.zoho.com/oauth/v2/token",
		"https://accounts.zoho.in":      "https://accounts.zoho.in/oauth/v2/token",
		"https://accounts.zoho.eu":      "https://accounts.zoho.eu/oauth/v2/token",
		"https://accounts.zoho.com.au":  "https://accounts.zoho.com.au/oauth/v2/token",
		"https://accounts.zohocloud.ca": "https://accounts.zohocloud.ca/oauth/v2/token",
	} {
		got, err := p.TokenURLFor(url.Values{"accounts-server": {server}})
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", server, got, err)
		}
	}
	// The callback query comes from the user's browser: anything else could send
	// the code and client secret to an attacker.
	for _, evil := range []string{"https://evil.example", "https://accounts.zoho.in.evil.example", "http://accounts.zoho.in",
		"https://accounts.zoho.in/", "https://accounts.zoho.in@evil.example"} {
		if _, err := p.TokenURLFor(url.Values{"accounts-server": {evil}}); err == nil {
			t.Errorf("%q accepted", evil)
		}
	}
	if _, ok := p.APIBaseFor("https://www.zohoapis.in"); !ok {
		t.Error("zohoapis.in rejected")
	}
	if _, ok := p.APIBaseFor("https://www.zohoapis.in.evil.example"); ok {
		t.Error("fake api domain accepted")
	}
}

func TestAuthorizeURL(t *testing.T) {
	google, _ := Get("google")
	state, verifier := NewState(google)
	u, _ := url.Parse(AuthorizeURL(google, "cid", "https://relaya.example/cb", []string{"openid", "email"}, state, verifier))
	q := u.Query()
	sum := sha256.Sum256([]byte(verifier))
	if q.Get("client_id") != "cid" || q.Get("redirect_uri") != "https://relaya.example/cb" || q.Get("state") != state ||
		q.Get("scope") != "openid email" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" ||
		q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) || q.Get("code_challenge_method") != "S256" {
		t.Errorf("google authorize URL: %s", u)
	}
	zoho, _ := Get("zoho")
	state, verifier = NewState(zoho)
	if verifier != "" {
		t.Error("zoho has no PKCE")
	}
	u, _ = url.Parse(AuthorizeURL(zoho, "cid", "https://relaya.example/cb", []string{"ZohoCRM.modules.ALL", "ZohoCRM.settings.READ"}, state, ""))
	if u.Query().Get("scope") != "ZohoCRM.modules.ALL,ZohoCRM.settings.READ" || u.Query().Get("code_challenge") != "" {
		t.Errorf("zoho authorize URL: %s", u)
	}
}

// tokenServer answers token requests with a fixed status and body.
func tokenServer(t *testing.T, status int, body string, check func(url.Values)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if check != nil {
			check(r.PostForm)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestTokenResponses(t *testing.T) {
	ctx := context.Background()
	c := NewClient()
	app := App{ClientID: "cid", ClientSecret: "sec"}

	// Zoho: success with api_domain; the refresh token is kept when not re-sent.
	zoho := *mustGet(t, "zoho")
	zoho.TokenURL = tokenServer(t, 200, `{"access_token":"at2","api_domain":"https://www.zohoapis.in","token_type":"Bearer","expires_in":3600}`,
		func(f url.Values) {
			if f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != "rt1" || f.Get("client_secret") != "sec" {
				t.Errorf("refresh form: %v", f)
			}
		})
	cred, meta, err := c.Refresh(ctx, &zoho, app, Credentials{AccessToken: "at1", RefreshToken: "rt1"})
	if err != nil || cred.AccessToken != "at2" || cred.RefreshToken != "rt1" || cred.ExpiresAt == nil || meta["api_base"] != "https://www.zohoapis.in" {
		t.Fatalf("zoho refresh: %+v %v %v", cred, meta, err)
	}

	cases := []struct {
		name      string
		status    int
		body      string
		permanent bool
	}{
		{"zoho error with HTTP 200", 200, `{"error":"invalid_code"}`, true},
		{"zoho rate limit", 200, `{"error":"Access Denied","error_description":"You have made too many requests continuously"}`, false},
		{"oauth invalid_grant", 400, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`, true},
		{"hubspot bad refresh token", 400, `{"status":"BAD_REFRESH_TOKEN","message":"missing or unknown refresh token"}`, true},
		{"provider down", 503, `<html>unavailable</html>`, false},
		{"rate limited", 429, `{"error":"rate_limited"}`, false},
		{"no token in response", 200, `{"token_type":"Bearer"}`, false},
	}
	for _, tc := range cases {
		p := *mustGet(t, "hubspot")
		p.TokenURL = tokenServer(t, tc.status, tc.body, nil)
		_, _, err := c.Refresh(ctx, &p, app, Credentials{RefreshToken: "rt1"})
		var ce *Error
		if !errors.As(err, &ce) || ce.Permanent != tc.permanent {
			t.Errorf("%s: err %v, want permanent=%v", tc.name, err, tc.permanent)
		}
		if err != nil && strings.Contains(err.Error(), "rt1") {
			t.Errorf("%s: error leaks the refresh token", tc.name)
		}
	}

	// Unreachable provider: temporary.
	p := *mustGet(t, "hubspot")
	p.TokenURL = "http://127.0.0.1:1/token"
	if _, _, err := c.Refresh(ctx, &p, app, Credentials{RefreshToken: "rt1"}); err == nil || err.(*Error).Permanent {
		t.Errorf("unreachable: %v", err)
	}
	// No refresh token: permanent without calling anyone.
	if _, _, err := c.Refresh(ctx, &p, app, Credentials{AccessToken: "x"}); err == nil || !err.(*Error).Permanent {
		t.Errorf("no refresh token: %v", err)
	}
}

func TestExchangeUsesZohoDataCentreAndWarnsWithoutRefreshToken(t *testing.T) {
	var got url.Values
	srv := tokenServer(t, 200, `{"access_token":"at1","expires_in":"3600"}`, func(f url.Values) { got = f })
	zoho := *mustGet(t, "zoho")
	zoho.TokenURLFor = func(url.Values) (string, error) { return srv, nil }
	cred, meta, err := NewClient().Exchange(context.Background(), &zoho, App{ClientID: "cid", ClientSecret: "sec"}, "code1", "https://r/cb", "ver", url.Values{})
	if err != nil || cred.AccessToken != "at1" || cred.TokenURL != srv || cred.ExpiresAt == nil {
		t.Fatalf("exchange: %+v %v", cred, err)
	}
	if got.Get("code") != "code1" || got.Get("redirect_uri") != "https://r/cb" || got.Get("code_verifier") != "ver" || got.Get("grant_type") != "authorization_code" {
		t.Errorf("exchange form: %v", got)
	}
	if meta["warning"] == nil {
		t.Error("no warning for a missing refresh token")
	}
}

func TestLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(readAll(r), `"password":"right"`) {
			_, _ = w.Write([]byte(`{"token":"srtoken","company_id":1}`))
			return
		}
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"message":"Invalid email and password combination","status_code":400}`))
	}))
	defer srv.Close()
	p := *mustGet(t, "shiprocket")
	p.LoginURL = srv.URL
	c := NewClient()
	cred, err := c.Login(context.Background(), &p, "api@shop.in", "right")
	if err != nil || cred.AccessToken != "srtoken" || cred.ExpiresAt == nil || cred.Password != "right" {
		t.Fatalf("login: %+v %v", cred, err)
	}
	// Refresh logs in again with the stored login.
	fresh, _, err := c.Refresh(context.Background(), &p, App{}, cred)
	if err != nil || fresh.AccessToken != "srtoken" {
		t.Fatalf("refresh by login: %v", err)
	}
	_, err = c.Login(context.Background(), &p, "api@shop.in", "wrong")
	var ce *Error
	if !errors.As(err, &ce) || !ce.Permanent || !strings.Contains(ce.Message, "Invalid email") {
		t.Errorf("wrong password: %v", err)
	}
}

func mustGet(t *testing.T, key string) *Provider {
	t.Helper()
	p, ok := Get(key)
	if !ok {
		t.Fatalf("no provider %s", key)
	}
	return p
}

func readAll(r *http.Request) string {
	b := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.String()
		}
	}
}
