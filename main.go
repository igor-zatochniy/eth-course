package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

var db *sql.DB

// Створюємо кнопку під повідомленням
var priceKeyboard = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Оновити зараз", "refresh_price"),
	),
)

type BinancePrice struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

func getETHPrice() (string, error) {
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.binance.com/api/v3/ticker/price?symbol=ETHUSDT")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var data BinancePrice
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	// Форматуємо ціну, щоб було 2 знаки після коми
	return data.Price, nil
}

func initDB() {
	var err error
	connStr := os.Getenv("DATABASE_URL")
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	query := `CREATE TABLE IF NOT EXISTS subscribers (chat_id BIGINT PRIMARY KEY);`
	_, err = db.Exec(query)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("База даних готова.")
}

func startPriceAlerts(bot *tgbotapi.BotAPI) {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		rows, err := db.Query("SELECT chat_id FROM subscribers")
		if err != nil {
			continue
		}
		price, _ := getETHPrice()
		text := fmt.Sprintf("🕒 *Регулярне оновлення*\nКурс Ethereum (ETH): *$%s*", price)

		for rows.Next() {
			var id int64
			rows.Scan(&id)
			msg := tgbotapi.NewMessage(id, text)
			msg.ParseMode = "Markdown"
			msg.ReplyMarkup = priceKeyboard // Додаємо кнопку до розсилки
			bot.Send(msg)
		}
		rows.Close()
		log.Println("Розсилка виконана")
	}
}

func main() {
	_ = godotenv.Load()
	initDB()

	bot, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_APITOKEN"))
	if err != nil {
		log.Panic(err)
	}

	go startPriceAlerts(bot)

	// Веб-сервер для Koyeb
	go func() {
		port := os.Getenv("PORT")
		if port == "" { port = "8000" }
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "Бот з кнопками працює!")
		})
		http.ListenAndServe(":"+port, nil)
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		// ОБРОБКА НАТИСКАННЯ КНОПКИ
		if update.CallbackQuery != nil {
			if update.CallbackQuery.Data == "refresh_price" {
				price, _ := getETHPrice()
				newText := fmt.Sprintf("🕒 *Оновлено о %s*\nКурс Ethereum (ETH): *$%s*", 
					time.Now().Format("15:04:05"), price)

				// Редагуємо поточне повідомлення замість надсилання нового
				editMsg := tgbotapi.NewEditMessageText(
					update.CallbackQuery.Message.Chat.ID,
					update.CallbackQuery.Message.MessageID,
					newText,
				)
				editMsg.ParseMode = "Markdown"
				editMsg.ReplyMarkup = &priceKeyboard // Повертаємо кнопку назад

				bot.Send(editMsg)

				// Відповідаємо Телеграму, що запит оброблено (прибирає "годинник" на кнопці)
				callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "Ціну оновлено!")
				bot.Request(callback)
			}
			continue
		}

		if update.Message == nil { continue }
		chatID := update.Message.Chat.ID

		switch update.Message.Command() {
		case "start":
			msg := tgbotapi.NewMessage(chatID, "Використовуйте /subscribe для регулярних звітів.")
			bot.Send(msg)
		case "subscribe":
			db.Exec("INSERT INTO subscribers (chat_id) VALUES ($1) ON CONFLICT DO NOTHING", chatID)
			msg := tgbotapi.NewMessage(chatID, "✅ Підписка оформлена!")
			msg.ReplyMarkup = priceKeyboard
			bot.Send(msg)
		case "price":
			price, _ := getETHPrice()
			msg := tgbotapi.NewMessage(chatID, "💰 Курс ETH: *$"+price+"*")
			msg.ParseMode = "Markdown"
			msg.ReplyMarkup = priceKeyboard
			bot.Send(msg)
		}
	}
}
