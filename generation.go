package main

import (
	"context"
	"fmt"
	"strings"
)

const (
	kindDrill = "drill"
	kindWord  = "word"
)

// drillPromptBase is the grammar-drill prompt without the per-user exclusion clause.
const drillPromptBase = `Choose ONE common, useful everyday English verb and produce a Grammar Muscle Memory Drill.

Use this exact HTML format (for Telegram). Replace each {sentence} with a short, natural sentence (max 12 words). Wrap the target verb form inside <b>…</b> in every sentence.

🎯 <b>Verb of the Hour: {VERB}</b>
————————————————————

<b>1. Simple Present</b> · Routine / Habit
→ {sentence}

<b>2. Present Continuous</b> · Right Now / Temporary
→ {sentence}

<b>3. Present Perfect</b> · Experience / Recent Result
→ {sentence}

<b>4. Present Perfect Continuous</b> · Ongoing Until Now
→ {sentence}

<b>5. Simple Past</b> · Finished Action
→ {sentence}

<b>6. Past Continuous</b> · Was in Progress
→ {sentence}

<b>7. Past Perfect</b> · Before Another Past Event
→ {sentence}

<b>8. Past Perfect Continuous</b> · Duration Before a Past Event
→ {sentence}

<b>9. Future: be going to</b> · Plan / Intention
→ {sentence}

<b>10. Future: will</b> · Prediction / Spontaneous Decision
→ {sentence}

<b>11. Future Continuous</b> · In Progress at a Future Moment
→ {sentence}

<b>12. Future Perfect</b> · Completed by a Future Point
→ {sentence}

<b>13. Future Perfect Continuous</b> · Duration up to a Future Point
→ {sentence}

<b>14. First Conditional</b> · Real Future Possibility
→ {sentence}

💡 <i>Say each sentence out loud — build the muscle memory!</i>

Rules:
- Replace {VERB} with the base form of the chosen verb (e.g. walk).
- Keep sentences short (max 12 words) and natural for daily conversation.
- Bold only the verb form using <b>…</b>. Use no other HTML tags.
- Output the drill only — no preamble, no explanation.`

// wordPromptBase is the vocabulary-card prompt without the per-user exclusion clause.
const wordPromptBase = `Choose ONE useful intermediate / upper-intermediate English word (any part of speech) that an English learner would benefit from knowing, and produce a Vocabulary Card.

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
	if kind == kindWord {
		prompt = buildWordPrompt(level, exclude)
	} else {
		prompt = buildDrillPrompt(level, exclude)
	}

	text, provider, err = chain.Generate(ctx, prompt)
	if err != nil {
		return "", "", "", "", err
	}

	if kind == kindWord {
		term = parseWord(text)
		meaning = parseMeaning(text)
	} else {
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
