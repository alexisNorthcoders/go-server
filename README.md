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
| `POST /system-info` | pi_health, every minute | Local only. Stores the reading as sent, units included (`"70.8°C"`). |
| `GET /system-info/{limit}` | | Newest readings first. The limit defaults to 60, and the most is 10080 (a week). |
| `GET /system-info/sse?limit=` | monitor-canvas | The same readings as server-sent events, once at connect and then every minute. |
| `POST /amazon-prices` | amazon-scraper | Local only. |
| `GET /amazon-prices/last?url=` | amazon-scraper | Local only. `lastPrice` is `null` when nothing is recorded. |
| `POST /zigzag/score` | zigzag game | Only accepted from `http://raspberrypi.local` or `https://alexisraspberry.duckdns.org` (Origin or Referer). |
| `GET /zigzag/score` | zigzag game | The top 10 scores in ascending order: the last one is the high score. |

"Local only" means the request came straight from the machine itself (loopback or one of its own addresses), not through nginx.

The game pages (`/zigzag/`, `/kings-and-pigs/`, `/monitor-canvas/`) are static files served by nginx: see `docs/deploy/nginx-pi.conf` (in `/etc/nginx/snippets/pi-games.conf`) and `docs/deploy/nginx-extra-mime-types.conf` (in `/etc/nginx/conf.d/`, so `.mjs` modules load).

### Moving the old webserver's data

`cmd/import-webserver` copies the webserver's data into `users.db` once: `system_info` and `amazon_prices` from its SQLite database, and the zigzag scores from Redis on stdin. Running it again copies nothing twice.

```bash
redis-cli ZRANGE user:zigzag_highscore:scores 0 -1 WITHSCORES \
  | go run ./cmd/import-webserver -from ../clipboard/DB/database.sqlite
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