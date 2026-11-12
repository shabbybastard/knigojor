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
	sendErr   error
}

func (f *fakeTelegram) SendMessage(_ context.Context, params *tgbot.SendMessageParams) (*models.Message, error) {
	if f.sendErr != nil {
		return nil, f.sendErr
	}
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
	searchErr   error
	bookErr     error
	authorErr   error
}

func (f *fakeFlibusta) Search(_ context.Context, _ string, page int) (*flibusta.SearchResults, error) {
	f.searchCalls = append(f.searchCalls, page)
	if f.searchErr != nil {
		err := f.searchErr
		f.searchErr = nil
		return nil, err
	}
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
	if f.bookErr != nil {
		err := f.bookErr
		f.bookErr = nil
		return nil, err
	}
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
	if f.authorErr != nil {
		err := f.authorErr
		f.authorErr = nil
		return nil, err
	}
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
	tempPath := filepath.Join(dir, "temp")
	if err := os.MkdirAll(tempPath, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := &config.Settings{
		AllowedUserID:        1,
		BaseDir:              dir,
		PageSize:             5,
		DownloadTimeout:      10,
		DownloadAttempts:     3,
		MaxFileSizeMB:        5,
		TempPath:             tempPath,
		ContentBase:          "https://flisland.test",
		DownloadBase:         "https://flibusta.test",
		MaxParallelDownloads: 1,
	}
	telegram := &fakeTelegram{}
	app := &App{
		Settings:     settings,
		State:        store.NewStateStore(),
		Client:       client,
		Messages:     NewMessageManager(telegram),
		TG:           telegram,
		DownloadSem:  make(chan struct{}, 1),
		StartTime:    time.Now(),
		Version:      "test",
		ValidateEPUB: func(string) bool { return true },
	}
	app.DownloadEPUB = func(_ context.Context, _ *netx.Client, _, dest string, _ int, _ time.Duration, _ netx.ProgressFunc) (string, error) {
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(dest, []byte("epub"), 0o600); err != nil {
			return "", err
		}
		return dest, nil
	}
	app.DownloadFB2 = func(_ context.Context, _ *netx.Client, _, destDir string, _ int, _ time.Duration, _ netx.ProgressFunc) (string, error) {
		path := filepath.Join(destDir, "book.fb2")
		if err := os.MkdirAll(destDir, 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte("<FictionBook/>"), 0o600); err != nil {
			return "", err
		}
		return path, nil
	}
	app.Convert = func(_, dest string, _ func(string)) error {
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
	app.DownloadEPUB = func(_ context.Context, _ *netx.Client, _, _ string, _ int, _ time.Duration, _ netx.ProgressFunc) (string, error) {
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
	app.DownloadEPUB = func(_ context.Context, _ *netx.Client, _, _ string, _ int, _ time.Duration, _ netx.ProgressFunc) (string, error) {
		return "", errors.New("epub failed")
	}
	app.DownloadFB2 = func(_ context.Context, _ *netx.Client, _, _ string, _ int, _ time.Duration, _ netx.ProgressFunc) (string, error) {
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
	if !strings.Contains(telegram.messages[len(telegram.messages)-1].Text, "Каталог:") {
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

	app.HandleStart(context.Background(), nil, textUpdate(1, 10, "/start"))
	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != "main" || saved.ChatID != 10 || saved.MessageID == 0 {
		t.Fatalf("unexpected start state: %#v", saved)
	}
	if len(telegram.messages) == 0 || !strings.Contains(telegram.messages[len(telegram.messages)-1].Text, "Каталог:") {
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
	if len(telegram.messages) == 0 {
		t.Fatal("final UI message was not sent")
	}
	last := telegram.messages[len(telegram.messages)-1]
	if !strings.Contains(last.Text, "Книга отправлена") {
		t.Fatalf("unexpected final status: %s", last.Text)
	}
	if len(telegram.deletes) == 0 || telegram.deletes[0].MessageID != 5 {
		t.Fatalf("previous UI message was not deleted: %#v", telegram.deletes)
	}
}

func TestDownloadRetriesEPUBThreeTimes(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{
		Title:   "Book title",
		HasEPUB: true,
		HasFB2:  true,
	}}
	app, telegram := newTestApp(t, client)
	app.Settings.DownloadAttempts = 5
	epubCalls := 0
	app.DownloadEPUB = func(_ context.Context, _ *netx.Client, _, _ string, _ int, _ time.Duration, _ netx.ProgressFunc) (string, error) {
		epubCalls++
		return "", errors.New("epub failed")
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

	if epubCalls != 3 {
		t.Fatalf("expected 3 EPUB attempts, got %d", epubCalls)
	}
	if len(telegram.documents) != 1 {
		t.Fatalf("expected one FB2-derived document, got %d", len(telegram.documents))
	}
}

func TestDownloadSendsFB2WhenConversionFails(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{
		Title:  "Book title",
		HasFB2: true,
	}}
	app, telegram := newTestApp(t, client)
	app.Convert = func(_, _ string, _ func(string)) error { return errors.New("conversion failed") }
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
		t.Fatalf("expected FB2 fallback document, got %d", len(telegram.documents))
	}
	if len(telegram.messages) == 0 {
		t.Fatal("fallback UI message was not sent")
	}
	last := telegram.messages[len(telegram.messages)-1]
	if !strings.Contains(last.Text, "Исходный FB2 отправлен") {
		t.Fatalf("unexpected fallback status: %s", last.Text)
	}
	markup, ok := last.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || !hasCallback(markup, "retry:download") {
		t.Fatalf("retry button is missing: %#v", last.ReplyMarkup)
	}
}

func TestDownloadFailureOffersRetry(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{
		Title:   "Book title",
		HasEPUB: true,
		HasFB2:  true,
	}}
	app, telegram := newTestApp(t, client)
	app.DownloadEPUB = func(_ context.Context, _ *netx.Client, _, _ string, _ int, _ time.Duration, _ netx.ProgressFunc) (string, error) {
		return "", errors.New("epub failed")
	}
	app.DownloadFB2 = func(_ context.Context, _ *netx.Client, _, _ string, _ int, _ time.Duration, _ netx.ProgressFunc) (string, error) {
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

	last := telegram.edits[len(telegram.edits)-1]
	markup, ok := last.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || !hasCallback(markup, "retry:download") {
		t.Fatalf("retry button is missing: %#v", last.ReplyMarkup)
	}
}

func TestRetrySearchAfterError(t *testing.T) {
	client := &fakeFlibusta{searchErr: errors.New("temporary failure")}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "awaiting_search"
	state.Query = "Пушкин"
	state.Page = 0
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleText(context.Background(), nil, textUpdate(1, 10, "Пушкин"))
	last := telegram.edits[len(telegram.edits)-1]
	markup, ok := last.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || !hasCallback(markup, "retry:search") {
		t.Fatalf("retry button is missing: %#v", last.ReplyMarkup)
	}

	app.HandleRetry(context.Background(), nil, callbackUpdate(1, 10, "retry:search"))
	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != "results" || saved.Query != "Пушкин" {
		t.Fatalf("retry did not restore search: %#v", saved)
	}
	if len(client.searchCalls) != 2 {
		t.Fatalf("unexpected search calls: %#v", client.searchCalls)
	}
}

func hasCallback(markup *models.InlineKeyboardMarkup, data string) bool {
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if button.CallbackData == data {
				return true
			}
		}
	}
	return false
}

func TestRetryBookAfterError(t *testing.T) {
	client := &fakeFlibusta{bookErr: errors.New("temporary failure")}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "results"
	state.Query = "query"
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleBook(context.Background(), nil, callbackUpdate(1, 10, "book:1:1"))
	last := telegram.edits[len(telegram.edits)-1]
	markup, ok := last.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || !hasCallback(markup, "retry:book") {
		t.Fatalf("retry button is missing: %#v", last.ReplyMarkup)
	}

	app.HandleRetry(context.Background(), nil, callbackUpdate(1, 10, "retry:book"))
	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != "book" || saved.BookID != 1 {
		t.Fatalf("retry did not open the book: %#v", saved)
	}
	if len(client.bookCalls) != 2 {
		t.Fatalf("unexpected book calls: %#v", client.bookCalls)
	}
}

func TestRetryAuthorAfterError(t *testing.T) {
	client := &fakeFlibusta{authorErr: errors.New("temporary failure")}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = screenAuthors
	state.Query = "author"
	state.Extra = map[string]any{"author_id": int64(7171)}
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleAuthor(context.Background(), nil, callbackUpdate(1, 10, "author:7171"))
	last := telegram.edits[len(telegram.edits)-1]
	markup, ok := last.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || !hasCallback(markup, "retry:author") {
		t.Fatalf("retry button is missing: %#v", last.ReplyMarkup)
	}

	app.HandleRetry(context.Background(), nil, callbackUpdate(1, 10, "retry:author"))
	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != screenAuthorResults || saved.Operation != "author" {
		t.Fatalf("retry did not load the author: %#v", saved)
	}
	if len(client.authorCalls) != 2 {
		t.Fatalf("unexpected author calls: %#v", client.authorCalls)
	}
}

func TestDownloadUsesFlibustaBase(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{Title: "Book", HasEPUB: true}}
	app, _ := newTestApp(t, client)
	var gotURL string
	app.DownloadEPUB = func(_ context.Context, _ *netx.Client, rawURL, dest string, _ int, _ time.Duration, _ netx.ProgressFunc) (string, error) {
		gotURL = rawURL
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(dest, []byte("epub"), 0o600); err != nil {
			return "", err
		}
		return dest, nil
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
	if gotURL != "https://flibusta.test/b/1/epub" {
		t.Fatalf("unexpected download URL: %s", gotURL)
	}
}

func TestRetryAuthorsPageAfterError(t *testing.T) {
	authors := make([]flibusta.AuthorResult, 0, 12)
	for id := int64(1); id <= 12; id++ {
		authors = append(authors, flibusta.AuthorResult{ID: id, Name: "Author", Details: "Author"})
	}
	client := &fakeFlibusta{authors: authors, searchErr: errors.New("temporary failure")}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = screenAuthors
	state.Query = "Author"
	state.Page = 0
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleAuthorPage(context.Background(), nil, callbackUpdate(1, 10, "apage:next:0"))
	last := telegram.edits[len(telegram.edits)-1]
	markup, ok := last.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || !hasCallback(markup, "retry:authors") {
		t.Fatalf("retry authors button is missing: %#v", last.ReplyMarkup)
	}

	app.HandleRetry(context.Background(), nil, callbackUpdate(1, 10, "retry:authors"))
	saved, _ := app.State.Get(context.Background(), 1)
	if saved.Screen != screenAuthors || saved.Page != 1 {
		t.Fatalf("retry did not restore author page: %#v", saved)
	}
	if !strings.Contains(telegram.edits[len(telegram.edits)-1].Text, "Страница 2 из 2") {
		t.Fatalf("unexpected author page: %s", telegram.edits[len(telegram.edits)-1].Text)
	}
}

func TestDownloadSendsFB2WhenConvertedEPUBIsInvalid(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{
		Title:  "Book title",
		HasFB2: true,
	}}
	app, telegram := newTestApp(t, client)
	app.ValidateEPUB = func(string) bool { return false }
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
		t.Fatalf("expected FB2 fallback document, got %d", len(telegram.documents))
	}
	if len(telegram.messages) == 0 {
		t.Fatal("fallback UI message was not sent")
	}
	last := telegram.messages[len(telegram.messages)-1]
	if !strings.Contains(last.Text, "Исходный FB2 отправлен") {
		t.Fatalf("unexpected fallback status: %s", last.Text)
	}
}

func TestCleanTempOnlyRemovesOwnedDirs(t *testing.T) {
	dir := t.TempDir()
	owned := filepath.Join(dir, "123")
	foreign := filepath.Join(dir, "456")
	if err := os.MkdirAll(owned, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owned, tempMarker), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "book.fb2.zip"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{Settings: &config.Settings{TempPath: dir}}
	app.cleanTemp()

	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatal("owned temp directory was not removed")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("foreign numeric directory was removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "book.fb2.zip")); err != nil {
		t.Fatal("foreign file was removed")
	}
}

func TestRetryDownloadAfterFailure(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{Title: "Book", HasEPUB: true}}
	app, telegram := newTestApp(t, client)
	calls := 0
	app.DownloadEPUB = func(_ context.Context, _ *netx.Client, _, dest string, _ int, _ time.Duration, _ netx.ProgressFunc) (string, error) {
		calls++
		if calls <= 3 {
			return "", errors.New("epub failed")
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(dest, []byte("epub"), 0o600); err != nil {
			return "", err
		}
		return dest, nil
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
	app.HandleRetry(context.Background(), nil, callbackUpdate(1, 10, "retry:download"))

	if calls != 4 {
		t.Fatalf("expected retry to download again, calls=%d", calls)
	}
	if len(telegram.documents) != 1 {
		t.Fatalf("expected one document after retry, got %d", len(telegram.documents))
	}
}

func TestMessageManagerReplaceDoesNotDeleteOnSendFailure(t *testing.T) {
	telegram := &fakeTelegram{sendErr: errors.New("send failed")}
	manager := NewMessageManager(telegram)
	if _, err := manager.Replace(context.Background(), 10, 5, "text", nil); err == nil {
		t.Fatal("expected send error")
	}
	if len(telegram.deletes) != 0 {
		t.Fatalf("old message was deleted after a failed send: %#v", telegram.deletes)
	}
}

func TestDownloadShowsProgress(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{Title: "Book", HasEPUB: true}}
	app, telegram := newTestApp(t, client)
	app.DownloadEPUB = func(_ context.Context, _ *netx.Client, _, dest string, _ int, _ time.Duration, progress netx.ProgressFunc) (string, error) {
		progress(10, 10)
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(dest, []byte("epub"), 0o600); err != nil {
			return "", err
		}
		return dest, nil
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

	found := false
	for _, edit := range telegram.edits {
		if strings.Contains(edit.Text, "100%") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("download progress was not shown")
	}
}

func TestBookKBKeepsEPUBPrimaryAndAddsFB2(t *testing.T) {
	keyboard := BookKB(1, true, true)
	if len(keyboard.InlineKeyboard) < 2 {
		t.Fatalf("unexpected keyboard: %#v", keyboard)
	}
	if keyboard.InlineKeyboard[0][0].CallbackData != "dl:1" {
		t.Fatalf("EPUB is not primary: %#v", keyboard.InlineKeyboard)
	}
	if keyboard.InlineKeyboard[1][0].CallbackData != "dl_fb2:1" {
		t.Fatalf("FB2 button is missing: %#v", keyboard.InlineKeyboard)
	}
	if strings.Contains(keyboard.InlineKeyboard[1][0].Text, "📥") {
		t.Fatalf("FB2 button should be secondary: %s", keyboard.InlineKeyboard[1][0].Text)
	}
}

func TestBookKBOnlyFB2IsPrimary(t *testing.T) {
	keyboard := BookKB(1, false, true)
	if keyboard.InlineKeyboard[0][0].CallbackData != "dl_fb2:1" ||
		!strings.Contains(keyboard.InlineKeyboard[0][0].Text, "Скачать FB2") {
		t.Fatalf("unexpected keyboard: %#v", keyboard)
	}
}

func TestDownloadFB2SendsSourceFileWithoutConversion(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{
		Title:   "Book title",
		HasEPUB: true,
		HasFB2:  true,
	}}
	app, telegram := newTestApp(t, client)
	converted := false
	app.Convert = func(_, _ string, _ func(string)) error {
		converted = true
		return nil
	}
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "book"
	state.BookID = 1
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleDownloadFB2(context.Background(), nil, callbackUpdate(1, 10, "dl_fb2:1"))

	if converted {
		t.Fatal("FB2 download must not run the converter")
	}
	if len(telegram.documents) != 1 {
		t.Fatalf("expected one document, got %d", len(telegram.documents))
	}
	if len(telegram.messages) == 0 || !strings.Contains(telegram.messages[len(telegram.messages)-1].Text, "FB2 отправлен") {
		t.Fatalf("unexpected final message: %#v", telegram.messages)
	}
}

func TestRetryDownloadFB2(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{Title: "Book", HasFB2: true}}
	app, telegram := newTestApp(t, client)
	calls := 0
	app.DownloadFB2 = func(_ context.Context, _ *netx.Client, _, destDir string, _ int, _ time.Duration, _ netx.ProgressFunc) (string, error) {
		calls++
		if calls <= 3 {
			return "", errors.New("fb2 failed")
		}
		path := filepath.Join(destDir, "book.fb2")
		if err := os.MkdirAll(destDir, 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte("<FictionBook/>"), 0o600); err != nil {
			return "", err
		}
		return path, nil
	}
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "book"
	state.BookID = 1
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleDownloadFB2(context.Background(), nil, callbackUpdate(1, 10, "dl_fb2:1"))
	app.HandleRetry(context.Background(), nil, callbackUpdate(1, 10, "retry:download_fb2"))

	if calls != 4 {
		t.Fatalf("unexpected FB2 attempts: %d", calls)
	}
	if len(telegram.documents) != 1 {
		t.Fatalf("expected one document after retry, got %d", len(telegram.documents))
	}
}

func TestBookKBWithoutFormatsHasNoDownloadButtons(t *testing.T) {
	keyboard := BookKB(1, false, false)
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			if strings.HasPrefix(button.CallbackData, "dl") {
				t.Fatalf("unexpected download button: %#v", button)
			}
		}
	}
}

func TestDownloadFB2UsesSourceFilename(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{Title: "Название", HasFB2: true}}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "book"
	state.BookID = 1
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleDownloadFB2(context.Background(), nil, callbackUpdate(1, 10, "dl_fb2:1"))

	if len(telegram.documents) != 1 {
		t.Fatalf("expected one document, got %d", len(telegram.documents))
	}
	upload, ok := telegram.documents[0].Document.(*models.InputFileUpload)
	if !ok || upload.Filename != "Название.fb2" {
		t.Fatalf("unexpected filename: %#v", telegram.documents[0].Document)
	}
}

func TestDownloadFB2UnavailableOffersFB2Retry(t *testing.T) {
	client := &fakeFlibusta{book: &flibusta.BookInfo{Title: "Book", HasEPUB: true}}
	app, telegram := newTestApp(t, client)
	state := store.NewUIState(1)
	state.ChatID = 10
	state.MessageID = 5
	state.Screen = "book"
	state.BookID = 1
	if err := app.State.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	app.HandleDownloadFB2(context.Background(), nil, callbackUpdate(1, 10, "dl_fb2:1"))

	last := telegram.edits[len(telegram.edits)-1]
	markup, ok := last.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || !hasCallback(markup, "retry:download_fb2") {
		t.Fatalf("unexpected retry keyboard: %#v", last.ReplyMarkup)
	}
}
