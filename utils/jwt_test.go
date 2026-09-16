package utils

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestGenerateToken(t *testing.T) {
	username := "testuser"
	userID := "123456"

	token1, err1 := GenerateToken(username, userID)
	if err1 != nil {
		t.Fatalf("Esperava-se que GenerateToken não retornasse erro, obteve: %v", err1)
	}

	time.Sleep(1 * time.Second)

	token2, err2 := GenerateToken(username, userID)
	if err2 != nil {
		t.Fatalf("Esperava-se que GenerateToken não retornasse erro, obteve: %v", err2)
	}

	if token1 == token2 {
		t.Fatalf("Tokens devem ser únicas, mas obtivemos '%s' e '%s'", token1, token2)
	}
}

func TestValidateToken(t *testing.T) {
	username := "testuser"
	userID := "123456"
	tokenStr, err := GenerateToken(username, userID)
	if err != nil {
		t.Fatalf("Falha ao gerar token: %v", err)
	}

	claims, err := ValidateToken(tokenStr)
	if err != nil {
		t.Fatalf("Falha na validação da token: %v", err)
	}

	if claims["user"].(map[string]interface{})["username"] != username {
		t.Errorf("Esperava-se username '%s', obteve '%s'", username, claims["user"].(map[string]any)["username"])
	}

	if claims["userId"] != userID {
		t.Errorf("Esperava-se userID '%s', obteve '%s'", userID, claims["userId"])
	}

	_, err = ValidateToken("token_invalida")
	if err == nil {
		t.Error("Esperava-se um erro ao validar uma token inválida, mas não houve erro")
	}
}

func TestGenerateToken2(t *testing.T) {
	secretKey := []byte("")
	username := "user123"
	userID := "456"
	tokenStr, err := GenerateToken(username, userID)

	if err != nil {
		t.Fatalf("Expected nil error, got %v", err)
	}

	if tokenStr == "" {
		t.Fatal("Expected non-empty token string")
	}

	// Validar se o token contém os claims correctos
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return secretKey, nil
	})
	if err != nil || !token.Valid {
		t.Fatalf("Token is not valid: %v", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("Expected jwt.MapClaims type assertion to succeed")
	}

	if username != claims["user"].(map[string]interface{})["username"] {
		t.Errorf("Expected username %s, got %s", username, claims["user"].(map[string]interface{})["username"])
	}

	if userID != claims["userId"] {
		t.Errorf("Expected userID %s, got %s", userID, claims["userId"])
	}
}

func TestValidateToken2(t *testing.T) {
	username := "user123"
	userID := "456"
	tokenStr, err := GenerateToken(username, userID)

	if err != nil {
		t.Fatalf("Unexpected error generating token: %v", err)
	}

	// Testar a validação de um token válido
	claims, err := ValidateToken(tokenStr)
	if err != nil {
		t.Fatalf("Expected valid token, got error: %v", err)
	}

	if claims == nil {
		t.Fatal("Expected non-nil claims")
	}

	if username != claims["user"].(map[string]interface{})["username"] {
		t.Errorf("Expected username %s, got %s", username, claims["user"].(map[string]interface{})["username"])
	}

	if userID != claims["userId"] {
		t.Errorf("Expected userID %s, got %s", userID, claims["userId"])
	}

	// Testar a validação de um token inválido
	invalidTokenStr := "invalid_token"
	_, err = ValidateToken(invalidTokenStr)
	if err == nil {
		t.Fatal("Expected error for invalid token")
	}
}
