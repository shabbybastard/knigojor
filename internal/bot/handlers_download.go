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
	if fb2Only {
		a.doDownloadFB2(ctx, chatID, bookID, state)
		return
	}
	a.doDownload(ctx, chatID, bookID, state)
}

func (a *App) doDownload(ctx context.Context, chatID, bookID int64, state *store.UIState) {
	tmpdir := filepath.Join(a.Settings.TempPath, strconv.FormatInt(bookID, 10))
	if err := os.MkdirAll(tmpdir, 0o700); err != nil {
		a.failDownload(ctx, chatID, bookID, state, err, "download")
		return
	}
	_ = os.WriteFile(filepath.Join(tmpdir, tempMarker), []byte("1"), 0o600)
	defer os.RemoveAll(tmpdir)

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
		slog.Error("book fetch failed", "error", err)
		a.failDownload(ctx, chatID, bookID, state, err, "download")
		return
	}

	timeout := time.Duration(a.Settings.DownloadTimeout) * time.Second
	attempts := a.Settings.DownloadAttempts
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 3 {
		attempts = 3
	}
	maxSize := a.Settings.MaxFileSizeMB
	epubPath := ""
	lastFB2Path := ""
	var lastEPUBErr error
	var lastFB2Err error
	var lastConvertErr error

	if info.HasEPUB {
		for attempt := 1; attempt <= attempts; attempt++ {
			if ctx.Err() != nil {
				return
			}
			status(fmt.Sprintf(
				"<b>FLIBUSTA / DOWNLOAD</b>\n\n📥 EPUB · попытка %d/%d",
				attempt,
				attempts,
			))
			dest := filepath.Join(tmpdir, fmt.Sprintf("%d.epub", bookID))
			progress := startDownloadProgress(status, "EPUB", attempt, attempts)
			_, err := a.DownloadEPUB(
				ctx,
				a.HTTP,
				flibusta.EPUBURL(a.Settings.DownloadBase, bookID),
				dest,
				maxSize,
				timeout,
				progress.Update,
			)
			progress.Close()
			if err != nil {
				lastEPUBErr = err
				_ = os.Remove(dest)
				slog.Warn("EPUB download failed", "book_id", bookID, "attempt", attempt, "error", err)
				continue
			}
			status("<b>FLIBUSTA / DOWNLOAD</b>\n\n🔍 Проверка EPUB...")
			if !a.ValidateEPUB(dest) {
				lastEPUBErr = errors.New("invalid EPUB")
				_ = os.Remove(dest)
				slog.Warn("EPUB validation failed", "book_id", bookID, "attempt", attempt)
				continue
			}
			epubPath = dest
			break
		}
	}

	if epubPath == "" && info.HasFB2 {
		for attempt := 1; attempt <= attempts; attempt++ {
			if ctx.Err() != nil {
				return
			}
			status(fmt.Sprintf(
				"<b>FLIBUSTA / DOWNLOAD</b>\n\n📥 FB2 · попытка %d/%d",
				attempt,
				attempts,
			))
			progress := startDownloadProgress(status, "FB2", attempt, attempts)
			fb2Path, err := a.DownloadFB2(
				ctx,
				a.HTTP,
				flibusta.FB2URL(a.Settings.DownloadBase, bookID),
				tmpdir,
				maxSize,
				timeout,
				progress.Update,
			)
			progress.Close()
			if err != nil {
				lastFB2Err = err
				slog.Warn("FB2 download failed", "book_id", bookID, "attempt", attempt, "error", err)
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
	}

	if epubPath == "" {
		if lastFB2Path != "" {
			a.sendFB2Fallback(ctx, chatID, bookID, state, info, lastFB2Path, lastConvertErr)
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

	status("<b>FLIBUSTA / DOWNLOAD</b>\n\n📤 Отправка EPUB" + fileSizeLabel(epubPath) + "...")
	file, err := os.Open(epubPath)
	if err != nil {
		a.failDownload(ctx, chatID, bookID, state, err, "download")
		return
	}
	_, sendErr := a.TG.SendDocument(ctx, &tgbot.SendDocumentParams{
		ChatID: chatID,
		Document: &models.InputFileUpload{
			Filename: FilenameFor(info.Title, bookID),
			Data:     file,
		},
	})
	_ = file.Close()
	if sendErr != nil {
		a.failDownload(ctx, chatID, bookID, state, sendErr, "download")
		return
	}

	state.Screen = "book"
	state.BookID = bookID
	finalText := "<b>FLIBUSTA / DOWNLOAD</b>\n\n✅ Книга отправлена.\n\n" + RenderBook(info)
	messageID, sendErr := a.Messages.ReplaceWithLinkPreview(
		ctx,
		chatID,
		state.MessageID,
		finalText,
		BookKB(bookID, info.HasEPUB, info.HasFB2),
		CoverLinkPreview(info.CoverURL),
	)
	if sendErr != nil {
		// The file is already delivered. If a fresh message cannot be sent,
		// at least leave a usable card in the existing message.
		if state.MessageID != 0 {
			if editedID, editErr := a.Messages.SendOrEditWithLinkPreview(
				ctx,
				chatID,
				state.MessageID,
				finalText,
				BookKB(bookID, info.HasEPUB, info.HasFB2),
				CoverLinkPreview(info.CoverURL),
			); editErr == nil {
				state.MessageID = editedID
				_ = a.State.Save(ctx, state)
			}
		}
		a.OnError(sendErr)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)
}

func (a *App) doDownloadFB2(ctx context.Context, chatID, bookID int64, state *store.UIState) {
	tmpdir := filepath.Join(a.Settings.TempPath, strconv.FormatInt(bookID, 10))
	if err := os.MkdirAll(tmpdir, 0o700); err != nil {
		a.failDownload(ctx, chatID, bookID, state, err, "download_fb2")
		return
	}
	_ = os.WriteFile(filepath.Join(tmpdir, tempMarker), []byte("1"), 0o600)
	defer os.RemoveAll(tmpdir)

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
		a.failDownload(ctx, chatID, bookID, state, err, "download_fb2")
		return
	}
	if !info.HasFB2 {
		a.failDownload(ctx, chatID, bookID, state, errors.New("FB2 недоступен"), "download_fb2")
		return
	}

	timeout := time.Duration(a.Settings.DownloadTimeout) * time.Second
	attempts := a.Settings.DownloadAttempts
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 3 {
		attempts = 3
	}
	maxSize := a.Settings.MaxFileSizeMB
	fb2Path := ""
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if ctx.Err() != nil {
			return
		}
		status(fmt.Sprintf(
			"<b>FLIBUSTA / DOWNLOAD</b>\n\n📥 FB2 · попытка %d/%d",
			attempt,
			attempts,
		))
		progress := startDownloadProgress(status, "FB2", attempt, attempts)
		path, err := a.DownloadFB2(
			ctx,
			a.HTTP,
			flibusta.FB2URL(a.Settings.DownloadBase, bookID),
			tmpdir,
			maxSize,
			timeout,
			progress.Update,
		)
		progress.Close()
		if err != nil {
			lastErr = err
			slog.Warn("FB2 download failed", "book_id", bookID, "attempt", attempt, "error", err)
			continue
		}
		fb2Path = path
		break
	}
	if fb2Path == "" {
		if lastErr == nil {
			lastErr = errors.New("не удалось скачать FB2")
		}
		a.failDownload(ctx, chatID, bookID, state, lastErr, "download_fb2")
		return
	}

	status("<b>FLIBUSTA / DOWNLOAD</b>\n\n📤 Отправка FB2" + fileSizeLabel(fb2Path) + "...")
	file, err := os.Open(fb2Path)
	if err != nil {
		a.failDownload(ctx, chatID, bookID, state, err, "download_fb2")
		return
	}
	_, sendErr := a.TG.SendDocument(ctx, &tgbot.SendDocumentParams{
		ChatID: chatID,
		Document: &models.InputFileUpload{
			Filename: FilenameForFB2(info.Title, bookID),
			Data:     file,
		},
	})
	_ = file.Close()
	if sendErr != nil {
		a.failDownload(ctx, chatID, bookID, state, sendErr, "download_fb2")
		return
	}

	state.Screen = "book"
	state.BookID = bookID
	finalText := "<b>FLIBUSTA / DOWNLOAD</b>\n\n✅ FB2 отправлен.\n\n" + RenderBook(info)
	messageID, sendErr := a.Messages.ReplaceWithLinkPreview(
		ctx,
		chatID,
		state.MessageID,
		finalText,
		BookKB(bookID, info.HasEPUB, info.HasFB2),
		CoverLinkPreview(info.CoverURL),
	)
	if sendErr != nil {
		if state.MessageID != 0 {
			if editedID, editErr := a.Messages.SendOrEditWithLinkPreview(
				ctx,
				chatID,
				state.MessageID,
				finalText,
				BookKB(bookID, info.HasEPUB, info.HasFB2),
				CoverLinkPreview(info.CoverURL),
			); editErr == nil {
				state.MessageID = editedID
				_ = a.State.Save(ctx, state)
			}
		}
		a.OnError(sendErr)
		return
	}
	state.MessageID = messageID
	_ = a.State.Save(ctx, state)
}

func (a *App) sendFB2Fallback(
	ctx context.Context,
	chatID, bookID int64,
	state *store.UIState,
	info *flibusta.BookInfo,
	fb2Path string,
	conversionErr error,
) {
	reason := "конвертация завершилась ошибкой"
	if conversionErr != nil {
		reason = Esc(Clip(conversionErr.Error(), 300))
	}

	statusText := "<b>FLIBUSTA / DOWNLOAD</b>\n\n📤 Отправка FB2" + fileSizeLabel(fb2Path) + "..."
	if messageID, err := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, statusText, nil); err == nil {
		state.MessageID = messageID
		_ = a.State.Save(ctx, state)
	}

	file, err := os.Open(fb2Path)
	if err != nil {
		a.failDownload(ctx, chatID, bookID, state, err, "download")
		return
	}
	_, sendErr := a.TG.SendDocument(ctx, &tgbot.SendDocumentParams{
		ChatID: chatID,
		Document: &models.InputFileUpload{
			Filename: FilenameForFB2(info.Title, bookID),
			Data:     file,
		},
	})
	_ = file.Close()

	text := fmt.Sprintf(
		"<b>FLIBUSTA / DOWNLOAD</b>\n\n⚠️ EPUB получить не удалось: %s\n\nИсходный FB2 отправлен отдельным файлом.",
		reason,
	)
	if sendErr != nil {
		text = fmt.Sprintf(
			"<b>FLIBUSTA / DOWNLOAD</b>\n\n⚠️ EPUB получить не удалось: %s\n\nНе удалось отправить и FB2: %s",
			reason,
			Esc(Clip(sendErr.Error(), 300)),
		)
	}
	var messageID int
	var editErr error
	if sendErr == nil {
		messageID, editErr = a.Messages.Replace(
			ctx, chatID, state.MessageID, text, DownloadErrorKB(bookID, "download"),
		)
		if editErr != nil && state.MessageID != 0 {
			messageID, editErr = a.Messages.SendOrEdit(
				ctx, chatID, state.MessageID, text, DownloadErrorKB(bookID, "download"),
			)
		}
	} else {
		messageID, editErr = a.Messages.SendOrEdit(
			ctx, chatID, state.MessageID, text, DownloadErrorKB(bookID, "download"),
		)
	}
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
