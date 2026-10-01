package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// v2Response is the body of every successful v2 read/sync. Exactly one of Doc
// (upgraded space) or Legacy (doc IS NULL; version is then 0) is set.
type v2Response struct {
	Version int64           `json:"version"`
	Doc     *Doc            `json:"doc,omitempty"`
	Legacy  json.RawMessage `json:"legacy,omitempty"`
	// LegacyHash fingerprints Legacy. A client upgrading the space sends it
	// back as baseOf, proving its base was converted from the current payload.
	LegacyHash string `json:"legacyHash,omitempty"`
}

// readV2Body reads a ≤4 MB JSON object body into its top-level keys.
func readV2Body(c *gin.Context) (map[string]json.RawMessage, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxV2Body))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "request body exceeds 4 MB"})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		}
		return nil, false
	}
	top, err := parseObject(body, "request body")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	return top, true
}

func marshalDocAndPayload(d Doc) (string, string, error) {
	docJSON, err := json.Marshal(d)
	if err != nil {
		return "", "", err
	}
	payloadJSON, err := json.Marshal(flatten(d))
	if err != nil {
		return "", "", err
	}
	return string(docJSON), string(payloadJSON), nil
}

// POST /api/shared/v2  {"doc": Doc} -> {"code", "version": 1}
func createSharedV2Handler(c *gin.Context) {
	top, ok := readV2Body(c)
	if !ok {
		return
	}
	raw, present := top["doc"]
	if !present {
		c.JSON(http.StatusBadRequest, gin.H{"error": "doc is required"})
		return
	}
	doc, err := parseDoc(raw, "doc")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	stampAll(doc, 1)

	if dbPool == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database not connected"})
		return
	}

	docJSON, payloadJSON, err := marshalDocAndPayload(doc)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encode shared book"})
		return
	}

	ctx := c.Request.Context()
	const insert = "INSERT INTO shared_spaces (code, payload, doc, version, updated_at) VALUES ($1, $2, $3, 1, $4)"

	code, err := generateShareCode()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate code"})
		return
	}
	if _, err = dbPool.Exec(ctx, insert, code, payloadJSON, docJSON, time.Now()); err != nil {
		// Basic collision retry (one attempt), same as v1.
		if code, err = generateShareCode(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate code"})
			return
		}
		if _, err = dbPool.Exec(ctx, insert, code, payloadJSON, docJSON, time.Now()); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save shared book"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"code": code, "version": 1})
}

// decodeStoredDoc parses a doc column written by this server.
func decodeStoredDoc(raw []byte) (Doc, error) {
	var d Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, err
	}
	d.normalize()
	return d, nil
}

// GET /api/shared/v2/:code?since=N
func getSharedV2Handler(c *gin.Context) {
	code := c.Param("code")
	var since int64
	if s := c.Query("since"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "since must be an integer"})
			return
		}
		since = n
	}

	if dbPool == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database not connected"})
		return
	}

	var payloadRaw, docRaw []byte
	var version int64
	err := dbPool.QueryRow(c.Request.Context(),
		"SELECT payload, doc, version FROM shared_spaces WHERE code = $1", code).Scan(&payloadRaw, &docRaw, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Shared book not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read shared book"})
		return
	}

	if isAbsentJSON(docRaw) {
		c.JSON(http.StatusOK, v2Response{Version: 0, Legacy: legacyPayload(payloadRaw), LegacyHash: legacyHash(payloadRaw)})
		return
	}
	doc, err := decodeStoredDoc(docRaw)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Stored shared book is corrupt"})
		return
	}
	filtered := filterSince(doc, since)
	c.JSON(http.StatusOK, v2Response{Version: version, Doc: &filtered})
}

// legacyPayload returns the stored v1 payload, or {} if it is somehow empty,
// so the "legacy" key is always present for a legacy space.
// legacyHash fingerprints a stored v1 payload (hex SHA-256 of the jsonb text,
// whose output form is canonical, so two reads of the same value agree).
func legacyHash(raw []byte) string {
	sum := sha256.Sum256(legacyPayload(raw))
	return hex.EncodeToString(sum[:])
}

// baseMatchesPayload reports whether an upgrade's base may be adopted: there
// is one, and it was converted from exactly the stored payload.
func baseMatchesPayload(base *Doc, baseOf string, payloadRaw []byte) bool {
	return base != nil && baseOf != "" && baseOf == legacyHash(payloadRaw)
}

func legacyPayload(raw []byte) json.RawMessage {
	if isAbsentJSON(raw) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(raw)
}

// POST /api/shared/v2/:code/sync  {"since": N, "changes": Doc, "base"?: Doc}
func syncSharedV2Handler(c *gin.Context) {
	code := c.Param("code")
	top, ok := readV2Body(c)
	if !ok {
		return
	}

	var since int64
	if raw, ok := top["since"]; ok && !isJSONNull(raw) {
		if err := json.Unmarshal(raw, &since); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "since must be an integer"})
			return
		}
	}
	changes := newDoc()
	if raw, ok := top["changes"]; ok && !isJSONNull(raw) {
		var err error
		if changes, err = parseDoc(raw, "changes"); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	var baseOf string
	if raw, ok := top["baseOf"]; ok && !isJSONNull(raw) {
		if err := json.Unmarshal(raw, &baseOf); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "baseOf must be a string"})
			return
		}
	}
	var base *Doc
	if raw, ok := top["base"]; ok && !isJSONNull(raw) {
		b, err := parseDoc(raw, "base")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		base = &b
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

	var payloadRaw, docRaw []byte
	var version int64
	err = tx.QueryRow(ctx,
		"SELECT payload, doc, version FROM shared_spaces WHERE code = $1 FOR UPDATE", code).Scan(&payloadRaw, &docRaw, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Shared book not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read shared book"})
		return
	}

	newVersion := version + 1
	var doc Doc
	changed := false
	if isAbsentJSON(docRaw) {
		// The base must be a conversion of the payload as it is NOW (checked
		// under the row lock). A v1 PUT can land between the client's legacy
		// read and this sync; adopting a base from the older payload would
		// silently drop that write, and the v1 client couldn't resend it (it
		// gets 409 upgrade_required from then on). The client reconverts.
		if !baseMatchesPayload(base, baseOf, payloadRaw) {
			c.JSON(http.StatusConflict, gin.H{"error": "base_required", "legacy": legacyPayload(payloadRaw), "legacyHash": legacyHash(payloadRaw)})
			return
		}
		// Upgrade: the client's deterministic conversion of the legacy payload.
		doc = *base
		stampAll(doc, newVersion)
		changed = true
	} else {
		if doc, err = decodeStoredDoc(docRaw); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Stored shared book is corrupt"})
			return
		}
	}

	doc, ch := mergeDoc(doc, changes, newVersion)
	changed = changed || ch

	if changed {
		if err := checkDocLimits(doc); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		docJSON, payloadJSON, err := marshalDocAndPayload(doc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encode shared book"})
			return
		}
		if _, err := tx.Exec(ctx,
			"UPDATE shared_spaces SET doc = $1, version = $2, payload = $3, updated_at = $4 WHERE code = $5",
			docJSON, newVersion, payloadJSON, time.Now(), code); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update shared book"})
			return
		}
		version = newVersion
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	filtered := filterSince(doc, since)
	c.JSON(http.StatusOK, v2Response{Version: version, Doc: &filtered})
}
