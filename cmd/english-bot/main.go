// Command english-bot is the entrypoint for the Telegram English-learning bot.
// All wiring lives in internal/app; this binary just starts it.
package main

import "github.com/Dawoodkhorsandi/english-bot/internal/app"

func main() {
	app.Run()
}
