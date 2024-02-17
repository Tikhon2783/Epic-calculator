package shared

import (
	"calculator/vars"
	"encoding/json"
	"log"
	"os"
	"io"

	"github.com/jackc/pgx"
)

var (
	Logger           *log.Logger = GetDebugLogger()
	LoggerErr        *log.Logger = GetErrLogger()
	LoggerHeartbeats *log.Logger = GetHeartbeatLogger()
	Db               *pgx.ConnPool
	OpenFiles		 []*os.File = make([]*os.File, 0)
)

type Db_info struct {
	Db         bool
	T_requests bool
	T_agent    bool
	T_vars     bool
}

func GetDBSTate() Db_info { // Возвращает JSON структуру из файла db_existance.json
	logger := GetDebugLogger()
	db_existance, err := os.ReadFile("vars/db_existance.json")
	if err != nil {
		logger.Fatal(err)
	}
	var db_info Db_info
	err = json.Unmarshal([]byte(db_existance), &db_info)
	if err != nil {
		logger.Fatal(err)
	}
	return db_info
}

func GetDebugLogger() *log.Logger {
	switch vars.LoggerOutputDebug {
	case 0:
		return log.New(os.Stdout, "", vars.LoggerFlagsDebug)
	case 1:
		f, err := os.Create("backend/logs/debug.txt")
		if err != nil {
			log.Println("Не смогли открыть файл для логгера, их логи записаны не будут.")
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		return log.New(f, "", vars.LoggerFlagsDebug)
	case 2:
		f, err := os.Create("backend/logs/debug.txt")
		if err != nil {
			log.Println("Не смогли открыть файл для логгера, будет использоваться только Stdout.")
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		return log.New(io.MultiWriter(os.Stdout, f), "", vars.LoggerFlagsDebug)
	}
	return log.Default()
}

func GetErrLogger() *log.Logger {
	switch vars.LoggerOutputErr {
	case 0:
		return log.New(os.Stderr, "", vars.LoggerFlagsError)
	case 1:
		f, err := os.OpenFile("backend/logs/errors.txt", os.O_APPEND | os.O_WRONLY, 0600)
		if err != nil {
			log.Println("Не смогли открыть файл для логгера ошибок, их логи записаны не будут.")
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		f.WriteString("\nНОВАЯ СЕССИЯ\n")
		return log.New(f, "", vars.LoggerFlagsError)
	case 2:
		f, err := os.OpenFile("backend/logs/errors.txt", os.O_APPEND | os.O_WRONLY, 0600)
		if err != nil {
			log.Println("Не смогли открыть файл для логгера ошибок, будет использоваться только Stderr.")
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		f.WriteString("\nНОВАЯ СЕССИЯ\n")
		return log.New(io.MultiWriter(os.Stdout, f), "", vars.LoggerFlagsError)
	}
	return log.Default()
}

func GetHeartbeatLogger() *log.Logger {
	switch vars.LoggerOutputHeartbeats {
	case 0:
		return log.New(os.Stdout, "PULSE ", vars.LoggerFlagsPings)
	case 1:
		// f, err := os.OpenFile("backend/logs/heartbeats.txt", os.O_APPEND | os.O_WRONLY, 0600)
		f, err := os.Create("backend/logs/heartbeats.txt")
		if err != nil {
			log.Println("Не смогли открыть файл для логгера пингов, их логи записаны не будут.")
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		return log.New(f, "PULSE ", vars.LoggerFlagsPings)
	case 2:
		f, err := os.Create("backend/logs/heartbeats.txt")
		if err != nil {
			log.Println("Не смогли открыть файл для логгера пингов, будет использоваться только Stdout.")
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		return log.New(io.MultiWriter(os.Stdout, f), "", vars.LoggerFlagsPings)
	}
	return log.Default()
}
