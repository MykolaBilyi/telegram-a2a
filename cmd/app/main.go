package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"telegram-a2a/api/server"
	"telegram-a2a/internal/adapters"
	"telegram-a2a/internal/ports"
	"telegram-a2a/internal/providers"
)

func main() {
	config, err := adapters.NewConfig()
	if err != nil {
		log.Printf("error reading config: %v\n", err)
		os.Exit(1)
	}

	logger, err := adapters.NewLogger(config.Sub("logger"))
	if err != nil {
		log.Printf("error creating logger: %v\n", err)
		os.Exit(1)
	}

	a2aClient, err := adapters.NewA2AClient(config.Sub("agent"), logger)
	if err != nil {
		logger.Error("error creating A2A client", ports.Fields{"error": err})
		os.Exit(1)
	}

	sessionStore := providers.NewSessionStore(config.GetDuration("application.approvalTTL"))

	telegramBot, err := adapters.NewTelegramBot(config.Sub("telegram"), a2aClient, sessionStore, logger)
	if err != nil {
		logger.Error("error creating Telegram bot", ports.Fields{"error": err})
		os.Exit(1)
	}

	healthCheckers := map[string]ports.HealthChecker{
		"telegram": telegramBot,
		"a2a":      a2aClient,
	}

	router := server.NewRouter(config.Sub("middleware"), logger, healthCheckers)
	webServer := adapters.NewWebServer(config.Sub("http"), router, logger)

	appCtx, cancelApp := context.WithCancel(context.Background())
	defer cancelApp()

	go func() {
		if err := webServer.Serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", ports.Fields{"error": err})
			os.Exit(1)
		}
	}()

	go func() {
		if err := telegramBot.Start(appCtx); err != nil {
			logger.Error("telegram bot error", ports.Fields{"error": err})
			os.Exit(1)
		}
	}()

	signalChan := make(chan os.Signal, 1)

	signal.Notify(
		signalChan,
		syscall.SIGTERM,
		syscall.SIGINT,
	)

	<-signalChan
	logger.Info("shutting down")

	go func() {
		<-signalChan
		// second signal. Exit directly.
		logger.Warn("terminating")
		os.Exit(1)
	}()

	cancelApp()

	// graceful shutdown
	gracefulCtx, cancelShutdown := context.WithTimeout(context.Background(), config.GetDuration("application.shutdownTimeout"))
	defer cancelShutdown()

	if err := telegramBot.Stop(gracefulCtx); err != nil {
		logger.Error("telegram bot shutdown error", ports.Fields{"error": err})
	}

	if err := webServer.Shutdown(gracefulCtx); err != nil {
		logger.Error("http server shutdown error", ports.Fields{"error": err})
		os.Exit(1)
	}
}
