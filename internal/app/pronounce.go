package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/Dawoodkhorsandi/english-bot/internal/ai"
	"github.com/Dawoodkhorsandi/english-bot/internal/telegram"
)

// ---------------------------------------------------------------------------
// Pronunciation check (#6)
//
// The user runs /pronounce <word>, then sends a voice note saying it. The bot
// downloads the audio, transcribes it with Gemini's audio model, and scores how
// closely the transcript matches the target — a cheap "good enough" pronunciation
// hint (not phoneme-grade) that closes the speaking-practice gap. The same
// scoring backs the /api/pronounce endpoint for the apps.
// ---------------------------------------------------------------------------

// pronounceTargets holds each chat's pending target word (set by /pronounce,
// consumed by the next voice note). Ephemeral by design.
var pronounceTargets sync.Map // chatID(int64) -> word(string)

// handlePronounce sets (or explains) the pronunciation target for a chat.
func handlePronounce(notifier telegram.Notifier, chatID int64, args []string) {
	if len(args) == 0 {
		_ = notifier.Send(chatID, "🎙️ <b>Pronunciation practice</b>\n\n"+
			"Send <code>/pronounce &lt;word&gt;</code> (e.g. <code>/pronounce thorough</code>), "+
			"then record a voice note saying the word. I'll tell you how close you were.")
		return
	}
	word := strings.ToLower(strings.Join(args, " "))
	pronounceTargets.Store(chatID, word)
	_ = notifier.Send(chatID, fmt.Sprintf("🎙️ Okay — now send me a <b>voice note</b> saying <b>%s</b>.", word))
}

// handlePronounceVoice scores a voice note against the chat's pending target.
func handlePronounceVoice(ctx context.Context, chain *ai.ProviderChain, notifier telegram.Notifier, chatID int64, voice *telegram.Voice) {
	v, ok := pronounceTargets.Load(chatID)
	if !ok {
		_ = notifier.Send(chatID, "🎙️ To practise pronunciation, send <code>/pronounce &lt;word&gt;</code> first, then a voice note.")
		return
	}
	target := v.(string)

	audio, err := telegram.DownloadFile(voice.FileID)
	if err != nil {
		log.Printf("⚠️  [PRONOUNCE] Could not download voice for chat %d: %v", chatID, err)
		_ = notifier.Send(chatID, "❌ I couldn't fetch that audio. Please try again.")
		return
	}
	mime := voice.MimeType
	if mime == "" {
		mime = "audio/ogg"
	}
	transcript, err := chain.Transcribe(ctx, audio, mime)
	if err != nil {
		log.Printf("⚠️  [PRONOUNCE] Transcription failed for chat %d: %v", chatID, err)
		_ = notifier.Send(chatID, "❌ I couldn't make out the audio this time. Try again in a quiet spot.")
		return
	}
	pronounceTargets.Delete(chatID)
	_ = notifier.Send(chatID, scorePronunciationMessage(target, transcript))
}

// handleAPIPronounce scores an uploaded recording (multipart: target + audio)
// for the Mini App / mobile app. Returns {target, transcript, score, verdict}.
func handleAPIPronounce(w http.ResponseWriter, r *http.Request, _ int64, _ *Store, chain *ai.ProviderChain) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB cap
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	target := strings.TrimSpace(r.FormValue("target"))
	file, hdr, err := r.FormFile("audio")
	if err != nil || target == "" {
		http.Error(w, "missing target or audio", http.StatusBadRequest)
		return
	}
	defer file.Close()
	audio, err := io.ReadAll(io.LimitReader(file, 10<<20))
	if err != nil {
		http.Error(w, "bad audio", http.StatusBadRequest)
		return
	}
	mime := hdr.Header.Get("Content-Type")
	if mime == "" {
		mime = "audio/webm"
	}
	transcript, err := chain.Transcribe(r.Context(), audio, mime)
	if err != nil {
		http.Error(w, "transcription unavailable", http.StatusServiceUnavailable)
		return
	}
	score, verdict := scorePronunciation(target, transcript)
	writeJSON(w, map[string]interface{}{
		"target":     target,
		"transcript": strings.TrimSpace(transcript),
		"score":      score,
		"verdict":    stripTags(verdict),
	})
}

// stripTags removes the small set of HTML tags used in verdict strings so the
// JSON API returns clean text.
func stripTags(s string) string {
	r := strings.NewReplacer("<b>", "", "</b>", "", "<i>", "", "</i>", "")
	return r.Replace(s)
}

// scorePronunciationMessage builds the user-facing feedback for a pronunciation
// attempt.
func scorePronunciationMessage(target, transcript string) string {
	sim, verdict := scorePronunciation(target, transcript)
	heard := strings.TrimSpace(transcript)
	if heard == "" {
		heard = "(nothing)"
	}
	return fmt.Sprintf("%s\n\n🎯 Target: <b>%s</b>\n👂 I heard: <i>%s</i>\n📊 Match: <b>%d%%</b>",
		verdict, target, heard, sim)
}

// scorePronunciation returns a 0–100 similarity and a verdict line comparing a
// transcript to the target word/phrase. It checks the whole transcript and each
// word, taking the best match (so leading filler like "the word is …" is fine).
func scorePronunciation(target, transcript string) (int, string) {
	t := normalizeSpeech(target)
	whole := normalizeSpeech(transcript)
	best := similarityPct(t, whole)
	for _, w := range strings.Fields(whole) {
		if s := similarityPct(t, w); s > best {
			best = s
		}
	}
	switch {
	case best >= 90:
		return best, "✅ <b>Excellent!</b> That sounds spot on."
	case best >= 70:
		return best, "👍 <b>Close!</b> Nearly there — try once more, a little clearer."
	case best >= 40:
		return best, "🤔 <b>Not quite.</b> Listen to the audio card and give it another go."
	default:
		return best, "❌ <b>Let's try again.</b> Say just the word, clearly, in a quiet place."
	}
}

// normalizeSpeech lowercases and keeps only letters and spaces.
func normalizeSpeech(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || r == ' ' {
			b.WriteRune(r)
		} else if r == '-' {
			b.WriteRune(' ')
		}
	}
	return strings.TrimSpace(b.String())
}

// similarityPct is a 0–100 Levenshtein-based similarity between two strings.
func similarityPct(a, b string) int {
	if a == "" && b == "" {
		return 100
	}
	d := levenshtein(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 0
	}
	return int(float64(maxLen-d) / float64(maxLen) * 100)
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
