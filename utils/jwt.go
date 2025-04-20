package utils

import (
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var secretKey = []byte(os.Getenv("DATABASE_SECRET"))

func GenerateToken(username, userID string) (string, error) {
	claims := jwt.MapClaims{
		"user":   map[string]string{"username": username},
		"userId": userID,
		"exp":    time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secretKey)
}

func ValidateToken(tokenStr string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return secretKey, nil
	})
	if err != nil || !token.Valid {
		return nil, err
	}
	return token.Claims.(jwt.MapClaims), nil
}
