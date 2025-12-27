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

// Фіксуємо київський час (UTC+2 для зими)
// Якщо захочете літній час, зміните 2 на 3, 
// або ми пізніше додамо автоматику.
var kyivLoc = time.FixedZone("Kyiv", 2*60*60)

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
	log.Println("✅ База даних готова.")
}

func startPriceAlerts(bot *tgbotapi.BotAPI) {
	sendUpdate := func() {
		rows, err := db.Query("SELECT chat_id FROM subscribers")
		if err != nil {
			return
		}
		defer rows.Close()

		price, err := getETHPrice()
		if err != nil {
			return
		}

		// Використовуємо наш фіксований пояс
		currentTime := time.Now().In(kyivLoc).Format("15:04")
		text := fmt.Sprintf("🕒 *Регулярне оновлення (%s)*\nКурс Ethereum (ETH): *$%s*", currentTime, price)

		for rows.Next() {
			var id int64
			rows.Scan(&id)
			msg := tgbotapi.NewMessage(id, text)
			msg.ParseMode = "Markdown"
			msg.ReplyMarkup = priceKeyboard
			bot.Send(msg)
		}
		log.Println("Розсилка виконана")
	}

	sendUpdate()
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		sendUpdate()
	}
}

func main() {
	_ = godotenv.Load()
	initDB()

	bot, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_APITOKEN"))
	if err != nil {
		log.Panic(err)
	}

	log.Printf("Авторизовано як %s", bot.Self.UserName)

	go startPriceAlerts(bot)

	go func() {
		port := os.Getenv("PORT")
		if port == "" { port = "8000" }
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "Бот працює стабільно!")
		})
		http.ListenAndServe(":"+port, nil)
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.CallbackQuery != nil {
			if update.CallbackQuery.Data == "refresh_price" {
				price, _ := getETHPrice()
				currentTime := time.Now().In(kyivLoc).Format("15:04:05")
				
				newText := fmt.Sprintf("🕒 *Оновлено о %s (Київ)*\nКурс Ethereum (ETH): *$%s*", currentTime, price)

				editMsg := tgbotapi.NewEditMessageText(
					update.CallbackQuery.Message.Chat.ID,
					update.CallbackQuery.Message.MessageID,
					newText,
				)
				editMsg.ParseMode = "Markdown"
				editMsg.ReplyMarkup = &priceKeyboard

				bot.Send(editMsg)
				bot.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, "Оновлено!"))
			}
			continue
		}

		if update.Message == nil { continue }
		chatID := update.Message.Chat.ID

		switch update.Message.Command() {
		case "subscribe":
			db.Exec("INSERT INTO subscribers (chat_id) VALUES ($1) ON CONFLICT DO NOTHING", chatID)
			bot.Send(tgbotapi.NewMessage(chatID, "✅ Ви підписані на оновлення!"))
		case "price":
			price, _ := getETHPrice()
			msg := tgbotapi.NewMessage(chatID, "💰 Курс ETH: *$"+price+"*")
			msg.ParseMode = "Markdown"
			msg.ReplyMarkup = priceKeyboard
			bot.Send(msg)
		}
	}
}
