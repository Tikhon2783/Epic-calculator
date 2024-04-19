package application

import (
	"log"
	// "net/http"

	"calculator/internal"
	// "calculator/internal/config"
)

var (
	err          error
	db_existance []byte
	logger       *log.Logger = shared.Logger
	loggerErr    *log.Logger = shared.LoggerErr
)

