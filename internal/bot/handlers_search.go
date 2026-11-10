package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/shabbybastard/knigojor/internal/flibusta"
	"github.com/shabbybastard/knigojor/internal/store"
)

const (
	screenAuthors       = "authors"
	screenAuthorResults = "author_results"
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
	state.Operation = ""
	state.Extra = map[string]any{}
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
	state.Operation = ""
	state.Extra = map[string]any{}
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
	state.Operation = ""
	state.Page = 0
	state.Extra = map[string]any{}
	_ = a.State.Save(ctx, state)
	a.doSearch(ctx, message.Chat.ID, user.ID, query, 0, true)
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
	if !isResultsScreen(state.Screen) ||
		(state.Screen == "results" && state.Query == "") {
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
	if !isResultsScreen(state.Screen) ||
		(state.Screen == "results" && state.Query == "") {
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
	if state.Screen == screenAuthorResults {
		authorID := extraInt64(state.Extra, "author_id")
		if authorID == 0 {
			return
		}
		state.Page = newPage
		state.MessageID = a.showLoading(
			ctx,
			chatID,
			state.MessageID,
			"<b>FLIBUSTA / AUTHOR</b>\n\n⏳ Загрузка книг автора...",
		)
		_ = a.State.Save(ctx, state)
		books, loadErr := a.getAuthorBooks(ctx, authorID)
		if loadErr != nil {
			a.failAuthor(ctx, chatID, state, loadErr)
			return
		}
		a.renderAuthorPage(ctx, chatID, state, books, newPage)
		return
	}
	state.Page = newPage
	a.doSearch(ctx, chatID, user.ID, state.Query, newPage, false)
}

func (a *App) doSearch(
	ctx context.Context,
	chatID, userID int64,
	query string,
	uiPage int,
	showAuthors bool,
) {
	state, err := a.State.Get(ctx, userID)
	if err != nil {
		a.OnError(err)
		return
	}
	state.ChatID = chatID
	state.Query = query
	state.Page = uiPage
	if state.Extra == nil {
		state.Extra = map[string]any{}
	}
	state.Extra["show_authors"] = showAuthors
	state.Screen = "searching"
	_ = a.State.Save(ctx, state)

	loading := fmt.Sprintf(
		"<b>FLIBUSTA / SEARCH</b>\n\nПоиск: %s\n\n⏳ Загрузка...",
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
		a.failSearch(ctx, chatID, state, err, "search")
		return
	}

	if showAuthors && uiPage == 0 && len(page.Authors) > 0 {
		state.Query = query
		state.Page = 0
		state.Screen = screenAuthors
		state.Operation = "search"
		state.Extra = map[string]any{
			"total_books":  page.Total,
			"show_authors": true,
		}
		_ = a.State.Save(ctx, state)

		text, visible, authorPage, totalAuthorPages := RenderAuthorsPage(
			query,
			page.Authors,
			page.Total,
			0,
		)
		state.Page = authorPage
		keyboard := AuthorsKB(visible, page.Total, authorPage, totalAuthorPages)
		messageID, err = a.Messages.SendOrEdit(ctx, chatID, state.MessageID, text, keyboard)
		if err != nil {
			a.OnError(err)
			return
		}
		state.MessageID = messageID
		_ = a.State.Save(ctx, state)
		return
	}

	state.Query = query
	state.Page = page.UIPage
	state.Screen = "results"
	state.Operation = "search"
	state.Extra = map[string]any{
		"total":        page.Total,
		"total_pages":  page.TotalPages,
		"show_authors": false,
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

// HandleAuthor opens the book list of one selected author.
func (a *App) HandleAuthor(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	if cb == nil {
		return
	}
	parts := strings.Split(cb.Data, ":")
	if len(parts) < 2 {
		return
	}
	authorID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || authorID == 0 {
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
	state.Page = 0
	if state.Extra == nil {
		state.Extra = map[string]any{}
	}
	state.Extra["author_id"] = authorID
	state.MessageID = a.showLoading(
		ctx,
		chatID,
		state.MessageID,
		"<b>FLIBUSTA / AUTHOR</b>\n\n⏳ Загрузка книг автора...",
	)
	_ = a.State.Save(ctx, state)

	books, err := a.getAuthorBooks(ctx, authorID)
	if err != nil {
		slog.Error("author books fetch failed", "error", err)
		a.failAuthor(ctx, chatID, state, err)
		return
	}
	a.renderAuthorPage(ctx, chatID, state, books, 0)
}

// HandleAuthorPage moves between pages of author matches.
func (a *App) HandleAuthorPage(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
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
	if state.Screen != screenAuthors || state.Query == "" {
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
	if state.Screen != screenAuthors || state.Query == "" {
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
	state.Page = newPage
	state.MessageID = a.showLoading(
		ctx,
		chatID,
		state.MessageID,
		fmt.Sprintf(
			"<b>FLIBUSTA / SEARCH</b>\n\nПоиск: %s\n\n⏳ Загрузка авторов...",
			Esc(Clip(state.Query, 200)),
		),
	)
	_ = a.State.Save(ctx, state)

	a.loadAuthorsPage(ctx, chatID, state, newPage)
}

func (a *App) loadAuthorsPage(ctx context.Context, chatID int64, state *store.UIState, pageNumber int) {
	if state.Query == "" {
		a.failSearch(ctx, chatID, state, fmt.Errorf("нет сохранённого запроса"), "authors")
		return
	}
	page, err := LoadSearchPage(ctx, a.Client, state.Query, 0, a.Settings.PageSize)
	if err != nil {
		a.failSearch(ctx, chatID, state, err, "authors")
		return
	}
	if len(page.Authors) == 0 {
		a.failSearch(ctx, chatID, state, fmt.Errorf("authors not found"), "authors")
		return
	}
	text, visible, authorPage, totalAuthorPages := RenderAuthorsPage(
		state.Query,
		page.Authors,
		page.Total,
		pageNumber,
	)
	state.Page = authorPage
	state.Screen = screenAuthors
	state.Operation = "search"
	state.Extra = map[string]any{
		"total_books":  page.Total,
		"show_authors": true,
	}
	_ = a.State.Save(ctx, state)
	messageID, err := a.Messages.SendOrEdit(
		ctx,
		chatID,
		state.MessageID,
		text,
		AuthorsKB(visible, page.Total, authorPage, totalAuthorPages),
	)
	if err != nil {
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)
}

// HandleSearchBooks skips the author selection and shows title matches.
func (a *App) HandleSearchBooks(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
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
	if state.Query == "" {
		return
	}
	state.Page = 0
	state.Extra = map[string]any{"show_authors": false}
	_ = a.State.Save(ctx, state)
	a.doSearch(ctx, chatID, user.ID, state.Query, 0, false)
}

func (a *App) renderAuthorPage(
	ctx context.Context,
	chatID int64,
	state *store.UIState,
	books *flibusta.AuthorBooks,
	uiPage int,
) {
	if books == nil {
		a.failAuthor(ctx, chatID, state, fmt.Errorf("empty author page"))
		return
	}
	name := books.Name
	if name == "" {
		name = extraString(state.Extra, "author_name")
	}
	if name == "" {
		name = "Автор"
	}
	page := SliceAuthorBooks(name, books.Books, uiPage, a.Settings.PageSize)
	state.ChatID = chatID
	state.Screen = screenAuthorResults
	state.Operation = "author"
	state.Page = page.UIPage
	state.BookID = 0
	if state.Extra == nil {
		state.Extra = map[string]any{}
	}
	state.Extra["author_id"] = books.ID
	state.Extra["author_name"] = name
	state.Extra["author_total"] = page.Total
	_ = a.State.Save(ctx, state)

	text, visible := RenderSearchPage(page, "")
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

func (a *App) failSearch(
	ctx context.Context,
	chatID int64,
	state *store.UIState,
	err error,
	retryAction string,
) {
	text := fmt.Sprintf(
		"<b>FLIBUSTA / SEARCH</b>\n\nОшибка поиска: %s",
		Esc(Clip(err.Error(), 300)),
	)
	messageID, sendErr := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, text, RetryKB(retryAction))
	if sendErr != nil {
		a.OnError(sendErr)
		return
	}
	state.MessageID = messageID
	state.Screen = "error"
	state.Operation = "search"
	_ = a.State.Save(ctx, state)
}

func (a *App) failAuthor(ctx context.Context, chatID int64, state *store.UIState, err error) {
	text := fmt.Sprintf(
		"<b>FLIBUSTA / AUTHOR</b>\n\nОшибка загрузки автора: %s",
		Esc(Clip(err.Error(), 300)),
	)
	messageID, sendErr := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, text, RetryKB("author"))
	if sendErr != nil {
		a.OnError(sendErr)
		return
	}
	state.MessageID = messageID
	state.Screen = "error"
	state.Operation = "author"
	_ = a.State.Save(ctx, state)
}

func isResultsScreen(screen string) bool {
	return screen == "results" || screen == screenAuthorResults
}

func extraInt64(extra map[string]any, key string) int64 {
	if extra == nil {
		return 0
	}
	switch value := extra[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func extraString(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	value, _ := extra[key].(string)
	return value
}

func extraBool(extra map[string]any, key string) bool {
	if extra == nil {
		return false
	}
	value, _ := extra[key].(bool)
	return value
}
