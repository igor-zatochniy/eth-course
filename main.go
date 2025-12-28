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

// Фіксуємо київський час (UTC+2)
var kyivLoc = time.FixedZone("Kyiv", 2*60*60)

// --- Клавіатури ---

// Кнопка для швидкого оновлення під повідомленням
var refreshKeyboard = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Оновити всі курси", "refresh_price"),
	),
)

// Кнопки вибору інтервалу розсилки
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

// Функція отримання курсу з Binance та округлення до 2 знаків
func getPrice(pair string) (string, error) {
	url := fmt.Sprintf("https://api.binance.com/api/v3/ticker/price?symbol=%s", pair)
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var data BinancePrice
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}

	priceFloat, err := strconv.ParseFloat(data.Price, 64)
	if err != nil {
		return data.Price, nil
	}
	return fmt.Sprintf("%.2f", priceFloat), nil
}

// Ініціалізація та автоматичне оновлення структури БД
func initDB() {
	var err error
	connStr := os.Getenv("DATABASE_URL")
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal("Критична помилка підключення до БД:", err)
	}

	// Створюємо базову таблицю, якщо її немає
	db.Exec(`CREATE TABLE IF NOT EXISTS subscribers (chat_id BIGINT PRIMARY KEY);`)

	// Додаємо колонки для інтервалів, якщо вони відсутні
	db.Exec(`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS interval_hours INT DEFAULT 1;`)
	db.Exec(`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS last_sent TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP;`)

	log.Println("✅ База даних готова та структуру оновлено.")
}

// Фоновий процес інтелектуальної розсилки
func startPriceAlerts(bot *tgbotapi.BotAPI) {
	ticker := time.NewTicker(1 * time.Hour) // Перевірка бази щогодини
	for range ticker.C {
		// Вибираємо користувачів, яким настав час отримувати курс
		rows, err := db.Query(`
			SELECT chat_id, interval_hours FROM subscribers 
			WHERE last_sent <= NOW() - (interval_hours * INTERVAL '1 hour')
		`)
		if err != nil {
			log.Println("Помилка під час вибірки для розсилки:", err)
			continue
		}

		btc, _ := getPrice("BTCUSDT")
		eth, _ := getPrice("ETHUSDT")
		usdt, _ := getPrice("USDTUAH")
		currentTime := time.Now().In(kyivLoc).Format("15:04")

		text := fmt.Sprintf("🕒 *Планове оновлення (%s за Києвом)*\n\n🟠 BTC: *$%s*\n🔹 ETH: *$%s*\n💵 USDT: *%s UAH*", currentTime, btc, eth, usdt)

		for rows.Next() {
			var id int64
			var interval int
			if err := rows.Scan(&id, &interval); err == nil {
				msg := tgbotapi.NewMessage(id, text)
				msg.ParseMode = "Markdown"
				msg.ReplyMarkup = refreshKeyboard
				bot.Send(msg)
				// Оновлюємо мітку часу останньої відправки
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
		log.Panic("Помилка авторизації бота:", err)
	}

	// Налаштування меню команд бота (кнопка "Меню")
	commands := []tgbotapi.BotCommand{
		{Command: "start", Description: "Головне вітання"},
		{Command: "price", Description: "Поточні курси криптовалют"},
		{Command: "interval", Description: "Обрати частоту розсилки"},
		{Command: "subscribe", Description: "Підписатися на оновлення"},
		{Command: "unsubscribe", Description: "Вимкнути розсилку"},
	}
	bot.Request(tgbotapi.NewSetMyCommands(commands...))

	log.Printf("Бот успішно запущений: %s", bot.Self.UserName)

	// Запуск фонових процесів
	go startPriceAlerts(bot)

	// Веб-сервер для підтримки працездатності на Koyeb
	go func() {
		port := os.Getenv("PORT")
		if port == "" { port = "8000" }
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "Крипто-бот активний!")
		})
		log.Fatal(http.ListenAndServe(":"+port, nil))
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		// --- Обробка натискань на кнопки (Inline Buttons) ---
		if update.CallbackQuery != nil {
			data := update.CallbackQuery.Data
			chatID := update.CallbackQuery.Message.Chat.ID

			// Зміна інтервалу розсилки
			if len(data) > 4 && data[:4] == "int_" {
				hours, _ :=
