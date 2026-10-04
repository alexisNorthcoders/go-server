package monitor

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
)

// The dashboard is served by nginx on port 80 and reads these on port 8080,
// so every response allows any origin. They are only registered on the Pi,
// which nginx never proxies them for, so they stay on the LAN.

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Register adds the monitor's endpoints to mux.
func (m *Monitor) Register(mux *http.ServeMux, wrap func(http.HandlerFunc, string) http.HandlerFunc) {
	mux.HandleFunc("GET /monitor/stream", wrap(m.StreamHandler, "/monitor/stream"))
	mux.HandleFunc("GET /monitor/history", wrap(m.HistoryHandler, "/monitor/history"))
	mux.HandleFunc("GET /monitor/events", wrap(m.EventsHandler, "/monitor/events"))
	mux.HandleFunc("GET /monitor/storage", wrap(m.StorageHandler, "/monitor/storage"))
}

// StreamHandler sends a snapshot as a server-sent event straight away and
// again after every reading and every service check, until the client goes.
func (m *Monitor) StreamHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := m.subscribe()
	defer m.unsubscribe(ch)

	fmt.Fprintf(w, "retry: 5000\ndata: %s\n\n", m.snapshot())
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case b := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}

const maxSpan = 2 * 365 * 24 * time.Hour

// parseSpan reads a span such as 15m, 6h, 7d or 1y. Days and years are not
// time.ParseDuration units, so the unit is read here.
func parseSpan(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid range %q", s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid range %q", s)
	}
	units := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'y': 365 * 24 * time.Hour}
	unit, ok := units[s[len(s)-1]]
	if !ok {
		return 0, fmt.Errorf("invalid range %q", s)
	}
	return min(time.Duration(n)*unit, maxSpan), nil
}

// HistoryHandler returns ?range= (1h by default) of readings in columns.
// Up to 15 minutes comes from the five-second readings in memory; longer
// spans from the finest stored tier that covers them.
func (m *Monitor) HistoryHandler(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "1h"
	}
	span, err := parseSpan(rng)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if span <= liveWindow {
		writeJSON(w, http.StatusOK, m.liveHistory())
		return
	}
	h, err := m.store.History(span, time.Now())
	if err != nil {
		log.Printf("monitor: history failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read history"})
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// EventsHandler returns the newest ?limit= (50 by default, 500 at most)
// service status changes.
func (m *Monitor) EventsHandler(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 50
	}
	events, err := m.store.Events(min(limit, 500))
	if err != nil {
		log.Printf("monitor: events failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read events"})
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// StorageHandler says how much the monitor is storing per tier.
func (m *Monitor) StorageHandler(w http.ResponseWriter, r *http.Request) {
	u, err := m.store.Usage()
	if err != nil {
		log.Printf("monitor: usage failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read usage"})
		return
	}
	writeJSON(w, http.StatusOK, u)
}
