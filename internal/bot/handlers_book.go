package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/shabbybastard/knigojor/internal/store"
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
	if err != nil || bookID == 0 {
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
	if state.MessageID == 0 {
		state.MessageID = callbackMessageID(cb)
	}
	if state.Operation == "" {
		if state.Screen == screenAuthorResults {
			state.Operation = "author"
		} else {
			state.Operation = "search"
		}
	}
	a.openBook(ctx, chatID, bookID, state)
}

func (a *App) openBook(ctx context.Context, chatID, bookID int64, state *store.UIState) {
	state.ChatID = chatID
	state.BookID = bookID
	state.MessageID = a.showLoading(
		ctx,
		chatID,
		state.MessageID,
		fmt.Sprintf("<b>FLIBUSTA / BOOK</b>\n\n⏳ Загрузка карточки книги #%d...", bookID),
	)
	_ = a.State.Save(ctx, state)

	info, err := a.getBookInfo(ctx, bookID)
	if err != nil {
		slog.Error("book fetch failed", "error", err)
		a.failBook(ctx, chatID, state, err)
		return
	}

	state.Screen = "book"
	state.BookID = bookID
	messageID, err := a.Messages.SendOrEditWithLinkPreview(
		ctx,
		chatID,
		state.MessageID,
		RenderBook(info),
		BookKB(bookID, info.HasEPUB, info.HasFB2),
		CoverLinkPreview(info.CoverURL),
	)
	if err != nil {
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)
}

func (a *App) failBook(ctx context.Context, chatID int64, state *store.UIState, err error) {
	text := fmt.Sprintf(
		"<b>FLIBUSTA / BOOK</b>\n\nОшибка загрузки книги: %s",
		Esc(Clip(err.Error(), 300)),
	)
	messageID, sendErr := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, text, RetryKB("book"))
	if sendErr != nil {
		a.OnError(sendErr)
		return
	}
	state.MessageID = messageID
	state.Screen = "error"
	if state.Operation == "" {
		state.Operation = "search"
	}
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
	if state.Operation == "author" {
		authorID := extraInt64(state.Extra, "author_id")
		if authorID != 0 {
			state.MessageID = a.showLoading(
				ctx,
				chatID,
				state.MessageID,
				fmt.Sprintf("<b>FLIBUSTA / AUTHOR</b>\n\n⏳ Загрузка книг автора (страница %d)...", state.Page+1),
			)
			_ = a.State.Save(ctx, state)
			books, loadErr := a.getAuthorBooks(ctx, authorID)
			if loadErr != nil {
				a.failAuthor(ctx, chatID, state, loadErr)
				return
			}
			a.renderAuthorPage(ctx, chatID, state, books, state.Page)
			return
		}
	}
	if state.Query == "" {
		state.Screen = "main"
		state.Operation = ""
		state.Extra = map[string]any{}
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

	state.MessageID = a.showLoading(
		ctx,
		chatID,
		state.MessageID,
		fmt.Sprintf(
			"<b>FLIBUSTA / SEARCH</b>\n\nПоиск: %s\nСтраница %d\n\n⏳ Загрузка результатов...",
			Esc(Clip(state.Query, 200)),
			state.Page+1,
		),
	)
	_ = a.State.Save(ctx, state)
	page, err := LoadSearchPage(ctx, a.Client, state.Query, state.Page, a.Settings.PageSize)
	if err != nil {
		slog.Error("re-search failed", "error", err)
		a.failSearch(ctx, chatID, state, err, "search")
		return
	}

	state.Screen = "results"
	state.Operation = "search"
	state.Page = page.UIPage
	state.Extra = map[string]any{
		"total":        page.Total,
		"total_pages":  page.TotalPages,
		"show_authors": false,
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
