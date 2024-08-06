package irc

import (
	"crypto/tls"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"poker-bot/db"
	"poker-bot/game"
	"poker-bot/models"
	"poker-bot/modes"

	irc "github.com/thoj/go-ircevent"
)

const (
	cheatSuccessRate = 80   // 1 in 80 chance of success
	cheatPenaltyRate = 0.02 // 2% penalty for failed cheat attempt
)

type Handler struct {
	conn         *irc.Connection
	games        map[string]game.Game
	lastCommand  map[string]time.Time
	commandMutex sync.Mutex
	server       string
	nick         string
	currentTurn  map[string]string // channeling dat channel -> current player's nick
	turnTimer    map[string]*time.Timer
}

func NewHandler() *Handler {
	return &Handler{
		games:       make(map[string]game.Game),
		lastCommand: make(map[string]time.Time),
		currentTurn: make(map[string]string),
		turnTimer:   make(map[string]*time.Timer),
	}
}

func (h *Handler) Connect(server, nick string) error {
	h.server = server
	h.nick = nick
	h.conn = irc.IRC(nick, nick)
	h.conn.VerboseCallbackHandler = true
	h.conn.Debug = true
	h.conn.UseTLS = true
	h.conn.TLSConfig = &tls.Config{InsecureSkipVerify: true}

	h.conn.AddCallback("001", func(e *irc.Event) {
		log.Println("Connected to server, waiting before joining #poker")
		time.AfterFunc(5*time.Second, func() {
			log.Println("Joining #poker")
			h.conn.Join("#dev")
		})
	})
	h.conn.AddCallback("JOIN", func(e *irc.Event) {
		log.Printf("Joined channel: %s", e.Arguments[0])
	})
	h.conn.AddCallback("PRIVMSG", h.handleMessage)
	h.conn.AddCallback("JOIN", h.handleRejoin)

	err := h.conn.Connect(server)
	if err != nil {
		return fmt.Errorf("failed to connect to IRC server: %v", err)
	}

	log.Println("Connected to IRC server, waiting for welcome message")
	return nil
}

func (h *Handler) Run() {
	for {
		h.conn.Loop()
		log.Println("IRC connection loop ended. Attempting to reconnect in 5 seconds...")
		time.Sleep(5 * time.Second)
		err := h.Connect(h.server, h.nick)
		if err != nil {
			log.Printf("Failed to reconnect: %v", err)
		}
	}
}

func (h *Handler) handleMessage(event *irc.Event) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic in handleMessage: %v", r)
		}
	}()

	if !h.rateLimitCheck(event.Nick) {
		return
	}

	message := strings.TrimSpace(event.Message())
	parts := strings.Split(message, " ")
	if len(parts) == 0 {
		return
	}

	command := strings.ToLower(parts[0])
	channel := event.Arguments[0]

	// Commands that can be used at any time
	switch command {
	case "$start":
		h.handleStartGame(event)
		return
	case "$join":
		h.handleJoinGame(event)
		return
	case "$score":
		h.handleScore(event)
		return
	}

	if h.currentTurn[channel] != event.Nick {
		return
	}

	h.resetTurnTimer(channel)

	switch command {
	case "$bet":
		h.handleBet(event)
	case "$call":
		h.handleCall(event)
	case "$raise":
		h.handleRaise(event)
	case "$fold":
		h.handleFold(event)
	case "$check":
		h.handleCheck(event)
	case "$draw":
		h.handleDraw(event)
	case "$cheat":
		h.handleCheat(event)
	}
}

func (h *Handler) rateLimitCheck(nick string) bool {
	h.commandMutex.Lock()
	defer h.commandMutex.Unlock()

	lastTime, exists := h.lastCommand[nick]
	if !exists || time.Since(lastTime) >= 3*time.Second {
		h.lastCommand[nick] = time.Now()
		return true
	}
	return false
}

func (h *Handler) startTurnTimer(channel string) {
	h.turnTimer[channel] = time.AfterFunc(15*time.Second, func() {
		h.handleTimeout(channel)
	})
}

func (h *Handler) resetTurnTimer(channel string) {
	if timer, exists := h.turnTimer[channel]; exists {
		timer.Stop()
		h.startTurnTimer(channel)
	}
}

func (h *Handler) handleTimeout(channel string) {
	game := h.games[channel]
	if game == nil {
		return
	}

	currentPlayer := h.currentTurn[channel]
	player := game.FindPlayer(currentPlayer)
	if player == nil {
		return
	}

	h.conn.Privmsg(channel, fmt.Sprintf("%s's turn has timed out. Auto-folding.", currentPlayer))
	game.Fold(player)

	if h.checkAllPlayersInactive(channel) {
		h.conn.Privmsg(channel, "All players are inactive. Ending the game.")
		h.endGame(channel)
		return
	}

	if h.checkRoundEnd(channel) {
		return
	}

	h.nextTurn(channel)
}

func (h *Handler) nextTurn(channel string) {
	game := h.games[channel]
	if game == nil {
		return
	}

	game.NextTurn()
	h.announceNextTurn(channel)
}

func (h *Handler) checkAllPlayersInactive(channel string) bool {
	game := h.games[channel]
	if game == nil {
		return true
	}

	for _, player := range game.GetPlayers() {
		if !player.Folded {
			return false
		}
	}
	return true
}

func (h *Handler) handleStartGame(event *irc.Event) {
	channel := event.Arguments[0]

	if h.games[channel] != nil {
		h.conn.Privmsg(channel, "A game is already in progress. Please wait for it to finish before starting a new one.")
		return
	}

	message := strings.TrimSpace(event.Message())
	parts := strings.Split(message, " ")

	log.Printf("Received start game command: %s", message)

	if len(parts) < 2 {
		h.conn.Privmsg(event.Arguments[0], "Usage: $start <game_type>")
		return
	}

	gameType := strings.ToLower(parts[1])

	log.Printf("Attempting to start game of type: %s in channel: %s", gameType, channel)

	var game game.Game
	switch gameType {
	case "holdem":
		game = modes.NewHoldem(channel)
	case "omaha":
		game = modes.NewOmaha(channel)
	case "five card draw", "fivecarddraw":
		game = modes.NewFiveCardDraw(channel)
	default:
		h.conn.Privmsg(channel, "Invalid game type. Supported types: holdem, omaha, five card draw")
		return
	}

	h.games[channel] = game
	h.currentTurn[channel] = ""
	h.conn.Privmsg(channel, fmt.Sprintf("Starting a new game of %s. Type $join to participate!", gameType))
}

func (h *Handler) handleJoinGame(event *irc.Event) {
	channel := event.Arguments[0]
	game := h.games[channel]

	if game == nil {
		h.conn.Privmsg(channel, "No game in progress. Start one with $start <game_type>")
		return
	}

	if game.IsInProgress() {
		h.conn.Privmsg(channel, "Cannot join the game at this time. The game is already in progress.")
		return
	}

	player, err := db.GetOrCreatePlayer(event.Nick)
	if err != nil {
		log.Printf("Error getting or creating player %s: %v", event.Nick, err)
		h.conn.Privmsg(channel, fmt.Sprintf("Error adding player %s to the game.", event.Nick))
		return
	}

	game.AddPlayer(player)

	h.conn.Privmsg(channel, fmt.Sprintf("%s has joined the game.", event.Nick))

	if len(game.GetPlayers()) == 2 {
		h.startRound(channel)
	}
}

func (h *Handler) handleBet(event *irc.Event) {
	channel := event.Arguments[0]
	game := h.games[channel]

	if game == nil {
		h.conn.Privmsg(channel, "No game in progress.")
		return
	}

	player := game.FindPlayer(event.Nick)
	if player == nil {
		h.conn.Privmsg(channel, fmt.Sprintf("%s, you're not in the game.", event.Nick))
		return
	}

	if len(event.Arguments) < 2 {
		h.conn.Privmsg(channel, "Usage: $bet <amount>")
		return
	}

	amount, err := strconv.Atoi(event.Arguments[1])
	if err != nil {
		h.conn.Privmsg(channel, "Invalid bet amount.")
		return
	}

	err = game.Bet(player, amount)
	if err != nil {
		h.conn.Privmsg(channel, fmt.Sprintf("%s, %v", event.Nick, err))
		return
	}

	h.conn.Privmsg(channel, fmt.Sprintf("%s bets %d", event.Nick, amount))
	h.nextTurn(channel)
}

func (h *Handler) handleCall(event *irc.Event) {
	channel := event.Arguments[0]
	game := h.games[channel]

	if game == nil {
		h.conn.Privmsg(channel, "No game in progress.")
		return
	}

	player := game.FindPlayer(event.Nick)
	if player == nil {
		h.conn.Privmsg(channel, fmt.Sprintf("%s, you're not in the game.", event.Nick))
		return
	}

	err := game.Call(player)
	if err != nil {
		h.conn.Privmsg(channel, fmt.Sprintf("%s, %v", event.Nick, err))
		return
	}

	h.conn.Privmsg(channel, fmt.Sprintf("%s calls", event.Nick))
	h.nextTurn(channel)
}

func (h *Handler) handleRaise(event *irc.Event) {
	channel := event.Arguments[0]
	game := h.games[channel]

	if game == nil {
		h.conn.Privmsg(channel, "No game in progress.")
		return
	}

	player := game.FindPlayer(event.Nick)
	if player == nil {
		h.conn.Privmsg(channel, fmt.Sprintf("%s, you're not in the game.", event.Nick))
		return
	}

	if len(event.Arguments) < 2 {
		h.conn.Privmsg(channel, "Usage: $raise <amount>")
		return
	}

	amount, err := strconv.Atoi(event.Arguments[1])
	if err != nil {
		h.conn.Privmsg(channel, "Invalid raise amount.")
		return
	}

	err = game.Raise(player, amount)
	if err != nil {
		h.conn.Privmsg(channel, fmt.Sprintf("%s, %v", event.Nick, err))
		return
	}

	h.conn.Privmsg(channel, fmt.Sprintf("%s raises to %d", event.Nick, game.GetCurrentBet()))
	h.nextTurn(channel)
}

func (h *Handler) handleFold(event *irc.Event) {
	channel := event.Arguments[0]
	game := h.games[channel]

	if game == nil {
		h.conn.Privmsg(channel, "No game in progress.")
		return
	}

	player := game.FindPlayer(event.Nick)
	if player == nil {
		h.conn.Privmsg(channel, fmt.Sprintf("%s, you're not in the game.", event.Nick))
		return
	}

	game.Fold(player)
	h.conn.Privmsg(channel, fmt.Sprintf("%s folds", event.Nick))

	if h.checkRoundEnd(channel) {
		return
	}

	h.nextTurn(channel)
}

func (h *Handler) handleCheck(event *irc.Event) {
	channel := event.Arguments[0]
	game := h.games[channel]

	if game == nil {
		h.conn.Privmsg(channel, "No game in progress.")
		return
	}

	player := game.FindPlayer(event.Nick)
	if player == nil {
		h.conn.Privmsg(channel, fmt.Sprintf("%s, you're not in the game.", event.Nick))
		return
	}

	err := game.Check(player)
	if err != nil {
		h.conn.Privmsg(channel, fmt.Sprintf("%s, %v", event.Nick, err))
		return
	}

	h.conn.Privmsg(channel, fmt.Sprintf("%s checks", event.Nick))
	h.nextTurn(channel)
}

func (h *Handler) handleDraw(event *irc.Event) {
	channel := event.Arguments[0]
	game := h.games[channel]

	if game == nil {
		h.conn.Privmsg(channel, "No game in progress.")
		return
	}

	fiveCardDraw, ok := game.(*modes.FiveCardDraw)
	if !ok {
		h.conn.Privmsg(channel, "This command is only available in Five Card Draw.")
		return
	}

	player := game.FindPlayer(event.Nick)
	if player == nil {
		h.conn.Privmsg(channel, fmt.Sprintf("%s, you're not in the game.", event.Nick))
		return
	}

	if len(event.Arguments) < 2 {
		h.conn.Privmsg(channel, "Usage: $draw <card indices to discard>")
		return
	}

	indices := []int{}
	for _, arg := range event.Arguments[1:] {
		index, err := strconv.Atoi(arg)
		if err != nil {
			h.conn.Privmsg(channel, fmt.Sprintf("Invalid index: %s", arg))
			return
		}
		indices = append(indices, index-1) // Convert to 0-based index
	}

	fiveCardDraw.DrawCards(player, indices)
	h.conn.Notice(event.Nick, fmt.Sprintf("Your new hand: %v", player.Hand))
	h.nextTurn(channel)
}

func (h *Handler) handleCheat(event *irc.Event) {
	channel := event.Arguments[0]
	game := h.games[channel]

	if game == nil {
		h.conn.Privmsg(channel, "No game in progress.")
		return
	}

	player := game.FindPlayer(event.Nick)
	if player == nil {
		h.conn.Privmsg(channel, fmt.Sprintf("%s, you're not in the game.", event.Nick))
		return
	}

	// Attempt to cheat PRISON RULES YO
	if rand.Intn(cheatSuccessRate) == 0 {
		// Successful cheat
		h.handleSuccessfulCheat(channel, player, game)
	} else {
		// Failed cheat attempt
		h.handleFailedCheat(channel, player, game)
	}
}

func (h *Handler) handleSuccessfulCheat(channel string, player *models.Player, game game.Game) {
	switch g := game.(type) {
	case *modes.Holdem:
		h.handleHoldemCheat(channel, player, g)
	case *modes.Omaha:
		h.handleOmahaCheat(channel, player, g)
	case *modes.FiveCardDraw:
		h.handleFiveCardDrawCheat(channel, player, g)
	default:
		log.Printf("Unknown game type for cheating")
		h.conn.Notice(player.Nick, "Cheat failed due to unknown game type.")
	}
}

func (h *Handler) handleHoldemCheat(channel string, player *models.Player, game *modes.Holdem) {
	river := game.GetRiver()
	allCards := append(river, h.getAllOtherPlayerCards(game)...)
	stage := game.GetStage() // 0: preflop, 1: flop, 2: turn, 3: river

	switch stage {
	case 0: // Pre-flop
		player.Hand = getBestStartingHand(allCards)
	case 1, 2, 3: // Flop, Turn, River
		player.Hand = getBestPossibleHand(river, allCards)
	}

	h.conn.Notice(player.Nick, fmt.Sprintf("Your cheat was successful! Your new hand: %v", player.Hand))
}

func (h *Handler) handleOmahaCheat(channel string, player *models.Player, game *modes.Omaha) {
	river := game.GetRiver()
	allCards := append(river, h.getAllOtherPlayerCards(game)...)
	stage := game.GetStage() // 0: preflop, 1: flop, 2: turn, 3: river

	switch stage {
	case 0: // Pre-flop
		player.Hand = getBestOmahaStartingHand(allCards)
	case 1, 2, 3: // Flop, Turn, River
		player.Hand = getBestPossibleOmahaHand(river, allCards)
	}

	h.conn.Notice(player.Nick, fmt.Sprintf("Your cheat was successful! Your new hand: %v", player.Hand))
}

func (h *Handler) handleFiveCardDrawCheat(channel string, player *models.Player, game *modes.FiveCardDraw) {
	allCards := h.getAllOtherPlayerCards(game)
	player.Hand = getBestFiveCardDrawHand(allCards)
	h.conn.Notice(player.Nick, fmt.Sprintf("Your cheat was successful! Your new hand: %v", player.Hand))
}

func (h *Handler) handleFailedCheat(channel string, player *models.Player, game game.Game) {
	// we calculatin
	penalty := int(float64(player.Money) * cheatPenaltyRate)

	game.RemovePlayer(player.Nick)

	// Add their bet to the pot
	game.AddToPot(player.Bet)

	// Apply the penalty
	player.Money -= penalty
	game.AddToPot(penalty)

	// Update the player in the database
	err := db.UpdatePlayer(player)
	if err != nil {
		log.Printf("Error updating player %s after failed cheat: %v", player.Nick, err)
	}

	// Announce the failed cheat attempt
	h.conn.Privmsg(channel, fmt.Sprintf("%s is a bitch and tried to cheat! They're kicked from the round and lose %d chips as penalty.", player.Nick, penalty))

	// Check if the round should end
	if h.checkRoundEnd(channel) {
		return
	}

	// Move to the next turn
	h.nextTurn(channel)
}

func (h *Handler) getAllOtherPlayerCards(game game.Game) []models.Card {
	var cards []models.Card
	for _, p := range game.GetPlayers() {
		if !p.Folded {
			cards = append(cards, p.Hand...)
		}
	}
	return cards
}

func (h *Handler) handleScore(event *irc.Event) {
	money, handsWon, err := db.GetPlayerStats(event.Nick)
	if err != nil {
		log.Printf("Error getting stats for %s: %v", event.Nick, err)
		h.conn.Privmsg(event.Arguments[0], fmt.Sprintf("Error retrieving stats for %s", event.Nick))
		return
	}

	h.conn.Privmsg(event.Arguments[0], fmt.Sprintf("%s's stats - Money: %d, Hands won: %d", event.Nick, money, handsWon))
}

func (h *Handler) handleRejoin(event *irc.Event) {
	channel := event.Arguments[0]
	game := h.games[channel]

	if game == nil {
		return
	}

	player := game.FindPlayer(event.Nick)
	if player != nil {
		player.LastSeen = time.Now()
		h.conn.Notice(event.Nick, fmt.Sprintf("Welcome back! Your hand: %v", player.Hand))
	}
}

func (h *Handler) startRound(channel string) {
	game := h.games[channel]
	game.SetInProgress(true)
	game.ResetRound()
	game.DealCards()

	for _, player := range game.GetPlayers() {
		h.conn.Notice(player.Nick, fmt.Sprintf("Your hand: %v", player.Hand))
	}

	h.conn.Privmsg(channel, "New round started. Place your bets!")
	h.nextTurn(channel)
}

func (h *Handler) announceNextTurn(channel string) {
	game := h.games[channel]
	players := game.GetPlayers()
	currentTurn := game.GetTurn()

	if currentTurn < 0 || currentTurn >= len(players) {
		log.Printf("Error: Invalid turn index. Players: %d, Current turn: %d", len(players), currentTurn)
		h.endGame(channel)
		return
	}

	currentPlayer := players[currentTurn]
	h.currentTurn[channel] = currentPlayer.Nick

	log.Printf("Announcing next turn: %s", currentPlayer.Nick)

	availableCommands := "$bet, $call, $raise, $fold, $check, $cheat"
	if _, ok := game.(*modes.FiveCardDraw); ok {
		availableCommands += ", $draw"
	}

	h.conn.Privmsg(channel, fmt.Sprintf("It's %s's turn. Current bet: %d", currentPlayer.Nick, game.GetCurrentBet()))
	h.conn.Notice(currentPlayer.Nick, fmt.Sprintf("It's your turn. Available commands: %s", availableCommands))

	h.startTurnTimer(channel)
}

func (h *Handler) checkRoundEnd(channel string) bool {
	game := h.games[channel]
	if game.IsRoundOver() {
		activePlayers := 0
		for _, player := range game.GetPlayers() {
			if !player.Folded {
				activePlayers++
			}
		}

		if activePlayers <= 1 {
			var winner *models.Player
			for _, player := range game.GetPlayers() {
				if !player.Folded {
					winner = player
					break
				}
			}
			if winner != nil {
				h.endRoundWithWinner(channel, winner)
			} else {
				log.Println("Error: No winner found when all but one player folded")
				h.endGame(channel)
			}
		} else {
			h.endRound(channel)
		}
		return true
	}
	return false
}

func (h *Handler) endRoundWithWinner(channel string, winner *models.Player) {
	game := h.games[channel]
	winner.Money += game.GetPot()
	winner.HandsWon++

	err := db.UpdatePlayer(winner)
	if err != nil {
		log.Printf("Error updating winner %s: %v", winner.Nick, err)
	}

	h.conn.Privmsg(channel, fmt.Sprintf("Round over! %s wins %d", winner.Nick, game.GetPot()))

	if h.shouldEndGame(game) {
		h.endGame(channel)
	} else {
		h.startRound(channel)
	}
}

func (h *Handler) endRound(channel string) {
	game := h.games[channel]
	winner := game.EvaluateHands()
	if winner == nil {
		log.Println("Error: No winner found in endRound")
		h.endGame(channel)
		return
	}
	winner.Money += game.GetPot()
	winner.HandsWon++

	err := db.UpdatePlayer(winner)
	if err != nil {
		log.Printf("Error updating winner %s: %v", winner.Nick, err)
	}

	h.conn.Privmsg(channel, fmt.Sprintf("Round over! %s wins %d", winner.Nick, game.GetPot()))

	if h.shouldEndGame(game) {
		h.endGame(channel)
	} else {
		h.startRound(channel)
	}
}

func (h *Handler) shouldEndGame(game game.Game) bool {
	activePlayers := 0
	for _, player := range game.GetPlayers() {
		if player.Money > 0 {
			activePlayers++
		}
	}
	return activePlayers < 2
}

func (h *Handler) endGame(channel string) {
	game := h.games[channel]
	var winner *models.Player
	for _, player := range game.GetPlayers() {
		if player.Money > 0 {
			winner = player
			break
		}
	}

	if winner != nil {
		h.conn.Privmsg(channel, fmt.Sprintf("Game over! %s wins the game!", winner.Nick))
	} else {
		h.conn.Privmsg(channel, "Game over! It's a tie!")
	}

	// Clean up timers
	if timer, exists := h.turnTimer[channel]; exists {
		timer.Stop()
		delete(h.turnTimer, channel)
	}
	delete(h.currentTurn, channel)
	delete(h.games, channel)
}

// Helper functions for cheating mechanism

func getBestStartingHand(usedCards []models.Card) []models.Card {
	possibleHands := [][]string{
		{"A", "A"}, {"K", "K"}, {"Q", "Q"}, {"A", "K"},
		{"J", "J"}, {"10", "10"}, {"A", "Q"}, {"K", "Q"},
	}

	for _, hand := range possibleHands {
		newHand := tryMakeHand(hand, usedCards)
		if newHand != nil {
			return newHand
		}
	}

	// If all else fails, return two random high cards
	return getRandomHighCards(usedCards, 2)
}

func getBestPossibleHand(river, usedCards []models.Card) []models.Card {
	// Check for possible flush
	flushSuit := getFlushSuit(river)
	if flushSuit != "" {
		return getHighestCards(flushSuit, usedCards, 2)
	}

	// Check for possible straight
	straightCards := getPossibleStraightCards(river)
	if len(straightCards) > 0 {
		return getHighestCards(straightCards[0].Suit, usedCards, 2)
	}

	// If no flush or straight possible, get highest pair or high cards
	return getHighestPairOrCards(river, usedCards)
}

func getBestOmahaStartingHand(usedCards []models.Card) []models.Card {
	possibleHands := [][]string{
		{"A", "A", "K", "K"}, {"A", "A", "Q", "Q"}, {"K", "K", "Q", "Q"},
		{"A", "K", "Q", "J"}, {"A", "A", "J", "10"}, {"K", "K", "J", "10"},
	}

	for _, hand := range possibleHands {
		newHand := tryMakeHand(hand, usedCards)
		if newHand != nil {
			return newHand
		}
	}

	// If all else fails, return four random high cards
	return getRandomHighCards(usedCards, 4)
}

func getBestPossibleOmahaHand(river, usedCards []models.Card) []models.Card {
	// Similar to getBestPossibleHand, but returns 4 cards instead of 2
	// Implement Omaha-specific logic here
	// This is a simplified version and should be expanded for real use
	hand := getBestPossibleHand(river, usedCards)
	hand = append(hand, getRandomHighCards(append(usedCards, hand...), 2)...)
	return hand
}

func getBestFiveCardDrawHand(usedCards []models.Card) []models.Card {
	possibleHands := [][]string{
		{"A", "K", "Q", "J", "10"}, // Royal Flush
		{"A", "A", "A", "A", "K"},  // Four of a Kind
		{"A", "A", "A", "K", "K"},  // Full House
	}

	for _, hand := range possibleHands {
		newHand := tryMakeHand(hand, usedCards)
		if newHand != nil {
			return newHand
		}
	}

	// If all else fails, return five random high cards
	return getRandomHighCards(usedCards, 5)
}

func tryMakeHand(values []string, usedCards []models.Card) []models.Card {
	suits := []string{"Hearts", "Diamonds", "Clubs", "Spades"}
	hand := make([]models.Card, len(values))

	for i, value := range values {
		for _, suit := range suits {
			card := models.Card{Suit: suit, Value: value}
			if !containsCard(usedCards, card) {
				hand[i] = card
				break
			}
		}
		if hand[i].Suit == "" {
			return nil // Couldn't make this hand
		}
	}

	return hand
}

func getFlushSuit(river []models.Card) string {
	suitCounts := make(map[string]int)
	for _, card := range river {
		suitCounts[card.Suit]++
		if suitCounts[card.Suit] >= 3 {
			return card.Suit
		}
	}
	return ""
}

func getPossibleStraightCards(river []models.Card) []models.Card {
	values := make(map[string]bool)
	for _, card := range river {
		values[card.Value] = true
	}

	straightValues := []string{"A", "2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K", "A"}
	for i := 0; i <= 9; i++ {
		count := 0
		for j := 0; j < 5; j++ {
			if values[straightValues[i+j]] {
				count++
			}
		}
		if count >= 3 {
			return river // Potential straight, return the river cards
		}
	}
	return nil
}

func getHighestCards(suit string, usedCards []models.Card, count int) []models.Card {
	values := []string{"A", "K", "Q", "J", "10", "9", "8", "7", "6", "5", "4", "3", "2"}
	hand := make([]models.Card, 0)

	for _, value := range values {
		card := models.Card{Suit: suit, Value: value}
		if !containsCard(usedCards, card) {
			hand = append(hand, card)
			if len(hand) == count {
				break
			}
		}
	}

	return hand
}

func getHighestPairOrCards(river, usedCards []models.Card) []models.Card {
	values := make(map[string]int)
	for _, card := range river {
		values[card.Value]++
	}

	// Check for pair
	for value, count := range values {
		if count == 2 {
			return tryMakeHand([]string{value, value}, usedCards)
		}
	}

	// If no pair, get highest cards
	return getRandomHighCards(usedCards, 2)
}

func getRandomHighCards(usedCards []models.Card, count int) []models.Card {
	values := []string{"A", "K", "Q", "J", "10", "9", "8", "7", "6", "5", "4", "3", "2"}
	suits := []string{"Hearts", "Diamonds", "Clubs", "Spades"}
	hand := make([]models.Card, 0)

	for _, value := range values {
		for _, suit := range suits {
			card := models.Card{Suit: suit, Value: value}
			if !containsCard(usedCards, card) {
				hand = append(hand, card)
				if len(hand) == count {
					return hand
				}
			}
		}
	}

	return hand
}

func containsCard(cards []models.Card, card models.Card) bool {
	for _, c := range cards {
		if c.Suit == card.Suit && c.Value == card.Value {
			return true
		}
	}
	return false
}
