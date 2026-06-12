package content

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Dawoodkhorsandi/english-bot/internal/ai"
	"github.com/Dawoodkhorsandi/english-bot/internal/config"
	"github.com/Dawoodkhorsandi/english-bot/internal/telegram"
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

🎯 <b>Verb of the Session: {VERB}</b>
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

📘 <b>Word of the Session: {WORD}</b>
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

🇮🇷 <b>Persian</b>
<tg-spoiler>{short Persian/Farsi translation or definition — one or two words}</tg-spoiler>

💡 <i>Read it aloud and try using it in your own sentence today!</i>

Rules:
- Replace {WORD} with the chosen word in its base/dictionary form.
- Bold the target word using <b>…</b> inside each example sentence.
- Use only <b>, <i>, and <tg-spoiler> HTML tags — no other tags or Markdown.
- The Persian definition MUST be wrapped in <tg-spoiler>…</tg-spoiler> so it stays hidden until tapped.
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

// collocationPromptBase is the collocation-card prompt without the per-user
// exclusion clause.
const collocationPromptBase = `Choose ONE common, useful English collocation (a natural word partnership such as "make a decision", "heavy rain", "fast asleep") that a learner needs for natural-sounding English, and produce a Collocation Card.

Use this EXACT HTML format (for Telegram). Replace each {…} placeholder with real content. Keep it concise and easy to read.

🔗 <b>Collocation of the Day: {COLLOCATION}</b>
————————————————————

💬 <b>Meaning</b>
{one clear, simple sentence explaining what the collocation means}

📝 <b>Examples</b>
• {natural everyday sentence using the collocation}
• {a second example in a different context}

⚠️ <b>Watch out</b>
❌ {the wrong word combination learners often say} → ✅ {the correct collocation}

💡 <i>Say both examples out loud — word partnerships stick through repetition!</i>

Rules:
- Replace {COLLOCATION} with the collocation in its normal lowercase form (e.g. make a decision).
- Pick a real, widely-used collocation of two or three words — not an idiom and not a single word.
- Bold the collocation using <b>…</b> inside each example sentence.
- Use only <b> and <i> HTML tags — no other tags or Markdown.
- Keep example sentences short (max 14 words) and natural.
- Output the card only — no preamble, no explanation.`

// storyPromptBase is the mini-story prompt without the per-user exclusion clause.
const storyPromptBase = `Write ONE original mini story for an English learner, designed for reading practice, and produce a Story Card.

Use this EXACT HTML format (for Telegram). Replace each {…} placeholder with real content.

📖 <b>Mini Story: {TITLE}</b>
————————————————————

{the story — short paragraphs of everyday, natural English, separated by blank lines}

🔑 <b>Key Vocabulary</b>
• <b>{word or phrase from the story}</b> — {short, simple meaning}
• <b>{word or phrase from the story}</b> — {short, simple meaning}
• <b>{word or phrase from the story}</b> — {short, simple meaning}

🤔 <b>Think about it</b>
{one short comprehension question about the story}

💡 <i>Read it aloud once — then try retelling it in your own words!</i>

Rules:
- Replace {TITLE} with a short, original title of two to five words.
- Tell a small, self-contained everyday story (a situation, a small problem, a resolution).
- Pick 3 key vocabulary items that actually appear in the story and bold them with <b>…</b> inside the story text too.
- Use only <b> and <i> HTML tags — no other tags or Markdown.
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
	prompt := drillPromptBase + "\n\n" + levelInstruction(level) + "\n" + drillLengthDirective(level)
	if len(exclude) == 0 {
		return prompt
	}
	return prompt + fmt.Sprintf(
		"\n\nIMPORTANT: Do NOT use any of these verbs (already practiced): %s.\nChoose a completely different everyday verb not in that list.",
		strings.Join(exclude, ", "),
	)
}

// drillLengthDirective overrides the base prompt's sentence-length rule per level.
func drillLengthDirective(level string) string {
	switch level {
	case config.LevelBeginner:
		return "Override the max-words rule above: keep every drill sentence to max 8 words."
	case config.LevelUpperInt:
		return "Override the max-words rule above: sentences may be up to 14 words, with natural complexity."
	case config.LevelAdvanced:
		return "Override the max-words rule above: sentences may be up to 16 words, with rich structure."
	default:
		return "Keep every drill sentence to max 10 words with clear, simple grammar."
	}
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

func buildTipPrompt(level string, exclude []string) string {
	prompt := tipPromptBase + "\n\n" + tipLevelInstruction(level)
	if len(exclude) == 0 {
		return prompt
	}
	return prompt + fmt.Sprintf(
		"\n\nIMPORTANT: Avoid these already-pooled grammar topics: %s.\nChoose a different grammar topic not in that list.",
		strings.Join(exclude, ", "),
	)
}

// tipLevelInstruction returns a difficulty directive for grammar tips so the
// chosen topic and examples match the user's selected level.
func tipLevelInstruction(level string) string {
	switch level {
	case config.LevelBeginner:
		return "DIFFICULTY: Target CEFR A1–A2 (beginner) learners. Pick a basic grammar rule (articles, simple tenses, subject-verb agreement). Keep examples very short and simple."
	case config.LevelUpperInt:
		return "DIFFICULTY: Target CEFR B2 (upper-intermediate) learners. Pick a grammar nuance that trips up confident speakers — e.g. subtle tense distinctions, advanced conditionals, cleft sentences, inversion. Keep examples natural and conversational."
	case config.LevelAdvanced:
		return "DIFFICULTY: Target CEFR C1–C2 (advanced) learners. Pick a sophisticated grammar point — e.g. subjunctive mood, discourse markers, ellipsis, fronting. Use examples with literary or academic register."
	default:
		return "DIFFICULTY: Target CEFR B1 (intermediate) learners. Pick a common grammar topic (present perfect vs past simple, prepositions, modal verbs). Use clear, everyday examples."
	}
}

// BuildCollocationPrompt builds the collocation-card prompt for the given level,
// appending the per-user exclusion clause when the learner has already seen some.
func BuildCollocationPrompt(level string, exclude []string) string {
	prompt := collocationPromptBase + "\n\n" + levelInstruction(level)
	if len(exclude) == 0 {
		return prompt
	}
	return prompt + fmt.Sprintf(
		"\n\nIMPORTANT: Do NOT use any of these collocations (already sent): %s.\nChoose a completely different collocation not in that list.",
		strings.Join(exclude, ", "),
	)
}

// BuildStoryPrompt builds the mini-story prompt for the given level, appending
// the per-user exclusion clause (story titles) when the learner has seen some.
func BuildStoryPrompt(level string, exclude []string) string {
	prompt := storyPromptBase + "\n\n" + storyLevelInstruction(level)
	if len(exclude) == 0 {
		return prompt
	}
	return prompt + fmt.Sprintf(
		"\n\nIMPORTANT: Do NOT reuse any of these story titles or their plots: %s.\nWrite a completely different story with a different title.",
		strings.Join(exclude, ", "),
	)
}

// storyLevelInstruction returns a difficulty directive for mini stories so the
// story's length, vocabulary and sentence structure match the user's level.
func storyLevelInstruction(level string) string {
	switch level {
	case config.LevelBeginner:
		return "DIFFICULTY: Target CEFR A1–A2 (beginner) learners. Keep the story to about 60–80 words in 2 short paragraphs. Use only simple present and simple past, short sentences (max 8 words), and very common vocabulary."
	case config.LevelUpperInt:
		return "DIFFICULTY: Target CEFR B2 (upper-intermediate) learners. Keep the story to about 140–180 words in 3 paragraphs. Use varied tenses, natural conversational phrasing and some idiomatic expressions."
	case config.LevelAdvanced:
		return "DIFFICULTY: Target CEFR C1–C2 (advanced) learners. Keep the story to about 180–220 words in 3–4 paragraphs. Use rich, nuanced vocabulary, subordinate clauses and varied registers."
	default:
		return "DIFFICULTY: Target CEFR B1 (intermediate) learners. Keep the story to about 100–130 words in 2–3 paragraphs. Use clear everyday vocabulary and straightforward grammar with simple and continuous tenses."
	}
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
//
// Each level maps to a single CEFR band to avoid overlap:
//
//	beginner           → A1-A2  (high-frequency, simple)
//	intermediate       → B1     (common, practical, clear)
//	upper-intermediate → B2     (still conversational, but more nuanced)
//	advanced           → C1-C2  (sophisticated, less common)
func levelInstruction(level string) string {
	switch level {
	case config.LevelBeginner:
		return "DIFFICULTY: Target CEFR A1–A2 (beginner) learners. Pick a very common, simple, high-frequency word and keep all example sentences short and easy (max 8 words)."
	case config.LevelUpperInt:
		return "DIFFICULTY: Target CEFR B2 (upper-intermediate) learners. Pick a word that is useful in both everyday and slightly formal contexts — the kind of word a confident speaker uses naturally but a learner might still be acquiring. Use example sentences with moderate complexity: compound sentences, occasional idiomatic expressions, and natural conversational flow. Stay conversational — avoid academic or obscure vocabulary."
	case config.LevelAdvanced:
		return "DIFFICULTY: Target CEFR C1–C2 (advanced) learners. Pick a sophisticated, less common word found in academic, professional, or literary contexts. Use richer, more nuanced example sentences with subordinate clauses, idiomatic phrasing, and varied registers."
	default:
		return "DIFFICULTY: Target CEFR B1 (intermediate) learners. Pick a common, practical, everyday word. Use clear, simple example sentences (max 10 words) with straightforward grammar — no complex clauses or idioms."
	}
}

// GenerateContent builds the prompt for kind+level, runs the provider chain, and
// parses the term (and meaning, for words). Returns (text, term, meaning, provider, err).
func GenerateContent(ctx context.Context, chain *ai.ProviderChain, kind, level string, exclude []string) (text, term, meaning, provider string, err error) {
	var prompt string
	switch kind {
	case config.KindWord:
		prompt = buildWordPrompt(level, exclude)
	case config.KindIdiom:
		prompt = buildIdiomPrompt(level, exclude)
	case config.KindTip:
		prompt = buildTipPrompt(level, exclude)
	case config.KindCollocation:
		prompt = BuildCollocationPrompt(level, exclude)
	case config.KindStory:
		prompt = BuildStoryPrompt(level, exclude)
	default:
		prompt = buildDrillPrompt(level, exclude)
	}

	text, provider, err = chain.Generate(ctx, prompt)
	if err != nil {
		return "", "", "", "", err
	}

	switch kind {
	case config.KindWord:
		term = ParseWord(text)
		meaning = parseMeaning(text)
	case config.KindIdiom:
		term = ParseIdiom(text)
		meaning = parseMeaning(text)
	case config.KindTip:
		term = parseTipTopic(text)
	case config.KindCollocation:
		term = ParseCollocation(text)
		meaning = parseMeaning(text)
	case config.KindStory:
		term = ParseStoryTitle(text)
	default:
		term = ParseVerb(text)
	}
	return text, term, meaning, provider, nil
}

// GenerateWordFor generates a vocabulary card for a specific user-supplied term
// (Change M), resolving/translating it to an English headword. Returns the card
// text, the resolved English word, its meaning, and the provider used.
func GenerateWordFor(ctx context.Context, chain *ai.ProviderChain, level, term string) (text, word, meaning, provider string, err error) {
	text, provider, err = chain.Generate(ctx, buildWordLookupPrompt(level, term))
	if err != nil {
		return "", "", "", "", err
	}
	return text, ParseWord(text), parseMeaning(text), provider, nil
}

// ParseVerb extracts the verb from the "Verb of the Session:" line of a drill.
// Falls back to matching just "Verb:" if the full label isn't found, since some
// AI outputs use shorter headings like "Verb: walk" or "Today's Verb: run".
func ParseVerb(drill string) string {
	if v := parseLabeledTerm(drill, "verb of the session"); v != "" {
		return v
	}
	return parseLabeledTerm(drill, "verb")
}

// ParseWord extracts the word from the "Word of the Session:" line of a vocab card.
func ParseWord(card string) string {
	return parseLabeledTerm(card, "word of the session")
}

// ParseIdiom extracts the full idiom phrase from the "Idiom of the Day:" line.
func ParseIdiom(card string) string {
	return parseLabeledPhrase(card, "idiom of the day")
}

// ParseCollocation extracts the full collocation phrase from the
// "Collocation of the Day:" line of a collocation card.
func ParseCollocation(card string) string {
	return parseLabeledPhrase(card, "collocation of the day")
}

// ParseStoryTitle extracts the story title from the "Mini Story:" line.
func ParseStoryTitle(card string) string {
	return parseLabeledPhrase(card, "mini story")
}

// parseLabeledPhrase scans text for a line containing label (case-insensitive)
// and returns everything after the ":" separator. Unlike parseLabeledTerm it
// keeps the whole multi-word phrase (idioms, collocations and titles aren't
// single tokens), stripping any trailing HTML tag and surrounding punctuation.
func parseLabeledPhrase(text, label string) string {
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(strings.ToLower(line), label) {
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
		plain := StripHTMLTags(line)
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

// cardSection returns the first non-empty, HTML-stripped line that follows the
// bold section header containing keyword (case-insensitive) in a content card
// (e.g. "Meaning", "Pronunciation", "Persian", "Examples"). Empty if not found.
func cardSection(card, keyword string) string {
	kw := strings.ToLower(keyword)
	lines := strings.Split(card, "\n")
	for i, line := range lines {
		if !strings.Contains(strings.ToLower(line), kw) || !strings.Contains(line, "<b>") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			m := strings.TrimSpace(StripHTMLTags(lines[j]))
			if m != "" {
				return m
			}
		}
	}
	return ""
}

// parseMeaning extracts the one-line meaning that follows the "Meaning" header in
// a vocabulary card, stripping any HTML tags. Empty if not found.
func parseMeaning(card string) string { return cardSection(card, "Meaning") }

// ParsePronunciation extracts the pronunciation line (syllable spelling · IPA).
func ParsePronunciation(card string) string { return cardSection(card, "Pronunciation") }

// ParsePersian extracts the Persian/Farsi translation (the <tg-spoiler> content,
// with tags stripped).
func ParsePersian(card string) string { return cardSection(card, "Persian") }

// ParseExample returns the first example sentence from a card's Examples section,
// with any leading bullet removed.
func ParseExample(card string) string {
	ex := strings.TrimSpace(cardSection(card, "Examples"))
	ex = strings.TrimSpace(strings.TrimPrefix(ex, "•"))
	return strings.TrimSpace(ex)
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

// RenderDrillPage builds the message text for the given 1-based page of a drill
// and returns it along with the total page count. If the drill can't be parsed
// into forms, it returns the full text as a single page so delivery degrades
// gracefully. Out-of-range pages are clamped.
func RenderDrillPage(fullText string, page int) (text string, total int) {
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

// DrillNavKeyboard builds the prev/next navigation row for a paged drill. The
// verb is embedded in the callback data so a tap can reload the full drill from
// the pool. Returns nil when there is only one page (no navigation needed).
func DrillNavKeyboard(term string, page, total int) [][]telegram.InlineButton {
	if total <= 1 {
		return nil
	}
	var row []telegram.InlineButton
	if page > 1 {
		row = append(row, telegram.InlineButton{Text: "◀️ Back", CallbackData: fmt.Sprintf("drill:%d:%s", page-1, term)})
	}
	row = append(row, telegram.InlineButton{Text: fmt.Sprintf("%d/%d", page, total), CallbackData: "drill:noop"})
	if page < total {
		row = append(row, telegram.InlineButton{Text: "Next ▶️", CallbackData: fmt.Sprintf("drill:%d:%s", page+1, term)})
	}
	return [][]telegram.InlineButton{row}
}

// StripHTMLTags removes everything between '<' and '>' and trims the result.
func StripHTMLTags(s string) string {
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
