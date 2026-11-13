package bot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/shabbybastard/knigojor/internal/books"
	"github.com/shabbybastard/knigojor/internal/config"
	"github.com/shabbybastard/knigojor/internal/flibusta"
	"github.com/shabbybastard/knigojor/internal/netx"
	"github.com/shabbybastard/knigojor/internal/store"
)

// Flibusta is the Flibusta surface used by handlers.
type Flibusta interface {
	Search(ctx context.Context, query string, page int) (*flibusta.SearchResults, error)
	GetBook(ctx context.Context, bookID int64) (*flibusta.BookInfo, error)
	GetAuthorBooks(ctx context.Context, authorID int64) (*flibusta.AuthorBooks, error)
}

type authorCacheEntry struct {
	name  string
	books []flibusta.SearchResultBook
	at    time.Time
}

type bookCacheEntry struct {
	info *flibusta.BookInfo
	at   time.Time
}

type searchCacheEntry struct {
	results *flibusta.SearchResults
	at      time.Time
}

// App is the wired application used by handlers and tests.
type App struct {
	Settings *config.Settings
	State    *store.StateStore
	HTTP     *netx.Client
	Client   Flibusta
	Messages *MessageManager
	TG       Telegram
	Bot      *tgbot.Bot

	DownloadSem chan struct{}
	StartTime   time.Time
	Errors      atomic.Int64
	Version     string

	authorMu    sync.Mutex
	authorCache map[int64]authorCacheEntry

	bookMu    sync.Mutex
	bookCache map[int64]bookCacheEntry

	searchMu    sync.Mutex
	searchCache map[string]searchCacheEntry

	DownloadEPUB func(ctx context.Context, http *netx.Client, rawURL, dest string, maxSizeMB int, timeout time.Duration, progress netx.ProgressFunc) (string, error)
	DownloadFB2  func(ctx context.Context, http *netx.Client, rawURL, destDir string, maxSizeMB int, timeout time.Duration, progress netx.ProgressFunc) (string, error)
	Convert      func(fb2Path, epubPath string, progress func(string)) error
	ValidateEPUB func(path string) bool
}

// NewApp wires all dependencies and creates the Telegram bot.
func NewApp(settings *config.Settings, version string) (*App, error) {
	if settings.MaxParallelDownloads < 1 {
		settings.MaxParallelDownloads = 1
	}
	httpClient, err := netx.NewClient(
		time.Duration(settings.HTTPTimeout)*time.Second,
		settings.MaxParallelRequests,
		settings.MaxRetries,
	)
	if err != nil {
		return nil, err
	}

	app := &App{
		Settings:     settings,
		State:        store.NewStateStore(),
		HTTP:         httpClient,
		Client:       flibusta.NewClient(settings.ContentBase, httpClient),
		Messages:     NewMessageManager(nil),
		DownloadSem:  make(chan struct{}, settings.MaxParallelDownloads),
		StartTime:    time.Now(),
		Version:      version,
		DownloadEPUB: books.DownloadEPUBProgress,
		DownloadFB2:  books.DownloadFB2ZipProgress,
		Convert:      books.ConvertFB2ToEPUBProgress,
		ValidateEPUB: books.ValidateEPUB,
	}

	telegramBot, err := tgbot.New(
		settings.BotToken,
		tgbot.WithMiddlewares(app.AuthMiddleware, app.RecoverMiddleware),
		tgbot.WithErrorsHandler(app.OnError),
		tgbot.WithWorkers(4),
		tgbot.WithNotAsyncHandlers(),
	)
	if err != nil {
		app.Close()
		return nil, fmt.Errorf("create telegram bot: %w", err)
	}
	app.Bot = telegramBot
	app.TG = telegramBot
	app.Messages.SetTelegram(telegramBot)
	app.RegisterHandlers(telegramBot)
	return app, nil
}

// Run starts polling and blocks until the context is cancelled.
func (a *App) Run(ctx context.Context) {
	a.cleanTemp()
	a.Bot.Start(ctx)
}

// Close releases network resources.
func (a *App) Close() {
	if a.HTTP != nil {
		a.HTTP.Close()
	}
}

const (
	authorCacheTTL = 10 * time.Minute
	bookCacheTTL   = 10 * time.Minute
	searchCacheTTL = time.Minute

	maxAuthorCacheEntries = 64
	maxBookCacheEntries   = 128
	maxSearchCacheEntries = 16
)

// showLoading replaces the current UI message with a loading state.
func (a *App) showLoading(ctx context.Context, chatID int64, messageID int, text string) int {
	if a.Messages == nil {
		return messageID
	}
	newID, err := a.Messages.SendOrEdit(ctx, chatID, messageID, text, nil)
	if err != nil {
		a.OnError(err)
		return messageID
	}
	return newID
}

func (a *App) getAuthorBooks(ctx context.Context, authorID int64) (*flibusta.AuthorBooks, error) {
	a.authorMu.Lock()
	if entry, ok := a.authorCache[authorID]; ok && time.Since(entry.at) < authorCacheTTL {
		a.authorMu.Unlock()
		return &flibusta.AuthorBooks{
			ID:    authorID,
			Name:  entry.name,
			Books: entry.books,
		}, nil
	}
	a.authorMu.Unlock()

	books, err := a.Client.GetAuthorBooks(ctx, authorID)
	if err != nil {
		return nil, err
	}
	a.authorMu.Lock()
	if a.authorCache == nil {
		a.authorCache = make(map[int64]authorCacheEntry)
	}
	pruneAuthorCache(a.authorCache, time.Now())
	a.authorCache[authorID] = authorCacheEntry{
		name:  books.Name,
		books: books.Books,
		at:    time.Now(),
	}
	a.authorMu.Unlock()
	return books, nil
}

func (a *App) getBookInfo(ctx context.Context, bookID int64) (*flibusta.BookInfo, error) {
	a.bookMu.Lock()
	if entry, ok := a.bookCache[bookID]; ok && time.Since(entry.at) < bookCacheTTL {
		a.bookMu.Unlock()
		return entry.info, nil
	}
	a.bookMu.Unlock()

	info, err := a.Client.GetBook(ctx, bookID)
	if err != nil {
		return nil, err
	}
	a.bookMu.Lock()
	if a.bookCache == nil {
		a.bookCache = make(map[int64]bookCacheEntry)
	}
	pruneBookCache(a.bookCache, time.Now())
	a.bookCache[bookID] = bookCacheEntry{info: info, at: time.Now()}
	a.bookMu.Unlock()
	return info, nil
}

// Search returns a search page. Page zero is cached briefly because both
// pagination and the author view need its Total and Authors fields.
func (a *App) Search(ctx context.Context, query string, page int) (*flibusta.SearchResults, error) {
	if page != 0 {
		return a.Client.Search(ctx, query, page)
	}

	now := time.Now()
	a.searchMu.Lock()
	if entry, ok := a.searchCache[query]; ok && now.Sub(entry.at) < searchCacheTTL {
		results := cloneSearchResults(entry.results)
		a.searchMu.Unlock()
		return results, nil
	}
	a.searchMu.Unlock()

	results, err := a.Client.Search(ctx, query, page)
	if err != nil {
		return nil, err
	}

	cachedAt := time.Now()
	a.searchMu.Lock()
	if a.searchCache == nil {
		a.searchCache = make(map[string]searchCacheEntry)
	}
	pruneSearchCache(a.searchCache, cachedAt)
	a.searchCache[query] = searchCacheEntry{
		results: cloneSearchResults(results),
		at:      cachedAt,
	}
	a.searchMu.Unlock()
	return results, nil
}

func cloneSearchResults(results *flibusta.SearchResults) *flibusta.SearchResults {
	if results == nil {
		return nil
	}
	clone := *results
	clone.Books = append([]flibusta.SearchResultBook(nil), results.Books...)
	for index := range clone.Books {
		clone.Books[index].Authors = append([]string(nil), results.Books[index].Authors...)
	}
	clone.Authors = append([]flibusta.AuthorResult(nil), results.Authors...)
	return &clone
}

func pruneAuthorCache(cache map[int64]authorCacheEntry, now time.Time) {
	for id, entry := range cache {
		if now.Sub(entry.at) >= authorCacheTTL {
			delete(cache, id)
		}
	}
	for len(cache) >= maxAuthorCacheEntries {
		evictOldest(cache, func(entry authorCacheEntry) time.Time { return entry.at })
	}
}

func pruneBookCache(cache map[int64]bookCacheEntry, now time.Time) {
	for id, entry := range cache {
		if now.Sub(entry.at) >= bookCacheTTL {
			delete(cache, id)
		}
	}
	for len(cache) >= maxBookCacheEntries {
		evictOldest(cache, func(entry bookCacheEntry) time.Time { return entry.at })
	}
}

func pruneSearchCache(cache map[string]searchCacheEntry, now time.Time) {
	for query, entry := range cache {
		if now.Sub(entry.at) >= searchCacheTTL {
			delete(cache, query)
		}
	}
	for len(cache) >= maxSearchCacheEntries {
		evictOldest(cache, func(entry searchCacheEntry) time.Time { return entry.at })
	}
}

func evictOldest[K comparable, V any](cache map[K]V, at func(V) time.Time) {
	var oldestKey K
	var oldestAt time.Time
	for key, value := range cache {
		valueAt := at(value)
		if oldestAt.IsZero() || valueAt.Before(oldestAt) {
			oldestKey = key
			oldestAt = valueAt
		}
	}
	delete(cache, oldestKey)
}

const tempMarker = ".knigojor"

func (a *App) cleanTemp() {
	entries, err := os.ReadDir(a.Settings.TempPath)
	if err != nil {
		return
	}
	for _, entry := range entries {
		// TEMP_DIR is user-configurable and may point at a shared tmpfs.
		// Remove only numeric directories that carry our marker.
		if !entry.IsDir() || !isNumericName(entry.Name()) {
			continue
		}
		target := filepath.Join(a.Settings.TempPath, entry.Name())
		if _, err := os.Stat(filepath.Join(target, tempMarker)); err != nil {
			continue
		}
		_ = os.RemoveAll(target)
	}
}

func isNumericName(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// AuthMiddleware rejects every update from other users.
func (a *App) AuthMiddleware(next tgbot.HandlerFunc) tgbot.HandlerFunc {
	return func(ctx context.Context, b *tgbot.Bot, update *models.Update) {
		user := update.EffectiveUser()
		if user == nil || user.ID != a.Settings.AllowedUserID {
			if update.CallbackQuery != nil && a.TG != nil {
				_, _ = a.TG.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
					CallbackQueryID: update.CallbackQuery.ID,
					Text:            "Доступ запрещён",
					ShowAlert:       true,
				})
			}
			return
		}
		next(ctx, b, update)
	}
}

// RecoverMiddleware turns panics into logs and a user-facing error.
func (a *App) RecoverMiddleware(next tgbot.HandlerFunc) tgbot.HandlerFunc {
	return func(ctx context.Context, b *tgbot.Bot, update *models.Update) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.Errors.Add(1)
				slog.Error("handler panic", "panic", recovered, "update_id", update.ID)
				a.notifyError(ctx, update, fmt.Errorf("panic: %v", recovered))
			}
		}()
		next(ctx, b, update)
	}
}

// OnError is the bot-level error handler.
func (a *App) OnError(err error) {
	a.Errors.Add(1)
	slog.Error("telegram update failed", "error", err)
}

func (a *App) notifyError(ctx context.Context, update *models.Update, err error) {
	chatID, ok := updateChatID(update)
	if !ok || a.TG == nil {
		return
	}
	if update.CallbackQuery != nil {
		_, _ = a.TG.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Произошла ошибка. Попробуйте ещё раз.",
			ShowAlert:       true,
		})
		return
	}
	_, _ = a.TG.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:    chatID,
		Text:      "Произошла ошибка. Попробуйте ещё раз.",
		ParseMode: models.ParseModeHTML,
	})
}

// StatusText renders the /status screen.
func (a *App) StatusText(context.Context) string {
	lines := []string{
		"<b>FLIBUSTA / STATUS</b>",
		"",
		"Аптайм: " + Esc(formatUptime(time.Since(a.StartTime))),
		fmt.Sprintf("Ошибок: %d", a.Errors.Load()),
	}
	if load, ok := readLoadAverage(); ok {
		lines = append(lines, "CPU: "+load)
	} else {
		lines = append(lines, "CPU: н/д")
	}
	if free, total, ok := readMemoryUsage(); ok {
		lines = append(lines, "RAM: "+formatBytes(free)+" / "+formatBytes(total))
	} else {
		lines = append(lines, "RAM: н/д")
	}
	if rss, ok := readProcessRSS(); ok {
		lines = append(lines, "Процесс: "+formatBytes(rss))
	} else {
		lines = append(lines, "Процесс: н/д")
	}
	if free, total, ok := readDiskUsage(a.Settings.BaseDir); ok {
		lines = append(lines, "Диск: "+formatBytes(free)+" / "+formatBytes(total))
	} else {
		lines = append(lines, "Диск: н/д")
	}
	lines = append(lines,
		"",
		"Каталог: "+Esc(a.Settings.ContentBase),
		"Книги: "+Esc(a.Settings.DownloadBase),
		"Версия: "+a.Version,
	)
	return strings.Join(lines, "\n")
}

// MainMenuText renders the main screen.
func (a *App) MainMenuText() string {
	return a.mainMenuText("")
}

func (a *App) mainMenuText(notice string) string {
	text := "<b>FLIBUSTA / knigojor</b>\n\n"
	if notice != "" {
		text += Esc(notice) + "\n\n"
	}
	text += "Персональная электронная библиотека.\n\n" +
		"Каталог: " + Esc(a.Settings.ContentBase) + "\n" +
		"Книги: " + Esc(a.Settings.DownloadBase)
	return text
}

// updateChatID extracts the chat where an update happened.
func updateChatID(update *models.Update) (int64, bool) {
	if update.Message != nil {
		return update.Message.Chat.ID, true
	}
	if update.CallbackQuery != nil {
		if update.CallbackQuery.Message.Message != nil {
			return update.CallbackQuery.Message.Message.Chat.ID, true
		}
		if update.CallbackQuery.Message.InaccessibleMessage != nil {
			return update.CallbackQuery.Message.InaccessibleMessage.Chat.ID, true
		}
	}
	if chat := update.EffectiveChat(); chat != nil {
		return chat.ID, true
	}
	return 0, false
}

// callbackMessageID extracts an editable message ID from a callback.
func callbackMessageID(cb *models.CallbackQuery) int {
	if cb == nil {
		return 0
	}
	if cb.Message.Message != nil {
		return cb.Message.Message.ID
	}
	if cb.Message.InaccessibleMessage != nil {
		return cb.Message.InaccessibleMessage.MessageID
	}
	return 0
}
