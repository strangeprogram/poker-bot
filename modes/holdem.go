package modes

import (
	"errors"
	"fmt"
	"log"
	"poker-bot/game"
	"poker-bot/models"
	"sort"
)

type Holdem struct {
	game.BaseGame
	stage      int // 0: preflop, 1: flop, 2: turn, 3: river
	button     int
	smallBlind int
	bigBlind   int
	sidePots   []int
}

func NewHoldem(channel string) game.Game {
	return &Holdem{
		BaseGame: game.BaseGame{
			Type:       "holdem",
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

func (h *Holdem) DealCards() {
	for i := 0; i < 2; i++ {
		for _, player := range h.Players {
			player.Hand = append(player.Hand, h.Deck[0])
			h.Deck = h.Deck[1:]
		}
	}
	h.collectBlinds()
}

func (h *Holdem) collectBlinds() {
	numPlayers := len(h.Players)
	sbPos := (h.button + 1) % numPlayers
	bbPos := (h.button + 2) % numPlayers

	h.Players[sbPos].Bet = h.smallBlind
	h.Players[sbPos].Money -= h.smallBlind
	h.Pot += h.smallBlind

	h.Players[bbPos].Bet = h.bigBlind
	h.Players[bbPos].Money -= h.bigBlind
	h.Pot += h.bigBlind

	h.CurrentBet = h.bigBlind
	h.Turn = (bbPos + 1) % numPlayers
}

func (h *Holdem) UpdateRiver() {
	switch h.stage {
	case 0: // Flop
		h.River = append(h.River, h.Deck[:3]...)
		h.Deck = h.Deck[3:]
	case 1, 2: // Turn and River
		h.River = append(h.River, h.Deck[0])
		h.Deck = h.Deck[1:]
	}
	h.stage++
	h.resetBets()
}

func (h *Holdem) resetBets() {
	for _, player := range h.Players {
		player.Bet = 0
	}
	h.CurrentBet = 0
	h.Turn = (h.button + 1) % len(h.Players)
}

func (h *Holdem) EvaluateHands() *models.Player {
	var winner *models.Player
	var bestHand Hand

	for _, player := range h.Players {
		if player.Folded {
			continue
		}
		if len(player.Hand) == 0 {
			log.Printf("Warning: Player %s has no cards", player.Nick)
			continue
		}
		playerHand := evaluateHoldemHand(player.Hand, h.River)
		if winner == nil || playerHand.beats(bestHand) {
			winner = player
			bestHand = playerHand
		}
	}

	if winner == nil {
		log.Println("Warning: No winner found in EvaluateHands")
	}

	return winner
}

func (h *Holdem) Bet(player *models.Player, amount int) error {
	if amount > player.Money {
		return errors.New("not enough money")
	}
	if amount < h.CurrentBet-player.Bet {
		return errors.New("bet must be at least the current bet")
	}
	player.Money -= amount
	player.Bet += amount
	h.Pot += amount
	if player.Bet > h.CurrentBet {
		h.CurrentBet = player.Bet
	}
	return nil
}

func (h *Holdem) Call(player *models.Player) error {
	amountToCall := h.CurrentBet - player.Bet
	return h.Bet(player, amountToCall)
}

func (h *Holdem) Raise(player *models.Player, amount int) error {
	totalBet := h.CurrentBet - player.Bet + amount
	return h.Bet(player, totalBet)
}

func (h *Holdem) Check(player *models.Player) error {
	if player.Bet < h.CurrentBet {
		return errors.New("cannot check, must call or raise")
	}
	return nil
}

func (h *Holdem) Fold(player *models.Player) {
	player.Folded = true
}

func (h *Holdem) IsRoundOver() bool {
	activePlayers := 0
	for _, player := range h.Players {
		if !player.Folded {
			activePlayers++
			if player.Bet != h.CurrentBet {
				return false
			}
		}
	}
	return activePlayers <= 1 || h.stage == 3
}

func (h *Holdem) SetInProgress(inProgress bool) {
	h.InProgress = inProgress
}

func (h *Holdem) CalculateSidePots() {
	players := make([]*models.Player, len(h.Players))
	copy(players, h.Players)
	sort.Slice(players, func(i, j int) bool {
		return players[i].Bet < players[j].Bet
	})

	h.sidePots = make([]int, 0)
	prevBet := 0
	for _, player := range players {
		if player.Folded {
			continue
		}
		pot := 0
		for _, p := range h.Players {
			contribution := min(p.Bet, player.Bet) - prevBet
			pot += contribution
			p.Bet -= contribution
		}
		if pot > 0 {
			h.sidePots = append(h.sidePots, pot)
		}
		prevBet = player.Bet
	}
}

func (h *Holdem) ResetRound() {
	h.BaseGame.ResetRound()
	h.stage = 0
	h.button = (h.button + 1) % len(h.Players)
	h.sidePots = make([]int, 0)
}

func (h *Holdem) GetStage() int {
	return h.stage
}

func (h *Holdem) SetStage(stage int) {
	h.stage = stage
}

type Hand struct {
	category int
	values   []int
}

func (h Hand) beats(other Hand) bool {
	if h.category != other.category {
		return h.category > other.category
	}
	for i := range h.values {
		if h.values[i] != other.values[i] {
			return h.values[i] > other.values[i]
		}
	}
	return false
}

func evaluateHoldemHand(hole, community []models.Card) Hand {
	allCards := append(hole, community...)
	return getBestHand(allCards)
}

func getBestHand(cards []models.Card) Hand {
	handCheckers := []func([]models.Card) (bool, []int){
		isRoyalFlush,
		isStraightFlush,
		isFourOfAKind,
		isFullHouse,
		isFlush,
		isStraight,
		isThreeOfAKind,
		isTwoPair,
		isPair,
		isHighCard,
	}

	for category, checker := range handCheckers {
		if ok, values := checker(cards); ok {
			return Hand{category: 9 - category, values: values}
		}
	}

	panic("No valid hand found")
}

func isRoyalFlush(cards []models.Card) (bool, []int) {
	if ok, values := isStraightFlush(cards); ok && values[0] == 14 {
		return true, values
	}
	return false, nil
}

func isStraightFlush(cards []models.Card) (bool, []int) {
	for _, suit := range []string{"Hearts", "Diamonds", "Clubs", "Spades"} {
		suited := filterBySuit(cards, suit)
		if ok, values := isStraight(suited); ok {
			return true, values
		}
	}
	return false, nil
}

func isFourOfAKind(cards []models.Card) (bool, []int) {
	valueCounts := countValues(cards)
	for value, count := range valueCounts {
		if count >= 4 {
			kickers := getKickers(cards, []int{value}, 1)
			return true, append([]int{value}, kickers...)
		}
	}
	return false, nil
}

func isFullHouse(cards []models.Card) (bool, []int) {
	valueCounts := countValues(cards)
	var threeOfAKind, pair int
	for value, count := range valueCounts {
		if count >= 3 && value > threeOfAKind {
			threeOfAKind = value
		} else if count >= 2 && value > pair {
			pair = value
		}
	}
	if threeOfAKind > 0 && pair > 0 {
		return true, []int{threeOfAKind, pair}
	}
	return false, nil
}

func isFlush(cards []models.Card) (bool, []int) {
	for _, suit := range []string{"Hearts", "Diamonds", "Clubs", "Spades"} {
		suited := filterBySuit(cards, suit)
		if len(suited) >= 5 {
			values := getValues(suited)
			sort.Sort(sort.Reverse(sort.IntSlice(values)))
			return true, values[:5]
		}
	}
	return false, nil
}

func isStraight(cards []models.Card) (bool, []int) {
	values := getValues(cards)
	sort.Ints(values)
	values = removeDuplicates(values)

	if len(values) >= 5 {
		for i := len(values) - 1; i >= 4; i-- {
			if values[i]-values[i-4] == 4 {
				return true, []int{values[i]}
			}
		}
	}

	if containsValue(values, 14) && containsValue(values, 2) && containsValue(values, 3) && containsValue(values, 4) && containsValue(values, 5) {
		return true, []int{5}
	}

	return false, nil
}

func isThreeOfAKind(cards []models.Card) (bool, []int) {
	valueCounts := countValues(cards)
	for value, count := range valueCounts {
		if count >= 3 {
			kickers := getKickers(cards, []int{value}, 2)
			return true, append([]int{value}, kickers...)
		}
	}
	return false, nil
}

func isTwoPair(cards []models.Card) (bool, []int) {
	valueCounts := countValues(cards)
	pairs := make([]int, 0)
	for value, count := range valueCounts {
		if count >= 2 {
			pairs = append(pairs, value)
		}
	}
	if len(pairs) >= 2 {
		sort.Sort(sort.Reverse(sort.IntSlice(pairs)))
		kickers := getKickers(cards, pairs[:2], 1)
		return true, append(pairs[:2], kickers...)
	}
	return false, nil
}

func isPair(cards []models.Card) (bool, []int) {
	valueCounts := countValues(cards)
	for value, count := range valueCounts {
		if count >= 2 {
			kickers := getKickers(cards, []int{value}, 3)
			return true, append([]int{value}, kickers...)
		}
	}
	return false, nil
}

func isHighCard(cards []models.Card) (bool, []int) {
	values := getValues(cards)
	sort.Sort(sort.Reverse(sort.IntSlice(values)))

	returnCount := min(5, len(values))
	return true, values[:returnCount]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func filterBySuit(cards []models.Card, suit string) []models.Card {
	suited := make([]models.Card, 0)
	for _, card := range cards {
		if card.Suit == suit {
			suited = append(suited, card)
		}
	}
	return suited
}

func countValues(cards []models.Card) map[int]int {
	valueCounts := make(map[int]int)
	for _, card := range cards {
		value := cardValue(card)
		valueCounts[value]++
	}
	return valueCounts
}

func getValues(cards []models.Card) []int {
	values := make([]int, len(cards))
	for i, card := range cards {
		values[i] = cardValue(card)
	}
	return values
}

func removeDuplicates(values []int) []int {
	seen := make(map[int]bool)
	result := make([]int, 0)
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func containsValue(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func getKickers(cards []models.Card, excludeValues []int, count int) []int {
	kickers := make([]int, 0)
	for _, card := range cards {
		value := cardValue(card)
		if !containsValue(excludeValues, value) {
			kickers = append(kickers, value)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(kickers)))
	if len(kickers) > count {
		kickers = kickers[:count]
	}
	return kickers
}

func cardValue(card models.Card) int {
	switch card.Value {
	case "A":
		return 14
	case "K":
		return 13
	case "Q":
		return 12
	case "J":
		return 11
	default:
		var value int
		fmt.Sscanf(card.Value, "%d", &value)
		return value
	}
}
