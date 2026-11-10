package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// HandleNewSearch asks the user for a query.
func (a *App) HandleNewSearch(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	if update.CallbackQuery == nil {
		return
	}
	a.answerCallback(ctx, update.CallbackQuery, "", false)
	user := update.EffectiveUser()
	chatID, ok := updateChatID(update)
	if user == nil || !ok {
		return
	}
	lock := a.Messages.Lock(user.ID)
	lock.Lock()
	defer lock.Unlock()

	state, err := a.State.Get(ctx, user.ID)
	if err != nil {
		a.OnError(err)
		return
	}
	state.ChatID = chatID
	state.Screen = "awaiting_search"
	if state.MessageID == 0 {
		state.MessageID = callbackMessageID(update.CallbackQuery)
	}
	text := "<b>FLIBUSTA / SEARCH</b>\n\n" +
		"Введите название или автора отдельным сообщением."
	messageID, err := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, text, SearchPromptKB())
	if err != nil {
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)
}

// HandleCancelSearch returns to the main screen.
func (a *App) HandleCancelSearch(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	if update.CallbackQuery == nil {
		return
	}
	a.answerCallback(ctx, update.CallbackQuery, "", false)
	user := update.EffectiveUser()
	chatID, ok := updateChatID(update)
	if user == nil || !ok {
		return
	}
	lock := a.Messages.Lock(user.ID)
	lock.Lock()
	defer lock.Unlock()

	state, err := a.State.Get(ctx, user.ID)
	if err != nil {
		a.OnError(err)
		return
	}
	state.ChatID = chatID
	state.Screen = "main"
	text := "<b>FLIBUSTA / knigojor</b>\n\nОтменено."
	messageID, err := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, text, MainMenuKB())
	if err != nil {
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)
}

// HandleText accepts the search query when the bot is waiting for one.
func (a *App) HandleText(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	message := update.Message
	user := update.EffectiveUser()
	if message == nil || user == nil {
		return
	}
	lock := a.Messages.Lock(user.ID)
	lock.Lock()
	defer lock.Unlock()

	state, err := a.State.Get(ctx, user.ID)
	if err != nil {
		a.OnError(err)
		return
	}
	if state.Screen != "awaiting_search" {
		return
	}
	state.ChatID = message.Chat.ID

	query := strings.TrimSpace(message.Text)
	if a.TG != nil {
		_, _ = a.TG.DeleteMessage(ctx, &tgbot.DeleteMessageParams{
			ChatID:    message.Chat.ID,
			MessageID: message.ID,
		})
	}
	if query == "" {
		text := "<b>FLIBUSTA / SEARCH</b>\n\nВведите непустой запрос."
		messageID, err := a.Messages.SendOrEdit(ctx, message.Chat.ID, state.MessageID, text, SearchPromptKB())
		if err != nil {
			a.OnError(err)
			return
		}
		state.MessageID = messageID
		_ = a.State.Save(ctx, state)
		return
	}

	state.Screen = "main"
	_ = a.State.Save(ctx, state)
	a.doSearch(ctx, message.Chat.ID, user.ID, query, 0)
}

// HandlePage moves between search result pages.
func (a *App) HandlePage(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	if cb == nil {
		return
	}
	parts := strings.Split(cb.Data, ":")
	direction := ""
	if len(parts) > 1 {
		direction = parts[1]
	}
	expectedPage := -1
	if len(parts) > 2 {
		if value, err := strconv.Atoi(parts[2]); err == nil {
			expectedPage = value
		}
	}
	user := update.EffectiveUser()
	if user == nil {
		return
	}

	state, err := a.State.Get(ctx, user.ID)
	if err != nil {
		a.OnError(err)
		return
	}
	if state.Screen != "results" || state.Query == "" {
		a.answerCallback(ctx, cb, "Нет активного поиска", true)
		return
	}
	a.answerCallback(ctx, cb, "", false)

	lock := a.Messages.Lock(user.ID)
	lock.Lock()
	defer lock.Unlock()

	state, err = a.State.Get(ctx, user.ID)
	if err != nil {
		a.OnError(err)
		return
	}
	if state.Screen != "results" || state.Query == "" {
		return
	}
	if expectedPage >= 0 && state.Page != expectedPage {
		return
	}
	chatID, ok := updateChatID(update)
	if !ok {
		return
	}
	newPage := state.Page
	switch direction {
	case "next":
		newPage++
	case "prev":
		newPage = max(0, newPage-1)
	}
	a.doSearch(ctx, chatID, user.ID, state.Query, newPage)
}

func (a *App) doSearch(ctx context.Context, chatID, userID int64, query string, uiPage int) {
	state, err := a.State.Get(ctx, userID)
	if err != nil {
		a.OnError(err)
		return
	}
	state.ChatID = chatID
	state.Screen = "searching"
	_ = a.State.Save(ctx, state)

	loading := fmt.Sprintf(
		"<b>FLIBUSTA / SEARCH</b>\n\nПоиск: %s\n\nЗагрузка...",
		Esc(Clip(query, 200)),
	)
	messageID, err := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, loading, nil)
	if err != nil {
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)

	page, err := LoadSearchPage(ctx, a.Client, query, uiPage, a.Settings.PageSize)
	if err != nil {
		slog.Error("search failed", "error", err)
		text := fmt.Sprintf(
			"<b>FLIBUSTA / SEARCH</b>\n\nОшибка поиска: %s\n\nЗапрос: %s",
			Esc(Clip(err.Error(), 300)),
			Esc(Clip(query, 200)),
		)
		messageID, sendErr := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, text, MainMenuKB())
		if sendErr != nil {
			a.OnError(sendErr)
			return
		}
		state.MessageID = messageID
		state.Screen = "main"
		_ = a.State.Save(ctx, state)
		return
	}

	state.Query = query
	state.Page = page.UIPage
	state.Screen = "results"
	state.Extra = map[string]any{
		"total":       page.Total,
		"total_pages": page.TotalPages,
	}
	_ = a.State.Save(ctx, state)

	text, visible := RenderSearchPage(page, query)
	bookIDs := make([]int64, 0, len(visible))
	for _, book := range visible {
		bookIDs = append(bookIDs, book.ID)
	}
	keyboard := ResultsKB(page.UIPage, page.TotalPages, bookIDs)
	messageID, err = a.Messages.SendOrEdit(ctx, chatID, state.MessageID, text, keyboard)
	if err != nil {
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)
}
