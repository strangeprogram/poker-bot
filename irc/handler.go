package irc

import (
	"crypto/tls"
	"fmt"
	"log"
	"runtime/debug"
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

type Handler struct {
	conn         *irc.Connection
	games        map[string]game.Game
	lastCommand  map[string]time.Time
	commandMutex sync.Mutex
	server       string
	nick         string
}

func NewHandler() *Handler {
	return &Handler{
		games:       make(map[string]game.Game),
		lastCommand: make(map[string]time.Time),
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
			h.conn.Join("#poker")
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

	switch command {
	case "$start":
		h.handleStartGame(event)
	case "$join":
		h.handleJoinGame(event)
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
	case "$score":
		h.handleScore(event)
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

func (h *Handler) isGameReadyForPlayers(game game.Game) bool {
	return game != nil && !game.IsInProgress() && len(game.GetPlayers()) < 6 // Assumin max 6 players per game
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
	h.conn.Privmsg(channel, fmt.Sprintf("Starting a new game of %s. Type $join to participate!", gameType))
}

func (h *Handler) handleJoinGame(event *irc.Event) {
	channel := event.Arguments[0]
	game := h.games[channel]

	if game == nil {
		h.conn.Privmsg(channel, "No game in progress. Start one with $start <game_type>. Be sure to check your server buffer for notices from the bot.")
		return
	}

	if !h.isGameReadyForPlayers(game) {
		h.conn.Privmsg(channel, "Cannot join the game at this time. Either the game is in progress or the maximum number of players has been reached.")
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
	game.NextTurn()
	h.announceNextTurn(channel)
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
	game.NextTurn()
	h.announceNextTurn(channel)
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
	game.NextTurn()
	h.announceNextTurn(channel)
}

func (h *Handler) handleFold(event *irc.Event) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic in handleFold: %v", r)
			debug.PrintStack() // This will print the stack trace
		}
	}()

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

	log.Printf("Player %s is folding", player.Nick)
	log.Printf("Game state before fold: %+v", game)

	game.Fold(player)
	h.conn.Privmsg(channel, fmt.Sprintf("%s folds", event.Nick))

	log.Printf("Game state after fold: %+v", game)

	if h.checkRoundEnd(channel) {
		return
	}

	game.NextTurn()
	h.announceNextTurn(channel)
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
	game.NextTurn()
	h.announceNextTurn(channel)
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
		indices = append(indices, index-1)
	}

	fiveCardDraw.DrawCards(player, indices)
	h.conn.Notice(event.Nick, fmt.Sprintf("Your new hand: %v", player.Hand))
}

func (h *Handler) handleCheat(event *irc.Event) {
	h.conn.Privmsg(event.Arguments[0], "Cheating is not implemented yet.")
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
	h.announceNextTurn(channel)
}

func (h *Handler) announceNextTurn(channel string) {
	game := h.games[channel]
	players := game.GetPlayers()
	currentTurn := game.GetTurn()

	log.Printf("Announcing next turn. Players: %d, Current turn: %d", len(players), currentTurn)

	if currentTurn < 0 || currentTurn >= len(players) {
		log.Printf("Error: Invalid turn index. Players: %d, Current turn: %d", len(players), currentTurn)
		return
	}

	currentPlayer := players[currentTurn]
	log.Printf("Announcing next turn: %s", currentPlayer.Nick)

	availableCommands := "$bet, $call, $raise, $fold, $check"
	if _, ok := game.(*modes.FiveCardDraw); ok {
		availableCommands += ", $draw"
	}

	h.conn.Privmsg(channel, fmt.Sprintf("It's %s's turn. Current bet: %d. Don't forget to peep that server buffer for notices from me.", currentPlayer.Nick, game.GetCurrentBet()))
	h.conn.Notice(currentPlayer.Nick, fmt.Sprintf("It's your turn. Available commands: %s", availableCommands))
}

func (h *Handler) checkRoundEnd(channel string) bool {
	game := h.games[channel]
	if game.IsRoundOver() {
		h.endRound(channel)
		return true
	}
	return false
}

func (h *Handler) endRound(channel string) {
	game := h.games[channel]
	winner := game.EvaluateHands()
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

	delete(h.games, channel)
}
