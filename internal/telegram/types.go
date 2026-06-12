// Package telegram holds the Telegram Bot API data types, the low-level POST
// transport, and the Notifier abstraction used by the rest of the bot to send
// messages. It depends only on the config package (for the bot token).
package telegram

// Update is a single Telegram Bot API update.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
	PollAnswer    *PollAnswer    `json:"poll_answer"`
}

// Message is an inbound text message.
type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
	From      *User  `json:"from"`
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
