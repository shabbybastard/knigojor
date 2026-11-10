package bot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	HTTPFor(mirror *netx.Mirror) (*netx.Client, error)
}

type authorCacheEntry struct {
	name  string
	books []flibusta.SearchResultBook
	at    time.Time
}

// App is the wired application used by handlers and tests.
type App struct {
	Settings *config.Settings
	DB       *store.Database
	State    *store.StateStore
	Mirrors  *netx.MirrorManager
	Tor      *netx.TorManager
	HTTP     *netx.Client
	HTTPTor  *netx.Client
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

	DownloadEPUB func(ctx context.Context, http *netx.Client, rawURL, dest string, maxSizeMB int, timeout time.Duration) (string, error)
	DownloadFB2  func(ctx context.Context, http *netx.Client, rawURL, destDir string, maxSizeMB int, timeout time.Duration) (string, error)
	Convert      func(fb2Path, epubPath string) error
	ValidateEPUB func(path string) bool
}

// NewApp wires all dependencies and creates the Telegram bot.
func NewApp(ctx context.Context, settings *config.Settings, version string) (*App, error) {
	db, err := store.Open(settings.DatabasePath)
	if err != nil {
		return nil, err
	}

	state := store.NewStateStore(db)
	mirrorStats := store.NewMirrorStatsStore(db)
	mirrors := netx.NewMirrorManager(
		settings.Mirrors,
		settings.OnionMirrors,
		settings.TorEnabled,
		mirrorStats,
	)
	mirrors.LoadStats(ctx)

	httpClient, err := netx.NewClient(
		time.Duration(settings.HTTPTimeout)*time.Second,
		settings.MaxParallelRequests,
		"",
		settings.MaxRetries,
	)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	tor := netx.NewTorManager(
		settings.TorExecutable,
		settings.TorSocksHost,
		settings.TorSocksPort,
		settings.TorControlPort,
		settings.TorStateDir,
		settings.TorEnabled,
	)
	var httpTor *netx.Client
	if settings.TorEnabled {
		torOK := tor.Start(ctx)
		mirrors.SetTorAvailable(torOK)
		if torOK {
			httpTor, err = netx.NewClient(
				time.Duration(settings.HTTPTimeout)*time.Second,
				settings.MaxParallelRequests,
				tor.ProxyURL(),
				settings.MaxRetries,
			)
			if err != nil {
				httpClient.Close()
				tor.Stop()
				_ = db.Close()
				return nil, err
			}
		}
	} else {
		mirrors.SetTorAvailable(false)
	}

	app := &App{
		Settings:     settings,
		DB:           db,
		State:        state,
		Mirrors:      mirrors,
		Tor:          tor,
		HTTP:         httpClient,
		HTTPTor:      httpTor,
		Client:       flibusta.NewClient(mirrors, httpClient, httpTor),
		Messages:     NewMessageManager(nil),
		DownloadSem:  make(chan struct{}, settings.MaxParallelDownloads),
		StartTime:    time.Now(),
		Version:      version,
		DownloadEPUB: books.DownloadEPUB,
		DownloadFB2:  books.DownloadFB2Zip,
		Convert:      books.ConvertFB2ToEPUB,
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
		app.Close(context.Background())
		return nil, fmt.Errorf("create telegram bot: %w", err)
	}
	app.Bot = telegramBot
	app.TG = telegramBot
	app.Messages.SetTelegram(telegramBot)
	app.RegisterHandlers(telegramBot)
	return app, nil
}

// Run starts polling and blocks until the context is cancelled.
func (a *App) Run(ctx context.Context) error {
	a.cleanTemp()
	healthCtx, cancel := context.WithCancel(ctx)
	var health sync.WaitGroup
	health.Add(1)
	go func() {
		defer health.Done()
		netx.HealthcheckLoop(
			healthCtx,
			a.Mirrors,
			a.HTTP,
			a.HTTPTor,
			time.Duration(a.Settings.HealthcheckInterval)*time.Second,
		)
	}()

	a.Bot.Start(ctx)
	cancel()
	health.Wait()
	return nil
}

// Close releases network, Tor and database resources.
func (a *App) Close(ctx context.Context) {
	if a.HTTPTor != nil {
		a.HTTPTor.Close()
	}
	if a.HTTP != nil {
		a.HTTP.Close()
	}
	if a.Tor != nil {
		a.Tor.Stop()
	}
	if a.DB != nil {
		_ = a.DB.Close()
	}
}

const authorCacheTTL = 10 * time.Minute

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
	a.authorCache[authorID] = authorCacheEntry{
		name:  books.Name,
		books: books.Books,
		at:    time.Now(),
	}
	a.authorMu.Unlock()
	return books, nil
}

func (a *App) cleanTemp() {
	entries, err := os.ReadDir(a.Settings.TempPath)
	if err != nil {
		return
	}
	for _, entry := range entries {
		target := filepath.Join(a.Settings.TempPath, entry.Name())
		if entry.IsDir() {
			_ = os.RemoveAll(target)
		} else {
			_ = os.Remove(target)
		}
	}
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
func (a *App) StatusText(ctx context.Context) string {
	uptime := int(time.Since(a.StartTime).Seconds())
	torOK := false
	if a.Tor != nil && a.Settings.TorEnabled {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		torOK = a.Tor.Probe(probeCtx)
		cancel()
	}
	lines := []string{
		"<b>FLIBUSTA / STATUS</b>",
		"",
		fmt.Sprintf("Время работы: %d с", uptime),
		fmt.Sprintf("Ошибок за запуск: %d", a.Errors.Load()),
		fmt.Sprintf("Tor: %s (%s)", onOff(a.Settings.TorEnabled), yesNo(torOK)),
		"",
		"Зеркала:",
	}
	for _, mirror := range a.Mirrors.Status() {
		lines = append(lines, fmt.Sprintf(
			"- %s: %s (%d err)",
			Esc(mirror.URL), mirror.State, mirror.Failures,
		))
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("RAM: %d MB", rssMB()))
	lines = append(lines, "Версия: "+a.Version)
	return strings.Join(lines, "\n")
}

// ConnectionStatus is the short main-menu status.
func (a *App) ConnectionStatus() string {
	available := 0
	degraded := false
	for _, mirror := range a.Mirrors.AllMirrors() {
		if mirror.IsAvailable() {
			available++
			if mirror.State() != netx.MirrorHealthy {
				degraded = true
			}
		}
	}
	if available == 0 {
		return "Offline"
	}
	if degraded {
		return "Degraded"
	}
	return "Online"
}

// MainMenuText renders the main screen.
func (a *App) MainMenuText() string {
	return "<b>FLIBUSTA / knigojor</b>\n\n" +
		"Персональная электронная библиотека.\n\n" +
		"Состояние соединения: " + a.ConnectionStatus()
}

func onOff(value bool) string {
	if value {
		return "вкл"
	}
	return "выкл"
}

func yesNo(value bool) string {
	if value {
		return "ok"
	}
	return "нет"
}

func rssMB() uint64 {
	data, err := os.ReadFile("/proc/self/status")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "VmRSS:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
					return kb / 1024
				}
			}
		}
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.Sys / 1024 / 1024
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
	return 0
}
