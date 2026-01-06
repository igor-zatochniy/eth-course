func startPriceAlerts(bot *tgbotapi.BotAPI) {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		// 1. Сначала получаем актуальные данные ОДИН раз
		btcStr := getPriceWithTrend("BTCUSDT", "BTC")
		ethStr := getPriceWithTrend("ETHUSDT", "ETH")
		usdtStr := getPriceWithTrend("USDTUAH", "USDT")
		currentTime := time.Now().In(kyivLoc).Format("15:04")

		// 2. Выбираем только тех, кому пора отправить сообщение
		rows, err := db.Query(`SELECT chat_id, language_code FROM subscribers 
                               WHERE is_subscribed = TRUE 
                               AND last_sent <= NOW() - (interval_minutes * INTERVAL '1 minute')`)
		if err != nil {
			log.Println("DB Error:", err)
			continue
		}

		for rows.Next() {
			var id int64
			var lang string
			if err := rows.Scan(&id, &lang); err == nil {
				text := fmt.Sprintf(messages[lang]["alert_hdr"]+"\n\n%s\n%s\n%s\n\n_%s_", 
                        currentTime, btcStr, ethStr, usdtStr, messages[lang]["dynamics"])
				msg := tgbotapi.NewMessage(id, text)
				msg.ParseMode = "Markdown"
				msg.ReplyMarkup = getRefreshKeyboard(lang)
				bot.Send(msg)
				db.Exec("UPDATE subscribers SET last_sent = NOW() WHERE chat_id = $1", id)
			}
		}
		rows.Close()
	}
}
