package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

var (
	startCommand  = regexp.MustCompile(`^/start(@\w+)?(\s|$)`)
	statusCommand = regexp.MustCompile(`^/status(@\w+)?(\s|$)`)
)

// RegisterHandlers wires all update handlers.
func (a *App) RegisterHandlers(b *tgbot.Bot) {
	b.RegisterHandlerRegexp(tgbot.HandlerTypeMessageText, startCommand, a.HandleStart)
	b.RegisterHandlerRegexp(tgbot.HandlerTypeMessageText, statusCommand, a.HandleStatus)

	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "new_search", tgbot.MatchTypeExact, a.HandleNewSearch)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "cancel_search", tgbot.MatchTypeExact, a.HandleCancelSearch)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "main_menu", tgbot.MatchTypeExact, a.HandleMainMenu)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "refresh", tgbot.MatchTypeExact, a.HandleRefresh)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "status", tgbot.MatchTypeExact, a.HandleStatusCallback)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "back_results", tgbot.MatchTypeExact, a.HandleBackResults)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "page:", tgbot.MatchTypePrefix, a.HandlePage)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "author:", tgbot.MatchTypePrefix, a.HandleAuthor)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "apage:", tgbot.MatchTypePrefix, a.HandleAuthorPage)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "search_books", tgbot.MatchTypeExact, a.HandleSearchBooks)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "book:", tgbot.MatchTypePrefix, a.HandleBook)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "dl:", tgbot.MatchTypePrefix, a.HandleDownload)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "retry:", tgbot.MatchTypePrefix, a.HandleRetry)

	b.RegisterHandlerMatchFunc(func(update *models.Update) bool {
		return update.Message != nil && update.Message.Text != ""
	}, a.HandleText)
}

// HandleStart draws the main screen.
func (a *App) HandleStart(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
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
	state.Operation = ""
	state.Extra = map[string]any{}
	_ = a.State.Save(ctx, state)
	messageID, err := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, a.MainMenuText(), MainMenuKB())
	if err != nil {
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)
}

// HandleStatus draws the status screen.
func (a *App) HandleStatus(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
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
	state.MessageID = a.showLoading(
		ctx,
		chatID,
		state.MessageID,
		"<b>FLIBUSTA / STATUS</b>\n\n⏳ Проверка статуса...",
	)
	_ = a.State.Save(ctx, state)
	messageID, err := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, a.StatusText(ctx), MainMenuKB())
	if err != nil {
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	state.Screen = "status"
	_ = a.State.Save(ctx, state)
}

// HandleMainMenu redraws the main screen.
func (a *App) HandleMainMenu(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
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
	state.Operation = ""
	state.Extra = map[string]any{}
	messageID, err := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, a.MainMenuText(), MainMenuKB())
	if err != nil {
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)
}

// HandleRefresh is an alias for the main menu.
func (a *App) HandleRefresh(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	a.HandleMainMenu(ctx, b, update)
}

// HandleStatusCallback redraws the status screen from a callback.
func (a *App) HandleStatusCallback(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
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
	state.MessageID = a.showLoading(
		ctx,
		chatID,
		state.MessageID,
		"<b>FLIBUSTA / STATUS</b>\n\n⏳ Проверка статуса...",
	)
	_ = a.State.Save(ctx, state)
	messageID, err := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, a.StatusText(ctx), MainMenuKB())
	if err != nil {
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	state.Screen = "status"
	_ = a.State.Save(ctx, state)
}

// HandleRetry repeats the failed operation using the saved UI context.
func (a *App) HandleRetry(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	if cb == nil {
		return
	}
	action := strings.TrimPrefix(cb.Data, "retry:")
	a.answerCallback(ctx, cb, "", false)
	user := update.EffectiveUser()
	chatID, ok := updateChatID(update)
	if user == nil || !ok {
		return
	}
	if action == "download" && a.DownloadSem != nil {
		select {
		case a.DownloadSem <- struct{}{}:
			defer func() { <-a.DownloadSem }()
		case <-ctx.Done():
			return
		}
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

	switch action {
	case "search":
		if state.Query == "" {
			a.failSearch(ctx, chatID, state, errors.New("нет сохранённого запроса"), "search")
			return
		}
		showAuthors := extraBool(state.Extra, "show_authors")
		state.Screen = "searching"
		_ = a.State.Save(ctx, state)
		a.doSearch(ctx, chatID, user.ID, state.Query, state.Page, showAuthors)
	case "authors":
		if state.Query == "" {
			a.failSearch(ctx, chatID, state, errors.New("нет сохранённого запроса"), "authors")
			return
		}
		state.MessageID = a.showLoading(
			ctx,
			chatID,
			state.MessageID,
			fmt.Sprintf(
				"<b>FLIBUSTA / SEARCH</b>\n\nПоиск: %s\nСтраница %d\n\n⏳ Загрузка авторов...",
				Esc(Clip(state.Query, 200)),
				state.Page+1,
			),
		)
		_ = a.State.Save(ctx, state)
		a.loadAuthorsPage(ctx, chatID, state, state.Page)
	case "author":
		authorID := extraInt64(state.Extra, "author_id")
		if authorID == 0 {
			a.failSearch(ctx, chatID, state, errors.New("нет сохранённого автора"), "author")
			return
		}
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
	case "book":
		if state.BookID == 0 {
			a.failBook(ctx, chatID, state, errors.New("нет сохранённой книги"))
			return
		}
		a.openBook(ctx, chatID, state.BookID, state)
	case "download":
		if state.BookID == 0 {
			a.failSearch(ctx, chatID, state, errors.New("нет сохранённой книги"), "download")
			return
		}
		a.doDownload(ctx, chatID, state.BookID, state)
	default:
		a.failSearch(ctx, chatID, state, errors.New("неизвестное действие повтора"), "search")
	}
}

func (a *App) answerCallback(ctx context.Context, cb *models.CallbackQuery, text string, alert bool) {
	if cb == nil || a.TG == nil {
		return
	}
	_, err := a.TG.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: cb.ID,
		Text:            text,
		ShowAlert:       alert,
	})
	if err != nil {
		slog.Debug("answer callback failed", "error", err)
	}
}
