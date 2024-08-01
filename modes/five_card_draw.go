package modes

import (
	"errors"
	"poker-bot/game"
	"poker-bot/models"
)

type FiveCardDraw struct {
	game.BaseGame
	drawPhase bool
	ante      int
}

func NewFiveCardDraw(channel string) game.Game {
	return &FiveCardDraw{
		BaseGame: game.BaseGame{
			Type:       "five card draw",
			Players:    make([]*models.Player, 0),
			Deck:       game.GenerateDeck(),
			InProgress: false,
			Channel:    channel,
		},
		drawPhase: false,
		ante:      5,
	}
}

func (f *FiveCardDraw) DealCards() {
	for i := 0; i < 5; i++ {
		for _, player := range f.Players {
			player.Hand = append(player.Hand, f.Deck[0])
			f.Deck = f.Deck[1:]
		}
	}
	f.collectAnte()
}

func (f *FiveCardDraw) collectAnte() {
	for _, player := range f.Players {
		player.Money -= f.ante
		f.Pot += f.ante
	}
	f.Turn = 0
}

func (f *FiveCardDraw) UpdateRiver() {
	// No river in Five Card Draw
	f.drawPhase = true
}

func (f *FiveCardDraw) EvaluateHands() *models.Player {
	var winner *models.Player
	var bestHand Hand

	for _, player := range f.Players {
		if player.Folded {
			continue
		}
		playerHand := evaluateFiveCardDrawHand(player.Hand)
		if winner == nil || playerHand.beats(bestHand) {
			winner = player
			bestHand = playerHand
		}
	}

	return winner
}

func (f *FiveCardDraw) Bet(player *models.Player, amount int) error {
	if amount > player.Money {
		return errors.New("not enough money")
	}
	if amount < f.CurrentBet-player.Bet {
		return errors.New("bet must be at least the current bet")
	}
	player.Money -= amount
	player.Bet += amount
	f.Pot += amount
	if player.Bet > f.CurrentBet {
		f.CurrentBet = player.Bet
	}
	return nil
}

func (f *FiveCardDraw) Call(player *models.Player) error {
	amountToCall := f.CurrentBet - player.Bet
	return f.Bet(player, amountToCall)
}

func (f *FiveCardDraw) Raise(player *models.Player, amount int) error {
	totalBet := f.CurrentBet - player.Bet + amount
	return f.Bet(player, totalBet)
}

func (f *FiveCardDraw) Check(player *models.Player) error {
	if player.Bet < f.CurrentBet {
		return errors.New("cannot check, must call or raise")
	}
	return nil
}

func (f *FiveCardDraw) Fold(player *models.Player) {
	player.Folded = true
}

func (f *FiveCardDraw) IsRoundOver() bool {
	activePlayers := 0
	for _, player := range f.Players {
		if !player.Folded {
			activePlayers++
			if player.Bet != f.CurrentBet {
				return false
			}
		}
	}
	return activePlayers <= 1 || f.drawPhase
}

func (f *FiveCardDraw) SetInProgress(inProgress bool) {
	f.InProgress = inProgress
}

func (f *FiveCardDraw) DrawCards(player *models.Player, indices []int) {
	if !f.drawPhase {
		return
	}

	for _, index := range indices {
		if index >= 0 && index < len(player.Hand) {
			f.Deck = append(f.Deck, player.Hand[index])
			player.Hand[index] = f.Deck[0]
			f.Deck = f.Deck[1:]
		}
	}
}

func (f *FiveCardDraw) ResetRound() {
	f.BaseGame.ResetRound()
	f.drawPhase = false
}

func (f *FiveCardDraw) CalculateSidePots() {

}

func evaluateFiveCardDrawHand(hand []models.Card) Hand {
	return getBestHand(hand)
}
