package modes

import (
	"poker-bot/game"
	"poker-bot/models"
)

type FiveCardDraw struct {
	game.BaseGame
}

func NewFiveCardDraw(channel string) game.Game {
	return &FiveCardDraw{
		BaseGame: game.BaseGame{
			Type:       "five card draw",
			Players:    make([]*models.Player, 0),
			Deck:       game.GenerateDeck(),
			InProgress: true,
			Channel:    channel,
		},
	}
}

func (f *FiveCardDraw) DealCards() {
	for i := 0; i < 5; i++ {
		for _, player := range f.Players {
			player.Hand = append(player.Hand, f.Deck[0])
			f.Deck = f.Deck[1:]
		}
	}
}

func (f *FiveCardDraw) UpdateRiver() {
	// No river in Five Card Draw
}

func (f *FiveCardDraw) EvaluateHands() *models.Player {
	var winner *models.Player
	var bestHand int

	for _, player := range f.Players {
		if player.Folded {
			continue
		}

		hand := evaluateFiveCardDrawHand(player.Hand)
		if winner == nil || hand > bestHand {
			winner = player
			bestHand = hand
		}
	}

	return winner
}

func evaluateFiveCardDrawHand(hand []models.Card) int {
	// This is a simplified evaluation. In a real implementation, you'd need a more sophisticated algorithm.
	return calculateHandValue(hand)
}
