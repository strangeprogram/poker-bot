package modes

import (
	"poker-bot/game"
	"poker-bot/models"
)

type Holdem struct {
	game.BaseGame
}

func NewHoldem(channel string) game.Game {
	return &Holdem{
		BaseGame: game.BaseGame{
			Type:       "holdem",
			Players:    make([]*models.Player, 0),
			Deck:       game.GenerateDeck(),
			InProgress: true,
			Channel:    channel,
		},
	}
}

func (h *Holdem) DealCards() {
	for i := 0; i < 2; i++ {
		for _, player := range h.Players {
			player.Hand = append(player.Hand, h.Deck[0])
			h.Deck = h.Deck[1:]
		}
	}
}

func (h *Holdem) UpdateRiver() {
	if len(h.River) == 0 {
		h.River = append(h.River, h.Deck[:3]...)
		h.Deck = h.Deck[3:]
	} else if len(h.River) < 5 {
		h.River = append(h.River, h.Deck[0])
		h.Deck = h.Deck[1:]
	}
}

func (h *Holdem) EvaluateHands() *models.Player {
	var winner *models.Player
	var bestHand int

	for _, player := range h.Players {
		if player.Folded {
			continue
		}

		hand := evaluateHoldemHand(player.Hand, h.River)
		if winner == nil || hand > bestHand {
			winner = player
			bestHand = hand
		}
	}

	return winner
}

func evaluateHoldemHand(hand, river []models.Card) int {
	// This is a simplified evaluation. In a real implementation, you'd need a more sophisticated algorithm.
	allCards := append(hand, river...)
	return calculateHandValue(allCards)
}

func calculateHandValue(cards []models.Card) int {
	// Simplified calculation, just sum up card values
	sum := 0
	for _, card := range cards {
		switch card.Value {
		case "J":
			sum += 11
		case "Q":
			sum += 12
		case "K":
			sum += 13
		case "A":
			sum += 14
		default:
			sum += int(card.Value[0] - '0')
		}
	}
	return sum
}
