package app

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// Generate a random 8-character alphanumeric code.
// 8 chars over a 32-symbol alphabet ≈ 40 bits, which (with no server-side
// enumeration protection) is a reasonable floor for an unlisted share link.
func generateShareCode() (string, error) {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // Avoid ambiguous chars O/0, I/1
	result := make([]byte, 8)
	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		result[i] = charset[num.Int64()]
	}
	return string(result), nil
}

func shareBookHandler(c *gin.Context) {
	var payload interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if dbPool == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database not connected"})
		return
	}

	ctx := c.Request.Context()

	code, err := generateShareCode()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate code"})
		return
	}

	_, err = dbPool.Exec(ctx,
		"INSERT INTO shared_spaces (code, payload, updated_at) VALUES ($1, $2, $3)",
		code, payload, time.Now())

	if err != nil {
		// Basic collision retry (one attempt)
		code, _ = generateShareCode()
		_, err = dbPool.Exec(ctx,
			"INSERT INTO shared_spaces (code, payload, updated_at) VALUES ($1, $2, $3)",
			code, payload, time.Now())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save shared book"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"code": code})
}

func getSharedBookHandler(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Code is required"})
		return
	}

	if dbPool == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database not connected"})
		return
	}

	var payload interface{}
	err := dbPool.QueryRow(c.Request.Context(),
		"SELECT payload FROM shared_spaces WHERE code = $1", code).Scan(&payload)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Shared book not found"})
		return
	}

	c.JSON(http.StatusOK, payload)
}

// updateSharedBookHandler (v1 PUT) merges the pusher's snapshot into the
// stored payload instead of blindly overwriting it (see mergeSharedPayload).
// The read-merge-write runs in one transaction under a row lock (SELECT … FOR
// UPDATE) so concurrent PUTs cannot lose each other's changes. A space that has
// been upgraded to v2 (doc IS NOT NULL) is read-only for v1: 409
// upgrade_required, nothing written.
func updateSharedBookHandler(c *gin.Context) {
	code := c.Param("code")

	var incoming map[string]interface{}
	if err := c.ShouldBindJSON(&incoming); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if dbPool == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database not connected"})
		return
	}

	ctx := c.Request.Context()

	tx, err := dbPool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	// No-op after a successful Commit; releases the row lock on every other path.
	defer tx.Rollback(ctx)

	var existingRaw, docRaw []byte
	err = tx.QueryRow(ctx, "SELECT payload, doc FROM shared_spaces WHERE code = $1 FOR UPDATE", code).Scan(&existingRaw, &docRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Shared book not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read shared book"})
		return
	}
	if !isAbsentJSON(docRaw) {
		c.JSON(http.StatusConflict, gin.H{"error": "upgrade_required"})
		return
	}

	var existing map[string]interface{}
	if len(existingRaw) > 0 {
		_ = json.Unmarshal(existingRaw, &existing)
	}

	merged := mergeSharedPayload(existing, incoming)

	if _, err := tx.Exec(ctx,
		"UPDATE shared_spaces SET payload = $1, updated_at = $2 WHERE code = $3",
		merged, time.Now(), code); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update shared book"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// mergeSharedPayload merges an incoming {book, records, deletedIds,
// deletedMemberIds} snapshot into the existing {book, records, deletedIds,
// deletedMemberIds} payload and returns a new merged payload of the same shape;
// neither input map is modified. Tombstones are persistent: the result's
// deletedIds / deletedMemberIds are the union of stored and incoming, and any
// record / member whose id is tombstoned is dropped — including ones in the
// incoming snapshot, so a stale client cannot resurrect them.
func mergeSharedPayload(existing, incoming map[string]interface{}) map[string]interface{} {
	idOf := func(item interface{}) (string, bool) {
		m, ok := item.(map[string]interface{})
		if !ok {
			return "", false
		}
		id, ok := m["id"].(string)
		return id, ok && id != ""
	}
	asSlice := func(m map[string]interface{}, key string) []interface{} {
		if m == nil {
			return nil
		}
		if s, ok := m[key].([]interface{}); ok {
			return s
		}
		return nil
	}

	// --- Tombstones: union of stored and incoming (stored order first). ---
	unionIDs := func(key string) ([]interface{}, map[string]bool) {
		set := map[string]bool{}
		list := []interface{}{}
		for _, src := range []map[string]interface{}{existing, incoming} {
			for _, d := range asSlice(src, key) {
				if id, ok := d.(string); ok && id != "" && !set[id] {
					set[id] = true
					list = append(list, id)
				}
			}
		}
		return list, set
	}
	deletedIDs, deletedSet := unionIDs("deletedIds")
	deletedMemberIDs, deletedMemberSet := unionIDs("deletedMemberIds")

	// --- Merge records by id (incoming wins), then drop tombstoned ids. ---
	recordByID := map[string]interface{}{}
	order := []string{}
	appendRecords := func(items []interface{}) {
		for _, it := range items {
			id, ok := idOf(it)
			if !ok {
				continue
			}
			if _, seen := recordByID[id]; !seen {
				order = append(order, id)
			}
			recordByID[id] = it
		}
	}
	appendRecords(asSlice(existing, "records"))
	appendRecords(asSlice(incoming, "records"))

	for id := range deletedSet {
		delete(recordByID, id)
	}

	records := make([]interface{}, 0, len(order))
	for _, id := range order {
		if r, ok := recordByID[id]; ok {
			records = append(records, r)
		}
	}

	// --- Book: shallow field-level merge, then union members by id. ---
	// Start from a copy of the stored book and overlay every key the incoming
	// book carries (incoming wins for present keys). A key the pusher does not
	// send is kept, so an older client that doesn't know e.g. book.currency
	// cannot erase it. Copies only: the input maps are never mutated.
	existingBook, _ := existing["book"].(map[string]interface{})
	incomingBook, _ := incoming["book"].(map[string]interface{})
	var book map[string]interface{}
	if existingBook != nil || incomingBook != nil {
		book = make(map[string]interface{}, len(existingBook)+len(incomingBook))
		for k, v := range existingBook {
			book[k] = v
		}
		for k, v := range incomingBook {
			book[k] = v
		}
	}
	if book != nil {
		memberByID := map[string]interface{}{}
		memberOrder := []string{}
		addMembers := func(items []interface{}) {
			for _, it := range items {
				id, ok := idOf(it)
				if !ok {
					continue
				}
				if _, seen := memberByID[id]; !seen {
					memberOrder = append(memberOrder, id)
				}
				memberByID[id] = it
			}
		}
		addMembers(asSlice(existingBook, "members"))
		addMembers(asSlice(incomingBook, "members"))

		for id := range deletedMemberSet {
			delete(memberByID, id)
		}

		if len(memberOrder) > 0 {
			members := make([]interface{}, 0, len(memberOrder))
			for _, id := range memberOrder {
				if m, ok := memberByID[id]; ok {
					members = append(members, m)
				}
			}
			book["members"] = members
		}
	}

	return map[string]interface{}{
		"book":             book,
		"records":          records,
		"deletedIds":       deletedIDs,
		"deletedMemberIds": deletedMemberIDs,
	}
}
