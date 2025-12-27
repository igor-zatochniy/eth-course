package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv" // Новий пакет для конвертації чисел
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

var db *sql.DB
var kyivLoc = time.FixedZone("Kyiv", 2*60*60)

var priceKeyboard = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Оновити всі курси", "refresh_price"),
	),
)

type BinancePrice struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

// Універсальна функція з округленням до 2 знаків
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

	// Конвертуємо рядок "96450.123456" у число float64
	priceFloat, err := strconv.ParseFloat(data.Price, 64)
	if err != nil {
		return data.Price, nil // Якщо помилка, повертаємо як є
	}

	// Форматуємо число: %.2f означає 2 знаки після коми з округленням
	return fmt.Sprintf("%.2f", priceFloat), nil
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

		btc, _ := getPrice("BTCUSDT")
		eth, _ := getPrice("ETHUSDT")
		usdt, _ := getPrice("USDTUAH")

		currentTime := time.Now().In(kyivLoc).Format("15:04")
		text := fmt.Sprintf("🕒 *Регулярне оновлення (%s)*\n\n"+
			"🟠 *BTC*: *$%s*\n"+
			"🔹 *ETH*: *$%s*\n"+
			"💵 *USDT*: *%s UAH*", currentTime, btc, eth, usdt)

		for rows.Next() {
			var id int64
			rows.Scan(&id)
			msg := tgbotapi.NewMessage(id, text)
			msg.ParseMode = "Markdown"
			msg.ReplyMarkup = priceKeyboard
			bot.Send(msg)
		}
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
			fmt.Fprintf(w, "Бот працює! Округлення активовано.")
		})
		http.ListenAndServe(":"+port, nil)
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.CallbackQuery != nil {
			if update.CallbackQuery.Data == "refresh_price" {
				btc, _ := getPrice("BTCUSDT")
				eth, _ := getPrice("ETHUSDT")
				usdt, _ := getPrice("USDTUAH")
				currentTime := time.Now().In(kyivLoc).Format("15:04:05")
				
				newText := fmt.Sprintf("🕒 *Оновлено о %s (Київ)*\n\n"+
					"🟠 *BTC*: *$%s*\n"+
					"🔹 *ETH*: *$%s*\n"+
					"💵 *USDT*: *%s UAH*", currentTime, btc, eth, usdt)

				editMsg := tgbotapi.NewEditMessageText(
					update.CallbackQuery.Message.Chat.ID,
					update.CallbackQuery.Message.MessageID,
					newText,
				)
				editMsg.ParseMode = "Markdown"
				editMsg.ReplyMarkup = &priceKeyboard

				bot.Send(editMsg)
				bot.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, "Курси оновлено!"))
			}
			continue
		}

		if update.Message == nil { continue }
		chatID := update.Message.Chat.ID

		switch update.Message.Command() {
		case "start":
			welcomeText := "👋 *Вітаю у твоєму крипто-боті!*\n\n" +
				"Я відстежую курси *BTC*, *ETH* та *USDT/UAH*.\n\n" +
				"*Команди:*\n" +
				"✅ /subscribe — отримувати курс кожні 5 хв.\n" +
				"❌ /unsubscribe — вийти з бази.\n" +
				"💰 /price — миттєвий курс усіх монет."
			msg := tgbotapi.NewMessage(chatID, welcomeText)
			msg.ParseMode = "Markdown"
			bot.Send(msg)

		case "subscribe":
			db.Exec("INSERT INTO subscribers (chat_id) VALUES ($1) ON CONFLICT DO NOTHING", chatID)
			bot.Send(tgbotapi.NewMessage(chatID, "✅ Ви підписалися! Я буду надсилати курси кожні 5 хвилин."))

		case "unsubscribe":
			db.Exec("DELETE FROM subscribers WHERE chat_id = $1", chatID)
			bot.Send(tgbotapi.NewMessage(chatID, "❌ Ви відписалися від розсилки."))

		case "price":
			btc, _ := getPrice("BTCUSDT")
			eth, _ := getPrice("ETHUSDT")
			usdt, _ := getPrice("USDTUAH")
			text := fmt.Sprintf("💰 *Актуальні курси:*\n\n🟠 BTC: *$%s*\n🔹 ETH: *$%s*\n💵 USDT: *%s UAH*", btc, eth, usdt)
			msg := tgbotapi.NewMessage(chatID, text)
			msg.ParseMode = "Markdown"
			msg.ReplyMarkup = priceKeyboard
			bot.Send(msg)
		}
	}
}
