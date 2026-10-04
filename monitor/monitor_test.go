package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStatusChangesNeedTwoChecksInARow(t *testing.T) {
	m := &Monitor{recorded: map[string]string{"pm2/bot": StatusUp}, pending: map[string]pendingStatus{}}
	svc := func(status string) []Service { return []Service{{Group: "pm2", Name: "bot", Status: status}} }

	assert.Empty(t, m.statusChanges(svc(StatusUp), 1))
	assert.Empty(t, m.statusChanges(svc(StatusDown), 2), "one failed check is not recorded")
	assert.Empty(t, m.statusChanges(svc(StatusUp), 3), "and it recovered")
	assert.Empty(t, m.statusChanges(svc(StatusDown), 4))

	events := m.statusChanges(svc(StatusDown), 5)
	assert.Equal(t, []Event{{TS: 5, Group: "pm2", Name: "bot", Status: StatusDown}}, events)
	assert.Empty(t, m.statusChanges(svc(StatusDown), 6))
}

func TestStatusChangesRecordNewServicesStraightAway(t *testing.T) {
	m := &Monitor{recorded: map[string]string{}, pending: map[string]pendingStatus{}}
	events := m.statusChanges([]Service{{Group: "docker", Name: "joplin", Status: StatusUp}}, 1)
	assert.Len(t, events, 1)
}

func TestStreamSendsSnapshotStraightAwayAndOnBroadcast(t *testing.T) {
	m := &Monitor{
		services: []Service{{Group: "redis", Name: "redis", Status: StatusUp}},
		latest:   cpuSample(60, 12),
		subs:     map[chan []byte]struct{}{},
	}
	srv := httptest.NewServer(http.HandlerFunc(m.StreamHandler))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	resp, err := srv.Client().Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))

	rd := bufio.NewReader(resp.Body)
	next := func() snapshotJSON {
		for {
			line, err := rd.ReadString('\n')
			assert.NoError(t, err)
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				var s snapshotJSON
				assert.NoError(t, json.Unmarshal([]byte(data), &s))
				return s
			}
		}
	}

	first := next()
	assert.Equal(t, 12.0, first.Sample["cpu"])
	assert.Equal(t, "redis", first.Services[0].Name)

	// Wait until the handler has subscribed before broadcasting.
	for range 100 {
		m.subsMu.Lock()
		n := len(m.subs)
		m.subsMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.mu.Lock()
	m.latest = cpuSample(120, 34)
	m.mu.Unlock()
	m.broadcast()
	assert.Equal(t, 34.0, next().Sample["cpu"])
}

type snapshotJSON struct {
	Sample   map[string]any `json:"sample"`
	Services []Service      `json:"services"`
}
