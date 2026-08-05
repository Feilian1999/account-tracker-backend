package app

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/feilian1999/account-tracker-backend/internal/db"
	"github.com/feilian1999/account-tracker-backend/internal/middleware"
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

	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		log.Printf("Unable to parse database URL: %v\n", err)
		return
	}
	// Keep the pool tiny: on Vercel each serverless instance owns its own pool,
	// so a large MaxConns across many concurrent instances exhausts Neon's limit.
	cfg.MaxConns = 2

	dbPool, err = pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		log.Printf("Unable to connect to database: %v\n", err)
		return
	}

	err = dbPool.Ping(context.Background())
	if err != nil {
		log.Printf("Database ping failed: %v\n", err)
	} else {
		log.Println("Successfully connected to Neon Database!")

		// Run Migrations
		migrateURL := dbURL
		if strings.HasPrefix(migrateURL, "postgres://") {
			migrateURL = strings.Replace(migrateURL, "postgres://", "pgx5://", 1)
		} else if strings.HasPrefix(migrateURL, "postgresql://") {
			migrateURL = strings.Replace(migrateURL, "postgresql://", "pgx5://", 1)
		}

		if err := db.RunMigrations(migrateURL); err != nil {
			log.Printf("Migration failed: %v\n", err)
		}
	}
}

func setupRouter() {
	r := gin.Default()

	// CORS Middleware
	r.Use(middleware.CORS())

	r.GET("/ping", func(c *gin.Context) {
		dbStatus := "connected"
		if dbPool == nil {
			dbStatus = "disconnected"
		}
		// Report the deployed commit: a failed Vercel deploy keeps serving the
		// previous one, and /ping was otherwise identical across versions, so
		// there was no way to tell whether a fix had actually shipped.
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
			"db":      dbStatus,
			"commit":  os.Getenv("VERCEL_GIT_COMMIT_SHA"),
		})
	})

	api := r.Group("/api")
	{
		// Cloud backup/restore, identified by the user's secret local UUID.
		// The UUID is an unguessable capability token — it must never be exposed
		// publicly (it is not embedded in shared-book payloads; see frontend memberId).
		syncGrp := api.Group("/sync")
		{
			syncGrp.POST("/push-uuid", pushSyncByUUIDHandler)
			syncGrp.GET("/pull-uuid/:uuid", pullSyncByUUIDHandler)
		}

		// Public Shared Spaces — collaborative books identified by an 8-char code (older 6-char codes still resolve).
		sharedGrp := api.Group("/shared")
		{
			sharedGrp.POST("/share", shareBookHandler)       // Create a new share code
			sharedGrp.GET("/:code", getSharedBookHandler)    // Fetch book by code
			sharedGrp.PUT("/:code", updateSharedBookHandler) // Merge book by code
		}
	}

	router = r
}

func GetRouter() *gin.Engine {
	once.Do(func() {
		initDB()
		setupRouter()
	})
	return router
}
