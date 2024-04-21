package application

import (
	"log"
	"os"

	"calculator/internal"
	// "calculator/internal/config"

	// "calculator/internal/backend/application/agent"
	// "calculator/internal/backend/application/orchestrator"
	// "calculator/internal/frontend/server"
)

var (
	err          error
	db_existance []byte
	logger       *log.Logger = shared.Logger
	loggerErr    *log.Logger = shared.LoggerErr
	appExitChannel	chan os.Signal = make(chan os.Signal, 1)
)

func Launch() {
	
}

func LaunchOrchestrator() {

}

func launchAgent() {

}