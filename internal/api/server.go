// Package api exposes the JSON API and the embedded dashboard.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"twilightzone/ups-monitor/internal/model"
	"twilightzone/ups-monitor/internal/nut"
	"twilightzone/ups-monitor/internal/store"
	"twilightzone/ups-monitor/web"
)

// StatusProvider reports collector health.
type StatusProvider interface {
	Status() model.CollectorStatus
}

// NUTLister lists the UPSes exposed by upsd. It is used by the optional
// /api/ups endpoint.
type NUTLister interface {
	ListUPS(ctx context.Context) ([]nut.UPS, error)
}

// Server holds the HTTP dependencies.
type Server struct {
	store          *store.Store
	status         StatusProvider
	nut            NUTLister
	staleAfter     time.Duration
	refreshSeconds int
	logger         *slog.Logger
	templates      *template.Template
	handler        http.Handler
}

// New builds the API server.
func New(st *store.Store, status StatusProvider, nutLister NUTLister, staleAfter time.Duration, refreshSeconds int, logger *slog.Logger) (*Server, error) {
	if st == nil {
		return nil, errors.New("api: store is required")
	}
	if staleAfter <= 0 {
		staleAfter = time.Minute
	}
	if refreshSeconds <= 0 {
		refreshSeconds = 10
	}
	if logger == nil {
		logger = slog.Default()
	}

	templates, err := template.ParseFS(web.FS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("api: parse templates: %w", err)
	}

	server := &Server{
		store:          st,
		status:         status,
		nut:            nutLister,
		staleAfter:     staleAfter,
		refreshSeconds: refreshSeconds,
		logger:         logger,
		templates:      templates,
	}
	handler, err := server.routes()
	if err != nil {
		return nil, err
	}
	server.handler = handler
	return server, nil
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() (http.Handler, error) {
	staticFS, err := fs.Sub(web.FS, "static")
	if err != nil {
		return nil, fmt.Errorf("api: mount static files: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/samples", s.handleSamples)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/ups", s.handleUPS)

	return s.withHeaders(mux), nil
}

func (s *Server) withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
		s.logger.Debug("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start).Round(time.Millisecond),
		)
	})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := struct {
		RefreshSeconds int
	}{
		RefreshSeconds: s.refreshSeconds,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		s.logger.Error("render dashboard", "error", err)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	response := map[string]any{"status": "ok", "database": "ok"}
	if err := s.store.Ping(ctx); err != nil {
		s.logger.Error("health check failed", "error", err)
		response["status"] = "degraded"
		response["database"] = "error"
		writeJSON(w, http.StatusServiceUnavailable, response)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

type statusResponse struct {
	Sample            *model.Sample          `json:"sample"`
	AgeSeconds        *float64               `json:"age_seconds,omitempty"`
	Stale             bool                   `json:"stale"`
	StaleAfterSeconds float64                `json:"stale_after_seconds"`
	Collector         *model.CollectorStatus `json:"collector,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	response := statusResponse{
		Stale:             true,
		StaleAfterSeconds: s.staleAfter.Seconds(),
	}
	if s.status != nil {
		status := s.status.Status()
		response.Collector = &status
	}

	sample, err := s.store.LatestSample(ctx)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// No samples yet: leave Sample nil and report stale.
	case err != nil:
		s.serverError(w, "read latest sample", err)
		return
	default:
		response.Sample = &sample
		age := time.Since(sample.Timestamp)
		if age < 0 {
			age = 0
		}
		ageSeconds := age.Seconds()
		response.AgeSeconds = &ageSeconds
		response.Stale = age > s.staleAfter
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleSamples(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	from, to, err := parseWindow(r.URL.Query(), 24)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	limit := 2000
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 10000 {
			writeError(w, http.StatusBadRequest, "limit must be an integer between 1 and 10000")
			return
		}
		limit = parsed
	}

	samples, err := s.store.Samples(ctx, from, to, limit)
	if err != nil {
		s.serverError(w, "query samples", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"samples": samples,
		"count":   len(samples),
		"from":    from,
		"to":      to,
		"hours":   to.Sub(from).Hours(),
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	from, to, err := parseWindow(r.URL.Query(), 24)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	stats, err := s.store.Stats(ctx, from, to)
	if err != nil {
		s.serverError(w, "query stats", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"from":  from,
		"to":    to,
		"hours": to.Sub(from).Hours(),
		"stats": stats,
	})
}

func (s *Server) handleUPS(w http.ResponseWriter, r *http.Request) {
	if s.nut == nil {
		writeError(w, http.StatusServiceUnavailable, "NUT client is not configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	upses, err := s.nut.ListUPS(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cannot query NUT: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ups": upses})
}

func (s *Server) serverError(w http.ResponseWriter, operation string, err error) {
	s.logger.Error(operation, "error", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func parseWindow(query url.Values, defaultHours float64) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	to := now

	if raw := query.Get("until"); raw != "" {
		parsed, err := parseTimestamp(raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid until: %w", err)
		}
		to = parsed
	}

	accepted := map[string]bool{
		"since": true, "until": true, "hours": true, "limit": true,
	}
	for key := range query {
		if !accepted[key] {
			return time.Time{}, time.Time{}, fmt.Errorf("unknown query parameter %q", key)
		}
	}

	if raw := query.Get("since"); raw != "" {
		parsed, err := parseTimestamp(raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid since: %w", err)
		}
		if parsed.After(to) {
			return time.Time{}, time.Time{}, errors.New("since must be before until")
		}
		return parsed, to, nil
	}

	hours := defaultHours
	if raw := query.Get("hours"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("hours must be a number")
		}
		hours = parsed
	}
	if hours <= 0 || hours > 24*366 {
		return time.Time{}, time.Time{}, errors.New("hours must be between 0 and 8784")
	}

	return to.Add(-time.Duration(hours * float64(time.Hour))), to, nil
}

func parseTimestamp(raw string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed.UTC(), nil
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(seconds, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("expected an RFC3339 timestamp or unix seconds, got %q", raw)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
