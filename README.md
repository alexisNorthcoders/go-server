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