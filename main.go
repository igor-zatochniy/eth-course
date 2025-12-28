package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

var db *sql.DB
var kyivLoc = time.FixedZone("Kyiv", 2*60*60)

// --- Клавіатури ---

var refreshKeyboard = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Оновити всі курси", "refresh_price"),
	),
)

var intervalKeyboard = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("1 год", "int_1"),
		tgbotapi.NewInlineKeyboardButtonData("3 год", "int_3"),
		tgbotapi.NewInlineKeyboardButtonData("6 год", "int_6"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("12 год", "int_12"),
		tgbotapi.NewInlineKeyboardButtonData("24 год", "int_24"),
	),
)

type BinancePrice struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

// Функція отримує курс, порівнює з минулим і повертає рядок з індикатором (трендом)
func getPriceWithTrend(pair string, label string) string {
	url := fmt.Sprintf("https://api.binance.com/api/v3/ticker/price?symbol=%s", pair)
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Sprintf("⚪️ %s: помилка API", label)
	}
	defer resp.Body.Close()

	var data BinancePrice
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Sprintf("⚪️ %s: помилка даних", label)
	}
	currentPrice, _ := strconv.ParseFloat(data.Price, 64)

	var lastPrice float64
	// Отримуємо попередню ціну з бази даних
	err = db.QueryRow("SELECT price FROM market_prices WHERE symbol = $1", pair).Scan(&lastPrice)

	emoji := "⚪️"
	trend := "0.00%"

	if err == nil && lastPrice > 0 {
		diff := ((currentPrice - lastPrice) / lastPrice) * 100
		if diff > 0.01 {
			emoji = "🟢"
			trend = fmt.Sprintf("+%.2f%%", diff)
		} else if diff < -0.01 {
			emoji = "🔴"
			trend = fmt.Sprintf("%.2f%%", diff)
		}
	}

	// Оновлюємо ціну в базі для майбутнього порівняння
	db.Exec(`INSERT INTO market_prices (symbol, price) VALUES ($1, $2) 
	         ON CONFLICT (symbol) DO UPDATE SET price = EXCLUDED.price`, pair, currentPrice)

	if pair == "USDTUAH" {
		return fmt.Sprintf("%s %s: *%.2f UAH* (%s)", emoji, label, currentPrice, trend)
	}
	return fmt.Sprintf("%s %s: *$%.2f* (%s)", emoji, label, currentPrice, trend)
}

func initDB() {
	var err error
	connStr := os.Getenv("DATABASE_URL")
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal("Критична помилка БД:", err)
	}

	// Створюємо необхідні таблиці
	db.Exec(`CREATE TABLE IF NOT EXISTS subscribers (
		chat_id BIGINT PRIMARY KEY, 
		interval_hours INT DEFAULT 1, 
		last_sent TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);`)

	db.Exec(`CREATE TABLE IF NOT EXISTS market_prices (
		symbol TEXT PRIMARY KEY, 
		price DOUBLE PRECISION
	);`)

	log.Println("✅ Структура бази даних підготовлена.")
}

func startPriceAlerts(bot *tgbotapi.BotAPI) {
	ticker := time.NewTicker(1 * time.Hour) // Перевірка бази щогодини
	for range ticker.C {
		rows, err := db.Query(`SELECT chat_id FROM subscribers WHERE last_sent <= NOW() - (interval_hours * INTERVAL '1 hour')`)
		if err != nil {
			log.Println("Помилка розсилки:", err)
			continue
		}

		btc := getPriceWithTrend("BTCUSDT", "BTC")
		eth := getPriceWithTrend("ETHUSDT", "ETH")
		usdt := getPriceWithTrend("USDTUAH", "USDT")
		currentTime := time.Now().In(kyivLoc).Format("15:04")
		
		text := fmt.Sprintf("🕒 *Планове оновлення (%s)*\n\n%s\n%s\n%s\n\n_Динаміка порівняно з минулим запитом_", currentTime, btc, eth, usdt)

		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err == nil {
				msg := tgbotapi.NewMessage(id, text)
				msg.ParseMode = "Markdown"
				msg.ReplyMarkup = refreshKeyboard
				bot.Send(msg)
				db.Exec("UPDATE subscribers SET last_sent = NOW() WHERE chat_id = $1", id)
			}
		}
		rows.Close()
	}
}

func main() {
	_ = godotenv.Load()
	initDB()

	bot, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_APITOKEN"))
	if err != nil {
		log.Panic("Помилка авторизації в Telegram:", err)
	}

	// Налаштування меню команд (кнопка "Меню")
	commands := []tgbotapi.BotCommand{
		{Command: "start", Description: "Вітання та функції"},
		{Command: "price", Description: "Актуальні курси"},
		{Command: "interval", Description: "Налаштувати частоту"},
		{Command: "subscribe", Description: "Підписатися"},
		{Command: "unsubscribe", Description: "Відписатися"},
	}
	bot.Request(tgbotapi.NewSetMyCommands(commands...))

	go startPriceAlerts(bot)

	// --- ВЕБ-СЕРВЕР ДЛЯ ЗАПОБІГАННЯ 404 ТА SLEEP ---
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "✅ Бот прокинувся і працює!\n")
		fmt.Fprintf(w, "Поточний час за Києвом: %s", time.Now().In(kyivLoc).Format("15:04:05"))
	})

	go func() {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8000"
		}
		log.Printf("🌍 Веб-сервер запущено на порту %s", port)
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Printf("❌ Помилка сервера: %v", err)
		}
	}()
	// ----------------------------------------------

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		// Обробка кнопок (Callback Queries)
		if update.CallbackQuery != nil {
			data := update.CallbackQuery.Data
			chatID := update.CallbackQuery.Message.Chat.ID

			if len(data) > 4 && data[:4] == "int_" {
				hours, _ := strconv.Atoi(data[4:])
				db.Exec("UPDATE subscribers SET interval_hours = $1, last_sent = NOW() WHERE chat_id = $2", hours, chatID)
				bot.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, "Налаштування змінено!"))
				bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Готово! Буду надсилати курс кожні %d год.", hours)))
			}

			if data == "refresh_price" {
				btc := getPriceWithTrend("BTCUSDT", "BTC")
				eth := getPriceWithTrend("ETHUSDT", "ETH")
				usdt := getPriceWithTrend("USDTUAH", "USDT")
				t := time.Now().In(kyivLoc).Format("15:04:05")
				
				newText := fmt.Sprintf("🕒 *Оновлено о %s (Київ)*\n\n%s\n%s\n%s\n\n_Динаміка зафіксована_", t, btc, eth, usdt)
				
				// Виправлено: MessageID для коректного редагування
				edit := tgbotapi.NewEditMessageText(chatID, update.CallbackQuery.Message.MessageID, newText)
				edit.ParseMode = "Markdown"
				edit.ReplyMarkup = &refreshKeyboard
				bot.Send(edit)
				bot.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, "Оновлено!"))
			}
			continue
		}

		if update.Message == nil {
			continue
		}
		chatID := update.Message.Chat.ID

		switch update.Message.Command() {
		case "start":
			welcomeText := "Вітаю! 🖖 Твій крипто-асистент уже на зв’язку! ⚡️\n\n" +
				"Хочеш тримати руку на пульсі ринку? Я допоможу!\n\n" +
				"🔹 *Live-курси:* BTC, ETH, USDT за лічені секунди.\n" +
				"🔹 *Smart-сповіщення:* Сам обирай, як часто отримувати апдейти (1–24 год).\n" +
				"🔹 *UAH-маркет:* Слідкуй за реальним курсом USDT до гривні.\n" +
				"🔹 *Stability:* Стабільна робота та збереження твоїх пресетів.\n\n" +
				"🔥 Не гай часу! Тисни **/subscribe** та отримуй профіт від актуальної інформації!"
			
			msg := tgbotapi.NewMessage(chatID, welcomeText)
			msg.ParseMode = "Markdown"
			bot.Send(msg)

		case "subscribe":
			db.Exec("INSERT INTO subscribers (chat_id, interval_hours, last_sent) VALUES ($1, 1, NOW()) ON CONFLICT (chat_id) DO UPDATE SET last_sent = NOW()", chatID)
			bot.Send(tgbotapi.NewMessage(chatID, "✅ Підписка активована! Частота за замовчуванням — 1 год. Змінити: /interval"))

		case "unsubscribe":
			db.Exec("DELETE FROM subscribers WHERE chat_id = $1", chatID)
			bot.Send(tgbotapi.NewMessage(chatID, "❌ Ви відписалися від розсилки."))

		case "interval":
			msg := tgbotapi.NewMessage(chatID, "⚙️ *Оберіть частоту автоматичних повідомлень:*")
			msg.ParseMode = "Markdown"
			msg.ReplyMarkup = intervalKeyboard
			bot.Send(msg)

		case "price":
			btc := getPriceWithTrend("BTCUSDT", "BTC")
			eth := getPriceWithTrend("ETHUSDT", "ETH")
			usdt := getPriceWithTrend("USDTUAH", "USDT")
			text := fmt.Sprintf("💰 *Актуальні курси:*\n\n%s\n%s\n%s", btc, eth, usdt)
			msg := tgbotapi.NewMessage(chatID, text)
			msg.ParseMode = "Markdown"
			msg.ReplyMarkup = refreshKeyboard
			bot.Send(msg)
		}
	}
}
