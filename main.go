package main

import (
	"log"


	"poker-bot/db"
	"poker-bot/irc"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	err := db.Initialize("poker.db")
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	ircHandler := irc.NewHandler()
	err = ircHandler.Connect("irc.supernets.org:6697", "PokerBot")
	if err != nil {
		log.Fatalf("Failed to connect to IRC: %v", err)
	}

	ircHandler.Run()
}
