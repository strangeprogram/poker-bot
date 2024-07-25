package db

import (
	"database/sql"
	"fmt"
	"poker-bot/models"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

func Initialize(dbPath string) error {
	var err error
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	return createTables()
}

func createTables() error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS players (
			nick TEXT PRIMARY KEY,
			money INTEGER,
			hands_won INTEGER
		)
	`)
	return err
}

func GetPlayer(nick string) (*models.Player, error) {
	var money int
	var handsWon int
	err := db.QueryRow("SELECT money, hands_won FROM players WHERE nick = ?", nick).Scan(&money, &handsWon)
	if err == sql.ErrNoRows {
		// Player doesn't exist, create a new one
		money = 1000 // Starting money
		handsWon = 0
		_, err = db.Exec("INSERT INTO players (nick, money, hands_won) VALUES (?, ?, ?)", nick, money, handsWon)
		if err != nil {
			return nil, fmt.Errorf("failed to create new player: %v", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("failed to get player: %v", err)
	}

	return models.NewPlayer(nick, money, handsWon), nil
}

func UpdatePlayer(player *models.Player) error {
	_, err := db.Exec("UPDATE players SET money = ?, hands_won = ? WHERE nick = ?", player.Money, player.HandsWon, player.Nick)
	return err
}

func IncrementHandsWon(nick string) error {
	_, err := db.Exec("UPDATE players SET hands_won = hands_won + 1 WHERE nick = ?", nick)
	return err
}

func GetPlayerStats(nick string) (money int, handsWon int, err error) {
	err = db.QueryRow("SELECT money, hands_won FROM players WHERE nick = ?", nick).Scan(&money, &handsWon)
	return
}

func Close() {
	db.Close()
}
