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
	"github.com/shabbybastard/knigojor/internal/netx"
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
	if err != nil {
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
	a.doDownload(ctx, chatID, bookID, state)
}

func (a *App) doDownload(ctx context.Context, chatID, bookID int64, state *store.UIState) {
	tmpdir := filepath.Join(a.Settings.TempPath, strconv.FormatInt(bookID, 10))
	if err := os.MkdirAll(tmpdir, 0o700); err != nil {
		a.failDownload(ctx, chatID, bookID, state, err, false, false)
		return
	}
	defer os.RemoveAll(tmpdir)

	status := func(text string, keyboard models.ReplyMarkup) {
		messageID, err := a.Messages.SendOrEdit(ctx, chatID, state.MessageID, text, keyboard)
		if err != nil {
			a.OnError(err)
			return
		}
		state.MessageID = messageID
		_ = a.State.Save(ctx, state)
	}

	status("<b>FLIBUSTA / DOWNLOAD</b>\n\n⏳ Подготовка книги...", nil)

	info, err := a.Client.GetBook(ctx, bookID)
	if err != nil {
		slog.Error("book fetch failed", "error", err)
		status(
			"<b>FLIBUSTA / DOWNLOAD</b>\n\nОшибка загрузки книги: "+Esc(Clip(err.Error(), 300)),
			MainMenuKB(),
		)
		state.Screen = "main"
		_ = a.State.Save(ctx, state)
		return
	}

	timeout := time.Duration(a.Settings.DownloadTimeout) * time.Second
	epubPath := ""

	fetchEPUB := func(opCtx context.Context, mirror *netx.Mirror) (string, error) {
		http, err := a.Client.HTTPFor(mirror)
		if err != nil {
			return "", err
		}
		dest := filepath.Join(tmpdir, fmt.Sprintf("%d.epub", bookID))
		if _, err := a.DownloadEPUB(
			opCtx, http, flibusta.EPUBURL(mirror.URL(), bookID), dest,
			a.Settings.MaxFileSizeMB, timeout,
		); err != nil {
			return "", err
		}
		if !a.ValidateEPUB(dest) {
			_ = os.Remove(dest)
			return "", &netx.NonMirrorError{Err: errors.New("invalid EPUB")}
		}
		return dest, nil
	}
	fetchFB2 := func(opCtx context.Context, mirror *netx.Mirror) (string, error) {
		http, err := a.Client.HTTPFor(mirror)
		if err != nil {
			return "", err
		}
		return a.DownloadFB2(
			opCtx, http, flibusta.FB2URL(mirror.URL(), bookID), tmpdir,
			a.Settings.MaxFileSizeMB, timeout,
		)
	}

	if info.HasEPUB {
		status("<b>FLIBUSTA / DOWNLOAD</b>\n\nСкачивание EPUB...", nil)
		path, _, err := netx.RunWithFailover(ctx, a.Mirrors, fetchEPUB)
		if err != nil {
			slog.Warn("EPUB download failed", "error", err)
			if !info.HasFB2 {
				a.failDownload(ctx, chatID, bookID, state, err, info.HasEPUB, info.HasFB2)
				return
			}
		} else {
			epubPath = path
		}
	}

	if epubPath == "" && info.HasFB2 {
		status("<b>FLIBUSTA / DOWNLOAD</b>\n\nСкачивание FB2...", nil)
		fb2Path, _, err := netx.RunWithFailover(ctx, a.Mirrors, fetchFB2)
		if err != nil {
			a.failDownload(ctx, chatID, bookID, state, err, info.HasEPUB, info.HasFB2)
			return
		}
		status("<b>FLIBUSTA / DOWNLOAD</b>\n\nПреобразование FB2 → EPUB...", nil)
		epubPath = filepath.Join(tmpdir, fmt.Sprintf("%d.epub", bookID))
		if err := a.Convert(fb2Path, epubPath); err != nil {
			a.failDownload(ctx, chatID, bookID, state, err, info.HasEPUB, info.HasFB2)
			return
		}
		if !a.ValidateEPUB(epubPath) {
			_ = os.Remove(epubPath)
			a.failDownload(ctx, chatID, bookID, state, errors.New("invalid EPUB after conversion"), info.HasEPUB, info.HasFB2)
			return
		}
	}

	if epubPath == "" {
		status(
			"<b>FLIBUSTA / DOWNLOAD</b>\n\nEPUB/FB2 недоступны.",
			BookKB(bookID, info.HasEPUB, info.HasFB2),
		)
		state.Screen = "book"
		state.BookID = bookID
		_ = a.State.Save(ctx, state)
		return
	}

	status("<b>FLIBUSTA / DOWNLOAD</b>\n\nОтправка файла...", nil)
	file, err := os.Open(epubPath)
	if err != nil {
		a.failDownload(ctx, chatID, bookID, state, err, info.HasEPUB, info.HasFB2)
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
		a.failDownload(ctx, chatID, bookID, state, sendErr, info.HasEPUB, info.HasFB2)
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

func (a *App) failDownload(ctx context.Context, chatID, bookID int64, state *store.UIState, err error, hasEPUB, hasFB2 bool) {
	slog.Error("download failed", "book_id", bookID, "error", err)
	text := "<b>FLIBUSTA / DOWNLOAD</b>\n\nОшибка: " + Esc(Clip(err.Error(), 300))
	messageID, sendErr := a.Messages.SendOrEdit(
		ctx, chatID, state.MessageID, text, BookKB(bookID, hasEPUB, hasFB2),
	)
	if sendErr != nil {
		a.OnError(sendErr)
	}
	state.MessageID = messageID
	state.Screen = "book"
	state.BookID = bookID
	_ = a.State.Save(ctx, state)
}
