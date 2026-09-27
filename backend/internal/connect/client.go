package connect

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// App is an organization's OAuth client at a provider.
type App struct {
	ClientID     string
	ClientSecret string
}

// Credentials are what a connection stores, encrypted.
type Credentials struct {
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token,omitempty"`
	TokenType    string     `json:"token_type,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	// Where refreshes go when it isn't the provider's default (Zoho data centres).
	TokenURL string `json:"token_url,omitempty"`
	// Login providers: the API login, to get a new token when the old one expires.
	Email    string `json:"email,omitempty"`
	Password string `json:"password,omitempty"`
}

// Metadata are non-secret facts about a connection, stored in the clear.
type Metadata map[string]any

// Error is a failed exchange, refresh or login. Permanent errors (revoked
// access, wrong password, bad client) need the user to connect again;
// others (timeouts, 5xx, rate limits) are retried.
type Error struct {
	Permanent bool
	Status    int
	Code      string
	Message   string
}

func (e *Error) Error() string {
	s := e.Message
	if e.Code != "" {
		s = e.Code + ": " + s
	}
	if e.Status != 0 {
		s = fmt.Sprintf("HTTP %d %s", e.Status, s)
	}
	return strings.TrimSpace(s)
}

// OAuth error codes that mean the grant or client is no good any more.
var permanentCodes = map[string]bool{
	"invalid_grant": true, "invalid_client": true, "unauthorized_client": true, "invalid_code": true,
	"invalid_client_secret": true, "access_denied": true, "invalid_scope": true,
	"BAD_REFRESH_TOKEN": true, "BAD_AUTH_CODE": true, "BAD_CLIENT_ID": true, "BAD_REDIRECT_URI": true, // HubSpot
}

// Client talks to providers' token and login endpoints.
type Client struct {
	HTTP *http.Client
}

func NewClient() *Client { return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}} }

// NewState returns a random OAuth state and, for PKCE providers, a code verifier.
func NewState(p *Provider) (state, verifier string) {
	state = randomString(24)
	if p.PKCE {
		verifier = randomString(48)
	}
	return state, verifier
}

// AuthorizeURL is where the user approves access at the provider.
func AuthorizeURL(p *Provider, clientID, redirectURI string, scopes []string, state, verifier string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	if len(scopes) > 0 {
		sep := p.ScopeSep
		if sep == "" {
			sep = " "
		}
		q.Set("scope", strings.Join(scopes, sep))
	}
	if verifier != "" {
		sum := sha256.Sum256([]byte(verifier))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		q.Set("code_challenge_method", "S256")
	}
	for k, v := range p.AuthParams {
		q.Set(k, v)
	}
	sep := "?"
	if strings.Contains(p.AuthURL, "?") {
		sep = "&"
	}
	return p.AuthURL + sep + q.Encode()
}

// Exchange redeems an authorization code. callback is the query the provider
// sent back (some providers put routing details there).
func (c *Client) Exchange(ctx context.Context, p *Provider, app App, code, redirectURI, verifier string, callback url.Values) (Credentials, Metadata, error) {
	tokenURL := p.TokenURL
	if p.TokenURLFor != nil {
		u, err := p.TokenURLFor(callback)
		if err != nil {
			return Credentials{}, nil, &Error{Permanent: true, Message: err.Error()}
		}
		tokenURL = u
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {app.ClientID},
		"client_secret": {app.ClientSecret},
	}
	if verifier != "" {
		form.Set("code_verifier", verifier)
	}
	cred, meta, err := c.token(ctx, p, tokenURL, form)
	if err != nil {
		return cred, meta, err
	}
	if tokenURL != p.TokenURL {
		cred.TokenURL = tokenURL
	}
	if cred.RefreshToken == "" && cred.ExpiresAt != nil {
		// Without a refresh token the connection dies when this token expires.
		meta["warning"] = "the provider returned no refresh token; the connection will stop working when the access token expires"
	}
	return cred, meta, nil
}

// Refresh gets a new access token. Login providers log in again.
func (c *Client) Refresh(ctx context.Context, p *Provider, app App, cred Credentials) (Credentials, Metadata, error) {
	if p.Auth == Login {
		fresh, err := c.Login(ctx, p, cred.Email, cred.Password)
		return fresh, Metadata{}, err
	}
	if cred.RefreshToken == "" {
		return cred, nil, &Error{Permanent: true, Message: "no refresh token stored; the user must connect again"}
	}
	tokenURL := p.TokenURL
	if cred.TokenURL != "" {
		tokenURL = cred.TokenURL
	}
	fresh, meta, err := c.token(ctx, p, tokenURL, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {cred.RefreshToken},
		"client_id":     {app.ClientID},
		"client_secret": {app.ClientSecret},
	})
	if err != nil {
		return cred, nil, err
	}
	if fresh.RefreshToken == "" { // most providers keep the refresh token
		fresh.RefreshToken = cred.RefreshToken
	}
	fresh.TokenURL = cred.TokenURL
	return fresh, meta, nil
}

// Login exchanges an API login for a token (Shiprocket).
func (c *Client) Login(ctx context.Context, p *Provider, email, password string) (Credentials, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.LoginURL, bytes.NewReader(body))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Credentials{}, &Error{Message: "could not reach " + p.Name + ": " + errText(err)}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Token   string `json:"token"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode >= 300 || out.Token == "" {
		msg := out.Message
		if msg == "" {
			msg = "login failed"
		}
		// Wrong login: the user must fix it. Their outage or rate limit: retry.
		permanent := resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 422
		return Credentials{}, &Error{Permanent: permanent, Status: resp.StatusCode, Message: msg}
	}
	exp := time.Now().Add(p.LoginTTL).UTC()
	return Credentials{AccessToken: out.Token, TokenType: "Bearer", ExpiresAt: &exp, Email: email, Password: password}, nil
}

// token posts a token request and parses the standard OAuth response. Some
// providers (Zoho) report errors with HTTP 200 and an "error" field.
func (c *Client) token(ctx context.Context, p *Provider, tokenURL string, form url.Values) (Credentials, Metadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Credentials{}, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Credentials{}, nil, &Error{Message: "could not reach " + p.Name + ": " + errText(err)}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var out map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		permanent := resp.StatusCode == 400 || resp.StatusCode == 401
		return Credentials{}, nil, &Error{Permanent: permanent, Status: resp.StatusCode, Message: "unexpected response from " + p.Name}
	}
	str := func(k string) string { s, _ := out[k].(string); return s }

	code := str("error")
	if s := str("status"); code == "" && strings.HasPrefix(s, "BAD_") {
		code = s // HubSpot: {"status":"BAD_REFRESH_TOKEN","message":…}
	}
	if resp.StatusCode >= 300 || code != "" || str("access_token") == "" {
		msg := str("error_description")
		if msg == "" {
			msg = str("message")
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		permanent := permanentCodes[code] || resp.StatusCode == 400 || resp.StatusCode == 401
		if resp.StatusCode == 429 || resp.StatusCode >= 500 || strings.EqualFold(code, "Access Denied") { // Zoho's rate limit
			permanent = false
		}
		if code == "" && msg == "" {
			msg = "no access token in the response"
		}
		return Credentials{}, nil, &Error{Permanent: permanent, Status: resp.StatusCode, Code: code, Message: msg}
	}

	cred := Credentials{AccessToken: str("access_token"), RefreshToken: str("refresh_token"), TokenType: str("token_type")}
	if secs := number(out["expires_in"]); secs > 0 {
		exp := time.Now().Add(time.Duration(secs) * time.Second).UTC()
		cred.ExpiresAt = &exp
	}
	meta := Metadata{}
	if s := str("scope"); s != "" {
		meta["scope"] = s
	}
	if d := str("api_domain"); d != "" && p.APIBaseFor != nil {
		if base, ok := p.APIBaseFor(d); ok {
			meta["api_base"] = base
		}
	}
	return cred, meta, nil
}

// number reads expires_in, which providers send as a number or a string.
func number(v any) int64 {
	switch x := v.(type) {
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

// errText keeps network errors short and free of URLs with query strings.
func errText(err error) string {
	if ue, ok := err.(*url.Error); ok {
		err = ue.Err
	}
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
