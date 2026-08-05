package app

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
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

// updateSharedBookHandler merges the pusher's snapshot into the stored payload
// instead of blindly overwriting it. Without this, two members editing the same
// book concurrently would clobber each other's records (last-write-wins on the
// whole blob). Records are merged by id (incoming wins), explicit deletedIds are
// removed, and book members are unioned by id so a concurrently-added member is
// not lost — except ids listed in deletedMemberIds, which are removed same as
// deletedIds for records.
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

	// Load the existing payload (if any).
	var existingRaw []byte
	err := dbPool.QueryRow(ctx, "SELECT payload FROM shared_spaces WHERE code = $1", code).Scan(&existingRaw)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Shared book not found"})
		return
	}
	var existing map[string]interface{}
	if len(existingRaw) > 0 {
		_ = json.Unmarshal(existingRaw, &existing)
	}

	merged := mergeSharedPayload(existing, incoming)

	res, err := dbPool.Exec(ctx,
		"UPDATE shared_spaces SET payload = $1, updated_at = $2 WHERE code = $3",
		merged, time.Now(), code)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update shared book"})
		return
	}
	if res.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Shared book not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// mergeSharedPayload merges an incoming {book, records, deletedIds} snapshot into
// the existing {book, records} payload and returns the merged {book, records}.
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

	// --- Merge records by id (incoming wins), then drop deletedIds. ---
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

	if deleted, ok := incoming["deletedIds"].([]interface{}); ok {
		for _, d := range deleted {
			if id, ok := d.(string); ok {
				delete(recordByID, id)
			}
		}
	}

	records := make([]interface{}, 0, len(order))
	for _, id := range order {
		if r, ok := recordByID[id]; ok {
			records = append(records, r)
		}
	}

	// --- Book: take the incoming book, union members by id with the existing. ---
	var book map[string]interface{}
	if b, ok := incoming["book"].(map[string]interface{}); ok {
		book = b
	} else if b, ok := existing["book"].(map[string]interface{}); ok {
		book = b
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
		if eb, ok := existing["book"].(map[string]interface{}); ok {
			addMembers(asSlice(eb, "members"))
		}
		addMembers(asSlice(book, "members"))

		if deletedMembers, ok := incoming["deletedMemberIds"].([]interface{}); ok {
			for _, d := range deletedMembers {
				if id, ok := d.(string); ok {
					delete(memberByID, id)
				}
			}
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
		"book":    book,
		"records": records,
	}
}
