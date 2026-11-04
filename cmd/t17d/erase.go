package main

// Account erasure (openpak/prds#120). DELETE /internal/accounts/{account_id}/data with
// `Authorization: Bearer <ACCOUNT_ERASE_KEY>` removes everything this service holds for the
// OpenPak account. The service keys its players by the BAAS user id (the account's "switch" link
// subject) and the global player id made from it, so the core's links are asked for the subject.
// The endpoint is mounted on the health listener (container network only). Unset key: 503.

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	accountv1 "github.com/openpak/account-api/openpak/account/v1"
)

const eraseService = "t17"

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// linkLister answers which console ids the core still holds for an account.
type linkLister interface {
	SwitchSubjects(ctx context.Context, accountID string) ([]string, error)
}

type coreLinks struct{ links accountv1.LinksClient }

func newCoreLinks(addr, key string) (*coreLinks, error) {
	if len(key) < 32 {
		return nil, errors.New("ACCOUNT_INTERNAL_KEY must be set (>=32 bytes)")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any,
			cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			return invoker(metadata.AppendToOutgoingContext(ctx, "x-internal-key", key), method, req, reply, cc, opts...)
		}))
	if err != nil {
		return nil, err
	}
	return &coreLinks{links: accountv1.NewLinksClient(conn)}, nil
}

func (c *coreLinks) SwitchSubjects(ctx context.Context, accountID string) ([]string, error) {
	resp, err := c.links.ListAccountLinks(ctx, &accountv1.ListAccountLinksRequest{AccountId: accountID, Namespace: "switch"})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range resp.GetLinks() {
		if l.GetNamespace() == "switch" && l.GetSubjectId() != "" {
			out = append(out, l.GetSubjectId())
		}
	}
	return out, nil
}

// eraseBacking is the Postgres side of the erase.
type eraseBacking interface {
	deleteAccounts(ctx context.Context, baas []string) (int, error)
}

func (p *pgStore) deleteAccounts(ctx context.Context, baas []string) (int, error) {
	tag, err := p.pool.Exec(ctx, `DELETE FROM t17_accounts WHERE baas_user_id = ANY($1)`, baas)
	return int(tag.RowsAffected()), err
}

// Erase removes every trace of the account's players. Idempotent: a second run finds nothing.
func (s *server) Erase(ctx context.Context, accountID string) (int, error) {
	if s.links == nil {
		return 0, errors.New("account core not configured")
	}
	subs, err := s.links.SwitchSubjects(ctx, accountID)
	if err != nil {
		return 0, err
	}
	rows := 0
	dbCounted := false
	for _, sub := range subs {
		gid := globalID(sub)
		// Durable: the id map. Only a store that is attached holds rows; otherwise memory is all there is.
		if db, ok := s.accounts.backing().(eraseBacking); ok && db != nil {
			n, err := db.deleteAccounts(ctx, []string{sub})
			if err != nil {
				return rows, err
			}
			rows += n
			dbCounted = true
		}
		if s.accounts.forget(gid) && !dbCounted {
			rows++
		}
		if s.names.forget(gid) {
			rows++
		}
		if pool := s.storage.dbPool(); pool != nil {
			n, err := eraseDurable(ctx, pool, gid)
			if err != nil {
				return rows, err
			}
			rows += n
		}
		rows += s.storage.forgetOwner(gid)
		rows += s.sessions.forgetPlayer(gid)
		s.conns.drop(gid)
	}
	log.Printf("account erased account=%s rows=%d service=%s", accountID, rows, eraseService)
	return rows, nil
}

func (b *accountStore) backing() accountBacking {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.back
}

func (b *accountStore) forget(gid string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.by[gid]
	delete(b.by, gid)
	delete(b.pending, gid)
	return ok
}

func (n *names) forget(id string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.m[id]
	delete(n.m, id)
	return ok
}

// forgetOwner drops every storage slot whose owner is the player (the key is role, game, owner, slot).
func (st *storageStore) forgetOwner(gid string) int {
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	for k := range st.values {
		if parts := strings.Split(k, "\x00"); len(parts) >= 3 && parts[2] == gid {
			delete(st.values, k)
			n++
		}
	}
	return n
}

// forgetPlayer removes the player from every session, deletes the sessions left empty and the
// invites to or from them.
func (st *sessionStore) forgetPlayer(gid string) int {
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	for id, s := range st.byID {
		kept := s.Players[:0]
		for _, p := range s.Players {
			if p != gid {
				kept = append(kept, p)
			}
		}
		if len(kept) != len(s.Players) {
			n++
		}
		s.Players = kept
		if len(kept) == 0 {
			delete(st.byID, id)
		}
	}
	if _, ok := st.invites[gid]; ok {
		delete(st.invites, gid)
		n++
	}
	for invitee, list := range st.invites {
		keep := list[:0]
		for _, in := range list {
			if in.Inviter != gid {
				keep = append(keep, in)
			} else {
				n++
			}
		}
		st.invites[invitee] = keep
	}
	return n
}

func (cs *connections) drop(id string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.m, id)
}

// eraseHandler serves DELETE /internal/accounts/{account_id}/data.
func (s *server) eraseHandler(key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if key == "" {
			writeJSON(w, 503, map[string]any{"error": "erase not configured"})
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == r.Header.Get("Authorization") || subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
			writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		id := r.PathValue("account_id")
		if !uuidRe.MatchString(id) {
			writeJSON(w, 400, map[string]any{"error": "account_id must be a UUID"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		rows, err := s.Erase(ctx, strings.ToLower(id))
		if err != nil {
			log.Printf("[t17] erase failed: %v", err)
			writeJSON(w, 500, map[string]any{"error": "erase failed"})
			return
		}
		writeJSON(w, 200, map[string]any{"service": eraseService, "rows": rows})
	}
}

// setupErase wires the core client from the environment and mounts the endpoint on mux.
func (s *server) setupErase(mux *http.ServeMux) {
	key := os.Getenv("ACCOUNT_ERASE_KEY")
	addr := os.Getenv("ACCOUNT_GRPC_ADDR")
	if addr == "" {
		addr = "openpak-account:20001"
	}
	if key != "" {
		l, err := newCoreLinks(addr, os.Getenv("ACCOUNT_INTERNAL_KEY"))
		if err != nil {
			log.Printf("[t17] ERROR erase endpoint cannot reach the account core: %v", err)
		} else {
			s.links = l
		}
	}
	mux.HandleFunc("DELETE /internal/accounts/{account_id}/data", s.eraseHandler(key))
}
