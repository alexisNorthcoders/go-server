package monitor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type follower struct {
	events chan []string
	done   chan error
	cancel context.CancelFunc
}

func startFollow(t *testing.T, path string, lines int) *follower {
	t.Helper()
	old := logPoll
	logPoll = 10 * time.Millisecond
	t.Cleanup(func() { logPoll = old })
	ctx, cancel := context.WithCancel(context.Background())
	f := &follower{events: make(chan []string, 100), done: make(chan error, 1), cancel: cancel}
	go func() {
		f.done <- followFile(ctx, path, lines, func(l []string) error { f.events <- l; return nil })
	}()
	t.Cleanup(cancel)
	return f
}

func (f *follower) next(t *testing.T) []string {
	t.Helper()
	select {
	case l := <-f.events:
		return l
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
		return nil
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	fh.WriteString(s)
}

func TestTailLargerThanChunk(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&sb, "line %d %s\n", i, strings.Repeat("x", 40))
	}
	os.WriteFile(p, []byte(sb.String()), 0o644)
	if int(logChunk) >= sb.Len() {
		t.Fatal("file should be larger than a chunk")
	}
	f := startFollow(t, p, 3)
	got := f.next(t)
	want := []string{"line 4997 " + strings.Repeat("x", 40), "line 4998 " + strings.Repeat("x", 40), "line 4999 " + strings.Repeat("x", 40)}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestShortFileAndEmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	os.WriteFile(p, []byte("a\nb\n"), 0o644)
	if got := startFollow(t, p, 10).next(t); len(got) != 2 || got[0] != "a" {
		t.Fatalf("got %q", got)
	}
	q := filepath.Join(t.TempDir(), "e.log")
	os.WriteFile(q, nil, 0o644)
	if got := startFollow(t, q, 10).next(t); got == nil || len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestAppendedLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	os.WriteFile(p, []byte("one\n"), 0o644)
	f := startFollow(t, p, 10)
	f.next(t)
	appendTo(t, p, "two\nthr")
	if got := f.next(t); len(got) != 1 || got[0] != "two" {
		t.Fatalf("got %q", got)
	}
	appendTo(t, p, "ee\n")
	if got := f.next(t); len(got) != 1 || got[0] != "three" {
		t.Fatalf("got %q", got)
	}
	select {
	case l := <-f.events:
		t.Fatalf("unexpected event %q", l)
	case <-time.After(60 * time.Millisecond):
	}
}

func TestTruncation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0o644)
	f := startFollow(t, p, 10)
	f.next(t)
	os.Truncate(p, 0)
	time.Sleep(50 * time.Millisecond)
	appendTo(t, p, "new\n")
	if got := f.next(t); len(got) != 1 || got[0] != "new" {
		t.Fatalf("got %q", got)
	}
}

func TestRotation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	os.WriteFile(p, []byte("old\n"), 0o644)
	f := startFollow(t, p, 10)
	f.next(t)
	os.Rename(p, p+".1")
	// longer than the old file, so only the inode tells them apart
	os.WriteFile(p, []byte("fresh one\nfresh two\n"), 0o644)
	var got []string
	for len(got) < 2 {
		got = append(got, f.next(t)...)
	}
	if got[0] != "fresh one" || got[1] != "fresh two" {
		t.Fatalf("got %q", got)
	}
}

func TestOverlongAndInvalidLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	os.WriteFile(p, []byte(strings.Repeat("y", 3*maxLogLine)+"\nbad\xffbyte \x1b[31mred\x1b[0m\n"), 0o644)
	got := startFollow(t, p, 10).next(t)
	if len(got) != 2 || len(got[0]) != maxLogLine {
		t.Fatalf("lengths %d", len(got[0]))
	}
	if got[1] != "bad�byte \x1b[31mred\x1b[0m" {
		t.Fatalf("got %q", got[1])
	}
	// the same line arriving while following
	appendTo(t, p, strings.Repeat("z", 3*maxLogLine)+"\nnext\n")
	f := startFollow(t, p, 1)
	f.next(t)
	appendTo(t, p, strings.Repeat("q", 3*maxLogLine)+"\nafter\n")
	g := f.next(t)
	if len(g) != 2 || len(g[0]) != maxLogLine || g[1] != "after" {
		t.Fatalf("got %d lines", len(g))
	}
}

func TestClientDisconnect(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	os.WriteFile(p, []byte("a\n"), 0o644)
	f := startFollow(t, p, 10)
	f.next(t)
	f.cancel()
	select {
	case err := <-f.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("did not stop")
	}
	if runtime.GOOS == "linux" {
		ents, _ := os.ReadDir("/proc/self/fd")
		for _, e := range ents {
			if l, _ := os.Readlink("/proc/self/fd/" + e.Name()); l == p {
				t.Fatal("file handle left open")
			}
		}
	}
}

func TestUnreadableIsGone(t *testing.T) {
	for _, p := range []string{filepath.Join(t.TempDir(), "missing"), "/dev/null"} {
		err := followFile(context.Background(), p, 10, func([]string) error { return nil })
		if err == nil {
			t.Fatalf("%s: want error", p)
		}
	}
}

func logsMonitor(t *testing.T, outPath, errPath string) *Monitor {
	t.Helper()
	old := pm2LogPaths
	pm2LogPaths = func(_ context.Context, name string) (string, string, error) { return outPath, errPath, nil }
	t.Cleanup(func() { pm2LogPaths = old })
	return &Monitor{services: []Service{{Group: "pm2", Name: "app"}, {Group: "docker", Name: "box"}}}
}

func serve(m *Monitor, url string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	m.Register(mux, func(h http.HandlerFunc, _ string) http.HandlerFunc { return h })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
	return rec
}

func TestPM2LogsHandler(t *testing.T) {
	dir := t.TempDir()
	outP, errP := filepath.Join(dir, "out.log"), filepath.Join(dir, "err.log")
	os.WriteFile(outP, []byte("o1\n"), 0o644)
	os.WriteFile(errP, []byte("e1\ne2\n"), 0o644)
	m := logsMonitor(t, outP, errP)

	for _, u := range []string{"/monitor/logs/pm2/nope", "/monitor/logs/pm2/box", "/monitor/logs/pm2/..%2F..%2Fetc%2Fpasswd"} {
		if rec := serve(m, u); rec.Code != 404 {
			t.Errorf("%s: status %d", u, rec.Code)
		}
	}

	// A cancelled request gets the first event and ends.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mux := http.NewServeMux()
	m.Register(mux, func(h http.HandlerFunc, _ string) http.HandlerFunc { return h })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/monitor/logs/pm2/app?stream=err&lines=1", nil).WithContext(ctx))
	if rec.Header().Get("Content-Type") != "text/event-stream" || rec.Body.String() != "data: {\"lines\":[\"e2\"]}\n\n" {
		t.Fatalf("%q", rec.Body.String())
	}

	m = logsMonitor(t, "/dev/null", "/dev/null")
	rec = serve(m, "/monitor/logs/pm2/app")
	if !strings.Contains(rec.Body.String(), "event: gone\ndata: {\"message\":") {
		t.Fatalf("%q", rec.Body.String())
	}
}
