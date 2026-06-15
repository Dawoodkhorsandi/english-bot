package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestWithUserAcceptsJWT is the regression test for the core bug: the data API
// must accept a mobile-app Bearer JWT, not only Telegram initData. Before the
// fix every /api/* call from the app 401'd and the client logged the user out.
func TestWithUserAcceptsJWT(t *testing.T) {
	store := testStoreHelper(t)
	accountID, err := store.createEmailAccount("jwt-gate@example.com", hashPassword("password123"), "JWT Gate")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	token := generateJWT(accountID, "jwt-gate@example.com")

	var resolved int64
	h := func(w http.ResponseWriter, _ *http.Request, chatID int64, _ *Store) {
		resolved = chatID
		writeJSON(w, map[string]interface{}{"ok": true})
	}
	r := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	withUser(store, h)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("JWT through withUser: code = %d, want 200", w.Code)
	}
	if resolved != accountID {
		t.Fatalf("resolved account = %d, want %d", resolved, accountID)
	}
}

func TestWithUserRejectsBadJWT(t *testing.T) {
	store := testStoreHelper(t)
	r := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	r.Header.Set("Authorization", "Bearer not.a.realtoken")
	w := httptest.NewRecorder()
	withUser(store, handleAPIStats)(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad JWT: code = %d, want 401", w.Code)
	}
}

// TestEmailAccountSynthetic verifies email-only accounts get a negative id that
// can't collide with a real (positive) Telegram chat id.
func TestEmailAccountSynthetic(t *testing.T) {
	store := testStoreHelper(t)
	id, err := store.createEmailAccount("syn@example.com", hashPassword("password123"), "Syn")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if id >= 0 {
		t.Fatalf("expected negative synthetic account id, got %d", id)
	}
	id2, err := store.createEmailAccount("syn2@example.com", hashPassword("password123"), "Syn2")
	if err != nil {
		t.Fatalf("create account 2: %v", err)
	}
	if id2 >= 0 || id2 == id {
		t.Fatalf("expected a distinct negative id, got %d (first was %d)", id2, id)
	}
}

// TestClaimLoginCodeSingleUse covers the TOCTOU fix: a code can be redeemed once.
func TestClaimLoginCodeSingleUse(t *testing.T) {
	store := testStoreHelper(t)
	code, limited, err := store.CreateLoginCode(99999)
	if err != nil || limited {
		t.Fatalf("create code: err=%v limited=%v", err, limited)
	}
	id, status := store.claimLoginCode(code)
	if status != "ok" || id != 99999 {
		t.Fatalf("first claim: id=%d status=%q, want 99999/ok", id, status)
	}
	if _, status := store.claimLoginCode(code); status != "used" {
		t.Fatalf("second claim status=%q, want used", status)
	}
	if _, status := store.claimLoginCode("ZZZ-ZZZ"); status != "invalid" {
		t.Fatalf("unknown code status=%q, want invalid", status)
	}
}

// TestClaimLoginCodeFreshOnNonUTCServer is the regression test for the
// "login code is wrong/expired" bug: on a non-UTC server (production runs
// Asia/Tehran) the driver handed created_at back in process-local time, so a
// freshly-minted code looked hours old and always claimed as "expired".
// claimLoginCode must normalise the stored UTC timestamp regardless of
// time.Local.
func TestClaimLoginCodeFreshOnNonUTCServer(t *testing.T) {
	saved := time.Local
	tehran, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		t.Skipf("tz data unavailable: %v", err)
	}
	time.Local = tehran
	t.Cleanup(func() { time.Local = saved })

	store := testStoreHelper(t)
	code, limited, err := store.CreateLoginCode(123123)
	if err != nil || limited {
		t.Fatalf("create code: err=%v limited=%v", err, limited)
	}
	id, status := store.claimLoginCode(code)
	if status != "ok" || id != 123123 {
		t.Fatalf("fresh code on non-UTC server: id=%d status=%q, want 123123/ok", id, status)
	}
}

func TestLoginCodeRateLimit(t *testing.T) {
	store := testStoreHelper(t)
	for i := 0; i < loginCodeRateLimit; i++ {
		if _, limited, err := store.CreateLoginCode(42); err != nil || limited {
			t.Fatalf("code %d: err=%v limited=%v (should be under quota)", i, err, limited)
		}
	}
	_, limited, err := store.CreateLoginCode(42)
	if err != nil {
		t.Fatalf("over-quota create: %v", err)
	}
	if !limited {
		t.Fatalf("expected rate-limit after %d codes", loginCodeRateLimit)
	}
}

// TestMergeAccountRekeys verifies a merge folds the loser's data and identities
// into the winner (the Telegram side), leaving nothing behind.
func TestMergeAccountRekeys(t *testing.T) {
	store := testStoreHelper(t)
	loser, err := store.createEmailAccount("merge@example.com", hashPassword("password123"), "Merge")
	if err != nil {
		t.Fatalf("create loser: %v", err)
	}
	winner := int64(777001)
	if err := store.ensureTelegramIdentity(winner, "User 777001"); err != nil {
		t.Fatalf("ensure telegram identity: %v", err)
	}
	if _, err := store.db.Exec("INSERT INTO bookmarks (chat_id, word) VALUES (?, ?)", loser, "ephemeral"); err != nil {
		t.Fatalf("seed bookmark: %v", err)
	}

	if err := store.mergeAccount(loser, winner); err != nil {
		t.Fatalf("merge: %v", err)
	}

	var cnt int
	_ = store.db.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE chat_id = ?", winner).Scan(&cnt)
	if cnt != 1 {
		t.Fatalf("expected bookmark moved to winner, got %d", cnt)
	}
	_ = store.db.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE chat_id = ?", loser).Scan(&cnt)
	if cnt != 0 {
		t.Fatalf("expected no rows left on loser, got %d", cnt)
	}
	acc, _, _, ok, _ := store.identityAccount(providerEmail, "merge@example.com")
	if !ok || acc != winner {
		t.Fatalf("email identity not merged onto winner: ok=%v acc=%d", ok, acc)
	}
}

// TestAuthMeTelegramAccount guards the previously-broken case: /me for a token
// whose subject is a Telegram chat id (it used to 404 due to a users.id lookup).
func TestAuthMeTelegramAccount(t *testing.T) {
	store := testStoreHelper(t)
	chatID := int64(555001)
	if err := store.ensureTelegramIdentity(chatID, "User 555001"); err != nil {
		t.Fatalf("ensure identity: %v", err)
	}
	token := generateJWT(chatID, "")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handleAuthMe(w, req, store)

	if w.Code != http.StatusOK {
		t.Fatalf("me for telegram account: code = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["telegram_chat_id"] == nil {
		t.Fatalf("expected telegram_chat_id in /me response, got %v", resp)
	}
}
