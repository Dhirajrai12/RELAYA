package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"relaya/internal/auth"
)

// Timings for the realtime stream.
const (
	streamAuthTimeout = 10 * time.Second
	streamPing        = 25 * time.Second // keeps IIS/ARR and NATs from idling the socket out
	streamRecheck     = 5 * time.Minute  // re-validate session + membership
	streamWrite       = 10 * time.Second
)

// stream is a WebSocket that pushes change notifications for one organization.
//
// Protocol:
//
//	client → {"type":"auth","token":"rs_… or rk_…"}   (first message, within 10s)
//	server → {"type":"ready"}
//	server → {"type":"event"|"delivery"|"change", …}  (see realtime.Message)
//	server → {"type":"resync"}                        (messages were dropped: refetch everything)
//
// The token travels in a message rather than the URL so it never reaches
// proxy or IIS access logs.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	orgID, err := pathID(r, "org")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// The HTTP server's read/write timeouts would kill a long-lived socket.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.StreamOrigins})
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	defer c.CloseNow()
	c.SetReadLimit(4 << 10)

	// 1. Authenticate with the first message.
	actx, cancel := context.WithTimeout(r.Context(), streamAuthTimeout)
	var hello struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	_, raw, err := c.Read(actx)
	cancel()
	if err != nil || json.Unmarshal(raw, &hello) != nil || hello.Type != "auth" {
		c.Close(websocket.StatusPolicyViolation, "expected an auth message")
		return
	}
	// The dashboard sends an empty token: its session is the httpOnly cookie that
	// came with the upgrade request (SameSite=Strict, and foreign origins are
	// refused above, so other sites can't open a stream with it).
	if hello.Token == "" {
		if c, err := r.Cookie(auth.SessionCookie); err == nil {
			hello.Token = c.Value
		}
	}
	if err := s.authorizeStream(r.Context(), hello.Token, orgID); err != nil {
		c.Close(websocket.StatusPolicyViolation, "unauthorized")
		return
	}

	sub, leave := s.Hub.Subscribe(orgID)
	defer leave()

	// Reading is only needed to process pings/close frames from the client.
	ctx := c.CloseRead(r.Context())
	if err := writeJSON(ctx, c, map[string]string{"type": "ready"}); err != nil {
		return
	}

	ping := time.NewTicker(streamPing)
	defer ping.Stop()
	recheck := time.NewTicker(streamRecheck)
	defer recheck.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-sub.C:
			if sub.TakeLagged() {
				msg = []byte(`{"type":"resync"}`)
			}
			if err := writeRaw(ctx, c, msg); err != nil {
				return
			}
		case <-ping.C:
			if sub.TakeLagged() {
				if err := writeRaw(ctx, c, []byte(`{"type":"resync"}`)); err != nil {
					return
				}
			}
			pctx, cancel := context.WithTimeout(ctx, streamWrite)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		case <-recheck.C:
			// A logged-out session, revoked API key or removed member loses the stream.
			if err := s.authorizeStream(ctx, hello.Token, orgID); err != nil {
				c.Close(websocket.StatusPolicyViolation, "unauthorized")
				return
			}
		}
	}
}

func (s *Server) authorizeStream(ctx context.Context, token, orgID string) error {
	p, err := s.Auth.Authenticate(ctx, token)
	if err != nil {
		return err
	}
	_, _, err = s.Auth.Authorize(auth.WithPrincipal(ctx, p), orgID, auth.RoleMember)
	return err
}

func writeJSON(ctx context.Context, c *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeRaw(ctx, c, b)
}

func writeRaw(ctx context.Context, c *websocket.Conn, b []byte) error {
	wctx, cancel := context.WithTimeout(ctx, streamWrite)
	defer cancel()
	err := c.Write(wctx, websocket.MessageText, b)
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Debug("stream write", "err", err)
	}
	return err
}
