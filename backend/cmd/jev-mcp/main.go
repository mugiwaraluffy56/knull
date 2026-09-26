package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mugiwaraluffy56/knull/backend/internal/jev"
)

func main() {
	key := os.Getenv("KNULL_JEV_OPENAI_API_KEY")
	if key == "" {
		log.Fatal("KNULL_JEV_OPENAI_API_KEY is required")
	}
	model := env("KNULL_JEV_MODEL", "gpt-4o-mini")
	provider := &jev.OpenAIProvider{APIKey: key, Timeout: time.Duration(envInt("KNULL_JEV_TIMEOUT_SECONDS", 30)) * time.Second, MaxRequests: int64(envInt("KNULL_JEV_MAX_REQUESTS", 1000)), MaxOutputTokens: envInt("KNULL_JEV_MAX_OUTPUT_TOKENS", 1000), MaxRetries: envInt("KNULL_JEV_MAX_RETRIES", 1)}
	server := jev.NewServer(jev.NewService(provider, model))
	addr := env("KNULL_JEV_ADDR", "127.0.0.1:8090")
	log.Printf("jev-mcp listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)))
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func envInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			log.Fatalf("%s must be a positive integer", key)
		}
		return n
	}
	return fallback
}
