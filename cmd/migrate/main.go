package main

import (
	"context"
	"log"
	"os"

	"github.com/RainLib/open-review-platform/internal/migrate"
)

func main() {
	databaseURL := os.Getenv("CONTROL_DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("CONTROL_DATABASE_URL is required")
	}
	if err := migrate.Apply(context.Background(), databaseURL, "migrations"); err != nil {
		log.Fatal(err)
	}
	log.Print("migrations are current")
}
