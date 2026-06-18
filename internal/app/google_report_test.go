package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

// stubGoogleVerifier swaps verifyGoogleIDToken for the test and restores it.
func stubGoogleVerifier(t *testing.T, claims googleClaims, err error) {
	t.Helper()
	orig := verifyGoogleIDToken
	verifyGoogleIDToken = func(string) (googleClaims, error) { return claims, err }
	t.Cleanup(func() { verifyGoogleIDToken = orig })
}

func setGoogleClientID(t *testing.T, id string) {
	t.Helper()
	orig := config.GoogleClientID
	config.GoogleClientID = id
	t.Cleanup(func() { config.GoogleClientID = orig })
}

func TestAuthGoogleCreatesAccount(t *testing.T) {
	store := testStoreHelper(t)
	setGoogleClientID(t, "client-123.apps.googleusercontent.com")
	stubGoogleVerifier(t, googleClaims{
		Sub:           "google-sub-1",
		Email:         "alex@example.com",
		EmailVerified: "true",
		Name:          "Alex",
		Aud:           "client-123.apps.googleusercontent.com",
	}, nil)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/google",
		strings.NewReader(`{"id_token":"x"}`))
	w := httptest.NewRecorder()
	handleAuthGoogle(w, r, store)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
		User  struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token == "" {
		t.Error("expected a token")
	}
	if resp.User.Email != "alex@example.com" || resp.User.Name != "Alex" {
		t.Errorf("user = %+v, want alex@example.com / Alex", resp.User)
	}

	// A second sign-in with the same Google sub must reuse the account.
	if _, _, _, ok, _ := store.identityAccount(providerGoogle, "google-sub-1"); !ok {
		t.Error("google identity was not persisted")
	}
}

func TestAuthGoogleLinksExistingEmailAccount(t *testing.T) {
	store := testStoreHelper(t)
	setGoogleClientID(t, "aud-1")
	// Pre-existing email/password account.
	acct, err := store.createEmailAccount("sara@example.com", hashPassword("password123"), "Sara")
	if err != nil {
		t.Fatalf("seed email account: %v", err)
	}
	stubGoogleVerifier(t, googleClaims{
		Sub: "g-sara", Email: "sara@example.com", EmailVerified: "true", Name: "Sara G", Aud: "aud-1",
	}, nil)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/google",
		strings.NewReader(`{"id_token":"x"}`))
	w := httptest.NewRecorder()
	handleAuthGoogle(w, r, store)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	// Google identity should now point at the SAME account as the email identity.
	gAcct, _, _, ok, _ := store.identityAccount(providerGoogle, "g-sara")
	if !ok || gAcct != acct {
		t.Errorf("google linked to account %d, want %d (ok=%v)", gAcct, acct, ok)
	}
}

func TestAuthGoogleRejectsWrongAudience(t *testing.T) {
	store := testStoreHelper(t)
	setGoogleClientID(t, "the-real-client")
	stubGoogleVerifier(t, googleClaims{Sub: "s", Aud: "some-other-client"}, nil)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/google",
		strings.NewReader(`{"id_token":"x"}`))
	w := httptest.NewRecorder()
	handleAuthGoogle(w, r, store)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
}

func TestAuthGoogleUnconfigured(t *testing.T) {
	store := testStoreHelper(t)
	setGoogleClientID(t, "")
	r := httptest.NewRequest(http.MethodPost, "/api/auth/google",
		strings.NewReader(`{"id_token":"x"}`))
	w := httptest.NewRecorder()
	handleAuthGoogle(w, r, store)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", w.Code)
	}
}

func setMaintainer(t *testing.T, id string) {
	t.Helper()
	orig := config.MaintainerChatID
	config.MaintainerChatID = id
	t.Cleanup(func() { config.MaintainerChatID = orig })
}

func TestReportBugForwardsToMaintainer(t *testing.T) {
	store := testStoreHelper(t)
	setMaintainer(t, "555")
	mock := &mockNotifier{}

	r := httptest.NewRequest(http.MethodPost, "/api/report/bug",
		strings.NewReader(`{"message":"Quiz crashes on submit","context":"v1.7.0 android"}`))
	w := httptest.NewRecorder()
	handleAPIReportBug(w, r, 4242, store, mock)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(mock.sent))
	}
	got := mock.sent[0]
	if got.chatID != 555 {
		t.Errorf("sent to %d, want maintainer 555", got.chatID)
	}
	if !strings.Contains(got.text, "Quiz crashes on submit") ||
		!strings.Contains(got.text, "4242") {
		t.Errorf("message missing report/user: %q", got.text)
	}
}

func TestReportBugRequiresMessage(t *testing.T) {
	store := testStoreHelper(t)
	setMaintainer(t, "555")
	mock := &mockNotifier{}

	r := httptest.NewRequest(http.MethodPost, "/api/report/bug",
		strings.NewReader(`{"message":"   "}`))
	w := httptest.NewRecorder()
	handleAPIReportBug(w, r, 1, store, mock)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", w.Code)
	}
}
