package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLogLines = 200
	maxLogLines     = 1000
	maxLogLine      = 8 << 10  // longer lines are cut
	logChunk        = 64 << 10 // read size, backwards and forwards
	maxTailBytes    = 8 << 20  // the most the first event reads
	maxPollBytes    = 4 << 20  // the most one check reads
)

// logPoll is how often a followed file is checked.
var logPoll = time.Second

// followFile sends the last lines of the file at path through send, then any
// new complete lines each logPoll, until ctx is done (it returns nil) or the
// file cannot be read or send fails (it returns that error). It keeps nothing
// but the file handle and a partial line, and reads the end by seeking.
func followFile(ctx context.Context, path string, lines int, send func([]string) error) error {
	f, err := openLog(path)
	if err != nil {
		return err
	}
	defer func() { f.Close() }()

	first, offset, err := tailLines(f, lines)
	if err != nil {
		return err
	}
	if err := send(first); err != nil {
		return err
	}

	var partial []byte
	skipping := false // inside a line already cut at maxLogLine
	t := time.NewTicker(logPoll)
	defer t.Stop()
	buf := make([]byte, logChunk)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		var out []string
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		if fi.Size() < offset { // truncated, as by pm2 flush
			offset, partial, skipping = 0, nil, false
		}
		read := 0
		for read < maxPollBytes {
			n, err := f.ReadAt(buf, offset)
			if n > 0 {
				offset += int64(n)
				read += n
				out, partial, skipping = splitLines(out, partial, buf[:n], skipping)
			}
			if err != nil {
				if err != io.EOF {
					return err
				}
				break
			}
		}
		// A different file at the path means rotation; the old one has been
		// read to its end by now.
		pi, perr := os.Stat(path)
		if perr != nil {
			return perr
		}
		if read < maxPollBytes && !os.SameFile(fi, pi) {
			nf, err := openLog(path)
			if err != nil {
				return err
			}
			f.Close()
			f, offset, partial, skipping = nf, 0, nil, false
		}
		if len(out) > 0 {
			if err := send(out); err != nil {
				return err
			}
		}
	}
}

func openLog(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err == nil && !fi.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", path)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// tailLines reads backwards from the end of f until it has the last n
// complete lines, and returns them with the offset just after the last one.
func tailLines(f *os.File, n int) ([]string, int64, error) {
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, 0, err
	}
	var data []byte
	pos := size
	for pos > 0 && len(data) < maxTailBytes && bytes.Count(data, []byte{'\n'}) <= n {
		step := min(int64(logChunk), pos)
		chunk := make([]byte, step)
		if _, err := f.ReadAt(chunk, pos-step); err != nil {
			return nil, 0, err
		}
		pos -= step
		data = append(chunk, data...)
	}
	end := bytes.LastIndexByte(data, '\n') + 1 // a trailing partial line waits
	data = data[:end]
	if pos > 0 { // the first line may have begun before what was read
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			data = nil
		} else {
			data = data[i+1:]
		}
	}
	out, _, _ := splitLines(nil, nil, data, false)
	if len(out) > n {
		out = out[len(out)-n:]
	}
	if out == nil {
		out = []string{}
	}
	return out, pos + int64(end), nil
}

// splitLines appends the complete lines in partial+data to out and returns
// the rest as the new partial line. A line past maxLogLine is cut and the
// remainder of it dropped (skipping).
func splitLines(out []string, partial, data []byte, skipping bool) ([]string, []byte, bool) {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if !skipping {
				partial = append(partial, data...)
				if len(partial) > maxLogLine {
					partial, skipping = partial[:maxLogLine], true
					// emitted at the newline
				}
			}
			return out, partial, skipping
		}
		if !skipping {
			partial = append(partial, data[:i]...)
		}
		out = append(out, cleanLine(partial))
		partial, skipping = partial[:0], false
		data = data[i+1:]
	}
	return out, partial, skipping
}

func cleanLine(b []byte) string {
	if len(b) > maxLogLine {
		b = b[:maxLogLine]
	}
	return strings.ToValidUTF8(strings.TrimSuffix(string(b), "\r"), "�")
}

// pm2LogPaths asks pm2 for a process's log files. A variable so tests can
// replace it.
var pm2LogPaths = func(ctx context.Context, name string) (outPath, errPath string, err error) {
	out, err := run(ctx, "pm2", "jlist")
	if err != nil {
		return "", "", err
	}
	// Only the two paths are read: jlist also carries each process's
	// environment, secrets included.
	var procs []struct {
		Name string `json:"name"`
		Env  struct {
			Out string `json:"pm_out_log_path"`
			Err string `json:"pm_err_log_path"`
		} `json:"pm2_env"`
	}
	if err := json.Unmarshal(pm2JSON(out), &procs); err != nil {
		return "", "", fmt.Errorf("reading pm2 jlist: %w", err)
	}
	for _, p := range procs {
		if p.Name == name {
			return p.Env.Out, p.Env.Err, nil
		}
	}
	return "", "", fmt.Errorf("pm2 process %q not found", name)
}

func (m *Monitor) hasPM2(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.services {
		if s.Group == "pm2" && s.Name == name {
			return true
		}
	}
	return false
}

// PM2LogsHandler streams a pm2 process's log as server-sent events: the last
// ?lines= (200 by default, 1000 at most) lines, then new ones as they are
// written, from ?stream=out (default) or err. Nothing is stored; the file is
// only followed while the client is connected.
func (m *Monitor) PM2LogsHandler(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !m.hasPM2(name) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Unknown pm2 process"})
		return
	}
	stream := r.URL.Query().Get("stream")
	if stream == "" {
		stream = "out"
	}
	if stream != "out" && stream != "err" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stream must be out or err"})
		return
	}
	lines, err := strconv.Atoi(r.URL.Query().Get("lines"))
	if err != nil || lines < 1 {
		lines = defaultLogLines
	}
	lines = min(lines, maxLogLines)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}
	outPath, errPath, err := pm2LogPaths(r.Context(), name)
	if err != nil {
		log.Printf("monitor: pm2 log paths for %q: %v", name, err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Unknown pm2 process"})
		return
	}
	path := outPath
	if stream == "err" {
		path = errPath
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	send := func(lines []string) error {
		b, _ := json.Marshal(map[string][]string{"lines": lines})
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	err = followFile(r.Context(), path, lines, send)
	if err != nil && r.Context().Err() == nil {
		b, _ := json.Marshal(map[string]string{"message": "Log is not readable: " + err.Error()})
		fmt.Fprintf(w, "event: gone\ndata: %s\n\n", b)
		flusher.Flush()
	}
}
