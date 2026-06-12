package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
