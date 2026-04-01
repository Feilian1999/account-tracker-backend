package app

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// normalizeTimestamp attempts to convert non-standard timestamp strings 
// (like Go's default time.String() format) into RFC3339 which PostgreSQL prefers.
func normalizeTimestamp(ts string) string {
	if ts == "" {
		return ts
	}
	// If it already parses as RFC3339, it's good.
	if _, err := time.Parse(time.RFC3339, ts); err == nil {
		return ts
	}

	// Go's default .String() format is "2006-01-02 15:04:05.999999999 -0700 MST"
	// The trailing " MST" (zone name) can cause parsing issues if ambiguous.
	// We'll normalize by taking only the first three parts (Date, Time, Offset).
	parts := strings.Fields(ts)
	if len(parts) < 3 {
		return ts
	}
	
	cleanTS := strings.Join(parts[:3], " ")
	layouts := []string{
		"2006-01-02 15:04:05.999999999 -0700",
		"2006-01-02 15:04:05.999 -0700",
		"2006-01-02 15:04:05.99 -0700",
		"2006-01-02 15:04:05.9 -0700",
		"2006-01-02 15:04:05 -0700",
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, cleanTS); err == nil {
			return t.Format(time.RFC3339)
		}
	}
	return ts
}

// normalizeDate specifically returns YYYY-MM-DD for DATE columns.
func normalizeDate(d string) string {
	if d == "" || (len(d) == 10 && d[4] == '-' && d[7] == '-') {
		return d
	}
	ts := normalizeTimestamp(d)
	if len(ts) >= 10 && ts[4] == '-' && ts[7] == '-' {
		return ts[:10]
	}
	return d
}

type SyncMember struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SyncBook struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Members   []SyncMember `json:"members"`
	CreatedAt string       `json:"createdAt"`
}

type SyncRecord struct {
	ID            string   `json:"id"`
	BookID        string   `json:"bookId"`
	Type          string   `json:"type"`
	Amount        float64  `json:"amount"`
	Category      string   `json:"category"`
	Date          string   `json:"date"`
	Note          string   `json:"note"`
	PaidByID      string   `json:"paidById"`
	SplitAmongIds []string `json:"splitAmongIds"`
}

type SyncPersonalRecord struct {
	ID           string  `json:"id"`
	Type         string  `json:"type"`
	Amount       float64 `json:"amount"`
	Category     string  `json:"category"`
	Date         string  `json:"date"`
	Note         string  `json:"note"`
	SourceBookID string  `json:"sourceBookId"`
}

type SyncCategory struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Icon      string `json:"icon"`
	Color     string `json:"color"`
	IsDefault bool   `json:"isDefault"`
}

type SyncTemplate struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Amount   *float64 `json:"amount"`
	Category string   `json:"category"`
	Note     string   `json:"note"`
}

type SyncData struct {
	Books           []SyncBook           `json:"books"`
	Records         []SyncRecord         `json:"records"`
	PersonalRecords []SyncPersonalRecord `json:"personal_records"`
	Categories      []SyncCategory       `json:"categories"`
	Templates       []SyncTemplate       `json:"templates"`
}

func pushSyncHandler(c *gin.Context) {
	googleID := c.GetString("user_google_id")
	if googleID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not identified"})
		return
	}

	var data SyncData
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if dbPool == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database not connected"})
		return
	}

	ctx := context.Background()
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback(ctx)

	// Get Internal User ID
	var userID string
	err = tx.QueryRow(ctx, "SELECT id FROM users WHERE google_id = $1", googleID).Scan(&userID)
	if err != nil {
		log.Printf("[Sync] User lookup failed for google_id %s: %v\n", googleID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found in DB"})
		return
	}

	// 1. Clear existing data for this user
	// We delete books first, cascade will handle book_members and records
	if _, err := tx.Exec(ctx, "DELETE FROM books WHERE user_id = $1", userID); err != nil {
		log.Printf("[Sync] Failed to clear books: %v\n", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM personal_records WHERE user_id = $1", userID); err != nil {
		log.Printf("[Sync] Failed to clear personal_records: %v\n", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM record_templates WHERE user_id = $1", userID); err != nil {
		log.Printf("[Sync] Failed to clear templates: %v\n", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM categories WHERE user_id = $1", userID); err != nil {
		log.Printf("[Sync] Failed to clear categories: %v\n", err)
	}

	// 2. Insert new state
	// Insert Categories
	for _, cat := range data.Categories {
		_, err = tx.Exec(ctx,
			"INSERT INTO categories (id, user_id, name, type, icon, color, is_default) VALUES ($1, $2, $3, $4, $5, $6, $7)",
			cat.ID, userID, cat.Name, cat.Type, cat.Icon, cat.Color, cat.IsDefault)
		if err != nil {
			log.Printf("[Sync] Category insert error: %v\n", err)
			insertError(c, "categories", err)
			return
		}
	}

	// Insert Books & Members
	for _, book := range data.Books {
		createdAt := normalizeTimestamp(book.CreatedAt)
		_, err = tx.Exec(ctx, "INSERT INTO books (id, user_id, name, created_at) VALUES ($1, $2, $3, $4)",
			book.ID, userID, book.Name, createdAt)
		if err != nil {
			log.Printf("[Sync] Book insert error: %v\n", err)
			insertError(c, "books", err)
			return
		}

		for _, m := range book.Members {
			_, err = tx.Exec(ctx, "INSERT INTO book_members (id, book_id, name) VALUES ($1, $2, $3)",
				m.ID, book.ID, m.Name)
			if err != nil {
				log.Printf("[Sync] Member insert error: %v\n", err)
				insertError(c, "book_members", err)
				return
			}
		}
	}

	// Insert Shared Records
	for _, rec := range data.Records {
		date := normalizeDate(rec.Date)
		_, err = tx.Exec(ctx,
			"INSERT INTO records (id, book_id, type, amount, category, date, note, paid_by_id, split_among_ids) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)",
			rec.ID, rec.BookID, rec.Type, rec.Amount, rec.Category, date, rec.Note, rec.PaidByID, rec.SplitAmongIds)
		if err != nil {
			log.Printf("[Sync] Record insert error: %v\n", err)
			insertError(c, "records", err)
			return
		}
	}

	// Insert Personal Records
	for _, rec := range data.PersonalRecords {
		var sourceBookID *string
		if rec.SourceBookID != "" {
			sourceBookID = &rec.SourceBookID
		}
		date := normalizeDate(rec.Date)
		_, err = tx.Exec(ctx,
			"INSERT INTO personal_records (id, user_id, type, amount, category, date, note, source_book_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
			rec.ID, userID, rec.Type, rec.Amount, rec.Category, date, rec.Note, sourceBookID)
		if err != nil {
			log.Printf("[Sync] Personal record insert error: %v\n", err)
			insertError(c, "personal_records", err)
			return
		}
	}

	// Insert Templates
	for _, tpl := range data.Templates {
		_, err = tx.Exec(ctx,
			"INSERT INTO record_templates (id, user_id, name, type, amount, category, note) VALUES ($1, $2, $3, $4, $5, $6, $7)",
			tpl.ID, userID, tpl.Name, tpl.Type, tpl.Amount, tpl.Category, tpl.Note)
		if err != nil {
			log.Printf("[Sync] Template insert error: %v\n", err)
			insertError(c, "templates", err)
			return
		}
	}

	err = tx.Commit(ctx)
	if err != nil {
		log.Printf("[Sync] Transaction commit error: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Data synced to cloud successfully"})
}

func pullSyncHandler(c *gin.Context) {
	googleID := c.GetString("user_google_id")
	if googleID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not identified"})
		return
	}

	if dbPool == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database not connected"})
		return
	}

	ctx := context.Background()

	// Get Internal User ID
	var userID string
	err := dbPool.QueryRow(ctx, "SELECT id FROM users WHERE google_id = $1", googleID).Scan(&userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
		return
	}

	data := SyncData{
		Books:           []SyncBook{},
		Records:         []SyncRecord{},
		PersonalRecords: []SyncPersonalRecord{},
		Categories:      []SyncCategory{},
		Templates:       []SyncTemplate{},
	}

	// Fetch Categories
	rows, _ := dbPool.Query(ctx, "SELECT id, name, type, icon, color, is_default FROM categories WHERE user_id = $1", userID)
	for rows.Next() {
		var item SyncCategory
		_ = rows.Scan(&item.ID, &item.Name, &item.Type, &item.Icon, &item.Color, &item.IsDefault)
		data.Categories = append(data.Categories, item)
	}

	// Fetch Books
	rows, _ = dbPool.Query(ctx, "SELECT id, name, created_at FROM books WHERE user_id = $1", userID)
	for rows.Next() {
		var item SyncBook
		var createdAt time.Time
		_ = rows.Scan(&item.ID, &item.Name, &createdAt)
		item.CreatedAt = createdAt.Format(time.RFC3339)

		// Fetch Members for each book
		mRows, _ := dbPool.Query(ctx, "SELECT id, name FROM book_members WHERE book_id = $1", item.ID)
		item.Members = []SyncMember{}
		for mRows.Next() {
			var m SyncMember
			_ = mRows.Scan(&m.ID, &m.Name)
			item.Members = append(item.Members, m)
		}
		data.Books = append(data.Books, item)
	}

	// Fetch Shared Records
	rows, _ = dbPool.Query(ctx, `
		SELECT r.id, r.book_id, r.type, r.amount, r.category, r.date, r.note, r.paid_by_id, r.split_among_ids 
		FROM records r 
		JOIN books b ON r.book_id = b.id 
		WHERE b.user_id = $1`, userID)
	for rows.Next() {
		var item SyncRecord
		var date time.Time
		_ = rows.Scan(&item.ID, &item.BookID, &item.Type, &item.Amount, &item.Category, &date, &item.Note, &item.PaidByID, &item.SplitAmongIds)
		item.Date = date.Format("2006-01-02")
		data.Records = append(data.Records, item)
	}

	// Fetch Personal Records
	rows, _ = dbPool.Query(ctx, "SELECT id, type, amount, category, date, note, COALESCE(source_book_id::text, '') FROM personal_records WHERE user_id = $1", userID)
	for rows.Next() {
		var item SyncPersonalRecord
		var date time.Time
		_ = rows.Scan(&item.ID, &item.Type, &item.Amount, &item.Category, &date, &item.Note, &item.SourceBookID)
		item.Date = date.Format("2006-01-02")
		data.PersonalRecords = append(data.PersonalRecords, item)
	}

	// Fetch Templates
	rows, _ = dbPool.Query(ctx, "SELECT id, name, type, amount, category, note FROM record_templates WHERE user_id = $1", userID)
	for rows.Next() {
		var item SyncTemplate
		_ = rows.Scan(&item.ID, &item.Name, &item.Type, &item.Amount, &item.Category, &item.Note)
		data.Templates = append(data.Templates, item)
	}

	c.JSON(http.StatusOK, data)
}

func pushSyncByUUIDHandler(c *gin.Context) {
	// Since SyncData is a struct, we manually bind to a wrapper
	var wrapper struct {
		UUID            string               `json:"uuid"`
		Books           []SyncBook           `json:"books"`
		Records         []SyncRecord         `json:"records"`
		PersonalRecords []SyncPersonalRecord `json:"personal_records"`
		Categories      []SyncCategory       `json:"categories"`
		Templates       []SyncTemplate       `json:"templates"`
	}

	if err := c.ShouldBindJSON(&wrapper); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if wrapper.UUID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "UUID is required"})
		return
	}

	if dbPool == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database not connected"})
		return
	}

	ctx := context.Background()

	// Ensure user exists or create anonymous
	var userID string
	var googleID *string
	err := dbPool.QueryRow(ctx, "SELECT id, google_id FROM users WHERE id = $1", wrapper.UUID).Scan(&userID, &googleID)
	
	if err == nil {
		// User exists. If they have a Google ID, they must use authenticated sync.
		if googleID != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "This account is linked to Google. Please login to sync."})
			return
		}
		// Existing anonymous user
	} else {
		// Create anonymous user
		_, err = dbPool.Exec(ctx, "INSERT INTO users (id, name, email) VALUES ($1, $2, $3)", wrapper.UUID, "Anonymous", wrapper.UUID+"@anonymous.local")
		if err != nil {
			log.Printf("[Sync] Failed to create anonymous user: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user entry"})
			return
		}
		userID = wrapper.UUID
	}

	tx, err := dbPool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback(ctx)

	// Clear existing data
	if _, err := tx.Exec(ctx, "DELETE FROM books WHERE user_id = $1", userID); err != nil {
		log.Printf("[Sync] Failed to clear books: %v\n", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM personal_records WHERE user_id = $1", userID); err != nil {
		log.Printf("[Sync] Failed to clear personal_records: %v\n", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM record_templates WHERE user_id = $1", userID); err != nil {
		log.Printf("[Sync] Failed to clear templates: %v\n", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM categories WHERE user_id = $1", userID); err != nil {
		log.Printf("[Sync] Failed to clear categories: %v\n", err)
	}

	// Insert Categories
	for _, cat := range wrapper.Categories {
		_, err = tx.Exec(ctx,
			"INSERT INTO categories (id, user_id, name, type, icon, color, is_default) VALUES ($1, $2, $3, $4, $5, $6, $7)",
			cat.ID, userID, cat.Name, cat.Type, cat.Icon, cat.Color, cat.IsDefault)
		if err != nil {
			insertError(c, "categories", err)
			return
		}
	}

	// Books & Members
	for _, book := range wrapper.Books {
		createdAt := normalizeTimestamp(book.CreatedAt)
		_, err = tx.Exec(ctx, `
			INSERT INTO books (id, user_id, name, created_at) 
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (id) DO UPDATE SET 
				name = EXCLUDED.name,
				created_at = EXCLUDED.created_at
		`, book.ID, userID, book.Name, createdAt)
		if err != nil {
			insertError(c, "books", err)
			return
		}

		for _, m := range book.Members {
			_, err = tx.Exec(ctx, `
				INSERT INTO book_members (id, book_id, name) 
				VALUES ($1, $2, $3)
				ON CONFLICT (id) DO UPDATE SET
					name = EXCLUDED.name,
					book_id = EXCLUDED.book_id
			`, m.ID, book.ID, m.Name)
			if err != nil {
				insertError(c, "book_members", err)
				return
			}
		}
	}

	// Shared Records
	for _, rec := range wrapper.Records {
		date := normalizeDate(rec.Date)
		_, err = tx.Exec(ctx, `
			INSERT INTO records (id, book_id, type, amount, category, date, note, paid_by_id, split_among_ids) 
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (id) DO UPDATE SET
				type = EXCLUDED.type,
				amount = EXCLUDED.amount,
				category = EXCLUDED.category,
				date = EXCLUDED.date,
				note = EXCLUDED.note,
				paid_by_id = EXCLUDED.paid_by_id,
				split_among_ids = EXCLUDED.split_among_ids
		`, rec.ID, rec.BookID, rec.Type, rec.Amount, rec.Category, date, rec.Note, rec.PaidByID, rec.SplitAmongIds)
		if err != nil {
			insertError(c, "records", err)
			return
		}
	}

	// Personal Records
	for _, rec := range wrapper.PersonalRecords {
		var sourceBookID *string
		if rec.SourceBookID != "" {
			sourceBookID = &rec.SourceBookID
		}
		date := normalizeDate(rec.Date)
		_, err = tx.Exec(ctx,
			"INSERT INTO personal_records (id, user_id, type, amount, category, date, note, source_book_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
			rec.ID, userID, rec.Type, rec.Amount, rec.Category, date, rec.Note, sourceBookID)
		if err != nil {
			insertError(c, "personal_records", err)
			return
		}
	}

	// Templates
	for _, tpl := range wrapper.Templates {
		_, err = tx.Exec(ctx,
			"INSERT INTO record_templates (id, user_id, name, type, amount, category, note) VALUES ($1, $2, $3, $4, $5, $6, $7)",
			tpl.ID, userID, tpl.Name, tpl.Type, tpl.Amount, tpl.Category, tpl.Note)
		if err != nil {
			insertError(c, "templates", err)
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Data backed up to UUID successfully"})
}

func pullSyncByUUIDHandler(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "UUID is required"})
		return
	}

	if dbPool == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database not connected"})
		return
	}

	ctx := context.Background()

	// Get Internal User ID
	var userID string
	err := dbPool.QueryRow(ctx, "SELECT id FROM users WHERE id = $1", uuid).Scan(&userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Backup not found for this UUID"})
		return
	}

	data := SyncData{
		Books:           []SyncBook{},
		Records:         []SyncRecord{},
		PersonalRecords: []SyncPersonalRecord{},
		Categories:      []SyncCategory{},
		Templates:       []SyncTemplate{},
	}

	// Fetch Categories
	rows, _ := dbPool.Query(ctx, "SELECT id, name, type, icon, color, is_default FROM categories WHERE user_id = $1", userID)
	if rows != nil {
		for rows.Next() {
			var item SyncCategory
			_ = rows.Scan(&item.ID, &item.Name, &item.Type, &item.Icon, &item.Color, &item.IsDefault)
			data.Categories = append(data.Categories, item)
		}
		rows.Close()
	}

	// Fetch Books
	rows, _ = dbPool.Query(ctx, "SELECT id, name, created_at FROM books WHERE user_id = $1", userID)
	if rows != nil {
		for rows.Next() {
			var item SyncBook
			var createdAt time.Time
			_ = rows.Scan(&item.ID, &item.Name, &createdAt)
			item.CreatedAt = createdAt.Format(time.RFC3339)

			// Fetch Members for each book
			mRows, _ := dbPool.Query(ctx, "SELECT id, name FROM book_members WHERE book_id = $1", item.ID)
			item.Members = []SyncMember{}
			if mRows != nil {
				for mRows.Next() {
					var m SyncMember
					_ = mRows.Scan(&m.ID, &m.Name)
					item.Members = append(item.Members, m)
				}
				mRows.Close()
			}
			data.Books = append(data.Books, item)
		}
		rows.Close()
	}

	// Fetch Shared Records
	rows, _ = dbPool.Query(ctx, `
		SELECT r.id, r.book_id, r.type, r.amount, r.category, r.date, r.note, r.paid_by_id, r.split_among_ids 
		FROM records r 
		JOIN books b ON r.book_id = b.id 
		WHERE b.user_id = $1`, userID)
	if rows != nil {
		for rows.Next() {
			var item SyncRecord
			var date time.Time
			_ = rows.Scan(&item.ID, &item.BookID, &item.Type, &item.Amount, &item.Category, &date, &item.Note, &item.PaidByID, &item.SplitAmongIds)
			item.Date = date.Format("2006-01-02")
			data.Records = append(data.Records, item)
		}
		rows.Close()
	}

	// Fetch Personal Records
	rows, _ = dbPool.Query(ctx, "SELECT id, type, amount, category, date, note, COALESCE(source_book_id::text, '') FROM personal_records WHERE user_id = $1", userID)
	if rows != nil {
		for rows.Next() {
			var item SyncPersonalRecord
			var date time.Time
			_ = rows.Scan(&item.ID, &item.Type, &item.Amount, &item.Category, &date, &item.Note, &item.SourceBookID)
			item.Date = date.Format("2006-01-02")
			data.PersonalRecords = append(data.PersonalRecords, item)
		}
		rows.Close()
	}

	// Fetch Templates
	rows, _ = dbPool.Query(ctx, "SELECT id, name, type, amount, category, note FROM record_templates WHERE user_id = $1", userID)
	if rows != nil {
		for rows.Next() {
			var item SyncTemplate
			_ = rows.Scan(&item.ID, &item.Name, &item.Type, &item.Amount, &item.Category, &item.Note)
			data.Templates = append(data.Templates, item)
		}
		rows.Close()
	}

	c.JSON(http.StatusOK, data)
}

func insertError(c *gin.Context, table string, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to insert into " + table + ": " + err.Error()})
}
