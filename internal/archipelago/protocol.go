// Package archipelago speaks the Archipelago multiworld network protocol well
// enough to watch a game and report what happens in it.
//
// The protocol is JSON over WebSocket. Two details shape everything here:
//
//  1. Every frame is a JSON *array* of packets, even when it holds one packet.
//  2. Messages arrive as lists of parts referring to items, locations and
//     players by numeric ID. Turning those into names needs the data package
//     (item and location IDs, per game) and the slot info (which slot plays
//     which game), both fetched during the handshake.
package archipelago

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Version identifies an Archipelago build. The Class field is required by the
// server's deserializer.
type Version struct {
	Major int    `json:"major"`
	Minor int    `json:"minor"`
	Build int    `json:"build"`
	Class string `json:"class"`
}

// SupportedVersion is what this client reports during the handshake, and what
// other players see in the join message as Client(x.y.z).
//
// The server refuses connections from clients older than the multiworld
// requires, so raise this when a newer server rejects us. Keep it at or below
// the version of the server being connected to.
var SupportedVersion = Version{Major: 0, Minor: 6, Build: 8, Class: "Version"}

// envelope peeks at the command name before full decoding.
type envelope struct {
	Cmd string `json:"cmd"`
}

// --- Server to client ------------------------------------------------------

// RoomInfo is the first packet the server sends after the socket opens.
type RoomInfo struct {
	Cmd      string   `json:"cmd"`
	Games    []string `json:"games"`
	Password bool     `json:"password"`
	SeedName string   `json:"seed_name"`
	Version  Version  `json:"version"`
	Tags     []string `json:"tags"`
}

// NetworkSlot describes one slot in the multiworld.
type NetworkSlot struct {
	Name string `json:"name"`
	Game string `json:"game"`
	Type int    `json:"type"`
}

// NetworkPlayer is a connected player's identity. Alias is the display name,
// which players can change at runtime; Name is the slot name from generation.
type NetworkPlayer struct {
	Team  int    `json:"team"`
	Slot  int    `json:"slot"`
	Alias string `json:"alias"`
	Name  string `json:"name"`
}

// Connected confirms authentication and carries the roster.
type Connected struct {
	Team    int             `json:"team"`
	Slot    int             `json:"slot"`
	Players []NetworkPlayer `json:"players"`
	// SlotInfo is keyed by slot number rendered as a string, which is how JSON
	// object keys arrive. It is the only source of slot-to-game mapping, and
	// without it item IDs cannot be resolved to names.
	SlotInfo map[string]NetworkSlot `json:"slot_info"`
}

// ConnectionRefused explains a rejected Connect.
type ConnectionRefused struct {
	Errors []string `json:"errors"`
}

// RoomUpdate carries incremental changes; we only care about the roster, since
// aliases change while the game runs.
type RoomUpdate struct {
	Players []NetworkPlayer `json:"players"`
}

// JSONMessagePart is one fragment of a message. Type decides how to read it:
// "text" and "color" carry display text directly, while the *_id variants
// carry a numeric ID in Text that must be looked up.
type JSONMessagePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
	// Player is the slot whose game owns an item_id or location_id, and the
	// slot being named for player_id.
	Player int `json:"player"`
	Flags  int `json:"flags"`
}

// NetworkItem identifies an item and where it was found.
type NetworkItem struct {
	Item     int64 `json:"item"`
	Location int64 `json:"location"`
	Player   int   `json:"player"`
	Flags    int   `json:"flags"`
}

// PrintJSON is the event stream: item sends, hints, joins, chat, goals.
type PrintJSON struct {
	Data []JSONMessagePart `json:"data"`
	// Type is the event kind: ItemSend, Hint, Join, Part, Chat, ServerChat,
	// Goal, Release, Collect, Countdown, Tutorial, TagsChanged, CommandResult.
	// Absent on some plain messages.
	Type      string       `json:"type"`
	Receiving int          `json:"receiving"`
	Item      *NetworkItem `json:"item"`
	Found     *bool        `json:"found"`
	Countdown *int         `json:"countdown"`
	Slot      int          `json:"slot"`
	// Message is the raw text of a Chat packet, before rendering. It is how
	// a client command such as "!hint Lamp" can be recognized.
	Message string `json:"message"`
	// Tags are the connecting client's tags on a Join packet, which is how a
	// game client is told apart from a tracker or text client. Part carries no
	// tags, only the slot.
	Tags []string `json:"tags"`
}

// GameData holds one game's ID tables. The server sends name-to-ID; this
// client inverts them, because rendering needs the opposite direction.
type GameData struct {
	ItemNameToID     map[string]int64 `json:"item_name_to_id"`
	LocationNameToID map[string]int64 `json:"location_name_to_id"`
}

// DataPackage carries ID tables for the games in this multiworld.
type DataPackage struct {
	Data struct {
		Games map[string]GameData `json:"games"`
	} `json:"data"`
}

// --- Client to server ------------------------------------------------------

// Connect authenticates against a slot.
type Connect struct {
	Cmd      string   `json:"cmd"`
	Password string   `json:"password"`
	Game     string   `json:"game"`
	Name     string   `json:"name"`
	UUID     string   `json:"uuid"`
	Version  Version  `json:"version"`
	Tags     []string `json:"tags"`
	// ItemsHandling must be 0 for a TextOnly client: we neither send locations
	// nor want items delivered to us.
	ItemsHandling int  `json:"items_handling"`
	SlotData      bool `json:"slot_data"`
}

// GetDataPackage asks for ID tables. Naming the games keeps the response to
// what this multiworld actually needs, which matters because the full package
// across all supported games is very large.
type GetDataPackage struct {
	Cmd   string   `json:"cmd"`
	Games []string `json:"games"`
}

// --- Data storage ----------------------------------------------------------
//
// PrintJSON delivery is scoped to the slots a message concerns, so a client
// watching one slot sees only its own hints. Data storage is the way around
// that: hints are mirrored into read-only keys that any client may read.

// Get requests stored values, answered with Retrieved.
type Get struct {
	Cmd  string   `json:"cmd"`
	Keys []string `json:"keys"`
}

// SetNotify subscribes to changes on keys, answered with SetReply on each
// change. Needed in addition to Get, which is only a snapshot.
type SetNotify struct {
	Cmd  string   `json:"cmd"`
	Keys []string `json:"keys"`
}

// Retrieved answers a Get. Values are keyed by the requested key name.
type Retrieved struct {
	Keys map[string]json.RawMessage `json:"keys"`
}

// SetReply announces a changed key. For _read_ keys the OriginalValue field is
// absent, so a consumer must track what it has already seen rather than
// diffing against the previous value the server sends.
type SetReply struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
	Slot  int             `json:"slot"`
}

// Hint is one entry from a _read_hints_{team}_{slot} key.
type Hint struct {
	ReceivingPlayer int    `json:"receiving_player"`
	FindingPlayer   int    `json:"finding_player"`
	Location        int64  `json:"location"`
	Item            int64  `json:"item"`
	Found           bool   `json:"found"`
	Entrance        string `json:"entrance"`
	ItemFlags       int    `json:"item_flags"`
	Status          int    `json:"status"`
}

// HintsKey builds the read-only storage key holding one slot's hints.
func HintsKey(team, slot int) string {
	return "_read_hints_" + strconv.Itoa(team) + "_" + strconv.Itoa(slot)
}

// ClientStatusKey builds the read-only storage key holding one slot's status.
//
// The value is a bare integer, not a list: the server stores
// client_game_state[team, slot] there directly.
func ClientStatusKey(team, slot int) string {
	return "_read_client_status_" + strconv.Itoa(team) + "_" + strconv.Itoa(slot)
}

// HintsKeyPrefix and ClientStatusKeyPrefix identify the two read-only key
// families this client subscribes to.
const (
	HintsKeyPrefix        = "_read_hints_"
	ClientStatusKeyPrefix = "_read_client_status_"
)

// SlotFromHintsKey reads the slot number back out of a hints key.
func SlotFromHintsKey(key string) (int, bool) {
	return slotFromKey(key, HintsKeyPrefix)
}

// SlotFromClientStatusKey reads the slot number back out of a status key.
func SlotFromClientStatusKey(key string) (int, bool) {
	return slotFromKey(key, ClientStatusKeyPrefix)
}

// slotFromKey parses the trailing "{team}_{slot}" of a read-only key.
func slotFromKey(key, prefix string) (int, bool) {
	if !strings.HasPrefix(key, prefix) {
		return 0, false
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), "_")
	if len(parts) != 2 {
		return 0, false
	}
	slot, err := strconv.Atoi(parts[1])
	return slot, err == nil
}

// ClientStatus values, from NetUtils.ClientStatus. A slot that has never
// connected reads as StatusUnknown, so "not goaled" is the safe default.
const (
	StatusUnknown   = 0
	StatusConnected = 5
	StatusReady     = 10
	StatusPlaying   = 20
	StatusGoal      = 30
)

// SlotType values, from NetUtils.SlotType. Only SlotPlayer slots are real
// players: group slots exist for item links and never reach a goal, so
// counting them would mean a multiworld could never complete.
const (
	SlotSpectator = 0
	SlotPlayer    = 1
	SlotGroup     = 2
)

// HintStatus values, from NetUtils.HintStatus.
const (
	HintUnspecified = 0
	HintNoPriority  = 10
	HintAvoid       = 20
	HintPriority    = 30
	HintFound       = 40
)

// hintStatusNames mirrors NetUtils.status_names, so a hint rendered from
// storage reads exactly like one the server sent.
var hintStatusNames = map[int]string{
	HintFound:       "(found)",
	HintUnspecified: "(unspecified)",
	HintNoPriority:  "(no priority)",
	HintAvoid:       "(avoid)",
	HintPriority:    "(priority)",
}

// HintStatusName renders a hint status the way the server does.
func HintStatusName(status int) string {
	if name, ok := hintStatusNames[status]; ok {
		return name
	}
	return "(unknown)"
}