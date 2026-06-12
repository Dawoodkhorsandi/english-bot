package app

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Dawoodkhorsandi/english-bot/internal/telegram"
)

// ---------------------------------------------------------------------------
// Grammar lessons — a static, pre-authored curriculum (no AI at request time).
//
// Lessons live in the embedded webapp/grammar/lessons.json and are served both
// to the chat (/grammar) and the Mini App (/api/grammar[/lesson]). Everything is
// instant: free-tier model latency/quota make live AI chat impractical here, so
// all content and practice questions are baked in and scored client-side.
// ---------------------------------------------------------------------------

// GrammarPractice is one pre-authored multiple-choice item (known answer).
type GrammarPractice struct {
	Q       string   `json:"q"`
	Options []string `json:"options"`
	Answer  int      `json:"answer"`
}

// GrammarLesson is one grammar lesson, easy → hard by Order.
type GrammarLesson struct {
	ID          string            `json:"id"`
	Order       int               `json:"order"`
	Level       string            `json:"level"`
	Title       string            `json:"title"`
	Pattern     string            `json:"pattern"`
	Explanation string            `json:"explanation"`
	Examples    []string          `json:"examples"`
	Tip         string            `json:"tip"`
	Image       string            `json:"image,omitempty"` // optional diagram URL (rendered if present)
	Practice    []GrammarPractice `json:"practice"`
}

var (
	grammarOnce    sync.Once
	grammarLessons []GrammarLesson
)

// loadGrammarLessons reads + caches the embedded lessons, sorted by Order.
func loadGrammarLessons() []GrammarLesson {
	grammarOnce.Do(func() {
		raw, err := webappFiles.ReadFile("webapp/grammar/lessons.json")
		if err != nil {
			log.Printf("⚠️  [GRAMMAR] Could not read embedded lessons: %v", err)
			return
		}
		if err := json.Unmarshal(raw, &grammarLessons); err != nil {
			log.Printf("⚠️  [GRAMMAR] Could not parse lessons: %v", err)
			return
		}
		sort.Slice(grammarLessons, func(i, j int) bool { return grammarLessons[i].Order < grammarLessons[j].Order })
	})
	return grammarLessons
}

// grammarLessonByID returns a lesson by its id, or ok=false.
func grammarLessonByID(id string) (GrammarLesson, bool) {
	for _, l := range loadGrammarLessons() {
		if l.ID == id {
			return l, true
		}
	}
	return GrammarLesson{}, false
}

// ---------------------------------------------------------------------------
// Mini App API
// ---------------------------------------------------------------------------

// handleAPIGrammar returns the lesson list (lightweight: no examples/practice).
func handleAPIGrammar(w http.ResponseWriter, _ *http.Request, _ int64, _ *Store) {
	type item struct {
		ID    string `json:"id"`
		Order int    `json:"order"`
		Level string `json:"level"`
		Title string `json:"title"`
	}
	lessons := loadGrammarLessons()
	out := make([]item, 0, len(lessons))
	for _, l := range lessons {
		out = append(out, item{ID: l.ID, Order: l.Order, Level: l.Level, Title: l.Title})
	}
	writeJSON(w, map[string]interface{}{"lessons": out})
}

// handleAPIGrammarLesson returns one full lesson by ?id=.
func handleAPIGrammarLesson(w http.ResponseWriter, r *http.Request, _ int64, _ *Store) {
	l, ok := grammarLessonByID(strings.TrimSpace(r.URL.Query().Get("id")))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, l)
}

// ---------------------------------------------------------------------------
// Chat command — /grammar [number]
// ---------------------------------------------------------------------------

// formatGrammarIndex renders the numbered lesson list for the chat.
func formatGrammarIndex() string {
	lessons := loadGrammarLessons()
	var b strings.Builder
	b.WriteString("📖 <b>Grammar lessons</b>\n\n")
	for i, l := range lessons {
		fmt.Fprintf(&b, "%d. <b>%s</b> <i>(%s)</i>\n", i+1, l.Title, levelLabel(l.Level))
	}
	b.WriteString("\nSend <code>/grammar 1</code> for a lesson, or open the app for interactive practice.")
	return b.String()
}

// formatGrammarLesson renders one lesson as an HTML chat card.
func formatGrammarLesson(l GrammarLesson) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📖 <b>%s</b> <i>(%s)</i>\n", l.Title, levelLabel(l.Level))
	fmt.Fprintf(&b, "\n🧩 <b>Pattern</b>\n<code>%s</code>\n", l.Pattern)
	fmt.Fprintf(&b, "\n💬 <b>How it works</b>\n%s\n", l.Explanation)
	if len(l.Examples) > 0 {
		b.WriteString("\n📝 <b>Examples</b>\n")
		for _, ex := range l.Examples {
			fmt.Fprintf(&b, "• %s\n", ex)
		}
	}
	if l.Tip != "" {
		fmt.Fprintf(&b, "\n💡 <i>%s</i>", l.Tip)
	}
	return b.String()
}

// handleGrammar handles /grammar [number]: with no argument it sends the lesson
// index; with a number it sends that lesson's card.
func handleGrammar(store *Store, notifier telegram.Notifier, chatID int64, args []string) {
	lessons := loadGrammarLessons()
	if len(lessons) == 0 {
		_ = notifier.Send(chatID, "📖 Grammar lessons are unavailable right now. Please try again later.")
		return
	}
	if len(args) == 0 {
		_ = notifier.Send(chatID, formatGrammarIndex())
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || n < 1 || n > len(lessons) {
		_ = notifier.Send(chatID, fmt.Sprintf("Please choose a lesson between 1 and %d, e.g. <code>/grammar 1</code>.", len(lessons)))
		return
	}
	_ = notifier.Send(chatID, formatGrammarLesson(lessons[n-1]))
}
