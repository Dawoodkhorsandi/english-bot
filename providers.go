package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genai"
)

// Provider is a single AI backend that can turn a prompt into drill/word text.
type Provider interface {
	Name() string
	Enabled() bool
	Generate(ctx context.Context, prompt string) (string, error)
}

// providerHTTPError carries the HTTP status from an OpenAI-compatible provider so
// the chain can fail over immediately on rate limits.
type providerHTTPError struct {
	status int
	body   string
}

func (e *providerHTTPError) Error() string {
	return fmt.Sprintf("http %d: %s", e.status, truncate(e.body, 200))
}
func (e *providerHTTPError) isRateLimit() bool { return e.status == 429 }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------------------------------------------------------------------------
// Gemini (native SDK)
// ---------------------------------------------------------------------------

type GeminiProvider struct {
	client *genai.Client
	model  string
}

func newGeminiProvider(ctx context.Context) *GeminiProvider {
	if GeminiAPIKey == "" || GeminiAPIKey == "YOUR_GEMINI_API_KEY" {
		return &GeminiProvider{}
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: GeminiAPIKey})
	if err != nil {
		log.Printf("⚠️  [PROVIDER] Gemini client init failed: %v", err)
		return &GeminiProvider{}
	}
	return &GeminiProvider{client: client, model: getEnv("GEMINI_MODEL", "gemini-2.5-flash")}
}

func (p *GeminiProvider) Name() string  { return "gemini" }
func (p *GeminiProvider) Enabled() bool { return p.client != nil }

func (p *GeminiProvider) Generate(ctx context.Context, prompt string) (string, error) {
	resp, err := p.client.Models.GenerateContent(ctx, p.model, genai.Text(prompt), nil)
	if err != nil {
		return "", err
	}
	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil && len(resp.Candidates[0].Content.Parts) > 0 {
		return resp.Candidates[0].Content.Parts[0].Text, nil
	}
	return "", fmt.Errorf("gemini returned no content")
}

// ---------------------------------------------------------------------------
// Generic OpenAI-compatible provider (Groq, OpenRouter, Cerebras, GitHub
// Models, Cloudflare, Mistral, …)
// ---------------------------------------------------------------------------

type OpenAICompatProvider struct {
	name    string
	apiKey  string
	baseURL string
	model   string
	headers map[string]string
	client  *http.Client
}

func (p *OpenAICompatProvider) Name() string  { return p.name }
func (p *OpenAICompatProvider) Enabled() bool { return p.apiKey != "" && p.baseURL != "" }

func (p *OpenAICompatProvider) Generate(ctx context.Context, prompt string) (string, error) {
	reqBody := map[string]any{
		"model":       p.model,
		"temperature": 1.0,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	buf, _ := json.Marshal(reqBody)

	endpoint := strings.TrimRight(p.baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", &providerHTTPError{status: resp.StatusCode, body: string(body)}
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("%s: bad JSON: %w", p.name, err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("%s returned no choices", p.name)
	}
	return parsed.Choices[0].Message.Content, nil
}

// ---------------------------------------------------------------------------
// Provider registry & chain
// ---------------------------------------------------------------------------

func buildProviders(ctx context.Context) map[string]Provider {
	httpClient := &http.Client{Timeout: 60 * time.Second}

	cfAccount := getEnv("CLOUDFLARE_ACCOUNT_ID", "")
	cfBase := ""
	if cfAccount != "" {
		cfBase = fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/v1", cfAccount)
	}

	return map[string]Provider{
		"gemini": newGeminiProvider(ctx),
		"groq": &OpenAICompatProvider{
			name: "groq", apiKey: getEnv("GROQ_API_KEY", ""),
			baseURL: getEnv("GROQ_BASE_URL", "https://api.groq.com/openai/v1"),
			model:   getEnv("GROQ_MODEL", "llama-3.3-70b-versatile"),
			client:  httpClient,
		},
		"cerebras": &OpenAICompatProvider{
			name: "cerebras", apiKey: getEnv("CEREBRAS_API_KEY", ""),
			baseURL: getEnv("CEREBRAS_BASE_URL", "https://api.cerebras.ai/v1"),
			model:   getEnv("CEREBRAS_MODEL", "llama-3.3-70b"),
			client:  httpClient,
		},
		"openrouter": &OpenAICompatProvider{
			name: "openrouter", apiKey: getEnv("OPENROUTER_API_KEY", ""),
			baseURL: getEnv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
			model:   getEnv("OPENROUTER_MODEL", "meta-llama/llama-3.3-70b-instruct:free"),
			client:  httpClient,
		},
		"github": &OpenAICompatProvider{
			name: "github", apiKey: getEnv("GITHUB_MODELS_TOKEN", ""),
			baseURL: getEnv("GITHUB_MODELS_BASE_URL", "https://models.github.ai/inference"),
			model:   getEnv("GITHUB_MODELS_MODEL", "openai/gpt-4o-mini"),
			client:  httpClient,
		},
		"cloudflare": &OpenAICompatProvider{
			name: "cloudflare", apiKey: getEnv("CLOUDFLARE_API_TOKEN", ""),
			baseURL: cfBase,
			model:   getEnv("CLOUDFLARE_MODEL", "@cf/meta/llama-3.1-8b-instruct"),
			client:  httpClient,
		},
		"mistral": &OpenAICompatProvider{
			name: "mistral", apiKey: getEnv("MISTRAL_API_KEY", ""),
			baseURL: getEnv("MISTRAL_BASE_URL", "https://api.mistral.ai/v1"),
			model:   getEnv("MISTRAL_MODEL", "mistral-small-latest"),
			client:  httpClient,
		},
		"gemini2": &OpenAICompatProvider{
			name: "gemini2", apiKey: getEnv("GEMINI_API_KEY", ""),
			baseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
			model:   getEnv("GEMINI2_MODEL", "gemini-2.0-flash"),
			client:  httpClient,
		},
		"sambanova": &OpenAICompatProvider{
			name: "sambanova", apiKey: getEnv("SAMBANOVA_API_KEY", ""),
			baseURL: getEnv("SAMBANOVA_BASE_URL", "https://api.sambanova.ai/v1"),
			model:   getEnv("SAMBANOVA_MODEL", "Meta-Llama-3.3-70B-Instruct"),
			client:  httpClient,
		},
		"cohere": &OpenAICompatProvider{
			name: "cohere", apiKey: getEnv("COHERE_API_KEY", ""),
			baseURL: getEnv("COHERE_BASE_URL", "https://api.cohere.ai/compatibility/v1"),
			model:   getEnv("COHERE_MODEL", "command-r"),
			client:  httpClient,
		},
	}
}

// ProviderChain holds the ordered, enabled providers and tries each in turn.
type ProviderChain struct {
	providers []Provider
}

func newProviderChain(ctx context.Context) *ProviderChain {
	all := buildProviders(ctx)
	var enabled []Provider
	for _, name := range strings.Split(providerOrder, ",") {
		name = strings.TrimSpace(strings.ToLower(name))
		p, ok := all[name]
		if !ok || p == nil {
			continue
		}
		if p.Enabled() {
			enabled = append(enabled, p)
			log.Printf("✅ [PROVIDER] Enabled: %s", p.Name())
		} else {
			log.Printf("➖ [PROVIDER] Skipped (no key/config): %s", name)
		}
	}
	if len(enabled) == 0 {
		log.Println("⚠️  [PROVIDER] No AI providers are enabled — set at least one API key.")
	}
	return &ProviderChain{providers: enabled}
}

func (c *ProviderChain) HasAny() bool { return len(c.providers) > 0 }

const providerAttempts = 2

// Generate tries each enabled provider in order with a small retry budget,
// failing over immediately on rate limits. Returns the text and winning provider.
func (c *ProviderChain) Generate(ctx context.Context, prompt string) (string, string, error) {
	if len(c.providers) == 0 {
		return "", "", fmt.Errorf("no AI providers configured")
	}

	var lastErr error
	for _, p := range c.providers {
		backoff := 2 * time.Second
		for attempt := 1; attempt <= providerAttempts; attempt++ {
			log.Printf("🧠 [AI] Provider %s attempt %d/%d...", p.Name(), attempt, providerAttempts)
			text, err := p.Generate(ctx, prompt)
			if err == nil && strings.TrimSpace(text) != "" {
				log.Printf("✅ [AI] Provider %s succeeded.", p.Name())
				return text, p.Name(), nil
			}

			if err != nil {
				lastErr = err
				var he *providerHTTPError
				if errors.As(err, &he) && he.isRateLimit() {
					log.Printf("⛔ [AI] Provider %s rate-limited (429). Failing over.", p.Name())
					break
				}
				log.Printf("⚠️  [AI] Provider %s attempt %d failed: %v", p.Name(), attempt, err)
			} else {
				lastErr = fmt.Errorf("%s returned empty text", p.Name())
				log.Printf("⚠️  [AI] Provider %s returned empty text.", p.Name())
			}

			if ctx.Err() != nil {
				return "", "", ctx.Err()
			}
			if attempt < providerAttempts {
				select {
				case <-time.After(backoff):
					backoff *= 2
				case <-ctx.Done():
					return "", "", ctx.Err()
				}
			}
		}
		log.Printf("➡️  [AI] Provider %s exhausted; trying next.", p.Name())
	}
	return "", "", fmt.Errorf("all providers failed: %w", lastErr)
}
