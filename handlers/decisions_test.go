package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type stub struct {
	calls  [][]string
	stdout map[string]string
	err    error
	stderr string
}

func (s *stub) run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	s.calls = append(s.calls, args)
	if args[0] == "list" || s.err == nil {
		if args[0] == "list" {
			return []byte(s.stdout["list"]), nil, nil
		}
		return nil, nil, nil
	}
	return nil, []byte(s.stderr), s.err
}

func doDecision(h DecisionHandlers, method, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /claude-techniques/api/decisions", h.List)
	mux.HandleFunc("POST /claude-techniques/api/decisions/{id}", h.Post)
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestDecisionList(t *testing.T) {
	s := &stub{stdout: map[string]string{"list": `[{"techniqueId":3,"decision":"adopted","decidedAt":"x"}]`}}
	w := doDecision(DecisionHandlers{Run: s.run}, "GET", "/claude-techniques/api/decisions", "")
	assert.Equal(t, 200, w.Code)
	assert.JSONEq(t, s.stdout["list"], w.Body.String())
	assert.Equal(t, [][]string{{"list", "--json"}}, s.calls)
}

func TestDecisionPostArgs(t *testing.T) {
	s := &stub{stdout: map[string]string{"list": `[{"techniqueId":3,"decision":"rejected","decidedAt":"x"}]`}}
	w := doDecision(DecisionHandlers{Run: s.run}, "POST", "/claude-techniques/api/decisions/3", `{"decision":"rejected"}`)
	assert.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"techniqueId":3,"decision":"rejected","decidedAt":"x"}`, w.Body.String())
	assert.Equal(t, []string{"reject", "3"}, s.calls[0])

	s = &stub{stdout: map[string]string{"list": `[{"techniqueId":3}]`}}
	doDecision(DecisionHandlers{Run: s.run}, "POST", "/claude-techniques/api/decisions/3", `{"decision":"adopted"}`)
	assert.Equal(t, []string{"adopt", "3"}, s.calls[0])
}

func TestDecisionPostValidation(t *testing.T) {
	cases := []struct{ path, body string }{
		{"/claude-techniques/api/decisions/0", `{"decision":"adopted"}`},
		{"/claude-techniques/api/decisions/-1", `{"decision":"adopted"}`},
		{"/claude-techniques/api/decisions/abc", `{"decision":"adopted"}`},
		{"/claude-techniques/api/decisions/1.5", `{"decision":"adopted"}`},
		{"/claude-techniques/api/decisions/1;ls", `{"decision":"adopted"}`},
		{"/claude-techniques/api/decisions/1", `{"decision":"proposed"}`},
		{"/claude-techniques/api/decisions/1", `{"decision":"adopt"}`},
		{"/claude-techniques/api/decisions/1", `{}`},
		{"/claude-techniques/api/decisions/1", `nope`},
	}
	for _, c := range cases {
		s := &stub{}
		w := doDecision(DecisionHandlers{Run: s.run}, "POST", c.path, c.body)
		assert.Equal(t, 400, w.Code, c.path+" "+c.body)
		assert.Empty(t, s.calls, c.path+" "+c.body)
	}
}

func TestDecisionExitMapping(t *testing.T) {
	exitErr := exec.Command("sh", "-c", "exit 1").Run()
	s := &stub{err: exitErr, stderr: "[decision] unknown technique 99\n"}
	w := doDecision(DecisionHandlers{Run: s.run}, "POST", "/claude-techniques/api/decisions/99", `{"decision":"adopted"}`)
	assert.Equal(t, 422, w.Code)
	assert.Contains(t, w.Body.String(), "unknown technique 99")

	failing := func(ctx context.Context, args ...string) ([]byte, []byte, error) {
		return nil, []byte("boom"), exitErr
	}
	w = doDecision(DecisionHandlers{Run: failing}, "GET", "/claude-techniques/api/decisions", "")
	assert.Equal(t, 422, w.Code)
	assert.Contains(t, w.Body.String(), "boom")

	missing := func(ctx context.Context, args ...string) ([]byte, []byte, error) {
		return nil, nil, exec.ErrNotFound
	}
	w = doDecision(DecisionHandlers{Run: missing}, "GET", "/claude-techniques/api/decisions", "")
	assert.Equal(t, 500, w.Code)
}

func TestDecisionTimeout(t *testing.T) {
	hang := func(ctx context.Context, args ...string) ([]byte, []byte, error) {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	w := doDecision(DecisionHandlers{Run: hang, Timeout: 20 * time.Millisecond}, "GET", "/claude-techniques/api/decisions", "")
	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
}

func TestProxiedLoopbackOnly(t *testing.T) {
	h := ProxiedLoopbackOnly(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	call := func(remote string, headers map[string]string) int {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w.Code
	}
	assert.Equal(t, http.StatusTeapot, call("127.0.0.1:1", nil))
	assert.Equal(t, http.StatusTeapot, call("[::1]:1", nil))
	assert.Equal(t, http.StatusTeapot, call("127.0.0.1:1", map[string]string{"X-Forwarded-For": "8.8.8.8"}))
	assert.Equal(t, http.StatusForbidden, call("8.8.8.8:1", nil))
	assert.Equal(t, http.StatusForbidden, call("192.0.2.10:1", map[string]string{"X-Forwarded-For": "127.0.0.1"}))
}

func TestExecDecisionRunnerArgvAndDir(t *testing.T) {
	dir := t.TempDir()
	run := ExecDecisionRunner("/bin/echo", dir)
	out, _, err := run(context.Background(), "adopt", "3")
	assert.NoError(t, err)
	assert.Equal(t, DecisionScript+" adopt 3\n", string(out))
}
