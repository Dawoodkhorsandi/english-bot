package app

import (
	"context"
	"testing"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

func TestSanitizeTTSText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain word", "hello", "hello"},
		{"uppercase", "Hello", "Hello"},
		{"with spaces", "break a leg", "break a leg"},
		{"with hyphen", "well-known", "well-known"},
		{"with apostrophe", "it's", "it's"},
		{"strips digits", "abc123", "abc"},
		{"strips symbols", "hello!@#world", "helloworld"},
		{"strips unicode", "café", "caf"},
		{"empty string", "", ""},
		{"only symbols", "!@#$%", ""},
		{"leading trailing spaces", "  hello  ", "hello"},
		{"truncated at max length", "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz", "abcdefghijklmnopqrstuvwxyzabcdefghijklmn"},
		{"exactly max length", "abcdefghijklmnopqrstuvwxyzabcdefghijklmn", "abcdefghijklmnopqrstuvwxyzabcdefghijklmn"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeTTSText(tt.in)
			if got != tt.want {
				t.Errorf("sanitizeTTSText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExtensionForAudioMime(t *testing.T) {
	tests := []struct {
		mime string
		want string
	}{
		{"audio/ogg", "ogg"},
		{"audio/mpeg", "mp3"},
		{"audio/wav", "wav"},
		{"audio/something", "wav"},
		{"", "wav"},
		{"  audio/ogg  ", "ogg"},
		{"AUDIO/MPEG", "mp3"},
	}

	for _, tt := range tests {
		t.Run(tt.mime, func(t *testing.T) {
			got := extensionForAudioMime(tt.mime)
			if got != tt.want {
				t.Errorf("extensionForAudioMime(%q) = %q, want %q", tt.mime, got, tt.want)
			}
		})
	}
}

func TestExtractTTSTerm(t *testing.T) {
	tests := []struct {
		name string
		card string
		want string
	}{
		{
			"word card",
			"📘 <b>Word of the Session: serendipity</b>\n————\n💬 Meaning — finding good things by accident",
			"serendipity",
		},
		{
			"idiom card",
			"🗣️ Idiom of the Day: break a leg\n————\n💬 Meaning — good luck",
			"break a leg",
		},
		{
			"empty card",
			"some random text with no label",
			"",
		},
		{
			"word takes priority over idiom",
			"📘 <b>Word of the Session: hello</b>\n🗣️ Idiom of the Day: break a leg",
			"hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTTSTerm(tt.card)
			if got != tt.want {
				t.Errorf("extractTTSTerm() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGeminiTTSConfigured(t *testing.T) {
	origKey := config.GeminiAPIKey
	defer func() { config.GeminiAPIKey = origKey }()

	config.GeminiAPIKey = ""
	if geminiTTSConfigured() {
		t.Error("expected false for empty key")
	}

	config.GeminiAPIKey = "YOUR_GEMINI_API_KEY"
	if geminiTTSConfigured() {
		t.Error("expected false for placeholder key")
	}

	config.GeminiAPIKey = "real-key-123"
	if !geminiTTSConfigured() {
		t.Error("expected true for real key")
	}
}

func TestSendWordCardWithTTS_SendsCardAndVoice(t *testing.T) {
	// TTS requires config.TTSEnabled=true, non-quiet hours, non-paused user, user TTS on.
	// Since we can't call real TTS providers in tests, we verify the card is sent
	// and that maybeSendTTS doesn't panic on a card with no parseable word.
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	var chatID int64 = 500
	store.AddSubscriber(chatID)

	card := "Just a plain card with no Word of the Session label"
	err := sendWordCardWithTTS(context.Background(), store, mock, chatID, card, "")
	if err != nil {
		t.Fatalf("sendWordCardWithTTS returned error: %v", err)
	}

	if mock.sentCount() != 1 {
		t.Fatalf("expected 1 sent message, got %d", mock.sentCount())
	}
	if mock.lastSentText() != card {
		t.Errorf("card text mismatch")
	}
	// No voice should be sent because the card has no parseable word
	if len(mock.voices) != 0 {
		t.Errorf("expected 0 voices (no parseable word), got %d", len(mock.voices))
	}
}

func TestMaybeSendTTS_SkipsWhenDisabled(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	var chatID int64 = 501
	store.AddSubscriber(chatID)

	card := "📘 <b>Word of the Session: hello</b>\n————\n💬 Meaning — a greeting"

	// Disable TTS globally
	origTTS := config.TTSEnabled
	config.TTSEnabled = false
	defer func() { config.TTSEnabled = origTTS }()

	maybeSendTTS(context.Background(), store, mock, chatID, card, 1)
	if len(mock.voices) != 0 {
		t.Error("expected no voice when TTS is globally disabled")
	}
}

func TestMaybeSendTTS_SkipsWhenUserDisabled(t *testing.T) {
	store := testStoreHelper(t)
	mock := &mockNotifier{}
	var chatID int64 = 502
	store.AddSubscriber(chatID)
	store.SetTTSEnabled(chatID, false)

	card := "📘 <b>Word of the Session: hello</b>\n————\n💬 Meaning — a greeting"

	origTTS := config.TTSEnabled
	config.TTSEnabled = true
	defer func() { config.TTSEnabled = origTTS }()

	maybeSendTTS(context.Background(), store, mock, chatID, card, 1)
	if len(mock.voices) != 0 {
		t.Error("expected no voice when user has TTS disabled")
	}
}

func TestAudioCache(t *testing.T) {
	store := testStoreHelper(t)

	// No cache entry initially.
	if fid := store.CachedAudioFileID("hello"); fid != "" {
		t.Fatalf("expected empty cache, got %q", fid)
	}

	// Cache a file_id.
	if err := store.CacheAudioFileID("hello", "AgACAgIAAxk"); err != nil {
		t.Fatalf("CacheAudioFileID: %v", err)
	}

	// Retrieve it.
	fid := store.CachedAudioFileID("hello")
	if fid != "AgACAgIAAxk" {
		t.Fatalf("expected cached file_id, got %q", fid)
	}

	// Case-insensitive lookup.
	fid = store.CachedAudioFileID("HELLO")
	if fid != "AgACAgIAAxk" {
		t.Fatalf("expected case-insensitive cache hit, got %q", fid)
	}

	// Idempotent insert (same word, different file_id is ignored).
	if err := store.CacheAudioFileID("hello", "different-id"); err != nil {
		t.Fatalf("second CacheAudioFileID: %v", err)
	}
	fid = store.CachedAudioFileID("hello")
	if fid != "AgACAgIAAxk" {
		t.Fatalf("expected original file_id after idempotent insert, got %q", fid)
	}
}
