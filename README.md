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