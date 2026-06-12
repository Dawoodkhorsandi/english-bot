package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
)

// Notifier abstracts the Telegram Bot API send operations so handlers and
// schedulers can be tested without HTTP calls. The production implementation is
// returned by NewNotifier; tests inject their own mock.
type Notifier interface {
	Send(chatID int64, text string) error
	SendWithMessageID(chatID int64, text string) (int64, error)
	SendVoice(chatID int64, voice []byte, filename string, replyToMessageID int64) (fileID string, err error)
	SendVoiceByFileID(chatID int64, fileID string, replyToMessageID int64) error
	SendDocument(chatID int64, doc []byte, filename, caption string) error
	SendKeyboard(chatID int64, text string, keyboard [][]InlineButton) error
	EditMessage(chatID, messageID int64, text string, keyboard [][]InlineButton) error
	AnswerCallback(callbackID, text string) error
	// SendTyping fires a "typing..." chat action — best-effort, errors ignored.
	SendTyping(chatID int64)
	// SendPoll sends a native Telegram quiz poll and returns its poll_id.
	SendPoll(chatID int64, question string, options []string, correctIdx int, explanation string) (pollID string, err error)
	// SendWithReplyKeyboard sends a text message with a persistent bottom keyboard.
	SendWithReplyKeyboard(chatID int64, text string, rows [][]string) error
}

// NewNotifier returns the real Notifier that talks to the Telegram Bot API.
func NewNotifier() Notifier { return &notifier{} }

// notifier is the real Notifier that talks to the Telegram Bot API.
type notifier struct{}

func (n *notifier) Send(chatID int64, text string) error {
	_, err := n.SendWithMessageID(chatID, text)
	return err
}

func (n *notifier) SendWithMessageID(chatID int64, text string) (int64, error) {
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	jsonPayload, _ := json.Marshal(payload)

	log.Printf("➔ [HTTP_POST] sendMessage to ChatID %d | payload %d bytes", chatID, len(jsonPayload))
	resp, err := httpClient.Post(apiURL("sendMessage"), "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("telegram sendMessage read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("telegram returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed struct {
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return 0, fmt.Errorf("telegram sendMessage parse error: %w", err)
	}
	return parsed.Result.MessageID, nil
}

func (n *notifier) SendVoice(chatID int64, voice []byte, filename string, replyToMessageID int64) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return "", err
	}
	if replyToMessageID > 0 {
		if err := writer.WriteField("reply_to_message_id", strconv.FormatInt(replyToMessageID, 10)); err != nil {
			return "", err
		}
	}

	part, err := writer.CreateFormFile("voice", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(voice); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, apiURL("sendVoice"), &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("telegram sendVoice returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Extract file_id from the response for caching.
	var parsed struct {
		Result struct {
			Voice *struct {
				FileID string `json:"file_id"`
			} `json:"voice"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err == nil && parsed.Result.Voice != nil {
		return parsed.Result.Voice.FileID, nil
	}
	return "", nil
}

func (n *notifier) SendVoiceByFileID(chatID int64, fileID string, replyToMessageID int64) error {
	payload := map[string]interface{}{
		"chat_id": chatID,
		"voice":   fileID,
	}
	if replyToMessageID > 0 {
		payload["reply_to_message_id"] = replyToMessageID
	}
	return Post("sendVoice", payload)
}

func (n *notifier) SendDocument(chatID int64, doc []byte, filename, caption string) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return err
	}
	if strings.TrimSpace(caption) != "" {
		if err := writer.WriteField("caption", caption); err != nil {
			return err
		}
		if err := writer.WriteField("parse_mode", "HTML"); err != nil {
			return err
		}
	}

	part, err := writer.CreateFormFile("document", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(doc); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, apiURL("sendDocument"), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram sendDocument returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (n *notifier) SendKeyboard(chatID int64, text string, keyboard [][]InlineButton) error {
	log.Printf("➔ [HTTP_POST] sendMessage(+keyboard) to ChatID %d", chatID)
	return Post("sendMessage", map[string]interface{}{
		"chat_id":      chatID,
		"text":         text,
		"parse_mode":   "HTML",
		"reply_markup": map[string]interface{}{"inline_keyboard": keyboard},
	})
}

func (n *notifier) EditMessage(chatID, messageID int64, text string, keyboard [][]InlineButton) error {
	return Post("editMessageText", map[string]interface{}{
		"chat_id":      chatID,
		"message_id":   messageID,
		"text":         text,
		"parse_mode":   "HTML",
		"reply_markup": map[string]interface{}{"inline_keyboard": keyboard},
	})
}

func (n *notifier) AnswerCallback(callbackID, text string) error {
	payload := map[string]interface{}{"callback_query_id": callbackID}
	if text != "" {
		payload["text"] = text
	}
	return Post("answerCallbackQuery", payload)
}

func (n *notifier) SendTyping(chatID int64) {
	_ = Post("sendChatAction", map[string]interface{}{
		"chat_id": chatID,
		"action":  "typing",
	})
}

func (n *notifier) SendPoll(chatID int64, question string, options []string, correctIdx int, explanation string) (string, error) {
	opts := make([]map[string]string, len(options))
	for i, o := range options {
		opts[i] = map[string]string{"text": o}
	}
	payload := map[string]interface{}{
		"chat_id":           chatID,
		"question":          question,
		"options":           opts,
		"type":              "quiz",
		"correct_option_id": correctIdx,
		"is_anonymous":      false,
	}
	if explanation != "" {
		payload["explanation"] = explanation
	}
	jsonPayload, _ := json.Marshal(payload)
	resp, err := httpClient.Post(apiURL("sendPoll"), "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sendPoll status %d: %s", resp.StatusCode, string(respBody))
	}
	var parsed struct {
		Result struct {
			Poll struct {
				ID string `json:"id"`
			} `json:"poll"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("sendPoll parse: %w", err)
	}
	return parsed.Result.Poll.ID, nil
}

func (n *notifier) SendWithReplyKeyboard(chatID int64, text string, rows [][]string) error {
	btns := make([][]map[string]string, len(rows))
	for i, row := range rows {
		btns[i] = make([]map[string]string, len(row))
		for j, label := range row {
			btns[i][j] = map[string]string{"text": label}
		}
	}
	return Post("sendMessage", map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
		"reply_markup": map[string]interface{}{
			"keyboard":        btns,
			"resize_keyboard": true,
		},
	})
}
