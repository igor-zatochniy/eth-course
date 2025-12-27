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
	_ "github.com/lib/pq" // Драйвер для PostgreSQL
)

var db *sql.DB

type BinancePrice struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

// Функція для отримання ціни з Binance
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
	return data.Price, nil
}

// Ініціалізація бази даних
func initDB() {
	var err error
	connStr := os.Getenv("DATABASE_URL")
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal("Помилка підключення до БД:", err)
	}

	// Створення таблиці, якщо вона не існує
	query := `
	CREATE TABLE IF NOT EXISTS subscribers (
		chat_id BIGINT PRIMARY KEY
	);`
	_, err = db.Exec(query)
	if err != nil {
		log.Fatal("Помилка створення таблиці:", err)
	}
	log.Println("База даних готова до роботи.")
}

// Функція для розсилки
func startPriceAlerts(bot *tgbotapi.BotAPI) {
	sendUpdate := func() {
		// Отримуємо список підписників з бази
		rows, err := db.Query("SELECT chat_id FROM subscribers")
		if err != nil {
			log.Println("Помилка отримання підписників:", err)
			return
		}
		defer rows.Close()

		var chatIDs []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err == nil {
				chatIDs = append(chatIDs, id)
			}
		}

		if len(chatIDs) == 0 {
			log.Println("Розсилка скасована: 0 підписників у базі")
			return
		}

		price, err := getETHPrice()
		if err != nil {
			log.Println("Помилка ціни:", err)
			return
		}

		text := fmt.Sprintf("🕒 *Регулярне оновлення*\nКурс Ethereum (ETH): *$%s*", price)
		for _, id := range chatIDs {
			msg := tgbotapi.NewMessage(id, text)
			msg.ParseMode = "Markdown"
			bot.Send(msg)
		}
		log.Printf("Розсилка виконана для %d користувачів", len(chatIDs))
	}

	sendUpdate() // Перший запуск відразу

	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		sendUpdate()
	}
}

func main() {
	_ = godotenv.Load()
	initDB() // Запускаємо БД

	botToken := os.Getenv("TELEGRAM_APITOKEN")
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panic(err)
	}

	log.Printf("Авторизовано як %s", bot.Self.UserName)

	go startPriceAlerts(bot)

	// Веб-сервер для Koyeb
	go func() {
		port := os.Getenv("PORT")
		if port == "" { port = "8000" }
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "Бот працює з БД!")
		})
		log.Fatal(http.ListenAndServe(":"+port, nil))
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil { continue }
		chatID := update.Message.Chat.ID

		switch update.Message.Command() {
		case "subscribe":
			_, err := db.Exec("INSERT INTO subscribers (chat_id) VALUES ($1) ON CONFLICT DO NOTHING", chatID)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(chatID, "Помилка при підписці."))
			} else {
				bot.Send(tgbotapi.NewMessage(chatID, "✅ Ви підписані! Тепер дані збережені в базі."))
			}

		case "unsubscribe":
			_, err := db.Exec("DELETE FROM subscribers WHERE chat_id = $1", chatID)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(chatID, "Помилка при відписці."))
			} else {
				bot.Send(tgbotapi.NewMessage(chatID, "❌ Вас видалено з бази."))
			}

		case "price":
			price, _ := getETHPrice()
			msg := tgbotapi.NewMessage(chatID, "💰 Курс ETH: $"+price)
			bot.Send(msg)
		}
	}
}
