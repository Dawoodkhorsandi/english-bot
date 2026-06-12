package main

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Dawoodkhorsandi/english-bot/internal/ai"
)

// mockNotifier implements the Notifier interface for tests, capturing all calls
// so assertions can inspect what was sent.
type mockNotifier struct {
	mu       sync.Mutex
	sent     []sentMsg
	voices   []sentVoice
	docs     []sentDoc
	keyboard []sentKeyboard
	edits    []sentEdit
	answers  []sentAnswer
}

type sentMsg struct {
	chatID int64
	text   string
}

type sentKeyboard struct {
	chatID   int64
	text     string
	keyboard [][]inlineButton
}

type sentVoice struct {
	chatID           int64
	filename         string
	replyToMessageID int64
	size             int
}

type sentDoc struct {
	chatID   int64
	filename string
	caption  string
	size     int
}

type sentEdit struct {
	chatID    int64
	messageID int64
	text      string
	keyboard  [][]inlineButton
}

type sentAnswer struct {
	callbackID string
	text       string
}

func (m *mockNotifier) Send(chatID int64, text string) error {
	_, err := m.SendWithMessageID(chatID, text)
	return err
}

func (m *mockNotifier) SendWithMessageID(chatID int64, text string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMsg{chatID, text})
	return int64(len(m.sent)), nil
}

func (m *mockNotifier) SendVoice(chatID int64, voice []byte, filename string, replyToMessageID int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.voices = append(m.voices, sentVoice{
		chatID:           chatID,
		filename:         filename,
		replyToMessageID: replyToMessageID,
		size:             len(voice),
	})
	return fmt.Sprintf("file_%d", len(m.voices)), nil
}

func (m *mockNotifier) SendVoiceByFileID(chatID int64, fileID string, replyToMessageID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.voices = append(m.voices, sentVoice{
		chatID:           chatID,
		filename:         fileID,
		replyToMessageID: replyToMessageID,
		size:             -1, // sentinel: indicates a cached re-send
	})
	return nil
}

func (m *mockNotifier) SendDocument(chatID int64, doc []byte, filename, caption string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs = append(m.docs, sentDoc{
		chatID:   chatID,
		filename: filename,
		caption:  caption,
		size:     len(doc),
	})
	return nil
}

func (m *mockNotifier) SendKeyboard(chatID int64, text string, kb [][]inlineButton) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keyboard = append(m.keyboard, sentKeyboard{chatID, text, kb})
	return nil
}

func (m *mockNotifier) EditMessage(chatID, messageID int64, text string, kb [][]inlineButton) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edits = append(m.edits, sentEdit{chatID, messageID, text, kb})
	return nil
}

func (m *mockNotifier) AnswerCallback(callbackID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.answers = append(m.answers, sentAnswer{callbackID, text})
	return nil
}

func (m *mockNotifier) SendTyping(_ int64) {}

type sentPoll struct {
	chatID     int64
	question   string
	options    []string
	correctIdx int
}

func (m *mockNotifier) SendPoll(chatID int64, question string, options []string, correctIdx int, _ string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := fmt.Sprintf("poll_%d", len(m.sent)+len(m.keyboard)+1)
	_ = sentPoll{chatID, question, options, correctIdx}
	// Store as a keyboard so existing keyboard-count assertions still work.
	m.keyboard = append(m.keyboard, sentKeyboard{chatID: chatID, text: question})
	return id, nil
}

func (m *mockNotifier) SendWithReplyKeyboard(chatID int64, text string, _ [][]string) error {
	_, err := m.SendWithMessageID(chatID, text)
	return err
}

// lastSentText returns the text of the last Send call, or "".
func (m *mockNotifier) lastSentText() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		return ""
	}
	return m.sent[len(m.sent)-1].text
}

// sentCount returns how many Send calls were made.
func (m *mockNotifier) sentCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

// sentTexts returns all sent texts.
func (m *mockNotifier) sentTexts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.sent))
	for i, s := range m.sent {
		out[i] = s.text
	}
	return out
}

// mockProvider implements the Provider interface for tests.
type mockProvider struct {
	name    string
	enabled bool
	text    string
	err     error
}

func (p *mockProvider) Name() string  { return p.name }
func (p *mockProvider) Enabled() bool { return p.enabled }
func (p *mockProvider) Generate(ctx context.Context, prompt string) (string, error) {
	return p.text, p.err
}

// mockProviderChain creates a ProviderChain with one mock provider.
func mockProviderChain(text string) *ai.ProviderChain {
	return ai.NewChain(&mockProvider{
		name:    "mock",
		enabled: true,
		text:    text,
	})
}

// emptyProviderChain creates a ProviderChain with no providers.
func emptyProviderChain() *ai.ProviderChain {
	return ai.NewChain()
}

// testStoreHelper creates a fresh store for testing. Uses t.Cleanup for teardown.
func testStoreHelper(t *testing.T) *Store {
	t.Helper()
	// Reset the global rate limiter so state from previous tests never bleeds in.
	globalHourlyLimiter.reset()
	t.Cleanup(func() { globalHourlyLimiter.reset() })

	store, err := openStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}
