package modes

import (
	"errors"
	"poker-bot/game"
	"poker-bot/models"
)

type Omaha struct {
	game.BaseGame
	stage      int // 0: preflop, 1: flop, 2: turn, 3: river
	button     int
	smallBlind int
	bigBlind   int
	sidePots   []int
}

func NewOmaha(channel string) game.Game {
	return &Omaha{
		BaseGame: game.BaseGame{
			Type:       "omaha",
			Players:    make([]*models.Player, 0),
			Deck:       game.GenerateDeck(),
			InProgress: false,
			Channel:    channel,
		},
		stage:      0,
		button:     0,
		smallBlind: 5,
		bigBlind:   10,
		sidePots:   make([]int, 0),
	}
}

func (o *Omaha) DealCards() {
	for i := 0; i < 4; i++ {
		for _, player := range o.Players {
			player.Hand = append(player.Hand, o.Deck[0])
			o.Deck = o.Deck[1:]
		}
	}
	o.collectBlinds()
}

func (o *Omaha) collectBlinds() {
	numPlayers := len(o.Players)
	sbPos := (o.button + 1) % numPlayers
	bbPos := (o.button + 2) % numPlayers

	o.Players[sbPos].Bet = o.smallBlind
	o.Players[sbPos].Money -= o.smallBlind
	o.Pot += o.smallBlind

	o.Players[bbPos].Bet = o.bigBlind
	o.Players[bbPos].Money -= o.bigBlind
	o.Pot += o.bigBlind

	o.CurrentBet = o.bigBlind
	o.Turn = (bbPos + 1) % numPlayers
}

func (o *Omaha) UpdateRiver() {
	switch o.stage {
	case 0: // Flop
		o.River = append(o.River, o.Deck[:3]...)
		o.Deck = o.Deck[3:]
	case 1, 2: // Turn and River
		o.River = append(o.River, o.Deck[0])
		o.Deck = o.Deck[1:]
	}
	o.stage++
	o.resetBets()
}

func (o *Omaha) resetBets() {
	for _, player := range o.Players {
		player.Bet = 0
	}
	o.CurrentBet = 0
	o.Turn = (o.button + 1) % len(o.Players)
}

func (o *Omaha) EvaluateHands() *models.Player {
	var winner *models.Player
	var bestHand Hand

	for _, player := range o.Players {
		if player.Folded {
			continue
		}
		playerHand := evaluateOmahaHand(player.Hand, o.River)
		if winner == nil || playerHand.beats(bestHand) {
			winner = player
			bestHand = playerHand
		}
	}

	return winner
}

func (o *Omaha) Bet(player *models.Player, amount int) error {
	if amount > player.Money {
		return errors.New("not enough money")
	}
	if amount < o.CurrentBet-player.Bet {
		return errors.New("bet must be at least the current bet")
	}
	player.Money -= amount
	player.Bet += amount
	o.Pot += amount
	if player.Bet > o.CurrentBet {
		o.CurrentBet = player.Bet
	}
	return nil
}

func (o *Omaha) Call(player *models.Player) error {
	amountToCall := o.CurrentBet - player.Bet
	return o.Bet(player, amountToCall)
}

func (o *Omaha) Raise(player *models.Player, amount int) error {
	totalBet := o.CurrentBet - player.Bet + amount
	return o.Bet(player, totalBet)
}

func (o *Omaha) Check(player *models.Player) error {
	if player.Bet < o.CurrentBet {
		return errors.New("cannot check, must call or raise")
	}
	return nil
}

func (o *Omaha) Fold(player *models.Player) {
	player.Folded = true
}

func (o *Omaha) IsRoundOver() bool {
	activePlayers := 0
	for _, player := range o.Players {
		if !player.Folded {
			activePlayers++
			if player.Bet != o.CurrentBet {
				return false
			}
		}
	}
	return activePlayers <= 1 || o.stage == 3
}

func (o *Omaha) SetInProgress(inProgress bool) {
	o.InProgress = inProgress
}

func (o *Omaha) CalculateSidePots() {
	// Implementation similar to Holdem
}

func (o *Omaha) ResetRound() {
	o.BaseGame.ResetRound()
	o.stage = 0
	o.button = (o.button + 1) % len(o.Players)
	o.sidePots = make([]int, 0)
}

func (o *Omaha) GetStage() int {
	return o.stage
}

func (o *Omaha) SetStage(stage int) {
	o.stage = stage
}

func evaluateOmahaHand(hand, river []models.Card) Hand {
	// Implement Omaha-specific hand evaluation
	// This is a placeholder and should be replaced with proper Omaha rules
	return getBestHand(append(hand, river...))
}
