package models

import "time"

type Player struct {
	Nick     string
	Money    int
	HandsWon int
	Hand     []Card
	Bet      int
	Folded   bool
	Cheating bool
	LastSeen time.Time
}

func NewPlayer(nick string, money int, handsWon int) *Player {
	return &Player{
		Nick:     nick,
		Money:    money,
		HandsWon: handsWon,
		Hand:     make([]Card, 0),
		Bet:      0,
		Folded:   false,
		Cheating: false,
		LastSeen: time.Now(),
	}
}

type Card struct {
	Suit  string
	Value string
}

func (c Card) String() string {
	return c.Value + c.Suit[:1]
}
