package game

import (
	"errors"
	"poker-bot/models"
)

type Game interface {
	AddPlayer(*models.Player)
	RemovePlayer(string)
	FindPlayer(string) *models.Player
	NextTurn()
	Bet(*models.Player, int) error
	Call(*models.Player) error
	Raise(*models.Player, int) error
	Check(*models.Player) error
	Fold(*models.Player)
	DealCards()
	UpdateRiver()
	EvaluateHands() *models.Player
	GetType() string
	GetPlayers() []*models.Player
	GetDeck() []models.Card
	GetRiver() []models.Card
	GetPot() int
	GetCurrentBet() int
	GetTurn() int
	IsInProgress() bool
	SetInProgress(bool)
	IsRoundOver() bool
	GetChannel() string
	ResetRound()
}

type BaseGame struct {
	Type       string
	Players    []*models.Player
	Deck       []models.Card
	River      []models.Card
	Pot        int
	CurrentBet int
	Turn       int
	InProgress bool
	Channel    string
}

func (g *BaseGame) AddPlayer(player *models.Player) {
	g.Players = append(g.Players, player)
}

func (g *BaseGame) RemovePlayer(nick string) {
	for i, player := range g.Players {
		if player.Nick == nick {
			g.Players = append(g.Players[:i], g.Players[i+1:]...)
			return
		}
	}
}

func (g *BaseGame) FindPlayer(nick string) *models.Player {
	for _, player := range g.Players {
		if player.Nick == nick {
			return player
		}
	}
	return nil
}

func (g *BaseGame) NextTurn() {
	g.Turn = (g.Turn + 1) % len(g.Players)
	for g.Players[g.Turn].Folded {
		g.Turn = (g.Turn + 1) % len(g.Players)
	}
}

func (g *BaseGame) Bet(player *models.Player, amount int) error {
	if amount > player.Money {
		return errors.New("not enough money")
	}
	if amount < g.CurrentBet-player.Bet {
		return errors.New("bet must be at least the current bet")
	}
	player.Money -= amount
	player.Bet += amount
	g.Pot += amount
	if player.Bet > g.CurrentBet {
		g.CurrentBet = player.Bet
	}
	return nil
}

func (g *BaseGame) Call(player *models.Player) error {
	amountToCall := g.CurrentBet - player.Bet
	return g.Bet(player, amountToCall)
}

func (g *BaseGame) Raise(player *models.Player, amount int) error {
	totalBet := g.CurrentBet - player.Bet + amount
	return g.Bet(player, totalBet)
}

func (g *BaseGame) Check(player *models.Player) error {
	if player.Bet < g.CurrentBet {
		return errors.New("cannot check, must call or raise")
	}
	return nil
}

func (g *BaseGame) Fold(player *models.Player) {
	player.Folded = true
}

func (g *BaseGame) GetType() string {
	return g.Type
}

func (g *BaseGame) GetPlayers() []*models.Player {
	return g.Players
}

func (g *BaseGame) GetDeck() []models.Card {
	return g.Deck
}

func (g *BaseGame) GetRiver() []models.Card {
	return g.River
}

func (g *BaseGame) GetPot() int {
	return g.Pot
}

func (g *BaseGame) GetCurrentBet() int {
	return g.CurrentBet
}

func (g *BaseGame) GetTurn() int {
	return g.Turn
}

func (g *BaseGame) IsInProgress() bool {
	return g.InProgress
}

func (g *BaseGame) SetInProgress(inProgress bool) {
	g.InProgress = inProgress
}

func (g *BaseGame) GetChannel() string {
	return g.Channel
}

func (g *BaseGame) ResetRound() {
	for _, player := range g.Players {
		player.Bet = 0
		player.Folded = false
		player.Hand = make([]models.Card, 0)
	}
	g.Pot = 0
	g.CurrentBet = 0
	g.River = make([]models.Card, 0)
	g.Deck = GenerateDeck()
}

func GenerateDeck() []models.Card {
	suits := []string{"Hearts", "Diamonds", "Clubs", "Spades"}
	values := []string{"2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K", "A"}
	deck := make([]models.Card, 0, 52)

	for _, suit := range suits {
		for _, value := range values {
			deck = append(deck, models.Card{Suit: suit, Value: value})
		}
	}

	return deck
}
