package archipelago

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
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

// storagePollInterval is how often every watched key is re-read. Short enough
// that a Discord feed stays useful, long enough not to hammer the server.
const storagePollInterval = 20 * time.Second

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

// storageKeys returns the read-only keys this client watches: one hint key and
// one client-status key per slot.
//
// Goal status lives in storage for the same reason hints do. A PrintJSON Goal
// message is broadcast once, so a bot that was offline at that moment would
// never learn of it; the storage key can be read at any time.
func (c *Client) storageKeys() []string {
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

	keys := make([]string, 0, len(slots)*2)
	for slot := range slots {
		keys = append(keys, HintsKey(defaultTeam, slot), ClientStatusKey(defaultTeam, slot))
	}
	return keys
}

// subscribeStorage registers for push updates and reads the current state.
func (c *Client) subscribeStorage(conn *websocket.Conn) error {
	keys := c.storageKeys()
	if len(keys) == 0 {
		c.log.Warn("no slots known, storage watching disabled")
		return nil
	}

	if err := c.send(conn, SetNotify{Cmd: "SetNotify", Keys: keys}); err != nil {
		return fmt.Errorf("subscribe to storage: %w", err)
	}
	if err := c.send(conn, Get{Cmd: "Get", Keys: keys}); err != nil {
		return fmt.Errorf("read storage: %w", err)
	}

	c.log.Info("watching hints and goal status", "keys", keys, "poll", storagePollInterval)
	return nil
}

// pollStorage re-reads every watched key until the connection closes.
//
// This runs on its own goroutine, which is why writes go through Client.send:
// gorilla/websocket does not allow concurrent writers, and the read loop also
// sends from the handler.
func (c *Client) pollStorage(conn *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(storagePollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			keys := c.storageKeys()
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
func (c *Client) ingestHints(key string, raw json.RawMessage, prime bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}

	var hints []Hint
	if err := json.Unmarshal(raw, &hints); err != nil {
		c.log.Warn("unreadable hint list", "error", err, "raw", string(raw))
		return
	}

	// Cache the list so a !hint command in chat can be answered from it.
	if slot, ok := SlotFromHintsKey(key); ok {
		c.mu.Lock()
		c.hintsBySlot[slot] = hints
		c.mu.Unlock()
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

// renderHint renders a stored hint exactly as the server would.
//
// This mirrors Hint.as_network_message in NetUtils.py, so a hint reconstructed
// from storage is indistinguishable from one the server pushed as PrintJSON:
//
//	[Hint]: {receiver}'s {item} is at {location} in {finder}'s World. (found)
//
// Note which game owns which ID: the item belongs to the receiving player's
// game, the location to the finding player's. Using the wrong one yields a
// plausible but wrong name, which is worse than no name at all.
func (c *Client) renderHint(h Hint) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	receiver := colorize(h.ReceivingPlayer, c.playerName(strconv.Itoa(h.ReceivingPlayer)))
	finder := colorize(h.FindingPlayer, c.playerName(strconv.Itoa(h.FindingPlayer)))
	item := c.lookupName(c.itemNames, h.ReceivingPlayer, strconv.FormatInt(h.Item, 10), "item")
	location := c.lookupName(c.locationNames, h.FindingPlayer, strconv.FormatInt(h.Location, 10), "location")

	var b strings.Builder
	b.WriteString("[Hint]: ")
	b.WriteString(receiver)
	b.WriteString("'s ")
	b.WriteString(item)
	b.WriteString(" is at ")
	b.WriteString(location)
	b.WriteString(" in ")
	b.WriteString(finder)
	if h.Entrance != "" {
		b.WriteString("'s World at ")
		b.WriteString(h.Entrance)
	} else {
		b.WriteString("'s World")
	}
	b.WriteString(". ")

	// The server derives the trailing status from Hint.status, falling back to
	// found when a server predates the status field.
	status := h.Status
	if status == HintUnspecified && h.Found {
		status = HintFound
	}
	b.WriteString(HintStatusName(status))

	return b.String()
}

// handleHintCommand answers a !hint seen in chat from cached storage.
//
// The server treats re-displaying an existing hint as a no-op: nothing is
// written, and the result goes only to the slots the hint concerns. A relay
// watching from outside sees the command but never the answer. Since the hints
// themselves are already in storage, and the poller keeps them fresh, the
// answer can be reconstructed locally.
//
// This deliberately bypasses the seen-hint dedupe: the player asked, so the
// repeat is the point.
func (c *Client) handleHintCommand(slot int, message string) {
	query, ok := parseHintCommand(message)
	if !ok {
		return
	}

	c.mu.RLock()
	own := c.ownSlot
	hints := append([]Hint(nil), c.hintsBySlot[slot]...)
	c.mu.RUnlock()

	if len(hints) == 0 {
		return
	}

	matches := make([]Hint, 0, len(hints))
	for _, h := range hints {
		// A hint that concerns our own slot reaches us as an ordinary
		// PrintJSON, whoever asked for it. Listing it here as well would post
		// it twice.
		if h.ReceivingPlayer == own || h.FindingPlayer == own {
			continue
		}
		// An empty query is a bare !hint, which lists everything that slot has.
		if query == "" || c.hintMatches(h, query) {
			matches = append(matches, h)
		}
	}
	if len(matches) == 0 {
		return
	}

	// Outstanding hints first. They are the actionable ones, and on a slot
	// with a long history the found ones would otherwise fill the whole list
	// and push the useful ones below the fold in a long list.
	// SliceStable keeps the server's ordering within each group.
	sort.SliceStable(matches, func(i, j int) bool {
		return !matches[i].Found && matches[j].Found
	})

	// One event per hint, with no header of its own. Each then goes through
	// exactly the same formatting as a hint the server pushed, so the two
	// sources are indistinguishable in the channel. The preceding chat line
	// already shows who asked.
	for _, h := range matches {
		c.emit(Event{Kind: "Hint", Text: c.renderHint(h), At: time.Now()})
	}
}

// hintMatches reports whether a hint's item or location name contains query.
func (c *Client) hintMatches(h Hint, query string) bool {
	c.mu.RLock()
	item := c.lookupName(c.itemNames, h.ReceivingPlayer, strconv.FormatInt(h.Item, 10), "item")
	location := c.lookupName(c.locationNames, h.FindingPlayer, strconv.FormatInt(h.Location, 10), "location")
	c.mu.RUnlock()

	query = strings.ToLower(query)
	return strings.Contains(strings.ToLower(item), query) ||
		strings.Contains(strings.ToLower(location), query)
}

// parseHintCommand pulls the search text out of a !hint or !hint_location
// command. The second return is false when the message is not a hint command.
func parseHintCommand(message string) (query string, ok bool) {
	message = strings.TrimSpace(message)

	for _, prefix := range []string{"!hint_location", "!hint"} {
		if !strings.HasPrefix(message, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(message, prefix))
		// "!hintsomething" is not a hint command.
		if rest != "" && !strings.HasPrefix(strings.TrimPrefix(message, prefix), " ") {
			return "", false
		}
		return rest, true
	}
	return "", false
}