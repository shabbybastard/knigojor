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
	a.doDownload(ctx, chatID, bookID, state)
}

func (a *App) doDownload(ctx context.Context, chatID, bookID int64, state *store.UIState) {
	tmpdir := filepath.Join(a.Settings.TempPath, strconv.FormatInt(bookID, 10))
	if err := os.MkdirAll(tmpdir, 0o700); err != nil {
		a.failDownload(ctx, chatID, bookID, state, err)
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
		a.failDownload(ctx, chatID, bookID, state, err)
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
				"<b>FLIBUSTA / DOWNLOAD</b>\n\n📥 Скачивание EPUB (попытка %d/%d)...",
				attempt,
				attempts,
			))
			dest := filepath.Join(tmpdir, fmt.Sprintf("%d.epub", bookID))
			_, err := a.DownloadEPUB(
				ctx,
				a.HTTP,
				flibusta.EPUBURL(a.Settings.DownloadBase, bookID),
				dest,
				maxSize,
				timeout,
			)
			if err != nil {
				lastEPUBErr = err
				_ = os.Remove(dest)
				slog.Warn("EPUB download failed", "book_id", bookID, "attempt", attempt, "error", err)
				continue
			}
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
				"<b>FLIBUSTA / DOWNLOAD</b>\n\n📥 Скачивание FB2 (попытка %d/%d)...",
				attempt,
				attempts,
			))
			fb2Path, err := a.DownloadFB2(
				ctx,
				a.HTTP,
				flibusta.FB2URL(a.Settings.DownloadBase, bookID),
				tmpdir,
				maxSize,
				timeout,
			)
			if err != nil {
				lastFB2Err = err
				slog.Warn("FB2 download failed", "book_id", bookID, "attempt", attempt, "error", err)
				continue
			}
			lastFB2Path = fb2Path

			status("<b>FLIBUSTA / DOWNLOAD</b>\n\n🛠 Преобразование FB2 → EPUB...")
			converted := filepath.Join(tmpdir, fmt.Sprintf("%d.epub", bookID))
			_ = os.Remove(converted)
			if err := a.Convert(fb2Path, converted); err != nil {
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
			a.failDownload(ctx, chatID, bookID, state, lastEPUBErr)
		case lastFB2Err != nil:
			a.failDownload(ctx, chatID, bookID, state, lastFB2Err)
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
			a.failDownload(ctx, chatID, bookID, state, errors.New("не удалось получить книгу"))
		}
		return
	}

	status("<b>FLIBUSTA / DOWNLOAD</b>\n\n📤 Отправка файла...")
	file, err := os.Open(epubPath)
	if err != nil {
		a.failDownload(ctx, chatID, bookID, state, err)
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
		a.failDownload(ctx, chatID, bookID, state, sendErr)
		return
	}

	state.Screen = "book"
	state.BookID = bookID
	messageID, sendErr := a.Messages.SendOrEditWithLinkPreview(
		ctx,
		chatID,
		state.MessageID,
		"<b>FLIBUSTA / DOWNLOAD</b>\n\n✅ Книга отправлена.\n\n"+RenderBook(info),
		BookKB(bookID, info.HasEPUB, info.HasFB2),
		CoverLinkPreview(info.CoverURL),
	)
	if sendErr != nil {
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

	file, err := os.Open(fb2Path)
	if err != nil {
		a.failDownload(ctx, chatID, bookID, state, err)
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
	messageID, editErr := a.Messages.SendOrEdit(
		ctx, chatID, state.MessageID, text, DownloadErrorKB(bookID),
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
) {
	slog.Error("download failed", "book_id", bookID, "error", err)
	text := "<b>FLIBUSTA / DOWNLOAD</b>\n\nОшибка: " + Esc(Clip(err.Error(), 300))
	messageID, sendErr := a.Messages.SendOrEdit(
		ctx, chatID, state.MessageID, text, DownloadErrorKB(bookID),
	)
	if sendErr != nil {
		a.OnError(sendErr)
	}
	state.MessageID = messageID
	state.Screen = "book"
	state.BookID = bookID
	_ = a.State.Save(ctx, state)
}
