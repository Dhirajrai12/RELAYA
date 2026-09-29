// Package api is the dashboard and public REST API.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"relaya/internal/alerts"
	"relaya/internal/auth"
	"relaya/internal/connect"
	"relaya/internal/delivery"
	"relaya/internal/httpx"
	"relaya/internal/ratelimit"
	"relaya/internal/realtime"
	"relaya/internal/status"
	"relaya/internal/vault"
)

type Server struct {
	Pool          *pgxpool.Pool
	Auth          *auth.Service
	Vault         vault.Vault
	IngestBaseURL string

	// Outbound delivery: URL policy (SSRF) and the sender used for test deliveries.
	DeliveryPolicy delivery.Policy
	Sender         *delivery.Sender

	// Realtime: change notifications pushed over WebSocket.
	Hub           *realtime.Hub
	StreamOrigins []string // host patterns allowed to open the stream (same-origin is always allowed)

	// Contracts: shown as learning progress (the worker enforces it).
	ContractMinSamples int
	// Shown in the dashboard; the worker does the auto-resolving.
	IncidentAutoResolveAfter time.Duration
	// Sends test alerts synchronously; the worker sends the rest.
	AlertSender *alerts.Sender
	// Public status page data (nil = endpoint off).
	Status *status.Service
	// Connections: OAuth/login flows and token storage (nil = off).
	Connect *connect.Service
	// Proxy: calls providers' APIs with a connection's token (no redirects followed).
	ProxyHTTP *http.Client
	// Public dashboard URL, for Connect links and the pages users return to.
	DashboardURL string
	// Rate limits; nil fields are unlimited (tests).
	Limits            Limits
	TrustProxyHeaders bool // for client IPs behind our own proxy
}

// Limits protects sign-in from password guessing and the API from floods.
type Limits struct {
	LoginIP    *ratelimit.Limiter // sign-in attempts per IP
	StatusIP   *ratelimit.Limiter // public status requests per IP
	LoginEmail *ratelimit.Limiter // failed sign-ins per account
	SignupIP   *ratelimit.Limiter // sign-ups per IP
	Caller     *ratelimit.Limiter // authenticated requests per user or API key
	ConnectIP  *ratelimit.Limiter // public Connect page requests per IP
}

// limitCaller rate-limits authenticated requests per user or API key.
func (s *Server) limitCaller(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := auth.FromContext(r.Context()); ok {
			key := "u:" + p.UserID
			if p.APIKeyID != "" {
				key = "k:" + p.APIKeyID
			}
			if ok, wait := s.Limits.Caller.Allow(key); !ok {
				httpx.WriteError(w, r, httpx.TooManyRequests(w, wait, "too many requests; slow down and retry"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Routes() http.Handler {
	public := http.NewServeMux()
	public.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	public.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Pool.Ping(r.Context()); err != nil {
			httpx.JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unavailable"})
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	public.Handle("GET /v1/status", httpx.HandlerFunc(s.publicStatus))
	public.Handle("POST /v1/auth/signup", httpx.HandlerFunc(s.signup))
	public.Handle("POST /v1/auth/login", httpx.HandlerFunc(s.login))
	// Authenticates with its first message, so it sits outside the auth middleware.
	public.HandleFunc("GET /v1/orgs/{org}/stream", s.stream)
	// The outbound portal's users have no Relaya login: the portal token is the credential.
	public.Handle("GET /v1/portal/app", httpx.HandlerFunc(s.portalInfo))
	public.Handle("GET /v1/portal/endpoints", httpx.HandlerFunc(s.portalListEndpoints))
	public.Handle("POST /v1/portal/endpoints", httpx.HandlerFunc(s.portalCreateEndpoint))
	public.Handle("PATCH /v1/portal/endpoints/{endpoint}", httpx.HandlerFunc(s.portalUpdateEndpoint))
	public.Handle("DELETE /v1/portal/endpoints/{endpoint}", httpx.HandlerFunc(s.portalDeleteEndpoint))
	public.Handle("GET /v1/portal/endpoints/{endpoint}/secret", httpx.HandlerFunc(s.portalEndpointSecret))
	public.Handle("POST /v1/portal/endpoints/{endpoint}/rotate-secret", httpx.HandlerFunc(s.portalRotateSecret))
	public.Handle("POST /v1/portal/endpoints/{endpoint}/test", httpx.HandlerFunc(s.portalTestEndpoint))
	public.Handle("GET /v1/portal/deliveries", httpx.HandlerFunc(s.portalDeliveries))
	public.Handle("POST /v1/portal/deliveries/{delivery}/retry", httpx.HandlerFunc(s.portalRetry))
	if s.Connect != nil {
		// The Connect page's end users have no Relaya login: the link token is the credential.
		public.Handle("GET /v1/connect/sessions/{token}", httpx.HandlerFunc(s.getConnectSession))
		public.Handle("POST /v1/connect/sessions/{token}/authorize", httpx.HandlerFunc(s.authorizeConnectSession))
		public.Handle("POST /v1/connect/sessions/{token}/login", httpx.HandlerFunc(s.loginConnectSession))
		public.Handle("GET /v1/connect/callback", httpx.HandlerFunc(s.connectCallback))
	}

	private := http.NewServeMux()
	h := func(pattern string, fn httpx.HandlerFunc) { private.Handle(pattern, fn) }

	h("POST /v1/auth/logout", s.logout)
	h("GET /v1/me", s.me)
	h("GET /v1/providers", s.listProviders)

	h("GET /v1/orgs", s.listOrgs)
	h("POST /v1/orgs", s.createOrg)
	h("GET /v1/orgs/{org}", s.getOrg)
	h("GET /v1/orgs/{org}/members", s.listMembers)
	h("POST /v1/orgs/{org}/members", s.addMember)
	h("PATCH /v1/orgs/{org}/members/{user}", s.updateMember)
	h("DELETE /v1/orgs/{org}/members/{user}", s.removeMember)

	h("GET /v1/orgs/{org}/api-keys", s.listAPIKeys)
	h("POST /v1/orgs/{org}/api-keys", s.createAPIKey)
	h("DELETE /v1/orgs/{org}/api-keys/{key}", s.revokeAPIKey)

	h("GET /v1/orgs/{org}/projects", s.listProjects)
	h("POST /v1/orgs/{org}/projects", s.createProject)
	h("GET /v1/orgs/{org}/projects/{project}", s.getProject)
	h("DELETE /v1/orgs/{org}/projects/{project}", s.deleteProject)

	h("GET /v1/orgs/{org}/webhooks", s.listWebhooks)
	h("POST /v1/orgs/{org}/webhooks", s.createWebhook)
	h("GET /v1/orgs/{org}/webhooks/{webhook}", s.getWebhook)
	h("PATCH /v1/orgs/{org}/webhooks/{webhook}", s.updateWebhook)
	h("POST /v1/orgs/{org}/webhooks/{webhook}/rotate-url", s.rotateWebhookURL)
	h("GET /v1/orgs/{org}/webhooks/{webhook}/samples", s.listSimulationSamples)
	h("POST /v1/orgs/{org}/webhooks/{webhook}/simulate", s.simulateEvent)
	h("DELETE /v1/orgs/{org}/webhooks/{webhook}", s.deleteWebhook)

	h("GET /v1/orgs/{org}/events", s.listEvents)
	h("GET /v1/orgs/{org}/events/stats", s.eventStats)
	h("GET /v1/orgs/{org}/events/{event}", s.getEvent)
	h("GET /v1/orgs/{org}/events/{event}/raw", s.getRawEvent)

	h("GET /v1/orgs/{org}/webhooks/{webhook}/destinations", s.listDestinations)
	h("POST /v1/orgs/{org}/webhooks/{webhook}/destinations", s.createDestination)
	h("PATCH /v1/orgs/{org}/destinations/{destination}", s.updateDestination)
	h("DELETE /v1/orgs/{org}/destinations/{destination}", s.deleteDestination)
	h("POST /v1/orgs/{org}/destinations/{destination}/rotate-secret", s.rotateDestinationSecret)
	h("POST /v1/orgs/{org}/destinations/{destination}/test", s.testDestination)

	h("GET /v1/orgs/{org}/deliveries", s.listDeliveries)
	h("GET /v1/orgs/{org}/deliveries/{delivery}", s.getDelivery)
	h("POST /v1/orgs/{org}/deliveries/{delivery}/retry", s.retryDelivery)

	h("GET /v1/orgs/{org}/contracts", s.listContracts)
	h("GET /v1/orgs/{org}/contracts/{contract}", s.getContract)
	h("GET /v1/orgs/{org}/contracts/{contract}/findings", s.listFindings)
	h("POST /v1/orgs/{org}/contracts/{contract}/versions", s.createContractVersion)
	h("POST /v1/orgs/{org}/contracts/{contract}/relearn", s.relearnContract)
	h("GET /v1/orgs/{org}/incidents", s.listIncidents)
	h("POST /v1/orgs/{org}/incidents/{incident}/resolve", s.resolveIncident)
	h("GET /v1/orgs/{org}/incidents/{incident}/replay", s.previewReplay)
	h("POST /v1/orgs/{org}/incidents/{incident}/replay", s.startReplay)

	h("GET /v1/orgs/{org}/alert-settings", s.alertSettings)
	h("GET /v1/orgs/{org}/alert-channels", s.listAlertChannels)
	h("POST /v1/orgs/{org}/alert-channels", s.createAlertChannel)
	h("PATCH /v1/orgs/{org}/alert-channels/{channel}", s.updateAlertChannel)
	h("DELETE /v1/orgs/{org}/alert-channels/{channel}", s.deleteAlertChannel)
	h("POST /v1/orgs/{org}/alert-channels/{channel}/test", s.testAlertChannel)
	h("GET /v1/orgs/{org}/alerts", s.listAlerts)

	h("GET /v1/orgs/{org}/repair-rules", s.listRepairRules)
	h("POST /v1/orgs/{org}/repair-rules", s.createRepairRule)
	h("POST /v1/orgs/{org}/repair-rules/preview", s.previewRepairRule)
	h("PATCH /v1/orgs/{org}/repair-rules/{rule}", s.updateRepairRule)
	h("DELETE /v1/orgs/{org}/repair-rules/{rule}", s.deleteRepairRule)
	h("GET /v1/orgs/{org}/incidents/{incident}/repair-suggestion", s.repairSuggestion)

	h("GET /v1/orgs/{org}/audit-logs", s.listAuditLogs)

	h("GET /v1/orgs/{org}/outbound/apps", s.listOutboundApps)
	h("POST /v1/orgs/{org}/outbound/apps", s.createOutboundApp)
	h("GET /v1/orgs/{org}/outbound/apps/{app}", s.getOutboundApp)
	h("DELETE /v1/orgs/{org}/outbound/apps/{app}", s.deleteOutboundApp)
	h("POST /v1/orgs/{org}/outbound/apps/{app}/endpoints", s.createOutboundEndpoint)
	h("PATCH /v1/orgs/{org}/outbound/apps/{app}/endpoints/{endpoint}", s.updateOutboundEndpoint)
	h("DELETE /v1/orgs/{org}/outbound/apps/{app}/endpoints/{endpoint}", s.deleteOutboundEndpoint)
	h("GET /v1/orgs/{org}/outbound/apps/{app}/endpoints/{endpoint}/secret", s.outboundEndpointSecret)
	h("POST /v1/orgs/{org}/outbound/apps/{app}/endpoints/{endpoint}/test", s.testOutboundEndpoint)
	h("POST /v1/orgs/{org}/outbound/apps/{app}/portal-link", s.createPortalLink)
	h("POST /v1/orgs/{org}/outbound/messages", s.sendOutboundMessage)
	h("GET /v1/orgs/{org}/outbound/event-types", s.listOutboundEventTypes)
	h("POST /v1/orgs/{org}/outbound/event-types", s.upsertOutboundEventType)
	h("DELETE /v1/orgs/{org}/outbound/event-types/{name}", s.deleteOutboundEventType)

	if s.Connect != nil {
		h("GET /v1/connect/providers", s.listConnectProviders)
		h("GET /v1/orgs/{org}/integrations", s.listIntegrations)
		h("POST /v1/orgs/{org}/integrations", s.createIntegration)
		h("PATCH /v1/orgs/{org}/integrations/{integration}", s.updateIntegration)
		h("DELETE /v1/orgs/{org}/integrations/{integration}", s.deleteIntegration)
		h("GET /v1/orgs/{org}/connections", s.listConnections)
		h("GET /v1/orgs/{org}/connections/{connection}", s.getConnection)
		h("DELETE /v1/orgs/{org}/connections/{connection}", s.deleteConnection)
		h("POST /v1/orgs/{org}/connections/{connection}/refresh", s.refreshConnection)
		h("GET /v1/orgs/{org}/connections/{connection}/token", s.connectionToken)
		h("POST /v1/orgs/{org}/connect-sessions", s.createConnectSession)
		h("GET /v1/orgs/{org}/connections/{connection}/proxy/{path...}", s.proxyConnection)
		h("POST /v1/orgs/{org}/connections/{connection}/proxy/{path...}", s.proxyConnection)
		h("PUT /v1/orgs/{org}/connections/{connection}/proxy/{path...}", s.proxyConnection)
		h("PATCH /v1/orgs/{org}/connections/{connection}/proxy/{path...}", s.proxyConnection)
		h("DELETE /v1/orgs/{org}/connections/{connection}/proxy/{path...}", s.proxyConnection)
		h("GET /v1/orgs/{org}/proxy-calls", s.listProxyCalls)
		h("GET /v1/connect/sync-models", s.listSyncModels)
		h("GET /v1/orgs/{org}/syncs", s.listSyncs)
		h("POST /v1/orgs/{org}/syncs", s.createSync)
		h("PATCH /v1/orgs/{org}/syncs/{sync}", s.updateSync)
		h("DELETE /v1/orgs/{org}/syncs/{sync}", s.deleteSync)
		h("POST /v1/orgs/{org}/syncs/{sync}/run", s.runSync)
		h("GET /v1/orgs/{org}/syncs/{sync}/runs", s.listSyncRuns)
	}

	public.Handle("/", s.Auth.Middleware(s.limitCaller(private)))
	return public
}

// ---- shared helpers ------------------------------------------------------------

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// pathID returns a UUID path parameter, or 404 if it is malformed.
func pathID(r *http.Request, name string) (string, error) {
	v := r.PathValue(name)
	if !uuidRe.MatchString(v) {
		return "", httpx.ErrNotFound
	}
	return strings.ToLower(v), nil
}

// orgAccess authorizes the caller for the {org} in the path.
func (s *Server) orgAccess(r *http.Request, min auth.Role) (orgID string, p auth.Principal, role auth.Role, err error) {
	if orgID, err = pathID(r, "org"); err != nil {
		return
	}
	p, role, err = s.Auth.Authorize(r.Context(), orgID, min)
	return
}

func (s *Server) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.Pool, fn)
}

func notFoundIfNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	}
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "item"
	}
	return s
}

func randomSuffix() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func requireName(v, field string, max int) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", httpx.BadRequest("%s is required", field)
	}
	if len(v) > max {
		return "", httpx.BadRequest("%s must be at most %d characters", field, max)
	}
	return v, nil
}

// publicStatus serves the status page: no login, cached, rate-limited per IP.
func (s *Server) publicStatus(w http.ResponseWriter, r *http.Request) error {
	if s.Status == nil {
		return httpx.ErrNotFound
	}
	if ok, wait := s.Limits.StatusIP.Allow(httpx.ClientIP(r, s.TrustProxyHeaders)); !ok {
		return httpx.TooManyRequests(w, wait, "too many requests; the status page refreshes itself")
	}
	p, err := s.Status.Page(r.Context())
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	httpx.JSON(w, http.StatusOK, p)
	return nil
}
