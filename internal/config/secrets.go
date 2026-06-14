package config

// Credentials and identities, read from the environment at startup. GeminiAPIKey
// is consumed by the ai package and TelegramBotToken by the telegram package, so
// they live here in the shared leaf rather than in app.
var (
	TelegramBotToken = GetEnv("TELEGRAM_BOT_TOKEN", "YOUR_TELEGRAM_BOT_TOKEN")
	GeminiAPIKey     = GetEnv("GEMINI_API_KEY", "YOUR_GEMINI_API_KEY")
	MaintainerChatID = GetEnv("MAINTAINER_CHAT_ID", "YOUR_PERSONAL_CHAT_ID")
	JWTSecret        = GetEnv("JWT_SECRET", "change-me-to-a-random-string")
)
