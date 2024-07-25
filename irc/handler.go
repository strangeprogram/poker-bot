package irc

import (
	"crypto/tls"
	"fmt"
	"log"
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
	conn            *irc.Connection
	games           map[string]game.Game
	whoisCache      map[string]bool
	whoisCacheMutex sync.RWMutex
	whoisRequests   map[string]chan bool
	whoisMutex      sync.Mutex
}

func NewHandler() *Handler {
	return &Handler{
		games:         make(map[string]game.Game),
		whoisCache:    make(map[string]bool),
		whoisRequests: make(map[string]chan bool),
	}
}

func (h *Handler) Connect(server, nick string) error {
	h.conn = irc.IRC(nick, nick)
	h.conn.VerboseCallbackHandler = true
	h.conn.Debug = true
	h.conn.UseTLS = true
	h.conn.TLSConfig = &tls.Config{InsecureSkipVerify: true} // Note: In production, you should use proper certificate validation

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
	h.conn.AddCallback("330", h.handleIdentifiedNickReply) // RPL_WHOISACCOUNT
	h.conn.AddCallback("318", h.handleEndOfWhois)          // RPL_ENDOFWHOIS

	err := h.conn.Connect(server)
	if err != nil {
		return fmt.Errorf("failed to connect to IRC server: %v", err)
	}

	log.Println("Connected to IRC server, waiting for welcome message")
	return nil
}

func (h *Handler) Run() {
	h.conn.Loop()
}

func (h *Handler) handleMessage(event *irc.Event) {
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
	case "$cheat":
		h.handleCheat(event)
	case "$score":
		h.handleScore(event)
	}
}

func (h *Handler) isRegisteredNick(nick string) bool {
	h.whoisCacheMutex.RLock()
	if registered, exists := h.whoisCache[nick]; exists {
		h.whoisCacheMutex.RUnlock()
		return registered
	}
	h.whoisCacheMutex.RUnlock()

	h.whoisMutex.Lock()
	resultChan, exists := h.whoisRequests[nick]
	if !exists {
		resultChan = make(chan bool, 1)
		h.whoisRequests[nick] = resultChan
		h.whoisMutex.Unlock()
		h.conn.SendRawf("WHOIS %s", nick)
	} else {
		h.whoisMutex.Unlock()
	}

	select {
	case result := <-resultChan:
		return result
	case <-time.After(15 * time.Second):
		log.Printf("WHOIS timeout for nick: %s", nick)
		h.whoisMutex.Lock()
		delete(h.whoisRequests, nick)
		h.whoisMutex.Unlock()
		return false
	}
}

func (h *Handler) handleIdentifiedNickReply(e *irc.Event) {
	if len(e.Arguments) >= 3 {
		nick := e.Arguments[1]
		if strings.Contains(strings.ToLower(e.Arguments[2]), "is logged in as") {
			h.whoisCacheMutex.Lock()
			h.whoisCache[nick] = true
			h.whoisCacheMutex.Unlock()
			log.Printf("Nick %s is identified", nick)
			h.sendWhoisResult(nick, true)
		}
	}
}

func (h *Handler) handleEndOfWhois(e *irc.Event) {
	if len(e.Arguments) >= 2 {
		nick := e.Arguments[1]
		h.whoisCacheMutex.RLock()
		registered := h.whoisCache[nick]
		h.whoisCacheMutex.RUnlock()
		h.sendWhoisResult(nick, registered)
		log.Printf("End of WHOIS for %s, registered: %v", nick, registered)
	}
}

func (h *Handler) sendWhoisResult(nick string, result bool) {
	h.whoisMutex.Lock()
	if resultChan, exists := h.whoisRequests[nick]; exists {
		select {
		case resultChan <- result:
		default:
		}
		delete(h.whoisRequests, nick)
	}
	h.whoisMutex.Unlock()
}

func (h *Handler) handleStartGame(event *irc.Event) {
	message := strings.TrimSpace(event.Message())
	parts := strings.Split(message, " ")

	log.Printf("Received start game command: %s", message)

	if len(parts) < 2 {
		h.conn.Privmsg(event.Arguments[0], "Usage: $start <game_type>")
		return
	}

	gameType := strings.ToLower(parts[1])
	channel := event.Arguments[0]

	log.Printf("Attempting to start game of type: %s in channel: %s", gameType, channel)

	if h.games[channel] != nil {
		h.conn.Privmsg(channel, "A game is already in progress.")
		return
	}

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
		h.conn.Privmsg(channel, "No game in progress. Start one with $start <game_type>")
		return
	}

	if !h.isRegisteredNick(event.Nick) {
		h.conn.Privmsg(channel, fmt.Sprintf("%s, you need to register and identify your nick to play. If you've just identified, please try again.", event.Nick))
		return
	}

	player, err := db.GetPlayer(event.Nick)
	if err != nil {
		log.Printf("Error getting player %s: %v", event.Nick, err)
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

func (h *Handler) handleCheat(event *irc.Event) {
	// Implement cheat logic here
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
	currentPlayer := game.GetPlayers()[game.GetTurn()]
	log.Printf("Announcing next turn: %s", currentPlayer.Nick)
	h.conn.Privmsg(channel, fmt.Sprintf("It's %s's turn. Current bet: %d", currentPlayer.Nick, game.GetCurrentBet()))
}

func (h *Handler) checkRoundEnd(channel string) bool {
	game := h.games[channel]
	activePlayers := 0
	var lastActivePlayer *models.Player

	for _, player := range game.GetPlayers() {
		if !player.Folded {
			activePlayers++
			lastActivePlayer = player
		}
	}

	if activePlayers == 1 {
		h.endRound(channel, lastActivePlayer)
		return true
	}

	return false
}

func (h *Handler) endRound(channel string, winner *models.Player) {
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
