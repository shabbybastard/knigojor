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

// HandleBook opens a book card from a search result button.
func (a *App) HandleBook(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	if cb == nil {
		return
	}
	parts := strings.Split(cb.Data, ":")
	if len(parts) < 2 {
		return
	}
	bookID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	a.answerCallback(ctx, cb, "", false)

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
	info, err := a.Client.GetBook(ctx, bookID)
	if err != nil {
		slog.Error("book fetch failed", "error", err)
		text := fmt.Sprintf(
			"<b>FLIBUSTA / BOOK</b>\n\nОшибка загрузки книги: %s",
			Esc(Clip(err.Error(), 300)),
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

	state.Screen = "book"
	state.BookID = bookID
	messageID, err := a.Messages.SendOrEdit(
		ctx,
		chatID,
		state.MessageID,
		RenderBook(info),
		BookKB(bookID, info.HasEPUB, info.HasFB2),
	)
	if err != nil {
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)
}

// HandleBackResults returns to the last search results page.
func (a *App) HandleBackResults(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	if cb == nil {
		return
	}
	a.answerCallback(ctx, cb, "", false)

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
	if state.Query == "" {
		state.Screen = "main"
		text := "<b>FLIBUSTA / knigojor</b>\n\nПерсональная электронная библиотека."
		messageID, sendErr := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, text, MainMenuKB())
		if sendErr != nil {
			a.OnError(sendErr)
			return
		}
		state.MessageID = messageID
		_ = a.State.Save(ctx, state)
		return
	}

	page, err := LoadSearchPage(ctx, a.Client, state.Query, state.Page, a.Settings.PageSize)
	if err != nil {
		slog.Error("re-search failed", "error", err)
		text := fmt.Sprintf(
			"<b>FLIBUSTA / SEARCH</b>\n\nОшибка загрузки результатов: %s",
			Esc(Clip(err.Error(), 300)),
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

	state.Screen = "results"
	state.Page = page.UIPage
	state.Extra = map[string]any{
		"total":       page.Total,
		"total_pages": page.TotalPages,
	}
	text, visible := RenderSearchPage(page, state.Query)
	bookIDs := make([]int64, 0, len(visible))
	for _, book := range visible {
		bookIDs = append(bookIDs, book.ID)
	}
	messageID, err := a.Messages.SendOrEdit(
		ctx,
		chatID,
		state.MessageID,
		text,
		ResultsKB(page.UIPage, page.TotalPages, bookIDs),
	)
	if err != nil {
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)
}
