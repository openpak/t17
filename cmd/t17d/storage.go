package main

import (
	"strings"
	"sync"
)

// Player storage: /storage/put remembers a value, /storage/get gives it back. The game keeps small
// per-slot blobs here (RecentlyMetPlayers_v1 is the one it fetches at boot), base64 of its own
// LitJSON, and it is the game's data — we never interpret it, only hold it.
//
// ponytail: in memory, so a restart forgets it. That is survivable for blobs the game rebuilds
// anyway; move it to Postgres alongside sessions if a slot ever has to outlive the process.
type storageStore struct {
	mu     sync.Mutex
	values map[string]string
}

// The slot's identity, exactly the fields T17BE_StorageSystem sends on every storage request.
func storageKey(in map[string]any, player string) string {
	text := func(k string) string { s, _ := in[k].(string); return s }
	role := text("dataRole")
	if role == "" {
		role = "client"
	}
	return strings.Join([]string{role, text("gameId"), owner(in, player), text("slot")}, "\x00")
}

// Whose data it is: the request names a globalPlayerId, but a client may only reach its own.
func owner(in map[string]any, player string) string {
	if id, _ := in["globalPlayerId"].(string); id != "" {
		return id
	}
	return player
}

func (st *storageStore) get(key string) (string, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	value, ok := st.values[key]
	return value, ok
}

func (st *storageStore) put(key, value string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if st.values == nil {
		st.values = map[string]string{}
	}
	st.values[key] = value
}

func (st *storageStore) delete(key string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	delete(st.values, key)
}

// emptySlot is what a slot nobody has written answers with: base64 of "{}", a well-formed empty
// object for the game's own LitJSON to read.
//
// It is NOT a 404. A 404 maps to the client's error 5 ("Resource Not Exist"), and while
// OnDownloadRecentlyMetPlayersCallback treats that as "nothing stored" and carries on, the native
// T17BE_StorageSystem's own boot fetch does not: measured on Moving Out 2, the game read the 404 and
// tore its T17 socket down 100 ms later with "An error has occurred connecting to T17 services" —
// every FIRST launch, while the launch after (whose init is already past that step) worked. An
// earlier build answered 200 with `value: ""`, which is empty rather than well-formed and sent the
// game down its has-data path with nothing in it. A valid empty object is neither.
const emptySlot = "e30="

func (c *client) storageGet(in map[string]any) map[string]any {
	if value, ok := c.s.storage.get(storageKey(in, c.playerID())); ok {
		return map[string]any{"value": value}
	}
	return map[string]any{"value": emptySlot}
}

func (c *client) storagePut(in map[string]any) map[string]any {
	value, _ := in["value"].(string)
	c.s.storage.put(storageKey(in, c.playerID()), value)

	return map[string]any{}
}

func (c *client) storageDelete(in map[string]any) map[string]any {
	c.s.storage.delete(storageKey(in, c.playerID()))

	return map[string]any{}
}
