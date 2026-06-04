package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const geminiTTSModel = "gemini-2.5-flash"
const maxTTSTextLength = 40

var ttsHTTPClient = &http.Client{Timeout: 45 * time.Second}

// sendWordCardWithTTS sends a word card and then (best-effort) a pronunciation
// voice note as a reply to that card.
func sendWordCardWithTTS(ctx context.Context, store *Store, notifier Notifier, chatID int64, card string) error {
	msgID, err := notifier.SendWithMessageID(chatID, card)
	if err != nil {
		return err
	}
	maybeSendTTS(ctx, store, notifier, chatID, card, msgID)
	return nil
}

// sendIdiomCardWithTTS sends an idiom card and then (best-effort) a pronunciation
// voice note as a reply to that card.
func sendIdiomCardWithTTS(ctx context.Context, store *Store, notifier Notifier, chatID int64, card string) error {
	msgID, err := notifier.SendWithMessageID(chatID, card)
	if err != nil {
		return err
	}
	maybeSendTTS(ctx, store, notifier, chatID, card, msgID)
	return nil
}

// extractTTSTerm tries to extract a speakable term from a card. It checks for
// a vocabulary word first, then falls back to an idiom phrase.
func extractTTSTerm(card string) string {
	if w := strings.TrimSpace(parseWord(card)); w != "" {
		return w
	}
	return strings.TrimSpace(parseIdiom(card))
}

// maybeSendTTS tries Gemini TTS first, then falls back to espeak-ng.
// Any error is logged and swallowed so card delivery is never blocked.
// Generated audio is cached by word in audio_cache so subsequent sends for the
// same word reuse the Telegram file_id (zero-cost re-send).
func maybeSendTTS(ctx context.Context, store *Store, notifier Notifier, chatID int64, card string, replyToMessageID int64) {
	if !ttsEnabled || isQuietHours(time.Now()) || store.IsPaused(chatID) || !store.GetTTSEnabled(chatID) {
		return
	}

	word := extractTTSTerm(card)
	if word == "" {
		return
	}
	word = sanitizeTTSText(word)
	if word == "" {
		return
	}

	// Check audio_cache first: reuse a previously uploaded Telegram file_id.
	if fileID := store.CachedAudioFileID(word); fileID != "" {
		if err := notifier.SendVoiceByFileID(chatID, fileID, replyToMessageID); err != nil {
			log.Printf("⚠️  [TTS] Cached sendVoice failed for %q (chat %d): %v", word, chatID, err)
		}
		return
	}

	audio, ext, err := generateGeminiTTS(ctx, word)
	if err != nil {
		log.Printf("⚠️  [TTS] Gemini TTS failed for %q (chat %d): %v. Falling back to espeak-ng.", word, chatID, err)
		audio, ext, err = generateESpeakTTS(ctx, word)
		if err != nil {
			log.Printf("⚠️  [TTS] Fallback espeak-ng failed for %q (chat %d): %v", word, chatID, err)
			return
		}
	}

	filename := "word." + ext
	fileID, err := notifier.SendVoice(chatID, audio, filename, replyToMessageID)
	if err != nil {
		log.Printf("⚠️  [TTS] sendVoice failed for %q (chat %d): %v", word, chatID, err)
		return
	}
	// Cache the Telegram file_id so future sends of the same word skip generation.
	if fileID != "" {
		if err := store.CacheAudioFileID(word, fileID); err != nil {
			log.Printf("⚠️  [TTS] Could not cache file_id for %q: %v", word, err)
		}
	}
}

func generateGeminiTTS(ctx context.Context, text string) ([]byte, string, error) {
	if !geminiTTSConfigured() {
		return nil, "", fmt.Errorf("gemini api key not configured")
	}

	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		geminiTTSModel, url.QueryEscape(GeminiAPIKey),
	)
	payload := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]string{
					{"text": fmt.Sprintf("Pronounce this English word clearly and naturally: %q", text)},
				},
			},
		},
		"generationConfig": map[string]any{
			"responseModalities": []string{"AUDIO"},
		},
		"speechConfig": map[string]any{
			"voiceConfig": map[string]any{
				"prebuiltVoiceConfig": map[string]any{
					"voiceName": "Aoede",
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ttsHTTPClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("gemini tts read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("gemini tts http %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData *struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, "", err
	}
	for _, c := range parsed.Candidates {
		for _, p := range c.Content.Parts {
			if p.InlineData == nil || p.InlineData.Data == "" {
				continue
			}
			audio, err := base64.StdEncoding.DecodeString(p.InlineData.Data)
			if err != nil {
				return nil, "", err
			}
			return audio, extensionForAudioMime(p.InlineData.MimeType), nil
		}
	}

	return nil, "", fmt.Errorf("gemini tts returned no audio")
}

func geminiTTSConfigured() bool {
	key := strings.TrimSpace(GeminiAPIKey)
	if key == "" {
		return false
	}
	// Treat obvious placeholders as not configured.
	if strings.HasPrefix(strings.ToUpper(key), "YOUR_") {
		return false
	}
	return true
}

func generateESpeakTTS(ctx context.Context, text string) ([]byte, string, error) {
	tmp, err := os.CreateTemp("", "english-bot-tts-*.wav")
	if err != nil {
		return nil, "", err
	}
	path := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(path)

	out, err := exec.CommandContext(ctx, "espeak-ng", "-w", path, text).CombinedOutput()
	if err != nil {
		return nil, "", fmt.Errorf("espeak-ng failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	audio, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return audio, "wav", nil
}

func extensionForAudioMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "audio/ogg":
		return "ogg"
	case "audio/mpeg":
		return "mp3"
	default:
		return "wav"
	}
}

func sanitizeTTSText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '\'':
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return ""
	}
	// Keep voice clips short and focused.
	runes := []rune(out)
	if len(runes) > maxTTSTextLength {
		out = string(runes[:maxTTSTextLength])
	}
	return out
}
