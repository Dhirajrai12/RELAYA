package connect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Atlassian (Jira Cloud) OAuth 2.0 (3LO). A token isn't tied to one site: after
// connecting, Relaya asks which sites the user granted and calls the first Jira
// one through https://api.atlassian.com/ex/jira/<cloud id>. Refresh tokens
// rotate: every refresh returns a new one, which Refresh stores.

// AtlassianResourcesURL lists the sites a token can reach.
const AtlassianResourcesURL = "https://api.atlassian.com/oauth/token/accessible-resources"

var atlassianCloudID = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

// AtlassianDiscover returns a Discover step that reads the user's Jira sites from resourcesURL.
func AtlassianDiscover(resourcesURL string) func(ctx context.Context, hc *http.Client, accessToken string) (Metadata, error) {
	return func(ctx context.Context, hc *http.Client, accessToken string) (Metadata, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourcesURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			return nil, &Error{Message: "could not reach Atlassian: " + errText(err)}
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode >= 300 {
			return nil, &Error{Permanent: resp.StatusCode == 401 || resp.StatusCode == 403, Status: resp.StatusCode, Message: "could not list the Jira sites this account granted"}
		}
		var sites []struct {
			ID     string   `json:"id"`
			URL    string   `json:"url"`
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		}
		if err := json.Unmarshal(raw, &sites); err != nil {
			return nil, &Error{Message: "unexpected response from Atlassian"}
		}
		var list []map[string]string
		for _, s := range sites {
			jira := false
			for _, sc := range s.Scopes {
				jira = jira || strings.Contains(sc, "jira")
			}
			if jira && atlassianCloudID.MatchString(s.ID) {
				list = append(list, map[string]string{"cloud_id": s.ID, "url": s.URL, "name": s.Name})
			}
		}
		if len(list) == 0 {
			return nil, &Error{Permanent: true, Message: "this Atlassian account didn't grant access to any Jira site"}
		}
		first := list[0]
		meta := Metadata{
			"api_base":  "https://api.atlassian.com/ex/jira/" + first["cloud_id"],
			"cloud_id":  first["cloud_id"],
			"site_url":  first["url"],
			"site_name": first["name"],
		}
		if len(list) > 1 {
			meta["sites"] = list // the others; pass one's API base as baseUrl to use it
		}
		return meta, nil
	}
}
