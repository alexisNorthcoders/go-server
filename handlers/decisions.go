package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// DecisionScript is reddit-bot's Decision command, relative to REDDIT_BOT_DIR.
const DecisionScript = "src/claudeai/decisionMain.ts"

const decisionTimeout = 15 * time.Second

// CommandRunner runs the Decision command with args and returns its stdout and
// stderr. A non-zero exit is reported as an *exec.ExitError.
type CommandRunner func(ctx context.Context, args ...string) (stdout, stderr []byte, err error)

// ExecDecisionRunner runs reddit-bot's Decision command with node, without a
// shell, from the reddit-bot checkout in dir.
func ExecDecisionRunner(node, dir string) CommandRunner {
	return func(ctx context.Context, args ...string) ([]byte, []byte, error) {
		cmd := exec.CommandContext(ctx, node, append([]string{DecisionScript}, args...)...)
		cmd.Dir = dir
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}
}

// ProxiedLoopbackOnly accepts only requests whose connection came from this
// machine, with or without proxy headers. That is how requests arrive through
// nginx, which LocalOnly refuses.
func ProxiedLoopbackOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isOwnAddress(r.RemoteAddr) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Access denied"})
			return
		}
		next(w, r)
	}
}

// DecisionHandlers serve Claude Practice Decisions by running reddit-bot's
// Decision command through run.
type DecisionHandlers struct {
	Run     CommandRunner
	Timeout time.Duration
}

func (d DecisionHandlers) call(r *http.Request, args ...string) ([]byte, int, string) {
	timeout := d.Timeout
	if timeout == 0 {
		timeout = decisionTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	stdout, stderr, err := d.Run(ctx, args...)
	switch {
	case err == nil:
		return stdout, http.StatusOK, ""
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return nil, http.StatusGatewayTimeout, "decision command timed out"
	}
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		msg = err.Error()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		// The command rejects ids it does not know with a non-zero exit.
		return nil, http.StatusUnprocessableEntity, msg
	}
	return nil, http.StatusInternalServerError, msg
}

func (d DecisionHandlers) List(w http.ResponseWriter, r *http.Request) {
	out, status, msg := d.call(r, "list", "--json")
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	if !json.Valid(out) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "decision command printed invalid JSON"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

func (d DecisionHandlers) Post(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id must be a positive integer"})
		return
	}
	var req struct {
		Decision string `json:"decision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	verb := map[string]string{"adopted": "adopt", "rejected": "reject"}[req.Decision]
	if verb == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `decision must be "adopted" or "rejected"`})
		return
	}
	idArg := strconv.Itoa(id)
	if _, status, msg := d.call(r, verb, idArg); status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	out, status, msg := d.call(r, "list", "--json")
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	var all []struct {
		TechniqueID int `json:"techniqueId"`
	}
	var raw []json.RawMessage
	if json.Unmarshal(out, &all) != nil || json.Unmarshal(out, &raw) != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "decision command printed invalid JSON"})
		return
	}
	for i, e := range all {
		if e.TechniqueID == id {
			w.Header().Set("Content-Type", "application/json")
			w.Write(raw[i])
			return
		}
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "decision not found after recording"})
}
