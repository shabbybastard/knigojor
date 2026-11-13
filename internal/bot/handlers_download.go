package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/shabbybastard/knigojor/internal/flibusta"
	"github.com/shabbybastard/knigojor/internal/store"
)

// HandleDownload downloads and sends a book as EPUB.
func (a *App) HandleDownload(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	a.handleDownload(ctx, update, false)
}

// HandleDownloadFB2 downloads and sends the source FB2 without conversion.
func (a *App) HandleDownloadFB2(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	a.handleDownload(ctx, update, true)
}

func (a *App) handleDownload(ctx context.Context, update *models.Update, fb2Only bool) {
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

	if a.DownloadSem != nil {
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
	state.BookID = bookID
	if messageID := callbackMessageID(cb); messageID != 0 {
		// The download button belongs to this message. Use it even when the
		// in-memory state was lost or points at an older screen, otherwise
		// the visible card cannot be replaced after the file is sent.
		state.MessageID = messageID
	}
	if fb2Only {
		a.doDownloadFB2(ctx, chatID, bookID, state)
		return
	}
	a.doDownload(ctx, chatID, bookID, state)
}

type downloadOptions struct {
	attempts  int
	timeout   time.Duration
	maxSizeMB int
}

func (a *App) downloadOptions() downloadOptions {
	attempts := a.Settings.DownloadAttempts
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 3 {
		attempts = 3
	}
	return downloadOptions{
		attempts:  attempts,
		timeout:   time.Duration(a.Settings.DownloadTimeout) * time.Second,
		maxSizeMB: a.Settings.MaxFileSizeMB,
	}
}

// prepareDownload creates the per-book temp directory, starts the progress
// message and loads the book card.
func (a *App) prepareDownload(
	ctx context.Context,
	chatID, bookID int64,
	state *store.UIState,
	retryAction string,
) (string, func(string), *flibusta.BookInfo, bool) {
	tmpdir := filepath.Join(a.Settings.TempPath, strconv.FormatInt(bookID, 10))
	if err := os.MkdirAll(tmpdir, 0o700); err != nil {
		a.failDownload(ctx, chatID, bookID, state, err, retryAction)
		return "", nil, nil, false
	}
	_ = os.WriteFile(filepath.Join(tmpdir, tempMarker), []byte("1"), 0o600)

	state.ChatID = chatID
	state.BookID = bookID

	status := func(text string) {
		messageID, err := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, text, nil)
		if err != nil {
			a.OnError(err)
			return
		}
		state.MessageID = messageID
		_ = a.State.Save(ctx, state)
	}
	status("<b>FLIBUSTA / DOWNLOAD</b>\n\n⏳ Подготовка книги...")

	info, err := a.getBookInfo(ctx, bookID)
	if err != nil {
		_ = os.RemoveAll(tmpdir)
		a.failDownload(ctx, chatID, bookID, state, err, retryAction)
		return "", nil, nil, false
	}
	return tmpdir, status, info, true
}

func (a *App) downloadEPUB(
	ctx context.Context,
	bookID int64,
	tmpdir string,
	opts downloadOptions,
	status func(string),
) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= opts.attempts; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		status(fmt.Sprintf(
			"<b>FLIBUSTA / DOWNLOAD</b>\n\n📥 EPUB · попытка %d/%d",
			attempt,
			opts.attempts,
		))
		dest := filepath.Join(tmpdir, fmt.Sprintf("%d.epub", bookID))
		progress := startDownloadProgress(status, "EPUB", attempt, opts.attempts)
		_, err := a.DownloadEPUB(
			ctx,
			a.HTTP,
			flibusta.EPUBURL(a.Settings.DownloadBase, bookID),
			dest,
			opts.maxSizeMB,
			opts.timeout,
			progress.Update,
		)
		progress.Close()
		if err != nil {
			lastErr = err
			_ = os.Remove(dest)
			slog.Warn("EPUB download failed", "book_id", bookID, "attempt", attempt, "error", err)
			continue
		}
		status("<b>FLIBUSTA / DOWNLOAD</b>\n\n🔍 Проверка EPUB...")
		if !a.ValidateEPUB(dest) {
			lastErr = errors.New("invalid EPUB")
			_ = os.Remove(dest)
			slog.Warn("EPUB validation failed", "book_id", bookID, "attempt", attempt)
			continue
		}
		return dest, nil
	}
	return "", lastErr
}

func (a *App) downloadFB2Once(
	ctx context.Context,
	bookID int64,
	tmpdir string,
	opts downloadOptions,
	status func(string),
	attempt int,
) (string, error) {
	status(fmt.Sprintf(
		"<b>FLIBUSTA / DOWNLOAD</b>\n\n📥 FB2 · попытка %d/%d",
		attempt,
		opts.attempts,
	))
	progress := startDownloadProgress(status, "FB2", attempt, opts.attempts)
	path, err := a.DownloadFB2(
		ctx,
		a.HTTP,
		flibusta.FB2URL(a.Settings.DownloadBase, bookID),
		tmpdir,
		opts.maxSizeMB,
		opts.timeout,
		progress.Update,
	)
	progress.Close()
	if err != nil {
		slog.Warn("FB2 download failed", "book_id", bookID, "attempt", attempt, "error", err)
	}
	return path, err
}

func (a *App) downloadFB2(
	ctx context.Context,
	bookID int64,
	tmpdir string,
	opts downloadOptions,
	status func(string),
) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= opts.attempts; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		path, err := a.downloadFB2Once(ctx, bookID, tmpdir, opts, status, attempt)
		if err != nil {
			lastErr = err
			continue
		}
		return path, nil
	}
	return "", lastErr
}

func (a *App) sendBookFile(
	ctx context.Context,
	chatID, bookID int64,
	info *flibusta.BookInfo,
	path, label string,
	fb2 bool,
	status func(string),
) error {
	status("<b>FLIBUSTA / DOWNLOAD</b>\n\n📤 Отправка " + label + fileSizeLabel(path) + "...")
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	filename := FilenameFor(info.Title, bookID)
	if fb2 {
		filename = FilenameForFB2(info.Title, bookID)
	}
	_, err = a.TG.SendDocument(ctx, &tgbot.SendDocumentParams{
		ChatID: chatID,
		Document: &models.InputFileUpload{
			Filename: filename,
			Data:     file,
		},
	})
	return err
}

func (a *App) doDownload(ctx context.Context, chatID, bookID int64, state *store.UIState) {
	tmpdir, status, info, ok := a.prepareDownload(ctx, chatID, bookID, state, "download")
	if !ok {
		return
	}
	defer os.RemoveAll(tmpdir)

	opts := a.downloadOptions()
	epubPath := ""
	var lastEPUBErr error
	if info.HasEPUB {
		epubPath, lastEPUBErr = a.downloadEPUB(ctx, bookID, tmpdir, opts, status)
		if ctx.Err() != nil {
			return
		}
	}

	lastFB2Path := ""
	var lastFB2Err error
	var lastConvertErr error
	if epubPath == "" && info.HasFB2 {
		for attempt := 1; attempt <= opts.attempts; attempt++ {
			if ctx.Err() != nil {
				return
			}
			fb2Path, err := a.downloadFB2Once(ctx, bookID, tmpdir, opts, status, attempt)
			if err != nil {
				lastFB2Err = err
				continue
			}
			lastFB2Path = fb2Path

			status("<b>FLIBUSTA / DOWNLOAD</b>\n\n🛠 Конвертация FB2 → EPUB...")
			converted := filepath.Join(tmpdir, fmt.Sprintf("%d.epub", bookID))
			_ = os.Remove(converted)
			var lastStage string
			var lastStageAt time.Time
			if err := a.Convert(fb2Path, converted, func(stage string) {
				now := time.Now()
				if stage == lastStage {
					return
				}
				if !lastStageAt.IsZero() && now.Sub(lastStageAt) < 500*time.Millisecond {
					return
				}
				lastStage = stage
				lastStageAt = now
				status("<b>FLIBUSTA / DOWNLOAD</b>\n\n🛠 " + stage)
			}); err != nil {
				lastConvertErr = err
				slog.Warn("FB2 conversion failed", "book_id", bookID, "attempt", attempt, "error", err)
				_ = os.Remove(converted)
				continue
			}
			if !a.ValidateEPUB(converted) {
				lastConvertErr = errors.New("invalid EPUB after conversion")
				_ = os.Remove(converted)
				slog.Warn("converted EPUB validation failed", "book_id", bookID, "attempt", attempt)
				continue
			}
			epubPath = converted
			break
		}
		if ctx.Err() != nil {
			return
		}
	}

	if epubPath == "" {
		if lastFB2Path != "" {
			a.sendFB2Fallback(ctx, chatID, bookID, state, info, lastFB2Path, lastConvertErr, status)
			return
		}
		switch {
		case lastEPUBErr != nil:
			a.failDownload(ctx, chatID, bookID, state, lastEPUBErr, "download")
		case lastFB2Err != nil:
			a.failDownload(ctx, chatID, bookID, state, lastFB2Err, "download")
		case !info.HasEPUB && !info.HasFB2:
			state.Screen = "book"
			state.BookID = bookID
			messageID, sendErr := a.Messages.SendOrEdit(
				ctx, chatID, state.MessageID,
				"<b>FLIBUSTA / DOWNLOAD</b>\n\nEPUB/FB2 недоступны.",
				BookKB(bookID, info.HasEPUB, info.HasFB2),
			)
			if sendErr != nil {
				a.OnError(sendErr)
				return
			}
			state.MessageID = messageID
			_ = a.State.Save(ctx, state)
		default:
			a.failDownload(ctx, chatID, bookID, state, errors.New("не удалось получить книгу"), "download")
		}
		return
	}

	if err := a.sendBookFile(ctx, chatID, bookID, info, epubPath, "EPUB", false, status); err != nil {
		a.failDownload(ctx, chatID, bookID, state, err, "download")
		return
	}

	a.finishDownload(ctx, chatID, state, "✅ EPUB отправлен.")
}

func (a *App) finishDownload(ctx context.Context, chatID int64, state *store.UIState, notice string) {
	state.ChatID = chatID
	state.Screen = "main"
	state.Operation = ""
	state.Query = ""
	state.Page = 0
	state.BookID = 0
	state.Extra = map[string]any{}
	_ = a.State.Save(ctx, state)

	messageID, err := a.Messages.Replace(
		ctx, chatID, state.MessageID, a.mainMenuText(notice), MainMenuKB(),
	)
	if err != nil {
		// The book has already been delivered; try to leave a usable menu in
		// the existing message if a fresh one cannot be sent.
		if state.MessageID != 0 {
			if editedID, editErr := a.Messages.SendOrEdit(
				ctx, chatID, state.MessageID, a.mainMenuText(notice), MainMenuKB(),
			); editErr == nil {
				state.MessageID = editedID
				_ = a.State.Save(ctx, state)
			}
		}
		a.OnError(err)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)
}

func (a *App) doDownloadFB2(ctx context.Context, chatID, bookID int64, state *store.UIState) {
	tmpdir, status, info, ok := a.prepareDownload(ctx, chatID, bookID, state, "download_fb2")
	if !ok {
		return
	}
	defer os.RemoveAll(tmpdir)

	if !info.HasFB2 {
		a.failDownload(ctx, chatID, bookID, state, errors.New("FB2 недоступен"), "download_fb2")
		return
	}

	opts := a.downloadOptions()
	fb2Path, lastErr := a.downloadFB2(ctx, bookID, tmpdir, opts, status)
	if ctx.Err() != nil {
		return
	}
	if fb2Path == "" {
		if lastErr == nil {
			lastErr = errors.New("не удалось скачать FB2")
		}
		a.failDownload(ctx, chatID, bookID, state, lastErr, "download_fb2")
		return
	}

	if err := a.sendBookFile(ctx, chatID, bookID, info, fb2Path, "FB2", true, status); err != nil {
		a.failDownload(ctx, chatID, bookID, state, err, "download_fb2")
		return
	}

	a.finishDownload(ctx, chatID, state, "✅ FB2 отправлен.")
}

func (a *App) sendFB2Fallback(
	ctx context.Context,
	chatID, bookID int64,
	state *store.UIState,
	info *flibusta.BookInfo,
	fb2Path string,
	conversionErr error,
	status func(string),
) {
	reason := "конвертация завершилась ошибкой"
	if conversionErr != nil {
		reason = Esc(Clip(conversionErr.Error(), 300))
	}

	sendErr := a.sendBookFile(ctx, chatID, bookID, info, fb2Path, "FB2", true, status)
	if sendErr == nil {
		a.finishDownload(ctx, chatID, state, "⚠️ EPUB не удался, отправлен FB2.")
		return
	}
	var pathErr *os.PathError
	if errors.As(sendErr, &pathErr) {
		a.failDownload(ctx, chatID, bookID, state, sendErr, "download")
		return
	}

	text := fmt.Sprintf(
		"<b>FLIBUSTA / DOWNLOAD</b>\n\n⚠️ EPUB получить не удалось: %s\n\nНе удалось отправить и FB2: %s",
		reason,
		Esc(Clip(sendErr.Error(), 300)),
	)
	messageID, editErr := a.Messages.SendOrEdit(
		ctx, chatID, state.MessageID, text, DownloadErrorKB(bookID, "download"),
	)
	if editErr != nil {
		a.OnError(editErr)
		return
	}
	state.MessageID = messageID
	state.Screen = "book"
	state.BookID = bookID
	_ = a.State.Save(ctx, state)
}

func (a *App) failDownload(
	ctx context.Context,
	chatID, bookID int64,
	state *store.UIState,
	err error,
	retryAction string,
) {
	slog.Error("download failed", "book_id", bookID, "error", err)
	text := "<b>FLIBUSTA / DOWNLOAD</b>\n\nОшибка: " + Esc(Clip(err.Error(), 300))
	messageID, sendErr := a.Messages.SendOrEdit(
		ctx, chatID, state.MessageID, text, DownloadErrorKB(bookID, retryAction),
	)
	if sendErr != nil {
		a.OnError(sendErr)
	}
	state.MessageID = messageID
	state.Screen = "book"
	state.BookID = bookID
	_ = a.State.Save(ctx, state)
}
