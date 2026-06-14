package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthRegisterSuccess(t *testing.T) {
	store := testStoreHelper(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "password123",
		"name":     "Test User",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleAuthRegister(w, req, store)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["token"] == nil || resp["token"] == "" {
		t.Fatal("expected non-empty token")
	}
	user := resp["user"].(map[string]interface{})
	if user["email"] != "test@example.com" {
		t.Fatalf("expected email test@example.com, got %v", user["email"])
	}
}

func TestAuthRegisterDuplicateEmail(t *testing.T) {
	store := testStoreHelper(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "dup@example.com",
		"password": "password123",
		"name":     "First User",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleAuthRegister(w, req, store)
	if w.Code != http.StatusOK {
		t.Fatalf("first register failed: %d", w.Code)
	}

	body2, _ := json.Marshal(map[string]string{
		"email":    "dup@example.com",
		"password": "password456",
		"name":     "Second User",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	handleAuthRegister(w2, req2, store)

	var resp map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&resp)
	if resp["error"] != "email already registered" {
		t.Fatalf("expected 'email already registered', got %v", resp["error"])
	}
}

func TestAuthLoginSuccess(t *testing.T) {
	store := testStoreHelper(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "login@example.com",
		"password": "password123",
		"name":     "Login User",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleAuthRegister(w, req, store)
	if w.Code != http.StatusOK {
		t.Fatalf("register failed: %d", w.Code)
	}

	loginBody, _ := json.Marshal(map[string]string{
		"email":    "login@example.com",
		"password": "password123",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	handleAuthLogin(w2, req2, store)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&resp)
	if resp["token"] == nil || resp["token"] == "" {
		t.Fatal("expected non-empty token")
	}
}

func TestAuthLoginWrongPassword(t *testing.T) {
	store := testStoreHelper(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "wrongpw@example.com",
		"password": "password123",
		"name":     "PW User",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleAuthRegister(w, req, store)

	loginBody, _ := json.Marshal(map[string]string{
		"email":    "wrongpw@example.com",
		"password": "wrongpassword",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	handleAuthLogin(w2, req2, store)

	var resp map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&resp)
	if resp["error"] != "invalid email or password" {
		t.Fatalf("expected 'invalid email or password', got %v", resp["error"])
	}
}

func TestAuthLoginNonexistentEmail(t *testing.T) {
	store := testStoreHelper(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "nobody@example.com",
		"password": "password123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleAuthLogin(w, req, store)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "invalid email or password" {
		t.Fatalf("expected 'invalid email or password', got %v", resp["error"])
	}
}

func TestAuthJWTValidation(t *testing.T) {
	token := generateJWT(12345, "jwt@example.com")
	chatID, email, ok := validateJWT(token)
	if !ok {
		t.Fatal("expected valid JWT")
	}
	if chatID != 12345 {
		t.Fatalf("expected chatID 12345, got %d", chatID)
	}
	if email != "jwt@example.com" {
		t.Fatalf("expected email jwt@example.com, got %s", email)
	}
}

func TestAuthJWTInvalid(t *testing.T) {
	_, _, ok := validateJWT("invalid.token.value")
	if ok {
		t.Fatal("expected invalid JWT")
	}
}

func TestAuthMeEndpoint(t *testing.T) {
	store := testStoreHelper(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "me@example.com",
		"password": "password123",
		"name":     "Me User",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleAuthRegister(w, req, store)

	var regResp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&regResp)
	token := regResp["token"].(string)

	req2 := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	handleAuthMe(w2, req2, store)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&resp)
	if resp["email"] != "me@example.com" {
		t.Fatalf("expected email me@example.com, got %v", resp["email"])
	}
	if resp["name"] != "Me User" {
		t.Fatalf("expected name Me User, got %v", resp["name"])
	}
}

func TestAuthRefreshEndpoint(t *testing.T) {
	store := testStoreHelper(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "refresh@example.com",
		"password": "password123",
		"name":     "Refresh User",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleAuthRegister(w, req, store)

	var regResp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&regResp)
	token := regResp["token"].(string)

	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	handleAuthRefresh(w2, req2, store)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&resp)
	if resp["token"] == nil || resp["token"] == "" {
		t.Fatal("expected non-empty new token")
	}
	if resp["token"] == token {
		t.Fatal("expected different token after refresh")
	}
}
