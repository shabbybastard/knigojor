package bot

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Telegram is the small slice of the Bot API used by the UI.
type Telegram interface {
	SendMessage(ctx context.Context, params *tgbot.SendMessageParams) (*models.Message, error)
	EditMessageText(ctx context.Context, params *tgbot.EditMessageTextParams) (*models.Message, error)
	SendDocument(ctx context.Context, params *tgbot.SendDocumentParams) (*models.Message, error)
	AnswerCallbackQuery(ctx context.Context, params *tgbot.AnswerCallbackQueryParams) (bool, error)
	DeleteMessage(ctx context.Context, params *tgbot.DeleteMessageParams) (bool, error)
}

// MessageManager keeps one editable interface message per user.
type MessageManager struct {
	tg    Telegram
	locks sync.Map // map[int64]*sync.Mutex
}

// NewMessageManager creates a message manager.
func NewMessageManager(tg Telegram) *MessageManager {
	return &MessageManager{tg: tg}
}

// SetTelegram replaces the Telegram client (used during wiring and tests).
func (m *MessageManager) SetTelegram(tg Telegram) {
	m.tg = tg
}

// Lock returns the per-user serialization mutex.
func (m *MessageManager) Lock(userID int64) *sync.Mutex {
	value, _ := m.locks.LoadOrStore(userID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

// SendOrEdit edits messageID when possible and otherwise sends a new message.
func (m *MessageManager) SendOrEdit(
	ctx context.Context,
	chatID int64,
	messageID int,
	text string,
	markup models.ReplyMarkup,
) (int, error) {
	return m.sendOrEdit(ctx, chatID, messageID, text, markup, disabledLinkPreview())
}

// SendOrEditWithLinkPreview is SendOrEdit with explicit link preview options.
func (m *MessageManager) SendOrEditWithLinkPreview(
	ctx context.Context,
	chatID int64,
	messageID int,
	text string,
	markup models.ReplyMarkup,
	preview *models.LinkPreviewOptions,
) (int, error) {
	return m.sendOrEdit(ctx, chatID, messageID, text, markup, preview)
}

// Replace sends a fresh message and removes the previous one. This moves the
// main UI message below files that were just sent.
func (m *MessageManager) Replace(
	ctx context.Context,
	chatID int64,
	oldID int,
	text string,
	markup models.ReplyMarkup,
) (int, error) {
	return m.replace(ctx, chatID, oldID, text, markup, disabledLinkPreview())
}

// ReplaceWithLinkPreview is Replace with explicit link preview options.
func (m *MessageManager) ReplaceWithLinkPreview(
	ctx context.Context,
	chatID int64,
	oldID int,
	text string,
	markup models.ReplyMarkup,
	preview *models.LinkPreviewOptions,
) (int, error) {
	return m.replace(ctx, chatID, oldID, text, markup, preview)
}

func (m *MessageManager) replace(
	ctx context.Context,
	chatID int64,
	oldID int,
	text string,
	markup models.ReplyMarkup,
	preview *models.LinkPreviewOptions,
) (int, error) {
	newID, err := m.sendOrEdit(ctx, chatID, 0, text, markup, preview)
	if err != nil {
		return 0, err
	}
	if oldID != 0 && m.tg != nil {
		if _, deleteErr := m.tg.DeleteMessage(ctx, &tgbot.DeleteMessageParams{
			ChatID:    chatID,
			MessageID: oldID,
		}); deleteErr != nil {
			slog.Warn("failed to delete previous UI message", "message_id", oldID, "error", deleteErr)
		}
	}
	return newID, nil
}

func (m *MessageManager) sendOrEdit(
	ctx context.Context,
	chatID int64,
	messageID int,
	text string,
	markup models.ReplyMarkup,
	preview *models.LinkPreviewOptions,
) (int, error) {
	if m.tg == nil {
		return 0, errors.New("telegram client is not configured")
	}
	text = TruncateMessage(text)

	if messageID != 0 {
		for attempt := 0; attempt < 2; attempt++ {
			_, err := m.tg.EditMessageText(ctx, &tgbot.EditMessageTextParams{
				ChatID:             chatID,
				MessageID:          messageID,
				Text:               text,
				ParseMode:          models.ParseModeHTML,
				LinkPreviewOptions: preview,
				ReplyMarkup:        markup,
			})
			if err == nil {
				return messageID, nil
			}
			var retryAfter *tgbot.TooManyRequestsError
			if errors.As(err, &retryAfter) {
				if attempt == 0 {
					slog.Warn("telegram flood limit", "retry_after", retryAfter.RetryAfter)
					sleep(ctx, time.Duration(retryAfter.RetryAfter)*time.Second)
					continue
				}
				return 0, err
			}
			message := strings.ToLower(err.Error())
			if strings.Contains(message, "message is not modified") {
				return messageID, nil
			}
			if strings.Contains(message, "message to edit not found") ||
				strings.Contains(message, "message can't be edited") {
				break
			}
			slog.Warn("edit failed", "error", err)
			break
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		message, err := m.tg.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID:             chatID,
			Text:               text,
			ParseMode:          models.ParseModeHTML,
			LinkPreviewOptions: preview,
			ReplyMarkup:        markup,
		})
		if err == nil {
			return message.ID, nil
		}
		var retryAfter *tgbot.TooManyRequestsError
		if errors.As(err, &retryAfter) {
			if attempt == 0 {
				slog.Warn("telegram flood limit", "retry_after", retryAfter.RetryAfter)
				sleep(ctx, time.Duration(retryAfter.RetryAfter)*time.Second)
				continue
			}
			return 0, err
		}
		return 0, err
	}
	return 0, errors.New("failed to send message")
}

// TruncateMessage trims generated HTML on line boundaries.
func TruncateMessage(text string) string {
	if utf16Len(text) <= telegramSafeLimit {
		return text
	}
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	size := 0
	limit := telegramSafeLimit - 20
	for _, line := range lines {
		lineLength := utf16Len(line)
		if size+lineLength+1 > limit {
			break
		}
		kept = append(kept, line)
		size += lineLength + 1
	}
	if len(kept) == 0 {
		cut := cutUTF16(text, limit)
		if lastOpen := strings.LastIndex(cut, "<"); lastOpen > strings.LastIndex(cut, ">") {
			cut = cut[:lastOpen]
		}
		return cut + "\n\n<i>…</i>"
	}
	return strings.Join(kept, "\n") + "\n\n<i>…</i>"
}

func boolPtr(value bool) *bool {
	return &value
}

func disabledLinkPreview() *models.LinkPreviewOptions {
	return &models.LinkPreviewOptions{IsDisabled: boolPtr(true)}
}

func sleep(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
