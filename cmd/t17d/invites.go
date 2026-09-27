package main

import "log"

// The invite and membership operations, each of which has to tell the other players about itself.
// The message numbers are the session system's, from the dispatcher's jump table at 0x6821a0.

func (c *client) listInvites() []any {
	stored := c.s.sessions.invitesFor(c.playerID())
	out := make([]any, 0, len(stored))
	for _, in := range stored {
		out = append(out, map[string]any{
			"sessionId":          in.SessionID,
			"inviter":            in.Inviter,
			"invitee":            in.Invitee,
			"type":               in.Type,
			"supportedPlatforms": in.Platforms,
		})
	}

	return out
}

func (c *client) sendInvite(in map[string]any) map[string]any {
	sessionID, _ := in["sessionId"].(string)
	invitee, _ := in["inviteeId"].(string)
	if invitee == "" {
		invitee, _ = in["invitee"].(string)
	}
	inviter := c.playerID()
	s := c.s.sessions.get(sessionID)
	if s == nil || invitee == "" {
		log.Printf("[t17] send-invite: no session %q or no invitee (%q)", sessionID, invitee)

		return map[string]any{}
	}

	rec := invite{SessionID: s.ID, Inviter: inviter, Invitee: invitee, Type: s.Type, Platforms: s.Platforms}
	c.s.sessions.addInvite(rec)

	payload := map[string]any{
		"sessionId":          s.ID,
		"inviter":            inviter,
		"invitee":            invitee,
		"type":               s.Type,
		"supportedPlatforms": s.Platforms,
	}
	c.s.push(invitee, inviter, msgInviteReceived, payload)
	c.s.push(inviter, inviter, msgInviteSent, payload)

	return map[string]any{"sessionId": s.ID, "invitee": invitee, "type": s.Type}
}

func (c *client) acceptInvite(in map[string]any) map[string]any {
	sessionID, _ := in["sessionId"].(string)
	me := c.playerID()
	rec, had := c.s.sessions.takeInvite(me, sessionID)
	if sessionID == "" {
		sessionID = rec.SessionID
	}
	s, existing := c.s.sessions.join(sessionID, me)
	if s == nil {
		log.Printf("[t17] accept-invite: %s has no session %q (invite found: %v)", me, sessionID, had)

		return map[string]any{}
	}

	c.s.pushAll(existing, me, me, msgPlayerJoined, map[string]any{
		"sessionId": s.ID,
		"player":    c.playerOf(me),
	})
	if rec.Inviter != "" {
		c.s.push(rec.Inviter, me, msgInviteAccepted, map[string]any{"sessionId": s.ID, "invitee": me})
	}

	return map[string]any{"session": c.wire(s)}
}

func (c *client) updateSession(in map[string]any) map[string]any {
	sessionID, _ := in["sessionId"].(string)
	s := c.s.sessions.get(sessionID)
	if s == nil {
		return map[string]any{}
	}
	if chunk, ok := in["chunk"]; ok {
		s.Chunk = chunk
	}
	if n, ok := in["maxPlayers"].(float64); ok && n >= 1 {
		s.MaxPlayers = int(n)
	}
	if v, ok := in["membersCanInviteOthers"].(bool); ok {
		s.CanInvite = v
	}
	if v, ok := in["closedToNewPlayers"].(bool); ok {
		s.Closed = v
	}
	if v, ok := in["supportedPlatforms"]; ok {
		s.Platforms = v
	}
	c.s.sessions.put(s)

	me := c.playerID()
	c.s.pushAll(s.Players, me, me, msgSessionUpdated, map[string]any{
		"sessionId":              s.ID,
		"chunk":                  s.Chunk,
		"maxPlayers":             s.MaxPlayers,
		"membersCanInviteOthers": s.CanInvite,
		"closedToNewPlayers":     s.Closed,
		"supportedPlatforms":     s.Platforms,
	})

	return c.wire(s)
}

func (c *client) leaveSession(in map[string]any) map[string]any {
	sessionID, _ := in["sessionId"].(string)
	me := c.playerID()
	s, remaining := c.s.sessions.leave(sessionID, me)
	if s == nil {
		return map[string]any{}
	}
	c.s.pushAll(remaining, me, me, msgPlayerLeft, map[string]any{
		"sessionId":      s.ID,
		"globalPlayerId": me,
		"reason":         "left",
	})

	return map[string]any{}
}

func (c *client) closeSession(in map[string]any) map[string]any {
	sessionID, _ := in["sessionId"].(string)
	me := c.playerID()
	s := c.s.sessions.get(sessionID)
	if s == nil {
		return map[string]any{}
	}
	c.s.pushAll(s.Players, me, me, msgSessionClosed, map[string]any{"sessionId": s.ID})
	for _, p := range append([]string(nil), s.Players...) {
		c.s.sessions.leave(s.ID, p)
	}

	return map[string]any{}
}

func (c *client) rejectInvite(in map[string]any) map[string]any {
	sessionID, _ := in["sessionId"].(string)
	me := c.playerID()
	rec, _ := c.s.sessions.takeInvite(me, sessionID)
	if rec.Inviter != "" {
		c.s.push(rec.Inviter, me, msgInviteRejected, map[string]any{
			"sessionId": rec.SessionID, "invitee": me, "type": rec.Type,
		})
	}

	return map[string]any{}
}

func (c *client) withdrawInvite(in map[string]any) map[string]any {
	sessionID, _ := in["sessionId"].(string)
	invitee, _ := in["inviteeId"].(string)
	if invitee == "" {
		invitee, _ = in["invitee"].(string)
	}
	me := c.playerID()
	if _, ok := c.s.sessions.takeInvite(invitee, sessionID); ok {
		c.s.push(invitee, me, msgInviteWithdrawn, map[string]any{
			"sessionId": sessionID, "inviter": me, "invitee": invitee,
		})
	}

	return map[string]any{}
}
