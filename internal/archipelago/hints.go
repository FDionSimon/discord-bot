package archipelago

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

// Hint watching.
//
// PrintJSON hint messages only reach the slots a hint concerns, so a relay
// connected as one player sees a fraction of what happens. The server also
// mirrors every slot's hints into a read-only data storage key, which any
// client may read.
//
// Two mechanisms read those keys, and this client uses both:
//
//   - SetNotify asks the server to push a SetReply whenever a key changes.
//     Cheap and immediate, but it only fires when a hint is actually created
//     and stored.
//   - A periodic Get re-reads every key. Costlier, but it does not depend on
//     the server pushing anything, so it still works when a notification is
//     missed or never sent.
//
// Both funnel into ingestHints, which announces only what it has not seen, so
// running both produces no duplicates.

// defaultTeam is team 0. Multiworlds with more than one team are rare, and
// slot_info does not report team numbers, so this client assumes one team.
const defaultTeam = 0

// hintPollInterval is how often every hint key is re-read. Short enough that a
// Discord feed stays useful, long enough not to hammer the server.
const hintPollInterval = 20 * time.Second

// hintID identifies a hint well enough to notice a repeat. The tuple is
// stable: a hint for a given item at a given location between two players is
// the same hint, and only its found status changes.
type hintID struct {
	finding   int
	receiving int
	location  int64
	item      int64
	found     bool
}

func (h Hint) id() hintID {
	return hintID{
		finding:   h.FindingPlayer,
		receiving: h.ReceivingPlayer,
		location:  h.Location,
		item:      h.Item,
		// Including found means a hint that later gets collected is announced
		// a second time, which is usually wanted: "found" is news.
		found: h.Found,
	}
}

// hintKeys returns the storage keys for every slot this client knows about.
func (c *Client) hintKeys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	slots := make(map[int]bool, len(c.slotGames))
	for slot := range c.slotGames {
		slots[slot] = true
	}
	// slot_info is the complete list, but it is absent on some servers. The
	// roster only covers connected players, which is better than nothing.
	for slot := range c.slotNames {
		slots[slot] = true
	}

	keys := make([]string, 0, len(slots))
	for slot := range slots {
		keys = append(keys, HintsKey(defaultTeam, slot))
	}
	return keys
}

// subscribeHints registers for push updates and reads the current state.
func (c *Client) subscribeHints(conn *websocket.Conn) error {
	keys := c.hintKeys()
	if len(keys) == 0 {
		c.log.Warn("no slots known, hint watching disabled")
		return nil
	}

	if err := c.send(conn, SetNotify{Cmd: "SetNotify", Keys: keys}); err != nil {
		return fmt.Errorf("subscribe hints: %w", err)
	}
	if err := c.send(conn, Get{Cmd: "Get", Keys: keys}); err != nil {
		return fmt.Errorf("get hints: %w", err)
	}

	c.log.Info("watching hints", "keys", keys, "poll", hintPollInterval)
	return nil
}

// pollHints re-reads every hint key until the connection closes.
//
// This runs on its own goroutine, which is why writes go through Client.send:
// gorilla/websocket does not allow concurrent writers, and the read loop also
// sends from the handler.
func (c *Client) pollHints(conn *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(hintPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			keys := c.hintKeys()
			if len(keys) == 0 {
				continue
			}
			if err := c.send(conn, Get{Cmd: "Get", Keys: keys}); err != nil {
				// The read loop will notice the same failure and reconnect.
				return
			}
		}
	}
}

// ingestHints decodes a hint list and emits the ones not seen before.
//
// When prime is true the hints are recorded silently. That is the first
// snapshot: those hints already happened, and announcing them would flood the
// channel on startup.
func (c *Client) ingestHints(raw json.RawMessage, prime bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}

	var hints []Hint
	if err := json.Unmarshal(raw, &hints); err != nil {
		c.log.Warn("unreadable hint list", "error", err, "raw", string(raw))
		return
	}

	for _, h := range hints {
		id := h.id()

		c.mu.Lock()
		seen := c.seenHints[id]
		c.seenHints[id] = true
		c.mu.Unlock()

		if seen || prime {
			continue
		}

		c.emit(Event{
			Kind: "Hint",
			Text: c.renderHint(h),
			At:   time.Now(),
		})
	}
}

// renderHint turns a stored hint into a sentence.
//
// Note which game owns which ID: the item belongs to the receiving player's
// game, the location to the finding player's. Using the wrong one yields a
// plausible but wrong name, which is worse than no name at all.
func (c *Client) renderHint(h Hint) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	receiver := c.playerName(strconv.Itoa(h.ReceivingPlayer))
	finder := c.playerName(strconv.Itoa(h.FindingPlayer))
	item := c.lookupName(c.itemNames, h.ReceivingPlayer, strconv.FormatInt(h.Item, 10), "item")
	location := c.lookupName(c.locationNames, h.FindingPlayer, strconv.FormatInt(h.Location, 10), "location")

	msg := fmt.Sprintf("%s's **%s** is at %s", receiver, item, location)
	if h.FindingPlayer != h.ReceivingPlayer {
		msg += fmt.Sprintf(" in %s's world", finder)
	}
	if h.Entrance != "" {
		msg += fmt.Sprintf(" (entrance: %s)", h.Entrance)
	}
	if h.Found {
		msg += " — found"
	}

	return msg
}