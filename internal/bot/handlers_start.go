package bot

import (
	"context"
	"log/slog"
	"regexp"

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
