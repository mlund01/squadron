package oauth

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"sync"
	"time"

	"squadron/internal/browser"
)

// LoopbackCallbackSource implements CallbackSource by binding 127.0.0.1
// on an OS-assigned port (RFC 8252 loopback redirect) and serving
// /callback locally. Single-use — create a new instance per flow.
type LoopbackCallbackSource struct {
	listener net.Listener
	server   *http.Server

	callbackCh chan CallbackParams
	errCh      chan error

	once sync.Once
}

func NewLoopbackCallbackSource() *LoopbackCallbackSource {
	return &LoopbackCallbackSource{
		callbackCh: make(chan CallbackParams, 1),
		errCh:      make(chan error, 1),
	}
}

func (s *LoopbackCallbackSource) Prepare(ctx context.Context) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("binding loopback callback: %w", err)
	}
	s.listener = ln
	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", s.handleCallback)
	s.server = &http.Server{
		Handler:     mux,
		ReadTimeout: 10 * time.Second,
	}

	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case s.errCh <- fmt.Errorf("loopback callback server: %w", err):
			default:
			}
		}
	}()

	return fmt.Sprintf("http://127.0.0.1:%d/callback", port), nil
}

func (s *LoopbackCallbackSource) Present(ctx context.Context, authURL string) error {
	// Print first so the user has a fallback if the browser launch fails.
	fmt.Printf("Opening browser to authorize...\n")
	fmt.Printf("If it does not open, visit:\n  %s\n", authURL)
	browser.Open(authURL)
	return nil
}

func (s *LoopbackCallbackSource) Wait(ctx context.Context) (CallbackParams, error) {
	select {
	case params := <-s.callbackCh:
		return params, nil
	case err := <-s.errCh:
		return CallbackParams{}, err
	case <-ctx.Done():
		return CallbackParams{}, ctx.Err()
	}
}

func (s *LoopbackCallbackSource) Close() error {
	var err error
	s.once.Do(func() {
		if s.server != nil {
			// Brief grace period so the "you can close this tab" page can
			// finish writing before we drop the listener.
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err = s.server.Shutdown(shutdownCtx)
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
	})
	return err
}

// State validation lives in mcp-go's ProcessAuthorizationResponse; this
// handler just forwards the raw params to the waiter.
func (s *LoopbackCallbackSource) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	if oerr := q.Get("error"); oerr != "" {
		msg := oerr
		if desc := q.Get("error_description"); desc != "" {
			msg = oerr + ": " + desc
		}
		writeErrorPage(w, msg)
		select {
		case s.errCh <- fmt.Errorf("authorization server returned error: %s", msg):
		default:
		}
		return
	}

	params := CallbackParams{
		Code:  q.Get("code"),
		State: q.Get("state"),
	}
	if params.Code == "" {
		writeErrorPage(w, "callback missing authorization code")
		select {
		case s.errCh <- errors.New("authorization server redirected without code"):
		default:
		}
		return
	}

	writeSuccessPage(w)

	select {
	case s.callbackCh <- params:
	default:
	}
}

func writeSuccessPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html>
<html><head><title>Authorized</title></head>
<body style="font-family:system-ui;padding:3rem;max-width:40rem;margin:auto">
<h1>Authorization complete</h1>
<p>You can close this window and return to your terminal.</p>
<script>setTimeout(function(){window.close();},200);</script>
</body></html>`))
}

func writeErrorPage(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html><head><title>Authorization failed</title></head>
<body style="font-family:system-ui;padding:3rem;max-width:40rem;margin:auto">
<h1>Authorization failed</h1>
<p>%s</p>
<p>You can close this window.</p>
</body></html>`, html.EscapeString(msg))
}
