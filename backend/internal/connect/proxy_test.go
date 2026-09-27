package connect

import (
	"errors"
	"testing"
	"time"
)

func TestProxyURLOnlyProviderHosts(t *testing.T) {
	google, zoho, hubspot := mustGet(t, "google"), mustGet(t, "zoho"), mustGet(t, "hubspot")
	ok := []struct {
		p                        *Provider
		apiBase, base, path, raw string
		want                     string
	}{
		{google, "https://www.googleapis.com", "https://sheets.googleapis.com", "/v4/spreadsheets/abc", "ranges=A1", "https://sheets.googleapis.com/v4/spreadsheets/abc?ranges=A1"},
		{google, "https://www.googleapis.com", "https://www.googleapis.com", "drive/v3/files", "", "https://www.googleapis.com/drive/v3/files"},
		{zoho, "https://www.zohoapis.in", "https://www.zohoapis.in", "crm/v2/Leads", "", "https://www.zohoapis.in/crm/v2/Leads"},
		{hubspot, "https://api.hubapi.com", "https://api.hubapi.com/", "/crm/v3/objects/contacts", "limit=5", "https://api.hubapi.com/crm/v3/objects/contacts?limit=5"},
	}
	for _, c := range ok {
		got, _, err := proxyURL(c.p, c.apiBase, c.base, c.path, c.raw)
		if err != nil || got != c.want {
			t.Errorf("%s %s%s: got %q, %v", c.p.Key, c.base, c.path, got, err)
		}
	}
	bad := []struct {
		p          *Provider
		base, path string
	}{
		{google, "https://evil.example", "x"},
		{google, "https://sheets.googleapis.com.evil.example", "x"},
		{google, "http://sheets.googleapis.com", "x"}, // no plain http
		{google, "https://user:pw@sheets.googleapis.com", "x"},
		{hubspot, "https://api.hubspot.com.evil.example", "x"},
		{hubspot, "https://sheets.googleapis.com", "x"}, // another provider's host
		{zoho, "https://www.zohoapis.in", "../../other"},
		{zoho, "https://www.zohoapis.in", `crm\..\x`},
		{zoho, "not a url", "x"},
	}
	for _, c := range bad {
		if _, _, err := proxyURL(c.p, c.p.APIBase, c.base, c.path, ""); !errors.Is(err, ErrHostNotAllowed) {
			t.Errorf("%s %s %s: allowed (%v)", c.p.Key, c.base, c.path, err)
		}
	}
}

func TestBackoff(t *testing.T) {
	if backoff(1, "") != 300*time.Millisecond || backoff(2, "") != 600*time.Millisecond {
		t.Error("default backoff")
	}
	if backoff(1, "2") != 2*time.Second || backoff(1, "120") != 5*time.Second {
		t.Error("Retry-After is followed and capped")
	}
}
