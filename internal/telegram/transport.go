package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Dawoodkhorsandi/english-bot/internal/config"
)

// httpClient is the shared HTTP client for all Bot API calls.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// apiURL builds the Bot API endpoint URL for method.
func apiURL(method string) string {
	return fmt.Sprintf("https://api.telegram.org/bot%s/%s", config.TelegramBotToken, method)
}

// Post marshals payload and POSTs it to the given Bot API method.
func Post(method string, payload map[string]interface{}) error {
	jsonPayload, _ := json.Marshal(payload)

	resp, err := httpClient.Post(apiURL(method), "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram %s returned status %d: %s", method, resp.StatusCode, string(respBody))
	}
	return nil
}

// SendInvoice sends a Telegram Stars (currency "XTR") invoice. The provider
// token is empty for Stars. payload is echoed back on the pre-checkout query and
// the successful-payment message, so it carries what was bought (e.g. "freeze:5").
func SendInvoice(chatID int64, title, description, payload string, stars int) error {
	return Post("sendInvoice", map[string]interface{}{
		"chat_id":        chatID,
		"title":          title,
		"description":    description,
		"payload":        payload,
		"currency":       "XTR",
		"provider_token": "",
		"prices":         []map[string]interface{}{{"label": title, "amount": stars}},
	})
}

// DownloadFile fetches a Telegram file's bytes by file_id (getFile → download).
// Used to pull a user's voice note for pronunciation scoring.
func DownloadFile(fileID string) ([]byte, error) {
	resp, err := httpClient.Get(apiURL("getFile") + "?file_id=" + url.QueryEscape(fileID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var meta struct {
		Ok     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, err
	}
	if !meta.Ok || meta.Result.FilePath == "" {
		return nil, fmt.Errorf("getFile returned no file_path")
	}
	dlURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", config.TelegramBotToken, meta.Result.FilePath)
	resp2, err := httpClient.Get(dlURL)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("file download returned status %d", resp2.StatusCode)
	}
	return io.ReadAll(resp2.Body)
}

// AnswerPreCheckoutQuery confirms (or rejects) a pending checkout. Telegram
// requires an answer within ~10 seconds or the payment fails.
func AnswerPreCheckoutQuery(queryID string, ok bool, errorMessage string) error {
	payload := map[string]interface{}{"pre_checkout_query_id": queryID, "ok": ok}
	if !ok && errorMessage != "" {
		payload["error_message"] = errorMessage
	}
	return Post("answerPreCheckoutQuery", payload)
}
