// Package telegram holds the Telegram Bot API data types, the low-level POST
// transport, and the Notifier abstraction used by the rest of the bot to send
// messages. It depends only on the config package (for the bot token).
package telegram

// Update is a single Telegram Bot API update.
type Update struct {
	UpdateID         int64             `json:"update_id"`
	Message          *Message          `json:"message"`
	CallbackQuery    *CallbackQuery    `json:"callback_query"`
	PollAnswer       *PollAnswer       `json:"poll_answer"`
	PreCheckoutQuery *PreCheckoutQuery `json:"pre_checkout_query"`
}

// Message is an inbound text message.
type Message struct {
	MessageID         int64              `json:"message_id"`
	Chat              Chat               `json:"chat"`
	Text              string             `json:"text"`
	From              *User              `json:"from"`
	SuccessfulPayment *SuccessfulPayment `json:"successful_payment"`
	Voice             *Voice             `json:"voice"`
}

// Voice is an inbound voice note (e.g. for pronunciation practice).
type Voice struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
}

// PreCheckoutQuery is an inbound checkout confirmation that must be answered
// within ~10s before Telegram completes a payment (e.g. a Telegram Stars buy).
type PreCheckoutQuery struct {
	ID             string `json:"id"`
	From           *User  `json:"from"`
	Currency       string `json:"currency"`
	TotalAmount    int    `json:"total_amount"`
	InvoicePayload string `json:"invoice_payload"`
}

// SuccessfulPayment arrives (on a Message) once a payment completes.
type SuccessfulPayment struct {
	Currency         string `json:"currency"`
	TotalAmount      int    `json:"total_amount"`
	InvoicePayload   string `json:"invoice_payload"`
	TelegramChargeID string `json:"telegram_payment_charge_id"`
}

// CallbackQuery represents a tap on an inline-keyboard button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// Chat identifies a Telegram chat.
type Chat struct {
	ID int64 `json:"id"`
}

// User identifies a Telegram user.
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

// PollAnswer is sent when a user answers a native Telegram poll.
type PollAnswer struct {
	PollID    string `json:"poll_id"`
	User      *User  `json:"user"`
	OptionIDs []int  `json:"option_ids"`
}

// InlineButton is one button in an inline keyboard.
type InlineButton struct {
	Text         string      `json:"text"`
	CallbackData string      `json:"callback_data,omitempty"`
	WebApp       *WebAppInfo `json:"web_app,omitempty"`
}

// WebAppInfo points an inline button at a Telegram Mini App URL.
type WebAppInfo struct {
	URL string `json:"url"`
}
