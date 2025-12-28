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

// --- СЛОВНИК ПЕРЕКЛАДІВ ---
var messages = map[string]map[string]string{
	"ua": {
		"welcome":  "Вітаю! 🖖 Твій крипто-асистент уже на зв’язку!\n\nХочеш тримати руку на пульсі ринку? Я допоможу!\n\n🔹 *Live-курси:* BTC, ETH, USDT з трендами.\n🔹 *Smart-сповіщення:* Обирай інтервал (1 хв – 24 год).\n\nТисни **/subscribe**!",
		"sub_ok":   "✅ Підписка активована!",
		"unsub_ok": "❌ Ви відписалися від розсилки.",
		"price_t":  "💰 *Актуальні курси:*",
		"interval": "⚙️ *Оберіть частоту повідомлень:*",
		"lang_sel": "🌍 *Оберіть мову / Choose language / Выберите язык:*",
		"lang_ok":  "✅ Мову змінено на Українську!",
		"update":   "🕒 *Оновлено о %s*",
	},
	"en": {
		"welcome":  "Welcome! 🖖 Your crypto assistant is online!\n\nWant to keep your finger on the pulse of the market? I'll help!\n\n🔹 *Live rates:* BTC, ETH, USDT with trends.\n🔹 *Smart alerts:* Choose interval (1 min – 24h).\n\nPress **/subscribe**!",
		"sub_ok":   "✅ Subscription activated!",
		"unsub_ok": "❌ You have unsubscribed.",
		"price_t":  "💰 *Current rates:*",
		"interval": "⚙️ *Choose message frequency:*",
		"lang_sel": "🌍 *Choose language:*",
		"lang_ok":  "✅ Language changed to English!",
		"update":   "🕒 *Updated at %s*",
	},
	"ru": {
		"welcome":  "Привет! 🖖 Твой крипто-ассистент на связи!\n\nХочешь держать руку на пульсе рынка? Я помогу!\n\n🔹 *Live-курсы:* BTC, ETH, USDT с трендами.\n🔹 *Smart-уведомления:* Выбирай интервал (1 мин – 24 ч).\n\nЖми **/subscribe**!",
		"sub_ok":   "✅ Подписка активирована!",
		"unsub_ok": "❌ Вы отписались от рассылки.",
		"price_t":  "💰 *Актуальные курсы:*",
		"interval": "⚙️ *Выберите частоту сообщений:*",
		"lang_sel": "🌍 *Выберите язык:*",
		"lang_ok":  "✅ Язык изменен на Русский!",
		"update":   "🕒 *Обновлено в %s*",
	},
}

// Клавіатура вибору мови
var langKeyboard = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🇺🇦 Українська", "setlang_ua"),
		tgbotapi.NewInlineKeyboardButtonData("🇺🇸 English", "setlang_en"),
		tgbotapi.NewInlineKeyboardButtonData("🇷🇺 Русский", "setlang_ru"),
	),
)

// --- ЛОГІКА БОТА ---

type BinancePrice struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

func getLang(chatID int64) string {
	var lang string
	err := db.QueryRow("SELECT language_code FROM subscribers WHERE chat_id = $1", chatID).Scan(&lang)
	if err != nil {
		return "ua" // Мова за замовчуванням
	}
	return lang
}

func getPriceWithTrend(pair string, label string, lang string) string {
	url := fmt.Sprintf("https://api.binance.com/api/v3/ticker/price?symbol=%s", pair)
	resp, _ := http.Get(url)
	defer resp.Body.Close()
	var data BinancePrice
	json.NewDecoder(resp.Body).Decode(&data)
	currentPrice, _ := strconv.ParseFloat(data.Price, 64)

	var lastPrice float64
	db.QueryRow("SELECT price FROM market_prices WHERE symbol = $1", pair).Scan(&lastPrice)

	emoji := "⚪️"
	trend := "0.00%"
	if lastPrice > 0 {
		diff := ((currentPrice - lastPrice) / lastPrice) * 100
		if diff > 0.01 { emoji = "🟢"; trend = fmt.Sprintf("+%.2f%%", diff) }
		if diff < -0.01 { emoji = "🔴"; trend = fmt.Sprintf("%.2f%%", diff) }
	}
	db.Exec("INSERT INTO market_prices (symbol, price) VALUES ($1, $2) ON CONFLICT (symbol) DO UPDATE SET price = EXCLUDED.price", pair, currentPrice)

	if pair == "USDTUAH" { return fmt.Sprintf("%s %s: *%.2f UAH* (%s)", emoji, label, currentPrice, trend) }
	return fmt.Sprintf("%s %s: *$%.2f* (%s)", emoji, label, currentPrice, trend)
}

func initDB() {
	connStr := os.Getenv("DATABASE_URL")
	var err error
	db, err = sql.Open("postgres", connStr)
	if err != nil { log.Fatal(err) }
	db.Exec(`CREATE TABLE IF NOT EXISTS subscribers (chat_id BIGINT PRIMARY KEY, interval_minutes INT DEFAULT 60, last_sent TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP, language_code TEXT DEFAULT 'ua');`)
	db.Exec(`ALTER TABLE subscribers ADD COLUMN IF NOT EXISTS language_code TEXT DEFAULT 'ua';`)
	db.Exec(`CREATE TABLE IF NOT EXISTS market_prices (symbol TEXT PRIMARY KEY, price DOUBLE PRECISION);`)
}

func startPriceAlerts(bot *tgbotapi.BotAPI) {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		rows, _ := db.Query(`SELECT chat_id, language_code FROM subscribers WHERE last_sent <= NOW() - (interval_minutes * INTERVAL '1 minute') + INTERVAL '10 seconds'`)
		for rows.Next() {
			var id int64
			var lang string
			rows.Scan(&id, &lang)
			
			btc := getPriceWithTrend("BTCUSDT", "BTC", lang)
			eth := getPriceWithTrend("ETHUSDT", "ETH", lang)
			usdt := getPriceWithTrend("USDTUAH", "USDT", lang)
			
			text := fmt.Sprintf(messages[lang]["update"]+"\n\n%s\n%s\n%s", time.Now().In(kyivLoc).Format("15:04"), btc, eth, usdt)
			msg := tgbotapi.NewMessage(id, text)
			msg.ParseMode = "Markdown"
			bot.Send(msg)
			db.Exec("UPDATE subscribers SET last_sent = NOW() WHERE chat_id = $1", id)
		}
		rows.Close()
	}
}

func main() {
	_ = godotenv.Load()
	initDB()
	bot, _ := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_APITOKEN"))

	commands := []tgbotapi.BotCommand{
		{Command: "start", Description: "Start bot"},
		{Command: "language", Description: "Change language / Змінити мову"},
		{Command: "price", Description: "Check rates"},
		{Command: "interval", Description: "Set timer"},
		{Command: "subscribe", Description: "Subscribe"},
	}
	bot.Request(tgbotapi.NewSetMyCommands(commands...))

	go startPriceAlerts(bot)

	// Web server
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "Bot is running!") })
	go http.ListenAndServe(":"+os.Getenv("PORT"), nil)

	u := tgbotapi.NewUpdate(0)
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.CallbackQuery != nil {
			chatID := update.CallbackQuery.Message.Chat.ID
			data := update.CallbackQuery.Data

			if len(data) > 8 && data[:8] == "setlang_" {
				newLang := data[8:]
				db.Exec("UPDATE subscribers SET language_code = $1 WHERE chat_id = $2", newLang, chatID)
				bot.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, "OK"))
				bot.Send(tgbotapi.NewMessage(chatID, messages[newLang]["lang_ok"]))
			}
			// (Тут має бути також логіка оновлення ціни та інтервалів, адаптована під мову)
			continue
		}

		if update.Message == nil { continue }
		chatID := update.Message.Chat.ID
		lang := getLang(chatID)

		switch update.Message.Command() {
		case "start":
			msg := tgbotapi.NewMessage(chatID, messages[lang]["welcome"])
			msg.ParseMode = "Markdown"
			bot.Send(msg)
		case "language":
			msg := tgbotapi.NewMessage(chatID, messages[lang]["lang_sel"])
			msg.ReplyMarkup = langKeyboard
			bot.Send(msg)
		case "subscribe":
			db.Exec("INSERT INTO subscribers (chat_id, language_code) VALUES ($1, 'ua') ON CONFLICT (chat_id) DO NOTHING", chatID)
			bot.Send(tgbotapi.NewMessage(chatID, messages[lang]["sub_ok"]))
		case "price":
			btc := getPriceWithTrend("BTCUSDT", "BTC", lang)
			eth := getPriceWithTrend("ETHUSDT", "ETH", lang)
			usdt := getPriceWithTrend("USDTUAH", "USDT", lang)
			text := fmt.Sprintf(messages[lang]["price_t"]+"\n\n%s\n%s\n%s", btc, eth, usdt)
			msg := tgbotapi.NewMessage(chatID, text)
			msg.ParseMode = "Markdown"
			bot.Send(msg)
		}
	}
}
