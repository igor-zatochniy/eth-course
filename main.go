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
	_ "github.com/lib/pq" // Драйвер PostgreSQL
)

var db *sql.DB

// Клавіатура з кнопкою під повідомленням
var priceKeyboard = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Оновити зараз", "refresh_price"),
	),
)

type BinancePrice struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

// Отримання ціни з Binance
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

	query := `CREATE TABLE IF NOT EXISTS subscribers (chat_id BIGINT PRIMARY KEY);`
	_, err = db.Exec(query)
	if err != nil {
		log.Fatal("Помилка створення таблиці:", err)
	}
	log.Println("✅ База даних готова до роботи.")
}

// Функція автоматичної розсилки
func startPriceAlerts(bot *tgbotapi.BotAPI) {
	// Встановлюємо локацію Києва
	loc, _ := time.LoadLocation("Europe/Kyiv")

	sendUpdate := func() {
		rows, err := db.Query("SELECT chat_id FROM subscribers")
		if err != nil {
			log.Println("Помилка запиту до БД:", err)
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
			log.Println("Розсилка скасована: 0 підписників")
			return
		}

		price, err := getETHPrice()
		if err != nil {
			return
		}

		// Час за Києвом
		currentTime := time.Now().In(loc).Format("15:04")
		text := fmt.Sprintf("🕒 *Регулярне оновлення (%s)*\nКурс Ethereum (ETH): *$%s*", currentTime, price)

		for _, id := range chatIDs {
			msg := tgbotapi.NewMessage(id, text)
			msg.ParseMode = "Markdown"
			msg.ReplyMarkup = priceKeyboard
			bot.Send(msg)
		}
		log.Printf("Розсилка виконана для %d користувачів", len(chatIDs))
	}

	// Перший запуск при старті
	sendUpdate()

	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		sendUpdate()
	}
}

func main() {
	_ = godotenv.Load()
	initDB()

	botToken := os.Getenv("TELEGRAM_APITOKEN")
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panic(err)
	}

	log.Printf("Авторизовано як %s", bot.Self.UserName)

	// Запуск розсилки в фоні
	go startPriceAlerts(bot)

	// Веб-сервер для Koyeb (Health Check)
	go func() {
		port := os.Getenv("PORT")
		if port == "" { port = "8000" }
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "Бот працює! Час Києва налаштовано.")
		})
		log.Fatal(http.ListenAndServe(":"+port, nil))
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	loc, _ := time.LoadLocation("Europe/Kyiv")

	for update := range updates {
		// Обробка кнопок (Inline Buttons)
		if update.CallbackQuery != nil {
			if update.CallbackQuery.Data == "refresh_price" {
				price, _ := getETHPrice()
				currentTime := time.Now().In(loc).Format("15:04:05")
				
				newText := fmt.Sprintf("🕒 *Оновлено о %s (Київ)*\nКурс Ethereum (ETH): *$%s*", currentTime, price)

				editMsg := tgbotapi.NewEditMessageText(
					update.CallbackQuery.Message.Chat.ID,
					update.CallbackQuery.Message.MessageID,
					newText,
				)
				editMsg.ParseMode = "Markdown"
				editMsg.ReplyMarkup = &priceKeyboard

				bot.Send(editMsg)
				bot.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, "Ціну оновлено!"))
			}
			continue
		}

		if update.Message == nil { continue }
		chatID := update.Message.Chat.ID

		switch update.Message.Command() {
		case "start":
			msg := tgbotapi.NewMessage(chatID, "Привіт! Я ETH бот з пам'яттю.\n/subscribe — підписка на 5 хв\n/price — дізнатися курс")
			bot.Send(msg)

		case "subscribe":
			_, err := db.Exec("INSERT INTO subscribers (chat_id) VALUES ($1) ON CONFLICT DO NOTHING", chatID)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(chatID, "Помилка бази даних."))
			} else {
				bot.Send(tgbotapi.NewMessage(chatID, "✅ Тепер ви в базі! Розсилка кожні 5 хвилин."))
			}

		case "unsubscribe":
			db.Exec("DELETE FROM subscribers WHERE chat_id = $1", chatID)
			bot.Send(tgbotapi.NewMessage(chatID, "❌ Ви відписалися та видалені з бази."))

		case "price":
			price, _ := getETHPrice()
			msg := tgbotapi.NewMessage(chatID, "💰 Курс ETH: *$"+price+"*")
			msg.ParseMode = "Markdown"
			msg.ReplyMarkup = priceKeyboard
			bot.Send(msg)
		}
	}
}
