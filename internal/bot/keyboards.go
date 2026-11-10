package bot

import (
	"fmt"

	"github.com/go-telegram/bot/models"
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

// SearchPromptKB is the "waiting for query" keyboard.
func SearchPromptKB() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "❌ Отмена", CallbackData: "cancel_search"}},
	}}
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
	rows := make([][]models.InlineKeyboardButton, 0, 2)
	if hasEPUB || hasFB2 {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         "📥 Скачать EPUB",
			CallbackData: fmt.Sprintf("dl:%d", bookID),
		}})
	}
	rows = append(rows, []models.InlineKeyboardButton{
		{Text: "⬅️ К результатам", CallbackData: "back_results"},
		{Text: "🏠 Меню", CallbackData: "main_menu"},
	})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}
