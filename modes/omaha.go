package modes

import (
	"poker-bot/game"
	"poker-bot/models"
)

type Omaha struct {
	game.BaseGame
}

func NewOmaha(channel string) game.Game {
	return &Omaha{
		BaseGame: game.BaseGame{
			Type:       "omaha",
			Players:    make([]*models.Player, 0),
			Deck:       game.GenerateDeck(),
			InProgress: true,
			Channel:    channel,
		},
	}
}

func (o *Omaha) DealCards() {
	for i := 0; i < 4; i++ {
		for _, player := range o.Players {
			player.Hand = append(player.Hand, o.Deck[0])
			o.Deck = o.Deck[1:]
		}
	}
}

func (o *Omaha) UpdateRiver() {
	if len(o.River) == 0 {
		o.River = append(o.River, o.Deck[:3]...)
		o.Deck = o.Deck[3:]
	} else if len(o.River) < 5 {
		o.River = append(o.River, o.Deck[0])
		o.Deck = o.Deck[1:]
	}
}

func (o *Omaha) EvaluateHands() *models.Player {
	var winner *models.Player
	var bestHand int

	for _, player := range o.Players {
		if player.Folded {
			continue
		}

		hand := evaluateOmahaHand(player.Hand, o.River)
		if winner == nil || hand > bestHand {
			winner = player
			bestHand = hand
		}
	}

	return winner
}

func evaluateOmahaHand(hand, river []models.Card) int {
	// This is a simplified evaluation. In a real implementation, you'd need a more sophisticated algorithm.
	allCards := append(hand, river...)
	return calculateHandValue(allCards)
}
