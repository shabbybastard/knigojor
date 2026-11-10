package bot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/shabbybastard/knigojor/internal/config"
	"github.com/shabbybastard/knigojor/internal/flibusta"
	"github.com/shabbybastard/knigojor/internal/netx"
	"github.com/shabbybastard/knigojor/internal/store"
)

type fakeTelegram struct {
	messages  []*tgbot.SendMessageParams
	edits     []*tgbot.EditMessageTextParams
	documents []*tgbot.SendDocumentParams
	answers   []*tgbot.AnswerCallbackQueryParams
	deletes   []*tgbot.DeleteMessageParams
	editErr   error
}

func (f *fakeTelegram) SendMessage(_ context.Context, params *tgbot.SendMessageParams) (*models.Message, error) {
	f.messages = append(f.messages, params)
	return &models.Message{ID: len(f.messages)}, nil
}

func (f *fakeTelegram) EditMessageText(_ context.Context, params *tgbot.EditMessageTextParams) (*models.Message, error) {
	f.edits = append(f.edits, params)
	if f.editErr != nil {
		return nil, f.editErr
	}
	return &models.Message{ID: params.MessageID}, nil
}

func (f *fakeTelegram) SendDocument(_ context.Context, params *tgbot.SendDocumentParams) (*models.Message, error) {
	f.documents = append(f.documents, params)
	return &models.Message{ID: 1}, nil
}

func (f *fakeTelegram) AnswerCallbackQuery(_ context.Context, params *tgbot.AnswerCallbackQueryParams) (bool, error) {
	f.answers = append(f.answers, params)
	return true, nil
}

func (f *fakeTelegram) DeleteMessage(_ context.Context, params *tgbot.DeleteMessageParams) (bool, error) {
	f.deletes = append(f.deletes, params)
	return true, nil
}

type fakeFlibusta struct {
	searchCalls []int
	bookCalls   []int64
	authorCalls []int64
	book        *flibusta.BookInfo
	authors     []flibusta.AuthorResult
	authorBooks *flibusta.AuthorBooks
}

func (f *fakeFlibusta) Search(_ context.Context, _ string, page int) (*flibusta.SearchResults, error) {
	f.searchCalls = append(f.searchCalls, page)
	start := int64(page*50 + 1)
	end := min(start+50, 563)
	books := make([]flibusta.SearchResultBook, 0, 50)
	for id := start; id < end; id++ {
		books = append(books, flibusta.SearchResultBook{
			ID:      id,
			Title:   "Book",
			Authors: []string{"Author"},
			URL:     "/b/" + itoa(id),
		})
	}
	return &flibusta.SearchResults{
		Books:   books,
		Authors: f.authors,
		Total:   562,
		Page:    page,
		HasNext: end < 562,
	}, nil
}

func (f *fakeFlibusta) GetBook(_ context.Context, bookID int64) (*flibusta.BookInfo, error) {
	f.bookCalls = append(f.bookCalls, bookID)
	if f.book != nil {
		copy := *f.book
		copy.ID = bookID
		return &copy, nil
	}
	return &flibusta.BookInfo{
		ID:        bookID,
		Title:     "Book title",
		Authors:   []string{"Author"},
		HasEPUB:   true,
		SourceURL: "https://mirror/b/1",
	}, nil
}

func (f *fakeFlibusta) GetAuthorBooks(_ context.Context, authorID int64) (*flibusta.AuthorBooks, error) {
	f.authorCalls = append(f.authorCalls, authorID)
	if f.authorBooks != nil {
		copy := *f.authorBooks
		copy.ID = authorID
		return &copy, nil
	}
	return &flibusta.AuthorBooks{
		ID:   authorID,
		Name: "Author Name",
		Books: []flibusta.SearchResultBook{
			{ID: 1, Title: "Book 1"},
			{ID: 2, Title: "Book 2"},
		},
	}, nil
}

func (f *fakeFlibusta) HTTPFor(_ *netx.Mirror) (*netx.Client, error) { return nil, nil }

func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	position := len(buf)
	for value > 0 {
		position--
		buf[position] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[position:])
}

func newTestApp(t *testing.T, client Flibusta) (*App, *fakeTelegram) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tempPath := filepath.Join(dir, "temp")
	if err := os.MkdirAll(tempPath, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := &config.Settings{
		AllowedUserID:        1,
		PageSize:             5,
		DownloadTimeout:      10,
		MaxFileSizeMB:        5,
		TempPath:             tempPath,
		MaxParallelDownloads: 1,
	}
	telegram := &fakeTelegram{}
	app := &App{
		Settings:     settings,
		DB:           db,
		State:        store.NewStateStore(db),
		Mirrors:      netx.NewMirrorManager([]string{"https://mirror"}, nil, false, nil),
		Client:       client,
		Messages:     NewMessageManager(telegram),
		TG:           telegram,
		DownloadSem:  make(chan struct{}, 1),
		StartTime:    time.Now(),
		Version:      "test",
		ValidateEPUB: func(string) bool { return true },
	}
	app.DownloadEPUB = func(_ context.Context, _ *netx.Client, _, dest string, _ int, _ time.Duration) (string, error) {
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(dest, []byte("epub"), 0o600); err != nil {
			return "", err
		}
		return dest, nil
	}
	app.DownloadFB2 = func(_ context.Context, _ *netx.Client, _, destDir string, _ int, _ time.Duration) (string, error) {
		path := filepath.Join(destDir, "book.fb2")
		if err := os.MkdirAll(destDir, 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte("<FictionBook/>"), 0o600); err != nil {
			return "", err
		}
		return path, nil
	}
	app.Convert = func(_, dest string) error {
		return os.WriteFile(dest, []byte("epub"), 0o600)
	}
	return app, telegram
}

func textUpdate(userID, chatID int64, text string) *models.Update {
	return &models.Update{
		ID: 1,
		Message: &models.Message{
			ID:   1,
			Chat: models.Chat{ID: chatID},
			Text: text,
			From: &models.User{ID: userID},
		},
	}
}

func callbackUpdate(userID, chatID int64, data string) *models.Update {
	return &models.Update{
		ID: 2,
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback",
			From: models.User{ID: userID},
			Data: data,
			Message: models.MaybeInaccessibleMessage{
				Type: models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{
					ID:   5,
					Chat: models.Chat{ID: chatID},
				},
			},
		},
	}
}

func TestDownloadFallsBackToFB2(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{
		Title:   "Book title",
		Authors: []string{"Author"},
		HasEPUB: true,
		HasFB2:  true,
	}}
	app, telegram := newTestApp(t, client)
	app.DownloadEPUB = func(_ context.Context, _ *netx.Client, _, _ string, _ int, _ time.Duration) (string, error) {
		return "", errors.New("epub download failed")
	}
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "book"
	state.BookID = 1
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleDownload(context.Background(), nil, callbackUpdate(1, 10, "dl:1"))

	if len(telegram.documents) != 1 {
		t.Fatalf("fallback did not send a document: %d", len(telegram.documents))
	}
	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != "book" || saved.BookID != 1 {
		t.Fatalf("unexpected state: %#v", saved)
	}
}

func TestDownloadReportsBothFormatsFailure(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{
		Title:   "Book title",
		HasEPUB: true,
		HasFB2:  true,
	}}
	app, telegram := newTestApp(t, client)
	app.DownloadEPUB = func(_ context.Context, _ *netx.Client, _, _ string, _ int, _ time.Duration) (string, error) {
		return "", errors.New("epub failed")
	}
	app.DownloadFB2 = func(_ context.Context, _ *netx.Client, _, _ string, _ int, _ time.Duration) (string, error) {
		return "", errors.New("fb2 failed")
	}
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "book"
	state.BookID = 1
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleDownload(context.Background(), nil, callbackUpdate(1, 10, "dl:1"))

	if len(telegram.documents) != 0 {
		t.Fatal("a document was sent despite both formats failing")
	}
	last := telegram.edits[len(telegram.edits)-1]
	if !strings.Contains(last.Text, "Ошибка") {
		t.Fatalf("unexpected final status: %s", last.Text)
	}
}

func TestSearchTextHandler(t *testing.T) {
	client := &fakeFlibusta{}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "awaiting_search"
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleText(context.Background(), nil, textUpdate(1, 10, "Пушкин"))

	saved, err := app.State.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Screen != "results" || saved.Query != "Пушкин" || saved.Page != 0 {
		t.Fatalf("unexpected state: %#v", saved)
	}
	if len(client.searchCalls) != 1 || client.searchCalls[0] != 0 {
		t.Fatalf("unexpected search calls: %#v", client.searchCalls)
	}
	if len(telegram.edits) == 0 {
		t.Fatal("no edit calls")
	}
	last := telegram.edits[len(telegram.edits)-1]
	if !strings.Contains(last.Text, "Страница 1 из 113") {
		t.Fatalf("unexpected page text: %s", last.Text)
	}
}

func TestAuthorSearchShowsAuthorScreen(t *testing.T) {
	client := &fakeFlibusta{authors: []flibusta.AuthorResult{
		{ID: 7171, Name: "Лев Николаевич Толстой", Details: "Лев Николаевич Толстой (879 книг)"},
		{ID: 20035, Name: "Алексей Константинович Толстой", Details: "Алексей Константинович Толстой (94 книги)"},
	}}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "awaiting_search"
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleText(context.Background(), nil, textUpdate(1, 10, "Толстой"))

	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != screenAuthors || saved.Query != "Толстой" {
		t.Fatalf("unexpected state: %#v", saved)
	}
	last := telegram.edits[len(telegram.edits)-1]
	if !strings.Contains(last.Text, "Найдено авторов: 2") {
		t.Fatalf("unexpected authors text: %s", last.Text)
	}
	markup, ok := last.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("unexpected markup type: %T", last.ReplyMarkup)
	}
	found := false
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if button.CallbackData == "author:7171" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("author button is missing")
	}
}

func TestAuthorListPagination(t *testing.T) {
	authors := make([]flibusta.AuthorResult, 0, 12)
	for id := int64(1); id <= 12; id++ {
		authors = append(authors, flibusta.AuthorResult{
			ID:      id,
			Name:    "Author",
			Details: "Author (1 книга)",
		})
	}
	client := &fakeFlibusta{authors: authors}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "awaiting_search"
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleText(context.Background(), nil, textUpdate(1, 10, "Author"))
	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != screenAuthors || saved.Page != 0 {
		t.Fatalf("unexpected initial author state: %#v", saved)
	}

	app.HandleAuthorPage(context.Background(), nil, callbackUpdate(1, 10, "apage:next:0"))

	saved, _ = app.State.Get(context.Background(), 1)
	if saved.Screen != screenAuthors || saved.Page != 1 {
		t.Fatalf("unexpected paginated author state: %#v", saved)
	}
	last := telegram.edits[len(telegram.edits)-1]
	if !strings.Contains(last.Text, "<b>11. Author (1 книга)</b>") ||
		!strings.Contains(last.Text, "Страница 2 из 2") {
		t.Fatalf("unexpected second author page: %s", last.Text)
	}
}

func TestAuthorSelectionShowsBooks(t *testing.T) {
	books := make([]flibusta.SearchResultBook, 0, 12)
	for id := int64(1); id <= 12; id++ {
		books = append(books, flibusta.SearchResultBook{ID: id, Title: "Book"})
	}
	client := &fakeFlibusta{authorBooks: &flibusta.AuthorBooks{
		ID:    7171,
		Name:  "Лев Николаевич Толстой",
		Books: books,
	}}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = screenAuthors
	state.Query = "Толстой"
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleAuthor(context.Background(), nil, callbackUpdate(1, 10, "author:7171"))

	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != screenAuthorResults || saved.Operation != "author" {
		t.Fatalf("unexpected state: %#v", saved)
	}
	if extraInt64(saved.Extra, "author_id") != 7171 {
		t.Fatalf("author id was not saved: %#v", saved.Extra)
	}
	last := telegram.edits[len(telegram.edits)-1]
	if !strings.Contains(last.Text, "FLIBUSTA / AUTHOR") ||
		!strings.Contains(last.Text, "Страница 1 из 3") {
		t.Fatalf("unexpected author results: %s", last.Text)
	}
}

func TestAuthorPageNavigation(t *testing.T) {
	books := make([]flibusta.SearchResultBook, 0, 12)
	for id := int64(1); id <= 12; id++ {
		books = append(books, flibusta.SearchResultBook{ID: id, Title: "Book"})
	}
	client := &fakeFlibusta{authorBooks: &flibusta.AuthorBooks{
		ID:    7171,
		Name:  "Author",
		Books: books,
	}}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = screenAuthorResults
	state.Operation = "author"
	state.Page = 0
	state.Extra = map[string]any{"author_id": int64(7171), "author_name": "Author"}
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandlePage(context.Background(), nil, callbackUpdate(1, 10, "page:next:0"))

	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Page != 1 {
		t.Fatalf("page did not advance: %#v", saved)
	}
	last := telegram.edits[len(telegram.edits)-1]
	if !strings.Contains(last.Text, "Страница 2 из 3") ||
		!strings.Contains(last.Text, "книги 6–10 из 12") {
		t.Fatalf("unexpected author page: %s", last.Text)
	}
}

func TestBackResultsFromAuthor(t *testing.T) {
	client := &fakeFlibusta{authorBooks: &flibusta.AuthorBooks{
		ID:    7171,
		Name:  "Author",
		Books: []flibusta.SearchResultBook{{ID: 1, Title: "Book 1"}, {ID: 2, Title: "Book 2"}},
	}}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "book"
	state.Operation = "author"
	state.Page = 0
	state.Extra = map[string]any{"author_id": int64(7171), "author_name": "Author"}
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleBackResults(context.Background(), nil, callbackUpdate(1, 10, "back_results"))

	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != screenAuthorResults || saved.Operation != "author" {
		t.Fatalf("unexpected state: %#v", saved)
	}
	if !strings.Contains(telegram.edits[len(telegram.edits)-1].Text, "FLIBUSTA / AUTHOR") {
		t.Fatal("author results were not restored")
	}
}

func TestBookLoadingAndCoverPreview(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{
		Title:    "Book title",
		Authors:  []string{"Author"},
		CoverURL: "https://mirror/cover.jpg",
		HasEPUB:  true,
	}}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "results"
	state.Operation = "search"
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleBook(context.Background(), nil, callbackUpdate(1, 10, "book:1:1"))

	if len(telegram.edits) < 2 {
		t.Fatalf("expected loading and card edits, got %d", len(telegram.edits))
	}
	if !strings.Contains(telegram.edits[0].Text, "Загрузка") {
		t.Fatalf("loading indicator is missing: %s", telegram.edits[0].Text)
	}
	last := telegram.edits[len(telegram.edits)-1]
	if strings.Contains(last.Text, "Обложка") {
		t.Fatalf("cover link should be removed: %s", last.Text)
	}
	if last.LinkPreviewOptions == nil ||
		last.LinkPreviewOptions.URL == nil ||
		*last.LinkPreviewOptions.URL != "https://mirror/cover.jpg" ||
		last.LinkPreviewOptions.PreferSmallMedia == nil ||
		!*last.LinkPreviewOptions.PreferSmallMedia ||
		last.LinkPreviewOptions.ShowAboveText == nil ||
		!*last.LinkPreviewOptions.ShowAboveText {
		t.Fatalf("unexpected cover preview options: %#v", last.LinkPreviewOptions)
	}
}

func TestSearchBooksFromAuthorScreen(t *testing.T) {
	client := &fakeFlibusta{}
	app, _ := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = screenAuthors
	state.Query = "Толстой"
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleSearchBooks(context.Background(), nil, callbackUpdate(1, 10, "search_books"))

	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != "results" {
		t.Fatalf("unexpected state: %#v", saved)
	}
	if len(client.searchCalls) != 1 || client.searchCalls[0] != 0 {
		t.Fatalf("unexpected search calls: %#v", client.searchCalls)
	}
}

func TestMessageManagerDisablesLinkPreviewByDefault(t *testing.T) {
	telegram := &fakeTelegram{}
	manager := NewMessageManager(telegram)
	if _, err := manager.SendOrEdit(context.Background(), 10, 0, "text", nil); err != nil {
		t.Fatal(err)
	}
	if len(telegram.messages) != 1 {
		t.Fatalf("unexpected messages: %d", len(telegram.messages))
	}
	preview := telegram.messages[0].LinkPreviewOptions
	if preview == nil || preview.IsDisabled == nil || !*preview.IsDisabled {
		t.Fatalf("link preview was not disabled: %#v", preview)
	}
}

func TestStalePageCallbackIsIgnored(t *testing.T) {
	client := &fakeFlibusta{}
	app, _ := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "results"
	state.Query = "q"
	state.Page = 0
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandlePage(context.Background(), nil, callbackUpdate(1, 10, "page:next:5"))
	if len(client.searchCalls) != 0 {
		t.Fatalf("stale callback triggered search: %#v", client.searchCalls)
	}
	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Page != 0 {
		t.Fatalf("stale callback changed page: %d", saved.Page)
	}

	app.HandlePage(context.Background(), nil, callbackUpdate(1, 10, "page:next:0"))
	if len(client.searchCalls) != 1 || client.searchCalls[0] != 0 {
		t.Fatalf("unexpected search calls: %#v", client.searchCalls)
	}
	saved, _ = app.State.Get(context.Background(), 1)
	if saved.Page != 1 {
		t.Fatalf("page did not advance: %d", saved.Page)
	}
}

func TestBookCardUsesRealFormatFlags(t *testing.T) {
	client := &fakeFlibusta{}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "results"
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleBook(context.Background(), nil, callbackUpdate(1, 10, "book:1:1"))

	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != "book" || saved.BookID != 1 {
		t.Fatalf("unexpected state: %#v", saved)
	}
	if len(telegram.edits) == 0 {
		t.Fatal("no edit calls")
	}
	markup, ok := telegram.edits[len(telegram.edits)-1].ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("unexpected markup type: %T", telegram.edits[len(telegram.edits)-1].ReplyMarkup)
	}
	found := false
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if button.CallbackData == "dl:1" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("download button is missing")
	}
}

func TestRegisteredStartHandlerRunsSynchronously(t *testing.T) {
	app, telegram := newTestApp(t, &fakeFlibusta{})
	realBot, err := tgbot.New(
		"123:test",
		tgbot.WithSkipGetMe(),
		tgbot.WithNotAsyncHandlers(),
	)
	if err != nil {
		t.Fatal(err)
	}
	app.RegisterHandlers(realBot)
	realBot.ProcessUpdate(context.Background(), textUpdate(1, 10, "/start"))

	if len(telegram.messages) == 0 {
		t.Fatal("registered start handler did not run")
	}
	if !strings.Contains(telegram.messages[len(telegram.messages)-1].Text, "Состояние соединения") {
		t.Fatal("unexpected start response")
	}
}

func TestMessageManagerNotModifiedAndFallback(t *testing.T) {
	telegram := &fakeTelegram{editErr: errors.New("Bad Request: message is not modified")}
	manager := NewMessageManager(telegram)
	messageID, err := manager.SendOrEdit(context.Background(), 10, 5, "text", nil)
	if err != nil || messageID != 5 {
		t.Fatalf("unexpected result: %d %v", messageID, err)
	}
	if len(telegram.messages) != 0 {
		t.Fatal("not-modified edit caused a new message")
	}

	telegram.editErr = errors.New("Bad Request: message to edit not found")
	messageID, err = manager.SendOrEdit(context.Background(), 10, 5, "text", nil)
	if err != nil || messageID != 1 {
		t.Fatalf("unexpected fallback result: %d %v", messageID, err)
	}
	if len(telegram.messages) != 1 {
		t.Fatal("fallback message was not sent")
	}
}

func TestCommandRegexps(t *testing.T) {
	for _, value := range []string{"/start", "/start@knigojor_bot", "/start now"} {
		if !startCommand.MatchString(value) {
			t.Fatalf("start command did not match %q", value)
		}
	}
	if startCommand.MatchString("/starter") {
		t.Fatal("start command matched /starter")
	}
	for _, value := range []string{"/status", "/status@knigojor_bot"} {
		if !statusCommand.MatchString(value) {
			t.Fatalf("status command did not match %q", value)
		}
	}
}

func TestStartAndStatusHandlers(t *testing.T) {
	client := &fakeFlibusta{}
	app, telegram := newTestApp(t, client)
	app.Settings.TorEnabled = false

	app.HandleStart(context.Background(), nil, textUpdate(1, 10, "/start"))
	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != "main" || saved.ChatID != 10 || saved.MessageID == 0 {
		t.Fatalf("unexpected start state: %#v", saved)
	}
	if len(telegram.messages) == 0 || !strings.Contains(telegram.messages[len(telegram.messages)-1].Text, "Состояние соединения") {
		t.Fatal("main menu text was not rendered")
	}

	app.HandleStatus(context.Background(), nil, textUpdate(1, 10, "/status"))
	saved, _ = app.State.Get(context.Background(), 1)
	if saved.Screen != "status" {
		t.Fatalf("unexpected status state: %#v", saved)
	}
	last := telegram.edits[len(telegram.edits)-1]
	if !strings.Contains(last.Text, "FLIBUSTA / STATUS") || !strings.Contains(last.Text, "Версия: test") {
		t.Fatalf("unexpected status text: %s", last.Text)
	}
}

func TestNewSearchAndCancel(t *testing.T) {
	app, telegram := newTestApp(t, &fakeFlibusta{})
	app.HandleNewSearch(context.Background(), nil, callbackUpdate(1, 10, "new_search"))
	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != "awaiting_search" {
		t.Fatalf("unexpected new search state: %#v", saved)
	}
	if !strings.Contains(telegram.edits[len(telegram.edits)-1].Text, "Введите название") {
		t.Fatal("search prompt was not rendered")
	}

	app.HandleCancelSearch(context.Background(), nil, callbackUpdate(1, 10, "cancel_search"))
	saved, _ = app.State.Get(context.Background(), 1)
	if saved.Screen != "main" {
		t.Fatalf("unexpected cancel state: %#v", saved)
	}
	if !strings.Contains(telegram.edits[len(telegram.edits)-1].Text, "Отменено") {
		t.Fatal("cancel text was not rendered")
	}
}

func TestBackResults(t *testing.T) {
	client := &fakeFlibusta{}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "book"
	state.Query = "q"
	state.Page = 0
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleBackResults(context.Background(), nil, callbackUpdate(1, 10, "back_results"))

	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != "results" {
		t.Fatalf("unexpected state: %#v", saved)
	}
	if len(client.searchCalls) != 1 || client.searchCalls[0] != 0 {
		t.Fatalf("unexpected search calls: %#v", client.searchCalls)
	}
	if !strings.Contains(telegram.edits[len(telegram.edits)-1].Text, "Страница 1 из 113") {
		t.Fatal("results page was not rendered")
	}
}

func TestAuthMiddleware(t *testing.T) {
	app, telegram := newTestApp(t, &fakeFlibusta{})
	called := false
	middleware := app.AuthMiddleware(func(_ context.Context, _ *tgbot.Bot, _ *models.Update) {
		called = true
	})

	middleware(context.Background(), nil, callbackUpdate(2, 10, "status"))
	if called {
		t.Fatal("unauthorized user reached the handler")
	}
	if len(telegram.answers) != 1 || !telegram.answers[0].ShowAlert {
		t.Fatalf("unauthorized callback was not answered: %#v", telegram.answers)
	}

	middleware(context.Background(), nil, callbackUpdate(1, 10, "status"))
	if !called {
		t.Fatal("authorized user was rejected")
	}
}

func TestDownloadHandlerSuccess(t *testing.T) {
	client := &fakeFlibusta{}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "book"
	state.BookID = 1
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleDownload(context.Background(), nil, callbackUpdate(1, 10, "dl:1"))

	if len(telegram.documents) != 1 {
		t.Fatalf("expected one document, got %d", len(telegram.documents))
	}
	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != "book" || saved.BookID != 1 {
		t.Fatalf("unexpected state: %#v", saved)
	}
	if len(telegram.edits) == 0 {
		t.Fatal("no status edits")
	}
	if !strings.Contains(telegram.edits[0].Text, "Подготовка книги") {
		t.Fatalf("download loading indicator is missing: %s", telegram.edits[0].Text)
	}
	last := telegram.edits[len(telegram.edits)-1]
	if !strings.Contains(last.Text, "Книга отправлена") {
		t.Fatalf("unexpected final status: %s", last.Text)
	}
}
