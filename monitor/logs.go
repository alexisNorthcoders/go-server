package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
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

// errNotRegular marks a log path that is not a regular file, such as /dev/null.
var errNotRegular = errors.New("not a regular file")

// unreadable reports whether err means the log file itself is missing, not
// permitted or not a regular file, as opposed to a failure while reading it.
func unreadable(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) || errors.Is(err, errNotRegular)
}

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
		// A path that is briefly missing mid-rotation is retried next check.
		if pi, perr := os.Stat(path); perr == nil && read < maxPollBytes && !os.SameFile(fi, pi) {
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
		err = fmt.Errorf("%s: %w", path, errNotRegular)
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
	if end == 0 && pos > 0 { // no line boundary in the window: follow from the end
		return out, size, nil
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

func (m *Monitor) hasService(group, name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.services {
		if s.Group == group && s.Name == name {
			return true
		}
	}
	return false
}

// logRequest checks ?stream= and ?lines= and returns the Flusher the stream
// needs. When it returns false the response has been written.
func logRequest(w http.ResponseWriter, r *http.Request) (stream string, lines int, flusher http.Flusher, ok bool) {
	stream = r.URL.Query().Get("stream")
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
	flusher, ok = w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
	}
	return
}

// startEvents sets the event-stream headers and returns the function that
// sends one lines event.
func startEvents(w http.ResponseWriter, flusher http.Flusher) func([]string) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	return func(lines []string) error {
		b, _ := json.Marshal(map[string][]string{"lines": lines})
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
}

func sendGone(w http.ResponseWriter, flusher http.Flusher, message string) {
	b, _ := json.Marshal(map[string]string{"message": message})
	fmt.Fprintf(w, "event: gone\ndata: %s\n\n", b)
	flusher.Flush()
}

// PM2LogsHandler streams a pm2 process's log as server-sent events: the last
// ?lines= (200 by default, 1000 at most) lines, then new ones as they are
// written, from ?stream=out (default) or err. Nothing is stored; the file is
// only followed while the client is connected.
func (m *Monitor) PM2LogsHandler(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !m.hasService("pm2", name) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Unknown pm2 process"})
		return
	}
	stream, lines, flusher, ok := logRequest(w, r)
	if !ok {
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

	send := startEvents(w, flusher)
	err = followFile(r.Context(), path, lines, send)
	if err != nil && r.Context().Err() == nil {
		if !unreadable(err) {
			log.Printf("monitor: following %q: %v", path, err)
			return
		}
		sendGone(w, flusher, "Log file is missing or not readable")
	}
}

// logFlush is how often lines read from docker are sent together.
var logFlush = time.Second

// dockerLogsCmd builds the command that follows a container's logs. The name
// comes after "--" so it can never be read as a flag. A variable so tests can
// replace it.
var dockerLogsCmd = func(ctx context.Context, name string, lines int) *exec.Cmd {
	return exec.CommandContext(ctx, "docker", "logs", "--follow", "--tail", strconv.Itoa(lines), "--", name)
}

// followDocker runs docker logs for a container and sends its lines through
// send, batched each logFlush, until ctx is done (it returns nil) or docker
// logs exits or send fails (it returns that error). The process is killed
// and waited for before it returns.
func followDocker(ctx context.Context, name, stream string, lines int, send func([]string) error) error {
	cmd := dockerLogsCmd(ctx, name, lines)
	// docker logs writes the container's stdout and stderr to its own, and
	// the one not asked for goes to /dev/null. A container started with a TTY
	// sends everything to stdout, so its Errors tab stays empty.
	var pipe io.ReadCloser
	var err error
	if stream == "err" {
		pipe, err = cmd.StderrPipe()
	} else {
		pipe, err = cmd.StdoutPipe()
	}
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	readErr := followReader(ctx, pipe, send)
	// Reads are over: kill (a no-op if it has exited) and reap.
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if readErr != nil && readErr != io.EOF {
		return readErr
	}
	if waitErr != nil {
		return waitErr
	}
	return io.EOF
}

// followReader splits what it reads from r into lines and sends the ones that
// arrived within each logFlush together. It returns nil when ctx is done, and
// otherwise when r ends (io.EOF, after sending what is left) or send fails.
func followReader(ctx context.Context, r io.Reader, send func([]string) error) error {
	type chunk struct {
		lines []string
		err   error
	}
	ch := make(chan chunk)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		var partial []byte
		skipping := false
		buf := make([]byte, logChunk)
		for {
			n, err := r.Read(buf)
			var out []string
			if n > 0 {
				out, partial, skipping = splitLines(nil, partial, buf[:n], skipping)
			}
			if err != nil && len(partial) > 0 {
				out = append(out, cleanLine(partial))
			}
			if len(out) > 0 || err != nil {
				select {
				case ch <- chunk{out, err}:
				case <-stop:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	var pending []string
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		out := pending
		pending = nil
		return send(out)
	}
	t := time.NewTicker(logFlush)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := flush(); err != nil {
				return err
			}
		case c := <-ch:
			pending = append(pending, c.lines...)
			if c.err != nil {
				if err := flush(); err != nil {
					return err
				}
				return c.err
			}
		}
	}
}

// DockerLogsHandler streams a Docker container's logs as server-sent events,
// in the shape PM2LogsHandler uses. Nothing is stored; docker logs runs only
// while the client is connected.
func (m *Monitor) DockerLogsHandler(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !m.hasService("docker", name) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Unknown container"})
		return
	}
	stream, lines, flusher, ok := logRequest(w, r)
	if !ok {
		return
	}
	send := startEvents(w, flusher)
	err := followDocker(r.Context(), name, stream, lines, send)
	if err != nil && r.Context().Err() == nil {
		msg := "Container's logs ended"
		if err != io.EOF {
			msg = fmt.Sprintf("docker logs stopped: %v", err)
		}
		sendGone(w, flusher, msg)
	}
}
