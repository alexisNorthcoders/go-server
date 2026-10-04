// Package monitor watches the Raspberry Pi go-server runs on: it samples the
// machine every few seconds, checks the services the Pi runs, keeps a tiered
// history in its own SQLite file and serves it all to monitor-canvas.
package monitor

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"time"
)

// Config says what to watch and where to keep the history.
type Config struct {
	StorePath string   // METRICS_DB, ./metrics.db by default
	Units     []string // MONITOR_UNITS, the systemd units to check
	RedisAddr string   // REDIS_ADDR
	SQLite    []string // users.db, the store and MONITOR_SQLITE
}

func envList(name, fallback string) []string {
	v := os.Getenv(name)
	if v == "" {
		v = fallback
	}
	list := []string{}
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			list = append(list, item)
		}
	}
	return list
}

func ConfigFromEnv() Config {
	c := Config{
		StorePath: os.Getenv("METRICS_DB"),
		Units:     envList("MONITOR_UNITS", "nginx,redis-server,docker,ssh,cron,NetworkManager"),
		RedisAddr: os.Getenv("REDIS_ADDR"),
	}
	if c.StorePath == "" {
		c.StorePath = "./metrics.db"
	}
	if c.RedisAddr == "" {
		c.RedisAddr = "127.0.0.1:6379"
	}
	c.SQLite = append([]string{"./users.db", c.StorePath}, envList("MONITOR_SQLITE", "")...)
	return c
}

const (
	sampleEvery = 5 * time.Second
	checkEvery  = 30 * time.Second
	pruneEvery  = time.Hour
	// liveWindow is how much of the five-second readings is kept in memory
	// for the live chart. They are never stored.
	liveWindow = 15 * time.Minute
)

type pendingStatus struct {
	status string
	seen   int
}

type Monitor struct {
	store  *Store
	sys    sysReader
	checks []check

	mu          sync.RWMutex
	live        []Sample
	minute      []Sample
	minuteStart int64
	latest      Sample
	host        Host
	services    []Service
	recorded    map[string]string // the status last recorded per service
	pending     map[string]pendingStatus

	subsMu sync.Mutex
	subs   map[chan []byte]struct{}
}

func New(c Config) (*Monitor, error) {
	store, err := OpenStore(c.StorePath)
	if err != nil {
		return nil, err
	}
	recorded, err := store.LastStatuses()
	if err != nil {
		store.Close()
		return nil, err
	}
	return &Monitor{
		store: store,
		sys:   sysReader{root: "/"},
		checks: []check{
			systemdCheck(c.Units),
			pm2Check,
			dockerCheck,
			redisCheck(c.RedisAddr),
			sqliteCheck(c.SQLite),
		},
		services: []Service{},
		recorded: recorded,
		pending:  map[string]pendingStatus{},
		subs:     map[chan []byte]struct{}{},
	}, nil
}

// Run samples and checks until ctx is done.
func (m *Monitor) Run(ctx context.Context) {
	defer m.store.Close()
	if err := m.store.Prune(time.Now()); err != nil {
		log.Printf("monitor: prune failed: %v", err)
	}
	m.sample(time.Now())
	go m.checkLoop(ctx)

	samples := time.NewTicker(sampleEvery)
	prunes := time.NewTicker(pruneEvery)
	defer samples.Stop()
	defer prunes.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-samples.C:
			m.sample(now)
		case now := <-prunes.C:
			if err := m.store.Prune(now); err != nil {
				log.Printf("monitor: prune failed: %v", err)
			}
		}
	}
}

func (m *Monitor) checkLoop(ctx context.Context) {
	m.check(ctx, time.Now())
	t := time.NewTicker(checkEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.check(ctx, now)
		}
	}
}

// sample takes a reading, keeps it for the live chart and, when a minute has
// passed, stores that minute's average.
func (m *Monitor) sample(now time.Time) {
	s := m.sys.read(float64(now.UnixNano()) / 1e9)
	host := m.sys.host()
	minute := s.TS / 60 * 60

	m.mu.Lock()
	var flush []Sample
	if minute != m.minuteStart && len(m.minute) > 0 {
		flush = m.minute
		m.minute = nil
	}
	m.minuteStart = minute
	m.minute = append(m.minute, s)
	m.live = append(m.live, s)
	cutoff := s.TS - int64(liveWindow.Seconds())
	for len(m.live) > 0 && m.live[0].TS < cutoff {
		m.live = m.live[1:]
	}
	m.latest, m.host = s, host
	m.mu.Unlock()

	if len(flush) > 0 {
		if err := m.store.AddSample(meanSample(flush[0].TS/60*60, flush)); err != nil {
			log.Printf("monitor: storing reading failed: %v", err)
		}
	}
	m.broadcast()
}

// check runs every service check, records status changes and publishes the
// result.
func (m *Monitor) check(ctx context.Context, now time.Time) {
	var wg sync.WaitGroup
	results := make([][]Service, len(m.checks))
	for i, c := range m.checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c(ctx)
		}()
	}
	wg.Wait()
	services := []Service{}
	for _, r := range results {
		services = append(services, r...)
	}

	m.mu.Lock()
	events := m.statusChanges(services, now.Unix())
	m.services = services
	m.mu.Unlock()

	for _, e := range events {
		if err := m.store.AddEvent(e); err != nil {
			log.Printf("monitor: recording %s/%s failed: %v", e.Group, e.Name, err)
		}
	}
	m.broadcast()
}

// statusChanges returns the services whose status has changed since it was
// last recorded. A service seen for the first time is recorded straight away;
// otherwise a new status must hold for two checks in a row, so one slow
// command does not record a failure and a recovery.
func (m *Monitor) statusChanges(services []Service, ts int64) []Event {
	events := []Event{}
	for _, s := range services {
		k := s.key()
		recorded, known := m.recorded[k]
		if recorded == s.Status {
			delete(m.pending, k)
			continue
		}
		if known {
			p := m.pending[k]
			if p.status != s.Status {
				p = pendingStatus{status: s.Status}
			}
			p.seen++
			if p.seen < 2 {
				m.pending[k] = p
				continue
			}
			delete(m.pending, k)
		}
		m.recorded[k] = s.Status
		events = append(events, Event{TS: ts, Group: s.Group, Name: s.Name, Status: s.Status, Detail: s.Detail})
	}
	return events
}

// snapshot is what the stream sends: the newest reading and service states.
type snapshot struct {
	Sample   Sample    `json:"sample"`
	Host     Host      `json:"host"`
	Services []Service `json:"services"`
}

func (m *Monitor) snapshot() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, _ := json.Marshal(snapshot{Sample: m.latest, Host: m.host, Services: m.services})
	return b
}

func (m *Monitor) subscribe() chan []byte {
	ch := make(chan []byte, 1)
	m.subsMu.Lock()
	m.subs[ch] = struct{}{}
	m.subsMu.Unlock()
	return ch
}

func (m *Monitor) unsubscribe(ch chan []byte) {
	m.subsMu.Lock()
	delete(m.subs, ch)
	m.subsMu.Unlock()
}

// broadcast sends the snapshot to every stream. A stream still busy with the
// previous one skips this one rather than holding the others up.
func (m *Monitor) broadcast() {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	if len(m.subs) == 0 {
		return
	}
	b := m.snapshot()
	for ch := range m.subs {
		select {
		case ch <- b:
		default:
		}
	}
}

// liveHistory returns the five-second readings held in memory.
func (m *Monitor) liveHistory() History {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h := History{Tier: "live", Step: int64(sampleEvery.Seconds()), TS: []int64{}, Series: map[string][]*float64{}}
	for _, name := range Metrics {
		h.Series[name] = []*float64{}
	}
	for _, s := range m.live {
		h.TS = append(h.TS, s.TS)
		for i, v := range s.values() {
			var p *float64
			if !math.IsNaN(v) {
				r := math.Round(v*100) / 100
				p = &r
			}
			h.Series[Metrics[i]] = append(h.Series[Metrics[i]], p)
		}
	}
	return h
}
