package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// saveMaintainer saves and restores MaintainerChatID around a test.
func saveMaintainer(t *testing.T) {
	t.Helper()
	orig := MaintainerChatID
	t.Cleanup(func() { MaintainerChatID = orig })
}

// saveQuietHours saves and restores quietStart/quietEnd around a test.
func saveQuietHours(t *testing.T) {
	t.Helper()
	origS, origE := quietStart, quietEnd
	t.Cleanup(func() { quietStart = origS; quietEnd = origE })
}

// saveAppLocation saves and restores appLocation around a test.
func saveAppLocation(t *testing.T) {
	t.Helper()
	orig := appLocation
	t.Cleanup(func() { appLocation = orig })
}

func TestIsMaintainer(t *testing.T) {
	saveMaintainer(t)

	MaintainerChatID = "12345"
	if !isMaintainer(12345) {
		t.Error("expected isMaintainer(12345) = true")
	}
	if isMaintainer(99999) {
		t.Error("expected isMaintainer(99999) = false")
	}

	MaintainerChatID = "not_a_number"
	if isMaintainer(12345) {
		t.Error("expected false for non-numeric MaintainerChatID")
	}

	MaintainerChatID = ""
	if isMaintainer(0) {
		t.Error("expected false for empty MaintainerChatID")
	}
}

func TestHandleMessageStart(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveMaintainer(t)
	MaintainerChatID = "999"

	msg := &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 100},
		Text:      "/start",
		From:      &TelegramUser{ID: 100, Username: "testuser"},
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	subs, _ := store.Subscribers()
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscriber, got %d", len(subs))
	}

	found := false
	for _, s := range mock.sentTexts() {
		if strings.Contains(s, "Welcome") {
			found = true
		}
	}
	if !found {
		t.Error("expected welcome message containing 'Welcome'")
	}

	// Check maintainer notification
	notified := false
	for _, s := range mock.sent {
		if s.chatID == 999 && strings.Contains(s.text, "New User") {
			notified = true
		}
	}
	if !notified {
		t.Error("expected maintainer notification for new user")
	}
}

func TestHandleMessageStartReturning(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveMaintainer(t)
	MaintainerChatID = "999"

	store.AddSubscriber(100)

	msg := &TelegramMessage{
		MessageID: 2,
		Chat:      TelegramChat{ID: 100},
		Text:      "/start",
		From:      &TelegramUser{ID: 100, Username: "testuser"},
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	// Welcome should be sent
	found := false
	for _, s := range mock.sentTexts() {
		if strings.Contains(s, "Welcome") {
			found = true
		}
	}
	if !found {
		t.Error("expected welcome message")
	}

	// Maintainer should NOT be notified for returning user
	for _, s := range mock.sent {
		if s.chatID == 999 && strings.Contains(s.text, "New User") {
			t.Error("maintainer should not be notified for returning user")
		}
	}
}

func TestHandleMessageHelp(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	msg := &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 100},
		Text:      "/help",
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	if !strings.Contains(mock.lastSentText(), "How Muscle Memory") {
		t.Errorf("expected help text containing 'How Muscle Memory', got %q", mock.lastSentText())
	}
}

func TestHandleMessageDrill(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.AddToPool(kindDrill, defaultLevel, "walk", "", "drill text for walk")

	msg := &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 100},
		Text:      "/drill",
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	texts := mock.sentTexts()
	if len(texts) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(texts))
	}
	if !strings.Contains(texts[0], "Generating") {
		t.Errorf("first message should contain 'Generating', got %q", texts[0])
	}
	if texts[1] != "drill text for walk" {
		t.Errorf("second message should be drill text, got %q", texts[1])
	}
}

func TestHandleMessageDrillPaged(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.AddToPool(kindDrill, defaultLevel, "walk", "", sampleDrill(21))

	msg := &TelegramMessage{MessageID: 1, Chat: TelegramChat{ID: 100}, Text: "/drill"}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	// A multi-page drill is delivered with an inline keyboard, not a plain send.
	if len(mock.keyboard) != 1 {
		t.Fatalf("expected 1 keyboard send for paged drill, got %d", len(mock.keyboard))
	}
	kb := mock.keyboard[0]
	if !strings.Contains(kb.text, "Page 1/5") || !strings.Contains(kb.text, "Verb of the Hour: walk") {
		t.Errorf("first page text wrong: %q", kb.text)
	}
	if len(kb.keyboard) == 0 {
		t.Fatal("expected a navigation keyboard")
	}
	last := kb.keyboard[0][len(kb.keyboard[0])-1]
	if last.CallbackData != "drill:2:walk" {
		t.Errorf("expected Next button to page 2, got %q", last.CallbackData)
	}
}

func TestHandleDrillCallbackPaging(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	store.AddToPool(kindDrill, defaultLevel, "walk", "", sampleDrill(21))

	cb := &TelegramCallbackQuery{
		ID:      "cb1",
		Data:    "drill:3:walk",
		Message: &TelegramMessage{MessageID: 5, Chat: TelegramChat{ID: 100}},
	}
	handleCallback(store, mock, cb)

	if len(mock.edits) != 1 {
		t.Fatalf("expected 1 edit, got %d", len(mock.edits))
	}
	if !strings.Contains(mock.edits[0].text, "Page 3/5") || !strings.Contains(mock.edits[0].text, "More Past and Future") {
		t.Errorf("edit should render page 3, got %q", mock.edits[0].text)
	}
	if len(mock.edits[0].keyboard) == 0 {
		t.Error("paged edit should keep a navigation keyboard")
	}
}

func TestHandleDrillCallbackUnknownVerb(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	cb := &TelegramCallbackQuery{
		ID:      "cb1",
		Data:    "drill:2:zzz",
		Message: &TelegramMessage{MessageID: 5, Chat: TelegramChat{ID: 100}},
	}
	handleCallback(store, mock, cb)

	if len(mock.edits) != 0 {
		t.Errorf("expected no edit for unknown verb, got %d", len(mock.edits))
	}
	if len(mock.answers) != 1 || mock.answers[0].text != "That drill is no longer available" {
		t.Errorf("expected an 'expired' answer, got %+v", mock.answers)
	}
}

func TestHandleDrillCallbackNoop(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	cb := &TelegramCallbackQuery{
		ID:      "cb1",
		Data:    "drill:noop",
		Message: &TelegramMessage{MessageID: 5, Chat: TelegramChat{ID: 100}},
	}
	handleCallback(store, mock, cb)

	if len(mock.edits) != 0 {
		t.Errorf("noop should not edit, got %d edits", len(mock.edits))
	}
	if len(mock.answers) != 1 || mock.answers[0].text != "" {
		t.Errorf("noop should be acknowledged silently, got %+v", mock.answers)
	}
}

func TestHandleMessageIdiom(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.AddToPool(kindIdiom, defaultLevel, "break the ice", "to start a conversation", "idiom card text")

	msg := &TelegramMessage{MessageID: 1, Chat: TelegramChat{ID: 100}, Text: "/idiom"}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	texts := mock.sentTexts()
	if len(texts) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(texts))
	}
	if texts[len(texts)-1] != "idiom card text" {
		t.Errorf("last message should be the idiom card, got %q", texts[len(texts)-1])
	}
	// And it should be recorded so it isn't repeated.
	if _, _, _, ok, _ := store.PooledUnseen(kindIdiom, defaultLevel, 100); ok {
		t.Error("served idiom should be recorded as seen")
	}
}

func TestHandleMessageWord(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.AddToPool(kindWord, defaultLevel, "apple", "a fruit", "word card for apple")

	msg := &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 100},
		Text:      "/word",
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	texts := mock.sentTexts()
	if len(texts) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(texts))
	}
	if !strings.Contains(texts[0], "Finding") {
		t.Errorf("first message should contain 'Finding', got %q", texts[0])
	}
	if texts[1] != "word card for apple" {
		t.Errorf("second message should be word card, got %q", texts[1])
	}
}

func TestHandleMessageTTS(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	msg := &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 100},
		Text:      "/tts off",
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)
	if store.GetTTSEnabled(100) {
		t.Error("expected /tts off to disable TTS")
	}

	msg.Text = "/tts on"
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)
	if !store.GetTTSEnabled(100) {
		t.Error("expected /tts on to enable TTS")
	}
}

func TestHandleMessageTip(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.AddToPool(kindTip, defaultLevel, "used to vs would", "", "tip card text")

	msg := &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 100},
		Text:      "/tip",
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	texts := mock.sentTexts()
	if len(texts) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(texts))
	}
	if !strings.Contains(texts[0], "Fetching your grammar tip") {
		t.Errorf("first message should contain tip fetching ack, got %q", texts[0])
	}
	if texts[1] != "tip card text" {
		t.Errorf("second message should be tip card, got %q", texts[1])
	}
}

func TestHandleMessageTipToggle(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	handleMessage(context.Background(), emptyProviderChain(), store, mock, &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 100},
		Text:      "/tip off",
	})
	if store.GetTipsEnabled(100) {
		t.Error("expected tips disabled after /tip off")
	}

	handleMessage(context.Background(), emptyProviderChain(), store, mock, &TelegramMessage{
		MessageID: 2,
		Chat:      TelegramChat{ID: 100},
		Text:      "/tip on",
	})
	if !store.GetTipsEnabled(100) {
		t.Error("expected tips enabled after /tip on")
	}
}

func TestHandleMessageStats(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	msg := &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 100},
		Text:      "/stats",
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	if !strings.Contains(mock.lastSentText(), "Your Progress") {
		t.Errorf("expected stats containing 'Your Progress', got %q", mock.lastSentText())
	}
}

func TestHandleMessagePause(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	msg := &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 100},
		Text:      "/pause",
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	if !store.IsPaused(100) {
		t.Error("expected subscriber to be paused")
	}
	if !strings.Contains(mock.lastSentText(), "Paused") {
		t.Errorf("expected pause confirmation, got %q", mock.lastSentText())
	}
}

func TestHandleMessageResume(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.SetPaused(100, true)

	msg := &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 100},
		Text:      "/resume",
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	if store.IsPaused(100) {
		t.Error("expected subscriber to be unpaused")
	}
	if !strings.Contains(mock.lastSentText(), "Resumed") {
		t.Errorf("expected resume confirmation, got %q", mock.lastSentText())
	}
}

func TestHandleMessageReset(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.RecordSentWord(100, "run")
	store.RecordSentVocab(100, "apple")

	msg := &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 100},
		Text:      "/reset",
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	if !strings.Contains(mock.lastSentText(), "History reset") {
		t.Errorf("expected reset confirmation, got %q", mock.lastSentText())
	}
}

func TestHandleMessageUnknownCommand(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	msg := &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 100},
		Text:      "/foobar",
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	if !strings.Contains(mock.lastSentText(), "I don't know that command") {
		t.Errorf("expected unknown command reply, got %q", mock.lastSentText())
	}
}

func TestHandleLevelWithArg(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	handleLevel(store, mock, 100, []string{"advanced"})

	got := store.GetLevel(100)
	if got != "advanced" {
		t.Errorf("level = %q, want advanced", got)
	}
	if !strings.Contains(strings.ToLower(mock.lastSentText()), "advanced") {
		t.Errorf("expected confirmation with 'advanced', got %q", mock.lastSentText())
	}
}

func TestHandleLevelWithInvalidArg(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	handleLevel(store, mock, 100, []string{"expert"})

	if !strings.Contains(mock.lastSentText(), "I don't know that level") {
		t.Errorf("expected error message, got %q", mock.lastSentText())
	}
}

func TestHandleLevelNoArg(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	handleLevel(store, mock, 100, []string{})

	if len(mock.keyboard) == 0 {
		t.Error("expected a keyboard to be sent")
	}
}

func TestHandleIntervalWithArg(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	handleInterval(store, mock, 100, []string{"60"})

	got := store.GetInterval(100)
	if got != 60 {
		t.Errorf("interval = %d, want 60", got)
	}
	if !strings.Contains(mock.lastSentText(), "1 hour") {
		t.Errorf("expected '1 hour' in confirmation, got %q", mock.lastSentText())
	}
}

func TestHandleIntervalInvalidArg(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	handleInterval(store, mock, 100, []string{"abc"})

	if !strings.Contains(mock.lastSentText(), "minutes") {
		t.Errorf("expected error about minutes, got %q", mock.lastSentText())
	}
}

func TestHandleIntervalInvalidValue(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	handleInterval(store, mock, 100, []string{"45"})

	if !strings.Contains(mock.lastSentText(), "not one of the options") {
		t.Errorf("expected 'not one of the options', got %q", mock.lastSentText())
	}
}

func TestHandleIntervalNoArg(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	handleInterval(store, mock, 100, []string{})

	if len(mock.keyboard) == 0 {
		t.Error("expected a keyboard to be sent")
	}
}

func TestHandleCallbackLevel(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	cb := &TelegramCallbackQuery{
		ID:   "cb1",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 10,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "level:beginner",
	}
	handleCallback(store, mock, cb)

	if store.GetLevel(100) != "beginner" {
		t.Errorf("level = %q, want beginner", store.GetLevel(100))
	}
	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery to be called")
	}
	if len(mock.edits) == 0 {
		t.Fatal("expected editMessageText to be called")
	}
}

func TestHandleCallbackInterval(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	cb := &TelegramCallbackQuery{
		ID:   "cb2",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 10,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "interval:120",
	}
	handleCallback(store, mock, cb)

	if store.GetInterval(100) != 120 {
		t.Errorf("interval = %d, want 120", store.GetInterval(100))
	}
	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery")
	}
	if len(mock.edits) == 0 {
		t.Fatal("expected editMessageText")
	}
}

func TestHandleCallbackSrsKnown(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.SeedReview(100, "tedious", time.Now().Add(-time.Hour))

	cb := &TelegramCallbackQuery{
		ID:   "cb3",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 10,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "srs:known:tedious",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery")
	}
	if !strings.Contains(mock.answers[0].text, "Great") {
		t.Errorf("expected 'Great' toast, got %q", mock.answers[0].text)
	}
	if len(mock.edits) == 0 {
		t.Fatal("expected editMessageText")
	}
}

func TestHandleCallbackSrsForgot(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.SeedReview(100, "tedious", time.Now().Add(-time.Hour))

	cb := &TelegramCallbackQuery{
		ID:   "cb4",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 10,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "srs:forgot:tedious",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery")
	}
	if !strings.Contains(mock.answers[0].text, "No worries") {
		t.Errorf("expected 'No worries' toast, got %q", mock.answers[0].text)
	}
}

func TestHandleCallbackQuizCorrect(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.AddToPool(kindWord, defaultLevel, "tedious", "boring", "card")
	store.recordSentFor(kindWord, 100, "tedious")

	cb := &TelegramCallbackQuery{
		ID:   "cb5",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 10,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "quiz:c:tedious",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery")
	}
	if !strings.Contains(mock.answers[0].text, "Correct") {
		t.Errorf("expected 'Correct' toast, got %q", mock.answers[0].text)
	}
}

func TestHandleCallbackQuizWrong(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.AddToPool(kindWord, defaultLevel, "tedious", "boring", "card")
	store.recordSentFor(kindWord, 100, "tedious")

	cb := &TelegramCallbackQuery{
		ID:   "cb6",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 10,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "quiz:x:tedious",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery")
	}
	if !strings.Contains(mock.answers[0].text, "Not quite") {
		t.Errorf("expected 'Not quite' toast, got %q", mock.answers[0].text)
	}
}

func TestHandleMetrics(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	store.AddSubscriber(200)
	store.AddToPool(kindDrill, defaultLevel, "run", "", "drill text")

	chain := mockProviderChain("test")
	handleMetrics(store, chain, mock, 100)

	text := mock.lastSentText()
	if !strings.Contains(text, "Bot Metrics") {
		t.Errorf("expected 'Bot Metrics', got %q", text)
	}
	if !strings.Contains(text, "2") {
		t.Errorf("expected subscriber count in metrics, got %q", text)
	}
}

func TestHandleAnnounce(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveMaintainer(t)
	MaintainerChatID = "100"

	store.AddSubscriber(100)
	store.AddSubscriber(200)
	store.AddSubscriber(300)
	store.SetPaused(300, true)

	handleAnnounce(store, mock, 100, "Hello everyone!")

	// Count how many non-maintainer-confirmation messages went to 200
	countTo200 := 0
	for _, s := range mock.sent {
		if s.chatID == 200 && s.text == "Hello everyone!" {
			countTo200++
		}
	}
	if countTo200 != 1 {
		t.Errorf("expected 1 message to chat 200, got %d", countTo200)
	}

	// Paused user 300 should NOT receive
	for _, s := range mock.sent {
		if s.chatID == 300 {
			t.Error("paused user should not receive announcement")
		}
	}

	// Maintainer should get confirmation
	confirmation := mock.lastSentText()
	if !strings.Contains(confirmation, "delivered to") {
		t.Errorf("expected delivery confirmation, got %q", confirmation)
	}
}

func TestHandleAnnounceEmpty(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	handleAnnounce(store, mock, 100, "")

	if !strings.Contains(mock.lastSentText(), "Usage") {
		t.Errorf("expected usage message, got %q", mock.lastSentText())
	}
}

func TestHandleHealth(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	appLocation = time.UTC

	chain := mockProviderChain("test")
	handleHealth(store, chain, mock, 100)

	text := mock.lastSentText()
	if !strings.Contains(text, "Health Check") {
		t.Errorf("expected 'Health Check', got %q", text)
	}
	if !strings.Contains(text, "Database: ✅") {
		t.Errorf("expected 'Database: ✅', got %q", text)
	}
}

func TestHandleMetricsNotMaintainer(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveMaintainer(t)
	MaintainerChatID = "999"

	msg := &TelegramMessage{
		MessageID: 1,
		Chat:      TelegramChat{ID: 1},
		Text:      "/metrics",
	}
	handleMessage(context.Background(), emptyProviderChain(), store, mock, msg)

	if !strings.Contains(mock.lastSentText(), "only available to the bot maintainer") {
		t.Errorf("expected 'not authorized' reply, got %q", mock.lastSentText())
	}
}

func TestSendPendingChangelogs(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	// Don't mark any changelogs as seen — all should be pending

	sendPendingChangelogs(store, mock, 100)

	count1 := mock.sentCount()
	if count1 == 0 {
		t.Fatal("expected at least one changelog to be sent")
	}

	// Check some changelog text was sent
	found := false
	for _, s := range mock.sentTexts() {
		if strings.Contains(s, "What's New") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected changelog text containing \"What's New\"")
	}

	// Call again — should be idempotent
	sendPendingChangelogs(store, mock, 100)
	count2 := mock.sentCount()
	if count2 != count1 {
		t.Errorf("expected idempotent (no new sends), before=%d after=%d", count1, count2)
	}
}

func TestHandleQuiz(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	// Need 4+ words in pool with meanings, and at least 1 in sent_vocab
	words := []struct{ term, meaning string }{
		{"tedious", "boring"},
		{"vivid", "bright"},
		{"ample", "enough"},
		{"serene", "calm"},
	}
	for _, w := range words {
		store.AddToPool(kindWord, defaultLevel, w.term, w.meaning, "card for "+w.term)
	}
	store.recordSentFor(kindWord, 100, "tedious")

	handleQuiz(store, mock, 100)

	if len(mock.keyboard) == 0 {
		t.Error("expected a keyboard quiz to be sent")
	}
}

func TestHandleQuizNotEnoughWords(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	handleQuiz(store, mock, 100)

	if !strings.Contains(mock.lastSentText(), "Not enough learned words") {
		t.Errorf("expected 'Not enough learned words', got %q", mock.lastSentText())
	}
}

func TestHandleWordLookup(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	chain := mockProviderChain("📘 <b>Word of the Hour: serendipity</b>\n\n<b>Meaning:</b> happy accident")

	handleWordLookup(context.Background(), chain, store, mock, 100, "serendipity")

	texts := mock.sentTexts()
	if len(texts) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(texts))
	}
	if !strings.Contains(texts[0], "Looking that up") {
		t.Errorf("first message should contain 'Looking that up', got %q", texts[0])
	}

	// Check word was pooled
	terms, _ := store.PoolTerms(kindWord, defaultLevel)
	found := false
	for _, term := range terms {
		if term == "serendipity" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'serendipity' to be added to pool")
	}

	// Check word was recorded in sent_vocab
	vocab, _ := store.SentVocab(100)
	found = false
	for _, v := range vocab {
		if v == "serendipity" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'serendipity' in sent_vocab")
	}
}

func TestHandleWordLookupTooLong(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	handleWordLookup(context.Background(), emptyProviderChain(), store, mock, 100, "this is a very long sentence that should be rejected")

	if !strings.Contains(mock.lastSentText(), "more like a sentence") {
		t.Errorf("expected 'more like a sentence', got %q", mock.lastSentText())
	}
}

func TestServeContentFromPool(t *testing.T) {
	store := testStoreHelper(t)

	store.AddSubscriber(100)
	store.AddToPool(kindDrill, defaultLevel, "walk", "", "drill text for walk")

	text, err := serveContent(context.Background(), emptyProviderChain(), store, 100, kindDrill, defaultLevel, false)
	if err != nil {
		t.Fatalf("serveContent: %v", err)
	}
	if text != "drill text for walk" {
		t.Errorf("got %q, want 'drill text for walk'", text)
	}
}

func TestServeContentInlineGenerate(t *testing.T) {
	store := testStoreHelper(t)

	store.AddSubscriber(100)
	chain := mockProviderChain("📘 <b>Word of the Hour: ephemeral</b>\n\n<b>Meaning:</b> short-lived")

	text, err := serveContent(context.Background(), chain, store, 100, kindWord, defaultLevel, true)
	if err != nil {
		t.Fatalf("serveContent: %v", err)
	}
	if !strings.Contains(text, "ephemeral") {
		t.Errorf("expected generated text with 'ephemeral', got %q", text)
	}

	// Should be added to pool
	terms, _ := store.PoolTerms(kindWord, defaultLevel)
	found := false
	for _, term := range terms {
		if term == "ephemeral" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'ephemeral' to be added to pool")
	}
}

func TestBroadcastSweep(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	saveQuietHours(t)
	appLocation = time.UTC
	quietStart = "00:00"
	quietEnd = "06:00"

	store.AddSubscriber(100)
	store.AddSubscriber(200)

	// Pool items for both kinds at default level
	store.AddToPool(kindDrill, defaultLevel, "run", "", "drill for run")
	store.AddToPool(kindWord, defaultLevel, "apple", "fruit", "card for apple")

	// Use 10:00 UTC — not quiet, minute 600 % 30 == 0, so it's due
	now := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	broadcastSweep(context.Background(), emptyProviderChain(), store, mock, now)

	if mock.sentCount() == 0 {
		t.Error("expected at least one message to be sent during broadcast")
	}
}

func TestBroadcastSweepSkipsPaused(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	saveQuietHours(t)
	appLocation = time.UTC
	quietStart = "00:00"
	quietEnd = "06:00"

	store.AddSubscriber(100)
	store.AddSubscriber(200)
	store.SetPaused(200, true)

	store.AddToPool(kindDrill, defaultLevel, "run", "", "drill for run")
	store.AddToPool(kindWord, defaultLevel, "apple", "fruit", "card for apple")

	now := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	broadcastSweep(context.Background(), emptyProviderChain(), store, mock, now)

	// Only chat 100 should receive messages
	for _, s := range mock.sent {
		if s.chatID == 200 {
			t.Error("paused user should not receive broadcast")
		}
	}
}

func TestRunQuizSweep(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	saveQuietHours(t)
	appLocation = time.UTC
	quietStart = "00:00"
	quietEnd = "06:00"

	store.AddSubscriber(100)
	words := []struct{ term, meaning string }{
		{"tedious", "boring"},
		{"vivid", "bright"},
		{"ample", "enough"},
		{"serene", "calm"},
	}
	for _, w := range words {
		store.AddToPool(kindWord, defaultLevel, w.term, w.meaning, "card for "+w.term)
	}
	store.recordSentFor(kindWord, 100, "tedious")

	now := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	runQuizSweep(store, mock, now)

	if len(mock.keyboard) == 0 {
		t.Error("expected a quiz keyboard to be sent")
	}
}

func TestRunQuizSweepQuietHours(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	saveAppLocation(t)
	saveQuietHours(t)
	appLocation = time.UTC
	quietStart = "00:00"
	quietEnd = "12:00"

	store.AddSubscriber(100)
	words := []struct{ term, meaning string }{
		{"tedious", "boring"},
		{"vivid", "bright"},
		{"ample", "enough"},
		{"serene", "calm"},
	}
	for _, w := range words {
		store.AddToPool(kindWord, defaultLevel, w.term, w.meaning, "card for "+w.term)
	}
	store.recordSentFor(kindWord, 100, "tedious")

	// 3:00 UTC is within quiet hours (00:00-12:00)
	now := time.Date(2025, 1, 1, 3, 0, 0, 0, time.UTC)
	runQuizSweep(store, mock, now)

	if len(mock.keyboard) != 0 {
		t.Error("expected no quiz during quiet hours")
	}
	if mock.sentCount() != 0 {
		t.Error("expected no messages during quiet hours")
	}
}

// ---------------------------------------------------------------------------
// broadcastChangelogsOnStartup tests
// ---------------------------------------------------------------------------

func TestBroadcastChangelogsOnStartup_DeliversToAll(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	// Two subscribers, neither has seen any changelogs.
	store.AddSubscriber(100)
	store.AddSubscriber(200)

	broadcastChangelogsOnStartup(store, mock)

	// Each subscriber should have received len(Changelogs) messages.
	expectedTotal := len(Changelogs) * 2
	if got := mock.sentCount(); got != expectedTotal {
		t.Errorf("expected %d sends, got %d", expectedTotal, got)
	}

	// Verify both chat IDs received messages.
	gotChats := map[int64]int{}
	for _, s := range mock.sent {
		gotChats[s.chatID]++
	}
	for _, id := range []int64{100, 200} {
		if gotChats[id] != len(Changelogs) {
			t.Errorf("chat %d: expected %d sends, got %d", id, len(Changelogs), gotChats[id])
		}
	}
}

func TestBroadcastChangelogsOnStartup_Idempotent(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	broadcastChangelogsOnStartup(store, mock)
	count1 := mock.sentCount()
	if count1 == 0 {
		t.Fatal("expected at least one changelog sent")
	}

	// Second call: all changelogs already marked seen, so no new sends.
	broadcastChangelogsOnStartup(store, mock)
	if got := mock.sentCount(); got != count1 {
		t.Errorf("expected idempotent (no new sends), before=%d after=%d", count1, got)
	}
}

func TestBroadcastChangelogsOnStartup_SkipsAlreadySeen(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	// Mark all but the last changelog as seen.
	for i := 0; i < len(Changelogs)-1; i++ {
		store.MarkChangelogSeen(100, Changelogs[i].Version)
	}

	broadcastChangelogsOnStartup(store, mock)

	// Should send only the last changelog entry.
	if got := mock.sentCount(); got != 1 {
		t.Errorf("expected 1 send (only latest unseen), got %d", got)
	}
	if got := mock.lastSentText(); !strings.Contains(got, "What's New") {
		t.Error("expected changelog text containing \"What's New\"")
	}
}

func TestBroadcastChangelogsOnStartup_NoSubscribers(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	// No subscribers — should not panic or send anything.
	broadcastChangelogsOnStartup(store, mock)

	if got := mock.sentCount(); got != 0 {
		t.Errorf("expected 0 sends with no subscribers, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// handleCallback: level, interval, srs, unknown
// ---------------------------------------------------------------------------

func TestHandleCallbackLevelButton(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	cb := &TelegramCallbackQuery{
		ID:   "cb-level-1",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 20,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "level:advanced",
	}
	handleCallback(store, mock, cb)

	// Should answer callback.
	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery for level")
	}
	if !strings.Contains(mock.answers[0].text, "Advanced") {
		t.Errorf("expected 'Advanced' in toast, got %q", mock.answers[0].text)
	}
	// Should edit message.
	if len(mock.edits) == 0 {
		t.Fatal("expected editMessage for level")
	}
	if !strings.Contains(mock.edits[0].text, "Advanced") {
		t.Errorf("expected 'Advanced' in edited message, got %q", mock.edits[0].text)
	}
	// Verify persisted.
	if got := store.GetLevel(100); got != levelAdvanced {
		t.Errorf("level = %q, want %q", got, levelAdvanced)
	}
}

func TestHandleCallbackLevelInvalid(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	cb := &TelegramCallbackQuery{
		ID:   "cb-level-bad",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 20,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "level:expert",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery")
	}
	if mock.answers[0].text != "Unknown level" {
		t.Errorf("expected 'Unknown level' toast, got %q", mock.answers[0].text)
	}
}

func TestHandleCallbackIntervalButton(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	cb := &TelegramCallbackQuery{
		ID:   "cb-int-1",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 21,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "interval:120",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery for interval")
	}
	// Verify persisted.
	prefs, _ := store.GetPrefs(100)
	if prefs.Interval != 120 {
		t.Errorf("interval = %d, want 120", prefs.Interval)
	}
}

func TestHandleCallbackIntervalInvalid(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	cb := &TelegramCallbackQuery{
		ID:   "cb-int-bad",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 21,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "interval:45",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery")
	}
	if mock.answers[0].text != "Unknown interval" {
		t.Errorf("expected 'Unknown interval' toast, got %q", mock.answers[0].text)
	}
}

func TestHandleCallbackIntervalNonNumeric(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	cb := &TelegramCallbackQuery{
		ID:   "cb-int-nan",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 21,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "interval:abc",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery")
	}
	if mock.answers[0].text != "Unknown interval" {
		t.Errorf("expected 'Unknown interval', got %q", mock.answers[0].text)
	}
}

func TestHandleCallbackSrsKnownViaHandleReview(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	now := time.Now()
	store.SeedReview(100, "ephemeral", now.AddDate(0, 0, -2))

	cb := &TelegramCallbackQuery{
		ID:   "cb-srs-1",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 30,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "srs:known:ephemeral",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery for SRS known")
	}
	if !strings.Contains(mock.answers[0].text, "pushed further") {
		t.Errorf("expected 'pushed further' toast, got %q", mock.answers[0].text)
	}
	if len(mock.edits) == 0 {
		t.Fatal("expected editMessage confirmation")
	}
}

func TestHandleCallbackSrsForgotViaHandleReview(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	now := time.Now()
	store.SeedReview(100, "ephemeral", now.AddDate(0, 0, -2))

	cb := &TelegramCallbackQuery{
		ID:   "cb-srs-2",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 31,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "srs:forgot:ephemeral",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery for SRS forgot")
	}
	if !strings.Contains(mock.answers[0].text, "see it again") {
		t.Errorf("expected 'see it again' toast, got %q", mock.answers[0].text)
	}
}

func TestHandleCallbackSrsInvalidAction(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	cb := &TelegramCallbackQuery{
		ID:   "cb-srs-bad",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 32,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "srs:snooze:ephemeral",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery")
	}
}

func TestHandleCallbackSrsExpired(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)
	// Don't seed any review -- "ghost" word should report expired.
	cb := &TelegramCallbackQuery{
		ID:   "cb-srs-exp",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 33,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "srs:known:ghostword",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery")
	}
	if !strings.Contains(mock.answers[0].text, "expired") {
		t.Errorf("expected 'expired' toast, got %q", mock.answers[0].text)
	}
}

func TestHandleCallbackUnknownPrefix(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	cb := &TelegramCallbackQuery{
		ID:   "cb-unknown",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 40,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "unknown:data",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery for unknown prefix")
	}
	if mock.answers[0].text != "" {
		t.Errorf("expected empty toast for unknown prefix, got %q", mock.answers[0].text)
	}
}

func TestHandleCallbackNilMessage(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	cb := &TelegramCallbackQuery{
		ID:      "cb-nil",
		From:    &TelegramUser{ID: 100},
		Message: nil,
		Data:    "level:beginner",
	}
	handleCallback(store, mock, cb)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery even with nil message")
	}
}

// ---------------------------------------------------------------------------
// handleReviewCallback edge cases
// ---------------------------------------------------------------------------

func TestHandleReviewCallbackMalformedData(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}

	store.AddSubscriber(100)

	// Missing word part (just "srs:known" with no second colon)
	cb := &TelegramCallbackQuery{
		ID:   "cb-srs-mal",
		From: &TelegramUser{ID: 100},
		Message: &TelegramMessage{
			MessageID: 50,
			Chat:      TelegramChat{ID: 100},
		},
		Data: "srs:known",
	}
	// The Data prefix "srs:" is stripped by handleCallback before calling handleReviewCallback
	handleReviewCallback(store, mock, cb, 100)

	if len(mock.answers) == 0 {
		t.Fatal("expected answerCallbackQuery for malformed data")
	}
}

// ---------------------------------------------------------------------------
// Config function tests
// ---------------------------------------------------------------------------

func TestGetEnvInt(t *testing.T) {
	// Valid int.
	t.Setenv("TEST_INT_VALID", "42")
	if got := getEnvInt("TEST_INT_VALID", 10); got != 42 {
		t.Errorf("got %d, want 42", got)
	}

	// Invalid int falls back.
	t.Setenv("TEST_INT_INVALID", "abc")
	if got := getEnvInt("TEST_INT_INVALID", 10); got != 10 {
		t.Errorf("got %d, want fallback 10", got)
	}

	// Missing key falls back.
	if got := getEnvInt("TEST_INT_MISSING_KEY_XYZ", 99); got != 99 {
		t.Errorf("got %d, want fallback 99", got)
	}
}

func TestGetEnvDuration(t *testing.T) {
	// Valid duration.
	t.Setenv("TEST_DUR_VALID", "5m")
	if got := getEnvDuration("TEST_DUR_VALID", time.Second); got != 5*time.Minute {
		t.Errorf("got %v, want 5m", got)
	}

	// Invalid duration falls back.
	t.Setenv("TEST_DUR_INVALID", "not-a-duration")
	if got := getEnvDuration("TEST_DUR_INVALID", 30*time.Second); got != 30*time.Second {
		t.Errorf("got %v, want fallback 30s", got)
	}

	// Missing key falls back.
	if got := getEnvDuration("TEST_DUR_MISSING_KEY_XYZ", time.Hour); got != time.Hour {
		t.Errorf("got %v, want fallback 1h", got)
	}
}

func TestLoadLocation(t *testing.T) {
	saveAppLocation(t)

	// Save and restore the global timezone string.
	origTZ := appTimezone
	t.Cleanup(func() { appTimezone = origTZ })

	// Valid timezone.
	appTimezone = "America/New_York"
	loadLocation()
	if appLocation.String() != "America/New_York" {
		t.Errorf("appLocation = %q, want America/New_York", appLocation.String())
	}

	// Invalid timezone falls back to UTC.
	appTimezone = "Invalid/Nonexistent"
	loadLocation()
	if appLocation != time.UTC {
		t.Errorf("appLocation = %q, want UTC for invalid timezone", appLocation.String())
	}
}
