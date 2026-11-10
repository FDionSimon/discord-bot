package archipelago

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Config holds everything needed to watch one multiworld.
type Config struct {
	// Address is host:port, for example "archipelago.archipelago.svc.cluster.local:38281".
	Address string
	// Slot is the slot name to connect as. Archipelago has no spectator login:
	// a watcher authenticates against a real slot, and multiple clients may
	// share one. Use a slot you control.
	Slot string
	// Password is the room password, empty if the room is open.
	Password string
}

// Client watches one Archipelago multiworld and emits rendered event strings.
//
// It owns its connection: Run dials, authenticates, reads until the socket
// fails, then reconnects with backoff. Callers consume Events.
type Client struct {
	cfg Config
	log *slog.Logger

	// events is buffered so a slow consumer slows the reader rather than
	// blocking the whole connection. When it fills, events are dropped and
	// counted, which is better than stalling the socket and being timed out.
	events  chan Event
	dropped int64

	// writeMu serializes writes: gorilla/websocket forbids concurrent
	// writers, and the hint poller sends from its own goroutine.
	writeMu sync.Mutex

	mu sync.RWMutex
	// slotNames maps slot number to display name.
	slotNames map[int]string
	// slotGames maps slot number to game name, needed to pick the right ID
	// table when resolving an item or location.
	slotGames map[int]string
	// slotTypes maps slot number to SlotType. Only SlotPlayer slots count as
	// players: item-link groups appear in slot_info too and never goal.
	slotTypes map[int]int
	// itemNames and locationNames are game name to ID to display name.
	itemNames     map[string]map[int64]string
	locationNames map[string]map[int64]string
	seedName      string

	// ownSlot is the slot this client authenticated as. Hints concerning it
	// arrive as ordinary PrintJSON, so the chat-triggered lookup skips it to
	// avoid reporting the same hints twice.
	ownSlot int

	// hintsBySlot caches each slot's current hints, so a !hint command seen
	// in chat can be answered without another round trip.
	hintsBySlot map[int][]Hint

	// seenHints dedupes hints across reconnects, since the server resends the
	// full list on every Get rather than only what changed.
	seenHints map[hintID]bool
	// hintsPrimed is false until the first hint snapshot has been absorbed.
	// That first snapshot is recorded silently; later ones announce whatever
	// is new, which is how hints made while disconnected still get reported.
	hintsPrimed bool

	// tracker marks the start and end of a playthrough. Optional: nil simply
	// means no start or finish banners.
	tracker *Playthrough
}

// Event is one rendered message from the multiworld.
type Event struct {
	// Kind is the PrintJSON type: ItemSend, Hint, Join, Goal and so on.
	Kind string
	// Text is the message with all IDs resolved to names.
	Text string
	At   time.Time
}

// New builds a Client. Call Run to start it.
func New(cfg Config, log *slog.Logger) *Client {
	return &Client{
		cfg:           cfg,
		log:           log,
		events:        make(chan Event, 256),
		slotNames:     map[int]string{},
		slotGames:     map[int]string{},
		slotTypes:     map[int]int{},
		itemNames:     map[string]map[int64]string{},
		locationNames: map[string]map[int64]string{},
		seenHints:     map[hintID]bool{},
		hintsBySlot:   map[int][]Hint{},
	}
}

// Emit posts an event into the stream as though it came from the server.
// The playthrough tracker uses it for its own banners.
func (c *Client) Emit(ev Event) { c.emit(ev) }

// Track attaches a playthrough tracker, which posts start and finish
// banners. Call it before Run.
func (c *Client) Track(t *Playthrough) { c.tracker = t }

// Participants returns the real player slots, excluding item-link groups and
// spectators.
//
// When slot_info was not received there are no types to filter on, so every
// known slot is treated as a player: a roster that is slightly too wide is
// better than one that is empty, which would make completion unreachable.
func (c *Client) Participants() []Participant {
	c.mu.RLock()
	defer c.mu.RUnlock()

	haveTypes := len(c.slotTypes) > 0
	out := make([]Participant, 0, len(c.slotNames))

	for slot, name := range c.slotNames {
		if haveTypes && c.slotTypes[slot] != SlotPlayer {
			continue
		}
		out = append(out, Participant{Slot: slot, Name: name, Game: c.slotGames[slot]})
	}
	return out
}

// Events returns the stream of rendered events. It is never closed; cancel the
// context passed to Run to stop production.
func (c *Client) Events() <-chan Event { return c.events }

// SeedName returns the multiworld's seed name once the handshake has finished.
func (c *Client) SeedName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.seedName
}

// Run connects and keeps the connection alive until ctx is cancelled.
//
// Reconnection is unconditional: an Archipelago server restarts when the host
// reloads a multiworld, and a watcher that gave up on first failure would stay
// dead for the rest of the game.
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		err := c.session(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			c.log.Warn("archipelago session ended", "error", err, "retry_in", backoff)
		default:
			c.log.Info("archipelago connection closed", "retry_in", backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// Cap the backoff so a server that comes back after an hour is picked
		// up within a minute rather than after another hour.
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

// session runs one connection from dial to disconnect.
func (c *Client) session(ctx context.Context) error {
	// Always wss. Archipelago sends the room password and every chat message
	// in the clear over ws, and public hosts terminate TLS on the room port.
	endpoint := fmt.Sprintf("wss://%s", c.cfg.Address)

	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		// Archipelago warns clients that do not negotiate permessage-deflate
		// and says it may become required. gorilla offers it as
		// no-context-takeover only, which the server accepts.
		EnableCompression: true,
	}
	conn, _, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return fmt.Errorf("dial %s: %w", endpoint, err)
	}
	defer conn.Close()

	// Close the socket when the context is cancelled: ReadMessage does not
	// observe a context, so closing from the outside is how it unblocks.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	c.log.Info("archipelago connected", "address", c.cfg.Address)

	// Re-read storage on a timer, so a missed SetReply does not mean a missed
	// hint or an unnoticed goal. Stops with the session via done.
	go c.pollStorage(conn, done)

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		// Every frame is an array of packets.
		var frames []json.RawMessage
		if err := json.Unmarshal(raw, &frames); err != nil {
			c.log.Warn("unreadable frame", "error", err)
			continue
		}

		for _, frame := range frames {
			if err := c.handle(conn, frame); err != nil {
				return err
			}
		}
	}
}

// handle dispatches one packet. Returning an error ends the session.
func (c *Client) handle(conn *websocket.Conn, frame json.RawMessage) error {
	var env envelope
	if err := json.Unmarshal(frame, &env); err != nil {
		return nil // not a packet we can read; ignore rather than disconnect
	}

	switch env.Cmd {
	case "RoomInfo":
		var info RoomInfo
		if err := json.Unmarshal(frame, &info); err != nil {
			return fmt.Errorf("decode RoomInfo: %w", err)
		}
		c.mu.Lock()
		c.seedName = info.SeedName
		c.mu.Unlock()

		// Ask for the ID tables of only the games in this multiworld, then
		// authenticate. The server answers both in order.
		if err := c.send(conn, GetDataPackage{Cmd: "GetDataPackage", Games: info.Games}); err != nil {
			return err
		}
		return c.send(conn, Connect{
			Cmd:           "Connect",
			Password:      c.cfg.Password,
			Game:          "", // allowed, and game validation skipped, with TextOnly
			Name:          c.cfg.Slot,
			UUID:          uuid.NewString(),
			Version:       SupportedVersion,
			Tags:          []string{"TextOnly"},
			ItemsHandling: 0,
			SlotData:      false,
		})

	case "DataPackage":
		var pkg DataPackage
		if err := json.Unmarshal(frame, &pkg); err != nil {
			return fmt.Errorf("decode DataPackage: %w", err)
		}
		c.ingestDataPackage(pkg)

	case "Connected":
		var con Connected
		if err := json.Unmarshal(frame, &con); err != nil {
			return fmt.Errorf("decode Connected: %w", err)
		}
		c.mu.Lock()
		c.ownSlot = con.Slot
		c.mu.Unlock()

		c.ingestRoster(con.Players)
		c.ingestSlotInfo(con.SlotInfo)
		c.log.Info("archipelago authenticated", "slot", c.cfg.Slot, "players", len(con.Players))

		if c.tracker != nil {
			c.tracker.SetRoster(c.Participants())
			c.tracker.Begin(c.SeedName())
		}

		// Hints and goal status both reach us only through data storage, so
		// subscribe to every slot's keys to see the whole multiworld.
		return c.subscribeStorage(conn)

	case "RoomUpdate":
		var upd RoomUpdate
		if err := json.Unmarshal(frame, &upd); err == nil && len(upd.Players) > 0 {
			c.ingestRoster(upd.Players)
			if c.tracker != nil {
				c.tracker.SetRoster(c.Participants())
			}
		}

	case "ConnectionRefused":
		var ref ConnectionRefused
		_ = json.Unmarshal(frame, &ref)
		// Authentication failures do not fix themselves, so say clearly what
		// the server objected to rather than reconnecting in a tight loop.
		return fmt.Errorf("connection refused: %v (check slot name and password)", ref.Errors)

	case "Retrieved":
		var res Retrieved
		if err := json.Unmarshal(frame, &res); err != nil {
			return nil
		}
		// The first snapshot is recorded without announcing: whatever it holds
		// already happened. Later snapshots, after a reconnect, announce
		// anything the dedupe sets have not seen.
		c.mu.Lock()
		prime := !c.hintsPrimed
		c.hintsPrimed = true
		c.mu.Unlock()

		for key, value := range res.Keys {
			switch {
			case strings.HasPrefix(key, HintsKeyPrefix):
				c.ingestHints(key, value, prime)
			case strings.HasPrefix(key, ClientStatusKeyPrefix):
				c.ingestStatus(key, value, prime)
			}
		}

	case "SetReply":
		var reply SetReply
		if err := json.Unmarshal(frame, &reply); err != nil {
			return nil
		}
		switch {
		case strings.HasPrefix(reply.Key, HintsKeyPrefix):
			c.ingestHints(reply.Key, reply.Value, false)
		case strings.HasPrefix(reply.Key, ClientStatusKeyPrefix):
			c.ingestStatus(reply.Key, reply.Value, false)
		}

	case "PrintJSON":
		var msg PrintJSON
		if err := json.Unmarshal(frame, &msg); err != nil {
			return nil
		}
		c.emit(Event{
			Kind: msg.Type,
			Text: c.render(msg.Data),
			At:   time.Now(),
		})

		// Re-displaying an existing hint writes nothing to storage, so the
		// server sends it only to the slots it concerns and this client never
		// sees it. The chat line is visible though, so answer the command
		// from the cached storage instead.
		if msg.Type == "Chat" {
			c.handleHintCommand(msg.Slot, msg.Message)
		}

		// Releases and collects are announced and then forgotten: the server
		// keeps no readable record, so this event is the only chance to count
		// them.
		if c.tracker != nil {
			switch msg.Type {
			case "Release":
				c.tracker.Released(msg.Slot)
			case "Collect":
				c.tracker.Collected(msg.Slot)
			}
		}
	}

	return nil
}

// emit queues an event, dropping it if the consumer has fallen behind.
func (c *Client) emit(ev Event) {
	if ev.Text == "" {
		return
	}
	select {
	case c.events <- ev:
	default:
		c.dropped++
		if c.dropped%100 == 1 {
			c.log.Warn("dropping archipelago events, consumer is behind", "dropped", c.dropped)
		}
	}
}

func (c *Client) ingestDataPackage(pkg DataPackage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for game, data := range pkg.Data.Games {
		items := make(map[int64]string, len(data.ItemNameToID))
		for name, id := range data.ItemNameToID {
			items[id] = name
		}
		locations := make(map[int64]string, len(data.LocationNameToID))
		for name, id := range data.LocationNameToID {
			locations[id] = name
		}
		c.itemNames[game] = items
		c.locationNames[game] = locations
	}
}

func (c *Client) ingestRoster(players []NetworkPlayer) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, p := range players {
		name := p.Alias
		if name == "" {
			name = p.Name
		}
		c.slotNames[p.Slot] = name
	}
}

func (c *Client) ingestSlotInfo(info map[string]NetworkSlot) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, slot := range info {
		n, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		c.slotGames[n] = slot.Game
		c.slotTypes[n] = slot.Type
		if _, known := c.slotNames[n]; !known {
			c.slotNames[n] = slot.Name
		}
	}
}

// ingestStatus records one slot's client status from data storage.
//
// The stored value is a bare integer rather than an object, since the server
// writes client_game_state[team, slot] straight into the key.
func (c *Client) ingestStatus(key string, raw json.RawMessage, prime bool) {
	if c.tracker == nil || len(raw) == 0 || string(raw) == "null" {
		return
	}

	slot, ok := SlotFromClientStatusKey(key)
	if !ok {
		return
	}

	var status int
	if err := json.Unmarshal(raw, &status); err != nil {
		c.log.Warn("unreadable client status", "key", key, "raw", string(raw), "error", err)
		return
	}

	c.tracker.Status(slot, status, prime)
}

// send marshals a packet into the single-element array the protocol expects.
// It locks because more than one goroutine writes to the socket.
func (c *Client) send(conn *websocket.Conn, packet any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	payload, err := json.Marshal([]any{packet})
	if err != nil {
		return fmt.Errorf("encode packet: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return fmt.Errorf("write packet: %w", err)
	}
	return nil
}