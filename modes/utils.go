package modes

import "poker-bot/models"

func CalculateHandValue(cards []models.Card) int {
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
			var value int
			for i, v := range card.Value {
				value = value*10 + int(v-'0')
				if i == 1 {
					break
				}
			}
			sum += value
		}
	}
	return sum
}