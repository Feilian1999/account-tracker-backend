package app

import (
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
	ID     string `json:"id"`
	Name   string `json:"name"`
	UserID string `json:"userId"`
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

	ctx := c.Request.Context()

	tx, err := dbPool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback(ctx)

	// Ensure the user row exists (create anonymous account on first backup).
	// Done inside the transaction so a later failure does not leave an orphan user.
	userID := wrapper.UUID
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, name, email) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING
	`, userID, "Anonymous", userID+"@anonymous.local"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user entry"})
		return
	}

	// Clear existing data — delete child tables first to avoid FK cascade issues.
	// A failed clear must abort the whole push, otherwise stale rows would survive
	// the "full replace" and mix with the newly inserted state.
	clears := []string{
		"DELETE FROM records WHERE book_id IN (SELECT id FROM books WHERE user_id = $1)",
		"DELETE FROM book_members WHERE book_id IN (SELECT id FROM books WHERE user_id = $1)",
		"DELETE FROM books WHERE user_id = $1",
		"DELETE FROM personal_records WHERE user_id = $1",
		"DELETE FROM record_templates WHERE user_id = $1",
		"DELETE FROM categories WHERE user_id = $1",
	}
	for _, q := range clears {
		if _, err := tx.Exec(ctx, q, userID); err != nil {
			insertError(c, "clear", err)
			return
		}
	}

	// Insert Categories
	for _, cat := range wrapper.Categories {
		_, err = tx.Exec(ctx, `
			INSERT INTO categories (id, user_id, name, type, icon, color, is_default)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				type = EXCLUDED.type,
				icon = EXCLUDED.icon,
				color = EXCLUDED.color,
				is_default = EXCLUDED.is_default
			WHERE categories.user_id = $2
		`, cat.ID, userID, cat.Name, cat.Type, cat.Icon, cat.Color, cat.IsDefault)
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
			WHERE books.user_id = $2
		`, book.ID, userID, book.Name, createdAt)
		if err != nil {
			insertError(c, "books", err)
			return
		}

		for _, m := range book.Members {
			var mUserID *string
			if m.UserID != "" {
				mUserID = &m.UserID
				_, _ = tx.Exec(ctx, `
					INSERT INTO users (id, name, email)
					VALUES ($1, $2, $3)
					ON CONFLICT (id) DO NOTHING
				`, m.UserID, m.Name, m.UserID+"@anonymous.local")
			}

			// Guard: only write members into a book that this user owns.
			_, err = tx.Exec(ctx, `
				INSERT INTO book_members (id, book_id, name, user_id)
				SELECT $1, $2, $3, $4
				WHERE EXISTS (SELECT 1 FROM books WHERE id = $2 AND user_id = $5)
				ON CONFLICT (id) DO UPDATE SET
					name = EXCLUDED.name,
					book_id = EXCLUDED.book_id,
					user_id = EXCLUDED.user_id
			`, m.ID, book.ID, m.Name, mUserID, userID)
			if err != nil {
				insertError(c, "book_members", err)
				return
			}
		}
	}

	// Shared Records — guard: only write into a book that this user owns.
	for _, rec := range wrapper.Records {
		var paidByID *string
		if rec.PaidByID != "" {
			paidByID = &rec.PaidByID
		}
		date := normalizeDate(rec.Date)
		_, err = tx.Exec(ctx, `
			INSERT INTO records (id, book_id, type, amount, category, date, note, paid_by_id, split_among_ids)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9
			WHERE EXISTS (SELECT 1 FROM books WHERE id = $2 AND user_id = $10)
			ON CONFLICT (id) DO UPDATE SET
				type = EXCLUDED.type,
				amount = EXCLUDED.amount,
				category = EXCLUDED.category,
				date = EXCLUDED.date,
				note = EXCLUDED.note,
				paid_by_id = EXCLUDED.paid_by_id,
				split_among_ids = EXCLUDED.split_among_ids
		`, rec.ID, rec.BookID, rec.Type, rec.Amount, rec.Category, date, rec.Note, paidByID, rec.SplitAmongIds, userID)
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
		_, err = tx.Exec(ctx, `
			INSERT INTO personal_records (id, user_id, type, amount, category, date, note, source_book_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO UPDATE SET
				type = EXCLUDED.type,
				amount = EXCLUDED.amount,
				category = EXCLUDED.category,
				date = EXCLUDED.date,
				note = EXCLUDED.note,
				source_book_id = EXCLUDED.source_book_id
			WHERE personal_records.user_id = $2
		`, rec.ID, userID, rec.Type, rec.Amount, rec.Category, date, rec.Note, sourceBookID)
		if err != nil {
			insertError(c, "personal_records", err)
			return
		}
	}

	// Templates
	for _, tpl := range wrapper.Templates {
		_, err = tx.Exec(ctx, `
			INSERT INTO record_templates (id, user_id, name, type, amount, category, note)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				type = EXCLUDED.type,
				amount = EXCLUDED.amount,
				category = EXCLUDED.category,
				note = EXCLUDED.note
			WHERE record_templates.user_id = $2
		`, tpl.ID, userID, tpl.Name, tpl.Type, tpl.Amount, tpl.Category, tpl.Note)
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

	ctx := c.Request.Context()

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
	rows, err := dbPool.Query(ctx, "SELECT id, name, type, icon, color, is_default FROM categories WHERE user_id = $1", userID)
	if err != nil {
		pullError(c, err)
		return
	}
	for rows.Next() {
		var item SyncCategory
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &item.Icon, &item.Color, &item.IsDefault); err != nil {
			rows.Close()
			pullError(c, err)
			return
		}
		data.Categories = append(data.Categories, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		pullError(c, err)
		return
	}

	// Fetch Books
	rows, err = dbPool.Query(ctx, "SELECT id, name, created_at FROM books WHERE user_id = $1", userID)
	if err != nil {
		pullError(c, err)
		return
	}
	var books []SyncBook
	for rows.Next() {
		var item SyncBook
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.Name, &createdAt); err != nil {
			rows.Close()
			pullError(c, err)
			return
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		item.Members = []SyncMember{}
		books = append(books, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		pullError(c, err)
		return
	}

	// Fetch Members for each book (after the books cursor is closed, to avoid
	// holding two pooled connections at once).
	for i := range books {
		mRows, err := dbPool.Query(ctx, "SELECT id, name, COALESCE(user_id::text, '') FROM book_members WHERE book_id = $1", books[i].ID)
		if err != nil {
			pullError(c, err)
			return
		}
		for mRows.Next() {
			var m SyncMember
			if err := mRows.Scan(&m.ID, &m.Name, &m.UserID); err != nil {
				mRows.Close()
				pullError(c, err)
				return
			}
			books[i].Members = append(books[i].Members, m)
		}
		mRows.Close()
		if err := mRows.Err(); err != nil {
			pullError(c, err)
			return
		}
	}
	data.Books = books

	// Fetch Shared Records
	rows, err = dbPool.Query(ctx, `
		SELECT r.id, r.book_id, r.type, r.amount, r.category, r.date, r.note, COALESCE(r.paid_by_id::text, ''), r.split_among_ids
		FROM records r
		JOIN books b ON r.book_id = b.id
		WHERE b.user_id = $1`, userID)
	if err != nil {
		pullError(c, err)
		return
	}
	for rows.Next() {
		var item SyncRecord
		var date time.Time
		if err := rows.Scan(&item.ID, &item.BookID, &item.Type, &item.Amount, &item.Category, &date, &item.Note, &item.PaidByID, &item.SplitAmongIds); err != nil {
			rows.Close()
			pullError(c, err)
			return
		}
		item.Date = date.Format("2006-01-02")
		data.Records = append(data.Records, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		pullError(c, err)
		return
	}

	// Fetch Personal Records
	rows, err = dbPool.Query(ctx, "SELECT id, type, amount, category, date, note, COALESCE(source_book_id::text, '') FROM personal_records WHERE user_id = $1", userID)
	if err != nil {
		pullError(c, err)
		return
	}
	for rows.Next() {
		var item SyncPersonalRecord
		var date time.Time
		if err := rows.Scan(&item.ID, &item.Type, &item.Amount, &item.Category, &date, &item.Note, &item.SourceBookID); err != nil {
			rows.Close()
			pullError(c, err)
			return
		}
		item.Date = date.Format("2006-01-02")
		data.PersonalRecords = append(data.PersonalRecords, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		pullError(c, err)
		return
	}

	// Fetch Templates
	rows, err = dbPool.Query(ctx, "SELECT id, name, type, amount, category, note FROM record_templates WHERE user_id = $1", userID)
	if err != nil {
		pullError(c, err)
		return
	}
	for rows.Next() {
		var item SyncTemplate
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &item.Amount, &item.Category, &item.Note); err != nil {
			rows.Close()
			pullError(c, err)
			return
		}
		data.Templates = append(data.Templates, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		pullError(c, err)
		return
	}

	c.JSON(http.StatusOK, data)
}

func insertError(c *gin.Context, table string, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to insert into " + table + ": " + err.Error()})
}

// pullError aborts a pull with 500 so the client never mistakes a transient DB
// failure for "the cloud is empty" (which would wipe local data on the next push).
func pullError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read backup: " + err.Error()})
}
