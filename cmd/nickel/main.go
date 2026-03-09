package main

import (
	"log/slog"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/idkmaybedeveloper/nickel/internal/api"
	"github.com/idkmaybedeveloper/nickel/internal/config"
)

func main() {
	// setup slog
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	cfg := config.Load()

	app := fiber.New(fiber.Config{
		AppName:      "nickel",
		ServerHeader: "nickel",
	})

	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST",
		AllowHeaders: "Content-Type,Accept",
	}))

	// handler
	handler := api.NewHandler(cfg.UserAgent)

	app.Get("/", handler.HandleGet)
	app.Post("/", handler.HandlePost)
	app.Get("/tunnel", handler.HandleTunnel)

	slog.Info("starting nickel server", "port", cfg.Port)
	if err := app.Listen(":" + cfg.Port); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
