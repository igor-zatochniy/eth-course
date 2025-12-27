package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

type BinancePrice struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

type Subscribers struct {
	mu    sync.Mutex
	chats map[int64]bool
}

var subs = Subscribers{chats: make(map[int64]bool)}

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

func startPriceAlerts(bot *tgbotapi.BotAPI) {
	sendUpdate := func() {
		subs.mu.Lock()
		count := len(subs.chats)
		subs.mu.Unlock()

		if count == 0 {
			log.Println("Розсилка скасована: немає активних підписників")
			return
		}

		price, err := getETHPrice()
		if err != nil {
			log.Println("Помилка отримання ціни:", err)
			return
		}

		text := fmt.Sprintf("🕒 *Регулярне оновлення*\nКурс Ethereum (ETH): *$%s*", price)

		subs.mu.Lock()
		log.Printf("Запуск розсилки для %d користувачів", len(subs.chats))
		for chatID := range subs.chats {
			msg := tgbotapi.NewMessage(chatID, text)
			msg.ParseMode = "Markdown"
			bot.Send(msg)
		}
		subs.mu.Unlock()
	}

	// Перша розсилка відразу при запуску
	sendUpdate()

	// Наступні — кожні 5 хвилин
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		sendUpdate()
	}
}

func main() {
	_ = godotenv.Load()

	botToken := os.Getenv("TELEGRAM_APITOKEN")
	if botToken == "" {
		log.Fatal("Помилка: TELEGRAM_APITOKEN не встановлено")
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panic(err)
	}

	log.Printf("Авторизовано як %s", bot.Self.UserName)

	go startPriceAlerts(bot)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Бот %s працює!", bot.Self.UserName)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	go func() {
		log.Printf("HTTP-сервер запущено на порту %s", port)
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Fatal(err)
		}
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		chatID := update.Message.Chat.ID

		switch update.Message.Command() {
		case "start":
			msg := tgbotapi.NewMessage(chatID, "Привіт! Я бот-індикатор курсу ETH.\n\n/price — курс зараз\n/subscribe — отримувати звіт кожні 5 хв\n/unsubscribe — відписатися")
			bot.Send(msg)

		case "subscribe":
			subs.mu.Lock()
			subs.chats[chatID] = true
			subs.mu.Unlock()
			bot.Send(tgbotapi.NewMessage(chatID, "✅ Ви підписалися на оновлення кожні 5 хвилин."))

		case "unsubscribe":
			subs.mu.Lock()
			delete(subs.chats, chatID)
			subs.mu.Unlock()
			bot.Send(tgbotapi.NewMessage(chatID, "❌ Ви відписалися від розсилки."))

		case "price":
			price, err := getETHPrice()
			text := fmt.Sprintf("💰 Поточний курс ETH: *$%s*", price)
			if err != nil {
				text = "Помилка отримання даних з біржі."
			}
			msg := tgbotapi.NewMessage(chatID, text)
			msg.ParseMode = "Markdown"
			bot.Send(msg)
		}
	}
}
