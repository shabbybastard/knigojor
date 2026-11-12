package bot

import (
	"fmt"

	"github.com/go-telegram/bot/models"

	"github.com/shabbybastard/knigojor/internal/flibusta"
)

// MainMenuKB is the main screen keyboard.
func MainMenuKB() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "🔍 Новый поиск", CallbackData: "new_search"}},
		{
			{Text: "🔄 Обновить", CallbackData: "refresh"},
			{Text: "📊 Статус", CallbackData: "status"},
		},
	}}
}

// RetryKB is shown after a failed operation and repeats it without losing
// the current search context.
func RetryKB(action string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "🔄 Повторить", CallbackData: "retry:" + action}},
		{{Text: "🏠 Меню", CallbackData: "main_menu"}},
	}}
}

// DownloadErrorKB offers a retry without walking through search again.
func DownloadErrorKB(bookID int64, retryAction string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "🔄 Повторить скачивание", CallbackData: "retry:" + retryAction}},
		{
			{Text: "📖 К книге", CallbackData: fmt.Sprintf("book:%d", bookID)},
			{Text: "🏠 Меню", CallbackData: "main_menu"},
		},
	}}
}

// SearchPromptKB is the "waiting for query" keyboard.
func SearchPromptKB() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "❌ Отмена", CallbackData: "cancel_search"}},
	}}
}

// AuthorsKB builds the author selection keyboard.
func AuthorsKB(
	authors []flibusta.AuthorResult,
	totalBooks int,
	page int,
	totalPages int,
) *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, 5)
	buttons := make([]models.InlineKeyboardButton, 0, 5)
	for index, author := range authors {
		number := page*authorsPageSize + index + 1
		buttons = append(buttons, models.InlineKeyboardButton{
			Text:         fmt.Sprintf("%d", number),
			CallbackData: fmt.Sprintf("author:%d", author.ID),
		})
		if len(buttons) == 5 {
			rows = append(rows, buttons)
			buttons = make([]models.InlineKeyboardButton, 0, 5)
		}
	}
	if len(buttons) > 0 {
		rows = append(rows, buttons)
	}
	navigation := make([]models.InlineKeyboardButton, 0, 2)
	if page > 0 {
		navigation = append(navigation, models.InlineKeyboardButton{
			Text:         "⬅️ Пред.",
			CallbackData: fmt.Sprintf("apage:prev:%d", page),
		})
	}
	if page < totalPages-1 {
		navigation = append(navigation, models.InlineKeyboardButton{
			Text:         "➡️ След.",
			CallbackData: fmt.Sprintf("apage:next:%d", page),
		})
	}
	if len(navigation) > 0 {
		rows = append(rows, navigation)
	}
	if totalBooks > 0 {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         fmt.Sprintf("📚 Книги по названию (%d)", totalBooks),
			CallbackData: "search_books",
		}})
	}
	rows = append(rows, []models.InlineKeyboardButton{
		{Text: "🔍 Новый поиск", CallbackData: "new_search"},
		{Text: "🏠 Меню", CallbackData: "main_menu"},
	})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// ResultsKB builds the search results keyboard.
func ResultsKB(page, totalPages int, bookIDs []int64) *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0)
	buttons := make([]models.InlineKeyboardButton, 0, 5)
	for index, bookID := range bookIDs {
		buttons = append(buttons, models.InlineKeyboardButton{
			Text:         fmt.Sprintf("%d", index+1),
			CallbackData: fmt.Sprintf("book:%d:%d", bookID, index+1),
		})
		if len(buttons) == 5 {
			rows = append(rows, buttons)
			buttons = make([]models.InlineKeyboardButton, 0, 5)
		}
	}
	if len(buttons) > 0 {
		rows = append(rows, buttons)
	}

	navigation := make([]models.InlineKeyboardButton, 0, 2)
	if page > 0 {
		navigation = append(navigation, models.InlineKeyboardButton{
			Text:         "⬅️ Пред.",
			CallbackData: fmt.Sprintf("page:prev:%d", page),
		})
	}
	if page < totalPages-1 {
		navigation = append(navigation, models.InlineKeyboardButton{
			Text:         "➡️ След.",
			CallbackData: fmt.Sprintf("page:next:%d", page),
		})
	}
	if len(navigation) > 0 {
		rows = append(rows, navigation)
	}
	rows = append(rows, []models.InlineKeyboardButton{
		{Text: "🔍 Новый поиск", CallbackData: "new_search"},
		{Text: "🏠 Меню", CallbackData: "main_menu"},
	})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// BookKB builds the book card keyboard.
func BookKB(bookID int64, hasEPUB, hasFB2 bool) *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, 3)
	if hasEPUB {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         "📥 Скачать EPUB",
			CallbackData: fmt.Sprintf("dl:%d", bookID),
		}})
	}
	if hasFB2 {
		fb2Label := "Скачать FB2"
		if !hasEPUB {
			fb2Label = "📥 Скачать FB2"
		}
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         fb2Label,
			CallbackData: fmt.Sprintf("dl_fb2:%d", bookID),
		}})
	}
	rows = append(rows, []models.InlineKeyboardButton{
		{Text: "⬅️ К результатам", CallbackData: "back_results"},
		{Text: "🏠 Меню", CallbackData: "main_menu"},
	})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}
