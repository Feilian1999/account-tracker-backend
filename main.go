package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()

	// 允許 CORS (簡單版，正式環境建議使用專用 Middleware)
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

	// 測試用路由
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})

	// 預計之後的 API 結構
	api := r.Group("/api")
	{
		api.POST("/auth/register", registerHandler)
		api.POST("/auth/login", loginHandler)
		api.GET("/records", getRecordsHandler)
		api.POST("/records/sync", syncRecordsHandler)
	}

	r.Run(":8080") // 預設監聽 8080 埠口
}

// 佔位用的 Handler
func registerHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "msg": "Register API Placeholder"})
}

func loginHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "msg": "Login API Placeholder"})
}

func getRecordsHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "records": []string{}})
}

func syncRecordsHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "msg": "Sync API Placeholder"})
}
