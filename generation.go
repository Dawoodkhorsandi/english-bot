package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

const (
	kindDrill = "drill"
	kindWord  = "word"
	kindIdiom = "idiom"
	kindTip   = "tip"
)

// drillPageGroups defines how a drill's numbered forms are split across paged
// Telegram messages. The sizes sum to the 21 forms drillPromptBase emits; the
// last group absorbs any extra forms if the model returns more.
var drillPageGroups = []struct {
	Title string
	Size  int
}{
	{"Everyday Essentials", 4},
	{"Perfect Tenses", 4},
	{"More Past and Future", 5},
	{"Conditionals", 4},
	{"Modals and More", 4},
}

// drillItemStart matches the first line of a numbered drill form, e.g. "<b>3. …".
var drillItemStart = regexp.MustCompile(`^\s*<b>\d+\.`)

// drillPage is one rendered page of a drill: a themed title and its forms.
type drillPage struct {
	title string
	items []string
}

// drillPromptBase is the grammar-drill prompt without the per-user exclusion clause.
// The 21 numbered forms are ordered to match drillPageGroups so the drill can be
// paginated into themed pages (present / past / future / conditionals / modals).
const drillPromptBase = `Choose ONE common, useful everyday English verb and produce a Grammar Muscle Memory Drill.

Use this exact HTML format (for Telegram). Replace each {sentence} with a short, natural sentence (max 12 words). Wrap the target verb form inside <b>…</b> in every sentence.

🎯 <b>Verb of the Hour: {VERB}</b>
————————————————————

<b>1. Simple Present</b> · Routine / Habit
→ {sentence}

<b>2. Present Continuous</b> · Right Now / Temporary
→ {sentence}

<b>3. Simple Past</b> · Finished Action
→ {sentence}

<b>4. Future: will</b> · Prediction / Spontaneous Decision
→ {sentence}

<b>5. Present Perfect</b> · Experience / Recent Result
→ {sentence}

<b>6. Present Perfect Continuous</b> · Ongoing Until Now
→ {sentence}

<b>7. Past Perfect</b> · Before Another Past Event
→ {sentence}

<b>8. Past Perfect Continuous</b> · Duration Before a Past Event
→ {sentence}

<b>9. Past Continuous</b> · Was in Progress
→ {sentence}

<b>10. Future: be going to</b> · Plan / Intention
→ {sentence}

<b>11. Future Continuous</b> · In Progress at a Future Moment
→ {sentence}

<b>12. Future Perfect</b> · Completed by a Future Point
→ {sentence}

<b>13. Future Perfect Continuous</b> · Duration up to a Future Point
→ {sentence}

<b>14. Zero Conditional</b> · General Truth / Always Result
→ {sentence}

<b>15. First Conditional</b> · Real Future Possibility
→ {sentence}

<b>16. Second Conditional</b> · Unreal / Hypothetical Present
→ {sentence}

<b>17. Third Conditional</b> · Unreal Past / Regret
→ {sentence}

<b>18. Modal Verb</b> · Advice / Obligation (should / must)
→ {sentence}

<b>19. Passive Voice</b> · Focus on the Receiver
→ {sentence}

<b>20. Imperative</b> · Command / Instruction
→ {sentence}

<b>21. Used to</b> · Past Habit (no longer true)
→ {sentence}

💡 <i>Say each sentence out loud — build the muscle memory!</i>

Rules:
- Replace {VERB} with the base form of the chosen verb (e.g. walk).
- Keep every numbered form (1–21) and keep them in this exact order.
- Keep sentences short (max 12 words) and natural for daily conversation.
- Bold only the verb form using <b>…</b>. Use no other HTML tags.
- Output the drill only — no preamble, no explanation.`

// wordPromptBase is the vocabulary-card prompt without the per-user exclusion clause.
const wordPromptBase = `Choose ONE useful English word (any part of speech) that an English learner would benefit from knowing, and produce a Vocabulary Card.

Use this EXACT HTML format (for Telegram). Replace each {…} placeholder with real content. Keep it concise and easy to read.

📘 <b>Word of the Hour: {WORD}</b>
————————————————————

💬 <b>Meaning</b>
{one clear, simple sentence explaining the meaning}

🔊 <b>Pronunciation</b>
{simple syllable spelling, e.g. VIG-uhr-uhs-lee}  ·  {official IPA, e.g. /ˈvɪɡ.ɚ.əs.li/}

✅ <b>Synonyms</b>
{3–5 synonyms, comma-separated}

⛔ <b>Opposites</b>
{2–4 antonyms, comma-separated}

📝 <b>Examples</b>
• {natural everyday example sentence}
• {a second example in a different context}

💡 <i>Read it aloud and try using it in your own sentence today!</i>

Rules:
- Replace {WORD} with the chosen word in its base/dictionary form.
- Bold the target word using <b>…</b> inside each example sentence.
- Use only <b> and <i> HTML tags — no other tags or Markdown.
- Keep example sentences short (max 14 words) and natural.
- Output the card only — no preamble, no explanation.`

// idiomPromptBase is the idiom-card prompt without the per-user exclusion clause.
const idiomPromptBase = `Choose ONE common, useful English idiom or fixed expression that a learner would hear in everyday conversation, and produce an Idiom Card.

Use this EXACT HTML format (for Telegram). Replace each {…} placeholder with real content. Keep it concise and easy to read.

🗣️ <b>Idiom of the Day: {IDIOM}</b>
————————————————————

💬 <b>Meaning</b>
{one clear, simple sentence explaining what the idiom means}

📝 <b>Examples</b>
• {natural everyday sentence using the idiom}
• {a second example in a different context}

🌍 <b>When to use</b>
{one short sentence on tone/context — e.g. casual, work, encouraging}

💡 <i>Try slipping it into a conversation today!</i>

Rules:
- Replace {IDIOM} with the idiom in its normal lowercase form (e.g. break the ice).
- Pick a real, widely-used idiom — not a literal phrase or a single word.
- Bold the idiom using <b>…</b> inside each example sentence.
- Use only <b> and <i> HTML tags — no other tags or Markdown.
- Keep example sentences short (max 14 words) and natural.
- Output the card only — no preamble, no explanation.`

// tipPromptBase is the grammar-tip prompt without the exclusion clause.
const tipPromptBase = `Produce exactly ONE bite-sized grammar tip for English learners in Telegram HTML.

Use this exact format:

💡 <b>Grammar Tip of the Day</b>
————————————————————
📌 <b>Topic:</b> {TOPIC}

<b>Rule:</b> {one short, focused explanation of one rule/nuance}

✅ <b>Correct:</b>
• {correct example 1}
• {correct example 2}

❌ <b>Incorrect:</b>
• {incorrect example}

💬 <i>Say it aloud: "{one natural spoken sentence that applies the rule}"</i>

Rules:
- Focus on one grammar rule, common mistake, or usage nuance only.
- Keep all examples short and natural for everyday English.
- Use only <b> and <i> HTML tags.
- Topic should be concise and specific.
- Output only the formatted tip card with no preamble.`

func buildDrillPrompt(level string, exclude []string) string {
	prompt := drillPromptBase + "\n\n" + levelInstruction(level)
	if len(exclude) == 0 {
		return prompt
	}
	return prompt + fmt.Sprintf(
		"\n\nIMPORTANT: Do NOT use any of these verbs (already practiced): %s.\nChoose a completely different everyday verb not in that list.",
		strings.Join(exclude, ", "),
	)
}

func buildWordPrompt(level string, exclude []string) string {
	prompt := wordPromptBase + "\n\n" + levelInstruction(level)
	if len(exclude) == 0 {
		return prompt
	}
	return prompt + fmt.Sprintf(
		"\n\nIMPORTANT: Do NOT use any of these words (already sent): %s.\nChoose a completely different word not in that list.",
		strings.Join(exclude, ", "),
	)
}

// buildIdiomPrompt builds the idiom-card prompt for the given level, appending the
// per-user exclusion clause when the learner has already seen some idioms.
func buildIdiomPrompt(level string, exclude []string) string {
	prompt := idiomPromptBase + "\n\n" + levelInstruction(level)
	if len(exclude) == 0 {
		return prompt
	}
	return prompt + fmt.Sprintf(
		"\n\nIMPORTANT: Do NOT use any of these idioms (already sent): %s.\nChoose a completely different idiom not in that list.",
		strings.Join(exclude, ", "),
	)
}

func buildTipPrompt(exclude []string) string {
	if len(exclude) == 0 {
		return tipPromptBase
	}
	return tipPromptBase + fmt.Sprintf(
		"\n\nIMPORTANT: Avoid these already-pooled grammar topics: %s.\nChoose a different grammar topic not in that list.",
		strings.Join(exclude, ", "),
	)
}

// buildWordLookupPrompt builds a vocabulary-card prompt for a SPECIFIC term the
// user supplied (Change M). The model resolves the input to its English headword
// — translating from another language if needed — and builds the card around it.
func buildWordLookupPrompt(level, term string) string {
	return wordPromptBase + "\n\n" + levelInstruction(level) + fmt.Sprintf(`

LOOKUP MODE — the learner asked about: "%s"
- Build the card for THIS specific word. Do NOT pick a different word.
- If the input is not English (e.g. Persian/Farsi), translate it to its most common English equivalent and build the card around that English word.
- If the input is an inflected or misspelled form, use its correct base/dictionary form as {WORD}.
- If the input is a short phrase, treat its key headword as {WORD}.`, term)
}

// levelInstruction returns a difficulty directive injected into the prompt so the
// chosen verb/word and example sentences match the user's selected level.
func levelInstruction(level string) string {
	switch level {
	case levelBeginner:
		return "DIFFICULTY: Target CEFR A1–A2 (beginner) learners. Pick a very common, simple, high-frequency word and keep all example sentences short and easy."
	case levelAdvanced:
		return "DIFFICULTY: Target CEFR C1–C2 (advanced) learners. Pick a sophisticated, less common word and use richer, more nuanced example sentences."
	default:
		return "DIFFICULTY: Target CEFR B1–B2 (intermediate) learners. Pick a moderately common, useful word with natural example sentences."
	}
}

// generateContent builds the prompt for kind+level, runs the provider chain, and
// parses the term (and meaning, for words). Returns (text, term, meaning, provider, err).
func generateContent(ctx context.Context, chain *ProviderChain, kind, level string, exclude []string) (text, term, meaning, provider string, err error) {
	var prompt string
	switch kind {
	case kindWord:
		prompt = buildWordPrompt(level, exclude)
	case kindIdiom:
		prompt = buildIdiomPrompt(level, exclude)
	case kindTip:
		prompt = buildTipPrompt(exclude)
	default:
		prompt = buildDrillPrompt(level, exclude)
	}

	text, provider, err = chain.Generate(ctx, prompt)
	if err != nil {
		return "", "", "", "", err
	}

	switch kind {
	case kindWord:
		term = parseWord(text)
		meaning = parseMeaning(text)
	case kindIdiom:
		term = parseIdiom(text)
		meaning = parseMeaning(text)
	case kindTip:
		term = parseTipTopic(text)
	default:
		term = parseVerb(text)
	}
	return text, term, meaning, provider, nil
}

// generateWordFor generates a vocabulary card for a specific user-supplied term
// (Change M), resolving/translating it to an English headword. Returns the card
// text, the resolved English word, its meaning, and the provider used.
func generateWordFor(ctx context.Context, chain *ProviderChain, level, term string) (text, word, meaning, provider string, err error) {
	text, provider, err = chain.Generate(ctx, buildWordLookupPrompt(level, term))
	if err != nil {
		return "", "", "", "", err
	}
	return text, parseWord(text), parseMeaning(text), provider, nil
}

// parseVerb extracts the verb from the "Verb of the Hour:" line of a drill.
func parseVerb(drill string) string {
	return parseLabeledTerm(drill, "verb of the hour")
}

// parseWord extracts the word from the "Word of the Hour:" line of a vocab card.
func parseWord(card string) string {
	return parseLabeledTerm(card, "word of the hour")
}

// parseIdiom extracts the full idiom phrase from the "Idiom of the Day:" line.
// Unlike parseLabeledTerm it keeps the whole multi-word phrase (idioms aren't
// single tokens), stripping any trailing HTML tag and surrounding punctuation.
func parseIdiom(card string) string {
	for _, line := range strings.Split(card, "\n") {
		if !strings.Contains(strings.ToLower(line), "idiom of the day") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx == -1 {
			continue
		}
		term := line[idx+1:]
		if lt := strings.Index(term, "<"); lt != -1 {
			term = term[:lt]
		}
		term = strings.Trim(term, " *_`[]()\t\r")
		term = strings.TrimSpace(term)
		if term != "" {
			return strings.ToLower(term)
		}
	}
	return ""
}

// parseTipTopic extracts the topic from the "Topic:" line of a grammar tip.
func parseTipTopic(tip string) string {
	for _, line := range strings.Split(tip, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "topic") || !strings.Contains(line, ":") {
			continue
		}
		plain := stripHTMLTags(line)
		idx := strings.Index(plain, ":")
		if idx == -1 {
			continue
		}
		topic := strings.TrimSpace(plain[idx+1:])
		topic = strings.ToLower(strings.Trim(topic, " *_`[]().,!?"))
		if topic != "" {
			return topic
		}
	}
	return ""
}

// parseLabeledTerm scans text for a line containing label (case-insensitive),
// then extracts the first token after the ":" separator, stripping HTML and
// Markdown punctuation, and lowercases it.
func parseLabeledTerm(text, label string) string {
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, label) {
			continue
		}
		idx := strings.Index(line, ":")
		if idx == -1 {
			continue
		}
		term := line[idx+1:]
		if lt := strings.Index(term, "<"); lt != -1 {
			term = term[:lt]
		}
		term = strings.Trim(term, " *_`[]()\t\r")
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		if fields := strings.Fields(term); len(fields) > 0 {
			term = fields[0]
		}
		term = strings.Trim(term, " *_`[]().,!?")
		return strings.ToLower(term)
	}
	return ""
}

// parseMeaning extracts the one-line meaning that follows the "Meaning" header in
// a vocabulary card, stripping any HTML tags. Empty if not found.
func parseMeaning(card string) string {
	lines := strings.Split(card, "\n")
	for i, line := range lines {
		if !strings.Contains(strings.ToLower(line), "meaning") || !strings.Contains(line, "<b>") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			m := strings.TrimSpace(stripHTMLTags(lines[j]))
			if m != "" {
				return m
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Drill pagination (Change N) — split a drill into themed, navigable pages
// ---------------------------------------------------------------------------

// parseDrillBody splits a generated drill into its header (the title block before
// the first numbered form), the individual numbered form blocks, and the footer
// (the closing tip, identified by the 💡 line). Any of the three may be empty.
func parseDrillBody(text string) (header string, items []string, footer string) {
	lines := strings.Split(text, "\n")

	// Footer = from the first 💡 line to the end (the "say it aloud" tip).
	footerStart := len(lines)
	for i, ln := range lines {
		if strings.Contains(ln, "💡") {
			footerStart = i
			break
		}
	}
	footer = strings.TrimSpace(strings.Join(lines[footerStart:], "\n"))
	body := lines[:footerStart]

	firstItem := -1
	for i, ln := range body {
		if drillItemStart.MatchString(ln) {
			firstItem = i
			break
		}
	}
	if firstItem == -1 {
		return strings.TrimSpace(strings.Join(body, "\n")), nil, footer
	}
	header = strings.TrimSpace(strings.Join(body[:firstItem], "\n"))

	var cur []string
	flush := func() {
		if len(cur) > 0 {
			items = append(items, strings.TrimSpace(strings.Join(cur, "\n")))
			cur = nil
		}
	}
	for _, ln := range body[firstItem:] {
		if drillItemStart.MatchString(ln) {
			flush()
		}
		cur = append(cur, ln)
	}
	flush()
	return header, items, footer
}

// paginateDrillItems groups parsed drill forms into pages per drillPageGroups.
// The final group absorbs any overflow so no form is ever dropped.
func paginateDrillItems(items []string) []drillPage {
	var pages []drillPage
	idx := 0
	for gi, g := range drillPageGroups {
		if idx >= len(items) {
			break
		}
		end := idx + g.Size
		if gi == len(drillPageGroups)-1 || end > len(items) {
			end = len(items)
		}
		pages = append(pages, drillPage{title: g.Title, items: items[idx:end]})
		idx = end
	}
	if idx < len(items) {
		pages = append(pages, drillPage{title: "More", items: items[idx:]})
	}
	return pages
}

// renderDrillPage builds the message text for the given 1-based page of a drill
// and returns it along with the total page count. If the drill can't be parsed
// into forms, it returns the full text as a single page so delivery degrades
// gracefully. Out-of-range pages are clamped.
func renderDrillPage(fullText string, page int) (text string, total int) {
	header, items, footer := parseDrillBody(fullText)
	if len(items) == 0 {
		return fullText, 1
	}

	pages := paginateDrillItems(items)
	total = len(pages)
	if page < 1 {
		page = 1
	}
	if page > total {
		page = total
	}

	var b strings.Builder
	if header != "" {
		b.WriteString(header)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n📄 <i>Page %d/%d · %s</i>\n\n", page, total, pages[page-1].title)
	b.WriteString(strings.Join(pages[page-1].items, "\n\n"))
	if footer != "" {
		b.WriteString("\n\n")
		b.WriteString(footer)
	}
	return b.String(), total
}

// drillNavKeyboard builds the prev/next navigation row for a paged drill. The
// verb is embedded in the callback data so a tap can reload the full drill from
// the pool. Returns nil when there is only one page (no navigation needed).
func drillNavKeyboard(term string, page, total int) [][]inlineButton {
	if total <= 1 {
		return nil
	}
	var row []inlineButton
	if page > 1 {
		row = append(row, inlineButton{Text: "◀️ Back", CallbackData: fmt.Sprintf("drill:%d:%s", page-1, term)})
	}
	row = append(row, inlineButton{Text: fmt.Sprintf("%d/%d", page, total), CallbackData: "drill:noop"})
	if page < total {
		row = append(row, inlineButton{Text: "Next ▶️", CallbackData: fmt.Sprintf("drill:%d:%s", page+1, term)})
	}
	return [][]inlineButton{row}
}

// stripHTMLTags removes everything between '<' and '>' and trims the result.
func stripHTMLTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(b.String())
}
