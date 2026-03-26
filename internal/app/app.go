package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"

	"github.com/feilian1999/account-tracker-backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

var (
	dbPool *pgxpool.Pool
	router *gin.Engine
	once   sync.Once
)

func initDB() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, assuming environment variables are set")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Println("DATABASE_URL must be set")
		return
	}

	var err error
	dbPool, err = pgxpool.New(context.Background(), dbURL)
	if err != nil {
		log.Printf("Unable to connect to database: %v\n", err)
		return
	}

	err = dbPool.Ping(context.Background())
	if err != nil {
		log.Printf("Database ping failed: %v\n", err)
	} else {
		log.Println("Successfully connected to Neon Database!")
	}
}

func setupRouter() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// CORS Middleware
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	r.GET("/ping", func(c *gin.Context) {
		dbStatus := "connected"
		if dbPool == nil {
			dbStatus = "disconnected"
		}
		c.JSON(http.StatusOK, gin.H{"message": "pong", "db": dbStatus})
	})

	api := r.Group("/api")
	{
		api.GET("/auth/google/login", googleLoginHandler)
		api.GET("/auth/google/callback", googleCallbackHandler)

		api.POST("/auth/register", registerHandler)
		api.POST("/auth/login", loginHandler)
		api.GET("/records", getRecordsHandler)
		api.POST("/records/sync", syncRecordsHandler)
	}

	router = r
}

func GetRouter() *gin.Engine {
	once.Do(func() {
		initDB()
		auth.InitGoogleAuth()
		setupRouter()
	})
	return router
}

// ---- Handlers ----

func googleLoginHandler(c *gin.Context) {
	state := "random_state" // TODO: Use a secure random string and store in cookie/session
	url := auth.GetGoogleLoginURL(state)
	c.Redirect(http.StatusTemporaryRedirect, url)
}

func googleCallbackHandler(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")

	// TODO: Verify state properly with cookies/session
	if state == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid state"})
		return
	}

	_, userInfo, err := auth.GetGoogleUserInfo(code)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// ---- Upsert user into database ----
	if dbPool != nil {
		query := `
			INSERT INTO users (google_id, email, name, avatar, last_login)
			VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
			ON CONFLICT (google_id) DO UPDATE 
			SET name = $3, avatar = $4, last_login = CURRENT_TIMESTAMP
		`
		_, err := dbPool.Exec(context.Background(), query, userInfo.Id, userInfo.Email, userInfo.Name, userInfo.Picture)
		if err != nil {
			log.Printf("Failed to upsert user: %v\n", err)
			// We continue even if DB fails for now, or you can choose to return error
		}
	}

	// Generate our own JWT
	token, err := auth.GenerateJWT(userInfo.Id, userInfo.Email, userInfo.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	// Redirect back to frontend with token
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}

	// Properly encode user info for URL
	redirectURL := fmt.Sprintf("%s/login?token=%s&name=%s&email=%s&avatar=%s",
		frontendURL,
		token,
		url.QueryEscape(userInfo.Name),
		url.QueryEscape(userInfo.Email),
		url.QueryEscape(userInfo.Picture),
	)

	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

func registerHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "msg": "Register API Ready"})
}

func loginHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "token": "mock_jwt_token"})
}

func getRecordsHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "records": []string{}})
}

func syncRecordsHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "msg": "Sync API Ready"})
}
