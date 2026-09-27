// Package connect lets an organization's end users connect their accounts at
// other apps (Zoho, HubSpot, Google, Shiprocket…). Relaya runs the OAuth or
// login flow, stores the tokens encrypted and keeps them fresh; the
// organization's backend asks Relaya for a working token when it needs one.
package connect

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"sync"
	"time"
)

// AuthType is how a provider's users connect.
type AuthType string

const (
	OAuth2 AuthType = "oauth2" // the organization's own OAuth app; the user approves at the provider
	Login  AuthType = "login"  // the user enters their API login; Relaya exchanges it for a token
)

// Provider describes one app users can connect to.
type Provider struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Auth    AuthType `json:"auth"`
	DocsURL string   `json:"docs_url"`
	// Base URL for API calls with the token (Zoho's depends on the user's data centre).
	APIBase string `json:"api_base"`

	// OAuth2.
	AuthURL       string            `json:"-"`
	TokenURL      string            `json:"-"`
	DefaultScopes []string          `json:"default_scopes"`
	ScopeSep      string            `json:"-"` // between scopes in the authorize URL (default " ")
	PKCE          bool              `json:"-"` // send an S256 code challenge
	AuthParams    map[string]string `json:"-"` // extra authorize parameters, e.g. access_type=offline
	// TokenURLFor picks the token endpoint from the callback query (Zoho sends
	// the user's data centre there). nil means TokenURL.
	TokenURLFor func(callback url.Values) (string, error) `json:"-"`
	// APIBaseFor validates a provider-reported API base URL (Zoho's api_domain).
	APIBaseFor func(reported string) (string, bool) `json:"-"`

	// Login.
	LoginURL   string        `json:"-"`
	LoginTTL   time.Duration `json:"-"` // how long a login token lasts; refreshed before that
	LoginLabel string        `json:"login_label,omitempty"`
}

var (
	zohoAccounts = regexp.MustCompile(`^https://accounts\.zoho(cloud)?\.(com|in|eu|com\.au|jp|ca|sa|com\.cn|uk)$`)
	zohoAPI      = regexp.MustCompile(`^https://www\.zohoapis\.(com|in|eu|com\.au|jp|ca|sa|com\.cn|uk)$`)
)

var (
	mu        sync.RWMutex
	providers = map[string]*Provider{
		"zoho": {
			Key: "zoho", Name: "Zoho", Auth: OAuth2,
			DocsURL:       "https://www.zoho.com/accounts/protocol/oauth/web-server-applications.html",
			APIBase:       "https://www.zohoapis.com",
			AuthURL:       "https://accounts.zoho.com/oauth/v2/auth",
			TokenURL:      "https://accounts.zoho.com/oauth/v2/token",
			DefaultScopes: []string{"ZohoCRM.modules.ALL", "ZohoCRM.settings.READ"},
			ScopeSep:      ",",
			AuthParams:    map[string]string{"access_type": "offline", "prompt": "consent"},
			// A user in another data centre (India, EU…) logs in at accounts.zoho.com and
			// comes back with accounts-server=https://accounts.zoho.in: the code can only be
			// redeemed there. Only Zoho's own hosts are accepted, or the code and client
			// secret could be sent anywhere.
			TokenURLFor: func(q url.Values) (string, error) {
				server := q.Get("accounts-server")
				if server == "" {
					return "https://accounts.zoho.com/oauth/v2/token", nil
				}
				if !zohoAccounts.MatchString(server) {
					return "", fmt.Errorf("unexpected Zoho accounts server %q", server)
				}
				return server + "/oauth/v2/token", nil
			},
			APIBaseFor: func(reported string) (string, bool) { return reported, zohoAPI.MatchString(reported) },
		},
		"hubspot": {
			Key: "hubspot", Name: "HubSpot", Auth: OAuth2,
			DocsURL:       "https://developers.hubspot.com/docs/api/oauth-quickstart-guide",
			APIBase:       "https://api.hubapi.com",
			AuthURL:       "https://app.hubspot.com/oauth/authorize",
			TokenURL:      "https://api.hubapi.com/oauth/v1/token",
			DefaultScopes: []string{"oauth", "crm.objects.contacts.read"},
		},
		"google": {
			Key: "google", Name: "Google", Auth: OAuth2,
			DocsURL:       "https://developers.google.com/identity/protocols/oauth2/web-server",
			APIBase:       "https://www.googleapis.com",
			AuthURL:       "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:      "https://oauth2.googleapis.com/token",
			DefaultScopes: []string{"openid", "email", "https://www.googleapis.com/auth/spreadsheets.readonly"},
			PKCE:          true,
			// offline + consent: Google only returns a refresh token on consent.
			AuthParams: map[string]string{"access_type": "offline", "prompt": "consent", "include_granted_scopes": "true"},
		},
		"shiprocket": {
			Key: "shiprocket", Name: "Shiprocket", Auth: Login,
			DocsURL:    "https://apidocs.shiprocket.in/",
			APIBase:    "https://apiv2.shiprocket.in/v1/external",
			LoginURL:   "https://apiv2.shiprocket.in/v1/external/auth/login",
			LoginTTL:   9 * 24 * time.Hour, // tokens last 10 days
			LoginLabel: "Shiprocket API user (Settings → API → Configure → Create an API user)",
		},
	}
)

// Get returns the provider with this key.
func Get(key string) (*Provider, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := providers[key]
	return p, ok
}

// List returns all providers, sorted by name.
func List() []*Provider {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]*Provider, 0, len(providers))
	for _, p := range providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Override replaces or adds a provider and returns a function that undoes it.
// For tests, which point providers at fake OAuth servers.
func Override(p *Provider) (restore func()) {
	mu.Lock()
	defer mu.Unlock()
	old, had := providers[p.Key]
	providers[p.Key] = p
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if had {
			providers[p.Key] = old
		} else {
			delete(providers, p.Key)
		}
	}
}
