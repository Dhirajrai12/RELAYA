// Package alerts queues notifications (in the same transaction as the change
// that caused them) and sends them to Slack, email or webhook channels.
package alerts

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Channel is the Postgres channel the sender LISTENs on.
const Channel = "relaya_alerts"

// Kinds a channel can subscribe to.
const (
	IncidentOpened       = "incident_opened"
	IncidentResolved     = "incident_resolved"
	DestinationFailing   = "destination_failing"
	DestinationRecovered = "destination_recovered"
	SignatureFailures    = "signature_failures"
	ConnectionBroken     = "connection_broken"
	ConnectionRecovered  = "connection_recovered"
	SyncFailing          = "sync_failing"
	SyncRecovered        = "sync_recovered"
	Test                 = "test" // always delivered to the channel being tested
)

// Kinds lists the subscribable kinds, in display order.
var Kinds = []string{IncidentOpened, IncidentResolved, DestinationFailing, DestinationRecovered, SignatureFailures,
	ConnectionBroken, ConnectionRecovered, SyncFailing, SyncRecovered}

// FailingAfter is how many consecutive failed attempts mark a destination failing.
const FailingAfter = 3

// Alert is one notification. Link is a dashboard path; the sender adds the host.
type Alert struct {
	Kind  string
	Title string // first line: what broke, with impact
	Body  string
	Link  string
	// Subject is what the alert is about, e.g. "incident:<id>", so a recovery can find
	// what its failure opened (a Jira issue). Empty for alerts about nothing in particular.
	Subject string
}

// Recovers reports whether kind closes what an earlier alert about the same subject opened.
func Recovers(kind string) bool {
	switch kind {
	case IncidentResolved, DestinationRecovered, ConnectionRecovered, SyncRecovered:
		return true
	}
	return false
}

// Querier is satisfied by pgx.Tx and the pool.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Enqueue queues a for every enabled channel of orgID subscribed to its kind.
// It is sent after the surrounding transaction commits.
func Enqueue(ctx context.Context, q Querier, orgID string, a Alert) error {
	tag, err := q.Exec(ctx, `
		INSERT INTO alerts (org_id, channel_id, kind, title, body, link, subject)
		SELECT org_id, id, $2, $3, $4, $5, $6 FROM alert_channels
		WHERE org_id = $1 AND enabled AND $2 = ANY(events)`,
		orgID, a.Kind, a.Title, a.Body, a.Link, a.Subject)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, err = q.Exec(ctx, `SELECT pg_notify($1, '')`, Channel)
	return err
}

// EnqueueTo queues a for one specific channel (used by "Send test").
func EnqueueTo(ctx context.Context, q Querier, orgID, channelID string, a Alert) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO alerts (org_id, channel_id, kind, title, body, link, subject) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`, orgID, channelID, a.Kind, a.Title, a.Body, a.Link, a.Subject).Scan(&id)
	return id, err
}

// ---- messages --------------------------------------------------------------------------
// Every alert's first line states what broke and its impact.

// IncidentOpenedAlert loads an incident and builds its "opened" alert.
func IncidentOpenedAlert(ctx context.Context, q Querier, incidentID string) (string, Alert, error) {
	var orgID, title, webhook, expected, actual string
	var count int
	err := q.QueryRow(ctx, `
		SELECT i.org_id, i.title, coalesce(w.name, ''), i.expected, i.actual, i.event_count
		FROM incidents i LEFT JOIN webhooks w ON w.id = i.webhook_id WHERE i.id = $1`, incidentID).
		Scan(&orgID, &title, &webhook, &expected, &actual, &count)
	if err != nil {
		return "", Alert{}, err
	}
	return orgID, Alert{
		Kind:  IncidentOpened,
		Title: "Breaking change: " + title,
		Body: fmt.Sprintf("Webhook: %s\nExpected %s, got %s.\nNew events with this problem are grouped into this incident; nothing is dropped.",
			webhook, orDash(expected), orDash(actual)),
		Link:    "/incidents",
		Subject: "incident:" + incidentID,
	}, nil
}

// NotifyIncidentsResolved queues "resolved" alerts for the given incidents.
func NotifyIncidentsResolved(ctx context.Context, q Querier, incidentIDs []string) error {
	for _, id := range incidentIDs {
		var orgID, title, resolution, by string
		var count int
		err := q.QueryRow(ctx, `SELECT org_id, title, resolution, resolved_by, event_count FROM incidents WHERE id = $1`, id).
			Scan(&orgID, &title, &resolution, &by, &count)
		if err != nil {
			return err
		}
		how := "Resolved manually"
		if resolution != "" {
			how = "Resolved: " + resolution
		}
		if by == "system" {
			how = "Closed automatically: " + resolution
		}
		if err := Enqueue(ctx, q, orgID, Alert{
			Kind:    IncidentResolved,
			Title:   "Resolved: " + title,
			Body:    fmt.Sprintf("%s\n%d event(s) were affected.", how, count),
			Link:    "/incidents",
			Subject: "incident:" + id,
		}); err != nil {
			return err
		}
	}
	return nil
}

func DestinationFailingAlert(destinationID, name, url string, statusCode int, errText string, attempts int) Alert {
	why := errText
	if statusCode > 0 {
		why = fmt.Sprintf("HTTP %d", statusCode)
		if errText != "" {
			why += " (" + errText + ")"
		}
	}
	return Alert{
		Kind:    DestinationFailing,
		Title:   fmt.Sprintf("Deliveries to %s are failing", name),
		Body:    fmt.Sprintf("%d attempts in a row failed. Last error: %s\nEndpoint: %s\nFailed deliveries are retried automatically; you'll get a message when it recovers.", attempts, orDash(why), url),
		Link:    "/events",
		Subject: "destination:" + destinationID,
	}
}

func DestinationRecoveredAlert(destinationID, name, url string) Alert {
	return Alert{
		Kind:    DestinationRecovered,
		Title:   fmt.Sprintf("Deliveries to %s have recovered", name),
		Body:    fmt.Sprintf("The endpoint is accepting deliveries again.\nEndpoint: %s", url),
		Link:    "/events",
		Subject: "destination:" + destinationID,
	}
}

func SignatureFailuresAlert(webhookID, webhook string) Alert {
	return Alert{
		Kind:  SignatureFailures,
		Title: fmt.Sprintf("Webhook %s is rejecting deliveries: bad signature", webhook),
		Body: "Requests arrived with a missing or invalid signature and were rejected (the sender got HTTP 401).\n" +
			"Usually the signing secret in Relaya doesn't match the provider's; it can also mean someone is sending forged requests.\n" +
			"You'll get at most one of these per webhook per hour.",
		Link:    "/events?status=rejected",
		Subject: "signature:" + webhookID,
	}
}

func ConnectionBrokenAlert(connectionID, integration, endUser, reason string) Alert {
	return Alert{
		Kind:  ConnectionBroken,
		Title: fmt.Sprintf("%s connection for %s is broken", integration, endUser),
		Body: fmt.Sprintf("Relaya could not renew its access: %s\n"+
			"Calls with this connection fail until the user connects again. Send them a new Connect link (Connections page or API).", orDash(reason)),
		Link:    "/connections",
		Subject: "connection:" + connectionID,
	}
}

func ConnectionRecoveredAlert(connectionID, integration, endUser string) Alert {
	return Alert{
		Kind:    ConnectionRecovered,
		Title:   fmt.Sprintf("%s connection for %s works again", integration, endUser),
		Body:    "The connection has fresh access and is being kept up to date again.",
		Link:    "/connections",
		Subject: "connection:" + connectionID,
	}
}

func SyncFailingAlert(syncID, name, reason string, runs int) Alert {
	return Alert{
		Kind:  SyncFailing,
		Title: fmt.Sprintf("Sync %s is failing", name),
		Body: fmt.Sprintf("The last %d runs failed. Last error: %s\n"+
			"Changes at the provider aren't reaching you until it recovers; nothing is skipped, the next good run catches up.", runs, orDash(reason)),
		Link:    "/connections",
		Subject: "sync:" + syncID,
	}
}

func SyncRecoveredAlert(syncID, name string) Alert {
	return Alert{
		Kind:    SyncRecovered,
		Title:   fmt.Sprintf("Sync %s works again", name),
		Body:    "The last run succeeded and caught up with the changes made meanwhile.",
		Link:    "/connections",
		Subject: "sync:" + syncID,
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
