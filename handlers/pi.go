package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"go-server/models"
	"go-server/utils"
)

// The Pi endpoints answer in the shapes the old Node webserver used, because
// pi_health, amazon-scraper, monitor-canvas and the zigzag game still read them.

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// LocalOnly refuses requests that do not come from this machine. A request
// that came through a proxy is refused too, since nginx connects from loopback
// on behalf of anyone.
func LocalOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" || !isOwnAddress(r.RemoteAddr) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Access denied"})
			return
		}
		next(w, r)
	}
}

// isOwnAddress reports whether remoteAddr is loopback or one of this machine's
// interface addresses, which is how pi_health reaches raspberrypi.local.
func isOwnAddress(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// present mirrors the JavaScript truthiness the Node server required of each
// field: missing, null, false, 0 and "" are all absent.
func present(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	}
	return true
}

type systemInfoRequest struct {
	Temperature any `json:"temperature"`
	CPUUsage    any `json:"cpuUsage"`
	MemoryUsage *struct {
		UsedMemory  any `json:"usedMemory"`
		TotalMemory any `json:"totalMemory"`
	} `json:"memoryUsage"`
	DiskUsage *struct {
		Used      any `json:"used"`
		Available any `json:"available"`
	} `json:"diskUsage"`
	DiskActivity *struct {
		ReadSpeed  any `json:"readSpeed"`
		WriteSpeed any `json:"writeSpeed"`
	} `json:"diskActivity"`
}

func PostSystemInfoHandler(w http.ResponseWriter, r *http.Request) {
	var req systemInfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		!present(req.Temperature) || !present(req.CPUUsage) ||
		req.MemoryUsage == nil || req.DiskUsage == nil || req.DiskActivity == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Missing required fields"})
		return
	}

	id, err := models.AddSystemInfo(models.SystemInfo{
		Temperature:    req.Temperature,
		CPUUsage:       req.CPUUsage,
		MemoryUsed:     req.MemoryUsage.UsedMemory,
		MemoryTotal:    req.MemoryUsage.TotalMemory,
		DiskUsed:       req.DiskUsage.Used,
		DiskAvailable:  req.DiskUsage.Available,
		DiskReadSpeed:  req.DiskActivity.ReadSpeed,
		DiskWriteSpeed: req.DiskActivity.WriteSpeed,
	})
	if err != nil {
		log.Printf("AddSystemInfo failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to insert record"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": "Record added successfully", "recordId": id})
}

const (
	defaultSystemInfoLimit = 60
	maxSystemInfoLimit     = 10080 // a week of one-minute readings
)

// systemInfoLimit reads a requested number of readings. Anything unreadable or
// below 1 means the default, and a week is the most that is ever returned.
func systemInfoLimit(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return defaultSystemInfoLimit
	}
	return min(n, maxSystemInfoLimit)
}

func SystemInfoHandler(w http.ResponseWriter, r *http.Request) {
	records, err := models.GetLastSystemInfo(systemInfoLimit(r.PathValue("limit")))
	if err != nil {
		log.Printf("GetLastSystemInfo failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to retrieve records"})
		return
	}
	writeJSON(w, http.StatusOK, records)
}

// systemInfoStreamInterval matches how often pi_health reports.
var systemInfoStreamInterval = time.Minute

// SystemInfoStreamHandler sends the newest readings as server-sent events,
// straight away and then every minute until the client goes.
func SystemInfoStreamHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}
	limit := systemInfoLimit(r.URL.Query().Get("limit"))

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	log.Printf("System-info: client connected, streaming %d readings", limit)

	send := func() {
		records, err := models.GetLastSystemInfo(limit)
		if err != nil {
			log.Printf("GetLastSystemInfo failed: %v", err)
			fmt.Fprint(w, "event: error\ndata: {\"error\":\"Failed to retrieve records\"}\n\n")
		} else {
			data, _ := json.Marshal(records)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		flusher.Flush()
	}

	send()
	ticker := time.NewTicker(systemInfoStreamInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			log.Println("System-info: client disconnected")
			return
		case <-ticker.C:
			send()
		}
	}
}

func PostAmazonPriceHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL       string `json:"url"`
		Title     string `json:"title"`
		Price     string `json:"price"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.URL == "" || req.Title == "" || req.Price == "" || req.Timestamp == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Missing required fields"})
		return
	}

	id, err := models.AddAmazonPrice(req.URL, req.Title, req.Price, req.Timestamp)
	if err != nil {
		log.Printf("AddAmazonPrice failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to insert price record"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": "Price recorded", "id": id})
}

func LastAmazonPriceHandler(w http.ResponseWriter, r *http.Request) {
	productURL := r.URL.Query().Get("url")
	if productURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Missing URL"})
		return
	}
	price, err := models.GetLastAmazonPrice(productURL)
	if err != nil {
		log.Printf("GetLastAmazonPrice failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to get last price"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": productURL, "lastPrice": price})
}

// zigzagOrigins are the scheme and host the zigzag game is served from: nginx
// on the LAN and publicly. A score posted from anywhere else is refused.
var zigzagOrigins = []string{
	"http://raspberrypi.local",
	"https://alexisraspberry.duckdns.org",
}

func zigzagOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return slices.Contains(zigzagOrigins, u.Scheme+"://"+u.Hostname())
}

// maxZigzagScore is far above any real game (the best so far is about 70,000),
// so only made-up scores are refused.
const maxZigzagScore = 1_000_000

var zigzagScoreLimiter = utils.NewRateLimiter(6, time.Minute)

// zigzagClientIP is the address nginx saw (X-Real-IP, which it overwrites), or
// the connection's own address for a request that did not come through nginx.
// X-Forwarded-For is not used: nginx appends to it, so a client controls its
// first entry.
func zigzagClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" && isOwnAddress(r.RemoteAddr) {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// zigzagScore reads a posted score, a number or a numeric string, dropping any
// fraction. Zero, negative and implausibly high scores count as no score.
func zigzagScore(v any) (int, bool) {
	var n int
	switch v := v.(type) {
	case float64:
		if math.IsInf(v, 0) || math.Abs(v) > math.MaxInt32 {
			return 0, false
		}
		n = int(v)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, false
		}
		n = parsed
	default:
		return 0, false
	}
	return n, n > 0 && n <= maxZigzagScore
}

func PostZigzagScoreHandler(w http.ResponseWriter, r *http.Request) {
	if !zigzagOriginAllowed(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Invalid origin or referer"})
		return
	}
	if !zigzagScoreLimiter.Allow(zigzagClientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Too many scores, try again later"})
		return
	}
	var req struct {
		Score any `json:"score"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	score, ok := zigzagScore(req.Score)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Score is required"})
		return
	}

	if err := models.AddZigzagScore(score); err != nil {
		log.Printf("AddZigzagScore failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Error adding score"})
		return
	}
	log.Printf("Zigzag Game: Highscore added: %d", score)
	writeJSON(w, http.StatusCreated, map[string]string{"message": fmt.Sprintf("Highscore added: %d", score)})
}

func ZigzagScoresHandler(w http.ResponseWriter, r *http.Request) {
	scores, err := models.GetTopZigzagScores(10)
	if err != nil {
		log.Printf("GetTopZigzagScores failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to get scores"})
		return
	}
	writeJSON(w, http.StatusOK, scores)
}
