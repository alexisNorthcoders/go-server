# Go Auth Server

A simple authentication server built with Go. It provides endpoints for user registration, login, token validation, anonymous access, and logout using JWT and SQLite.

## Features

- 📦 REST API for authentication
- 🔒 Password hashing using bcrypt
- 🔑 JWT-based authentication
- 💾 SQLite database
- 🧪 Anonymous login option
- ✅ Token validation endpoint
- 📚 Basic logging middleware

## Getting Started

### Prerequisites

- Go 1.20 or higher
- Git

### Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/go-server.git
cd go-server

# Download dependencies
go mod tidy
```

### Running the Server

```bash
go run main.go
```

Server will start at `http://localhost:8080`.

---

## API Endpoints

### `POST /register`

Registers a new user.

**Request Body:**

```json
{
  "username": "your_username",
  "password": "your_password"
}
```

**Response:**

```json
{
  "message": "User created successfully"
}
```

---

### `POST /login`

Authenticates a user and returns a JWT token.

**Request Body:**

```json
{
  "username": "your_username",
  "password": "your_password"
}
```

**Response:**

```json
{
  "message": "Login successful!",
  "accessToken": "jwt_token",
  "userId": "user_id"
}
```

---

### `GET /anonymous`

Generates a token for an anonymous user.

**Response:**

```json
{
  "message": "Anonymous login successful!",
  "accessToken": "jwt_token",
  "userId": "generated_uuid"
}
```

---

### `POST /logout`

Stub endpoint to simulate user logout.

**Response:**

```json
{
  "message": "Logout successful!"
}
```

---

### `POST /verify-token`

Validates a JWT token.

**Headers (Optional):**

```
Authorization: Bearer <your_token>
```

**OR Request Body:**

```json
{
  "token": "your_token"
}
```

**Response:**

```json
{
  "message": "Token is valid",
  "user": "username",
  "userId": "user_id",
  "expiresIn": 1713457641
}
```

---

### `POST /bot-results`

Records the result of a finished vs-bot round. Called by the game server (snake-colyseus), never by a player. Requires the secret both servers share, set in the `BOT_RESULTS_SECRET` environment variable (passed through `ecosystem.config.cjs` in production). If the variable is not set, every report is refused and go-server logs that at start-up.

**Headers:**

```
Authorization: Bearer <BOT_RESULTS_SECRET>
```

**Request Body:**

```json
{
  "resultId": "unique-per-round",
  "botId": "rookie",
  "mode": "timed",
  "delay": 2,
  "outcome": "win"
}
```

- `resultId`: unique per round. Reporting it twice stores it once, and the second report still succeeds.
- `botId`: a short slug (lowercase letters, digits, `-`, `_`, up to 40 characters). Not checked against the roster.
- `mode`: `timed` or `endless`.
- `delay`: the bot's reaction delay, 0 to 4.
- `outcome`: `win`, `loss` or `draw`, from the **bot's** side.

**Responses:** `200` recorded, `400` bad body, `401` wrong, missing or unconfigured secret.

---

### `GET /bot-records`

Public. Returns each bot's record against humans, per mode. Bots and modes with no results are absent. `?botId=rookie` narrows it to one bot.

```json
{
  "rookie": {
    "timed": { "wins": 3, "losses": 5, "draws": 1 },
    "endless": { "wins": 0, "losses": 2, "draws": 0 }
  }
}
```

---

## Raspberry Pi endpoints

These took over from the Pi's old Node webserver. They are off unless `PI_ENDPOINTS=true`, which only `go-server-dev` (the Pi's instance) sets in `ecosystem.config.cjs`, so the VPS never serves them. Their tables are created everywhere but stay empty elsewhere. Swagger UI for them is at `/api-docs/`.

| Endpoint | Who calls it | Notes |
|---|---|---|
| `GET /monitor/stream` | monitor-canvas | Server-sent events: the newest reading, the host and every service's state. See [Monitoring the Pi](#monitoring-the-pi). |
| `GET /monitor/history?range=` | monitor-canvas | Readings over `15m` … `2y`, in columns, from the finest tier that covers the range. |
| `GET /monitor/events?limit=` | monitor-canvas | Service status changes, newest first. |
| `GET /monitor/storage` | monitor-canvas | How many rows each tier holds against its capacity, and the file's size. |
| `POST /amazon-prices` | amazon-scraper | Local only. |
| `GET /amazon-prices/last?url=` | amazon-scraper | Local only. `lastPrice` is `null` when nothing is recorded. |
| `POST /zigzag/score` | zigzag game | Only accepted from `http://raspberrypi.local` or `https://alexisraspberry.duckdns.org` (Origin or Referer). |
| `GET /zigzag/score` | zigzag game | The top 10 scores in ascending order: the last one is the high score. |

"Local only" means the request came straight from the machine itself (loopback or one of its own addresses), not through nginx.

The game pages are static files served by nginx. The Pi's nginx config, including the `/zigzag/score` proxy and its rate limit, lives in the [deployments](https://github.com/alexisNorthcoders/deployments) repo under `raspberrypi/nginx`.

`POST /zigzag/score` is also limited here: 6 scores a minute per player (by the address nginx passes in `X-Real-IP`), and scores must be between 1 and 1,000,000.

### Moving the old webserver's data

`cmd/import-webserver` copies the webserver's data into `users.db` once: `amazon_prices` from its SQLite database, and the zigzag scores from Redis on stdin. Running it again copies nothing twice.

```bash
redis-cli ZRANGE user:zigzag_highscore:scores 0 -1 WITHSCORES \
  | go run ./cmd/import-webserver -from ../clipboard/DB/database.sqlite
```

### Monitoring the Pi

The `monitor` package watches the Pi go-server runs on, for [monitor-canvas](https://github.com/alexisNorthcoders/monitor-canvas). It starts with the Pi endpoints, and if it cannot start, go-server logs that and carries on without it. It replaced pi_health, which posted a reading to `POST /system-info` every minute.

- **Readings**, every 5 seconds, straight from `/proc` and `/sys`: CPU, temperature, load, memory, swap, disk space on `/`, disk I/O and network traffic (physical disks and interfaces only).
- **Services**, every 30 seconds: the systemd units in `MONITOR_UNITS`, every pm2 process (from `pm2 jlist`, reading only status, CPU, memory, restarts and uptime, never the environment), every Docker container, Redis (`INFO` at `REDIS_ADDR`) and the size of each SQLite database (`users.db`, `metrics.db` and any in `MONITOR_SQLITE`).

History is kept in its own file, `metrics.db` (`METRICS_DB`), so it never bloats or locks `users.db`. Each tier is pruned to its retention every hour, and the freed space is handed back (incremental auto-vacuum), so the file levels off at about 3–4 MB:

| Tier | Table | Kept for | Rows when full |
|---|---|---|---|
| 5-second readings | in memory | 15 minutes | – |
| Per-minute average | `samples` | 48 hours | 2,880 |
| 5-minute average and peak | `rollup_5m` | 30 days | 8,640 |
| Hourly average and peak | `rollup_1h` | 2 years | 17,520 |
| Service status changes | `service_events` | 90 days | – |

A service's status is stored only when it changes, and only once the new status has held for two checks in a row, so one slow check does not record an outage. "Stopped" (a pm2 process or container turned off on purpose, or exited with code 0) is not counted as down.

| Variable | Default |
|---|---|
| `METRICS_DB` | `./metrics.db` |
| `MONITOR_UNITS` | `nginx,redis-server,docker,ssh,cron,NetworkManager` |
| `REDIS_ADDR` | `127.0.0.1:6379` |
| `MONITOR_SQLITE` | none; extra SQLite files to report the size of, comma-separated |

`cmd/migrate-system-info` moves pi_health's old readings out of `users.db` once: it rolls them up into the tiers, keeping what is within their retention, then drops `system_info` and vacuums `users.db`. Pass `-keep` to leave the table in place.

```bash
go run ./cmd/migrate-system-info
```

---

## Project Structure

```bash
go-server/
├── handlers/       # HTTP handler functions
├── models/         # DB schema and operations
├── utils/          # JWT token generation and validation
├── users.db        # SQLite database file
├── main.go         # Server entry point
```

---

## License

MIT License

---