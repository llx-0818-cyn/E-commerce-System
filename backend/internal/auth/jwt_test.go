package auth

import "testing"

func TestGeneratedFallbackSecretIsStableForProcess(t *testing.T) {
	t.Setenv("JWT_SECRET", "")

	token, err := GenerateToken(42, "local-user")
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	claims, err := ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	if claims.AccountID != 42 || claims.Username != "local-user" {
		t.Fatalf("claims = %#v, want account 42 and local-user", claims)
	}
}
