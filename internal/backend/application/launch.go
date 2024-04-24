package application

import (
	"log"
	"os"
	"time"

	"calculator/internal"
	"calculator/internal/config"

	"calculator/internal/backend/application/agent"
	"calculator/internal/backend/application/orchestrator"
	"calculator/internal/backend/application/orchestrator/monitoring"
	// "calculator/internal/backend/application/orchestrator"
	// "calculator/internal/frontend/server"
)

var (
	err          error
	db_existance []byte
	logger       *log.Logger = shared.Logger
	loggerErr    *log.Logger = shared.LoggerErr
	appExitChannel	chan os.Signal = make(chan os.Signal, 1)
	agentsTimeout	*time.Duration
)

func Launch() {

	manager := monitoring.NewAgentsManager()

	// Планируем агентов
	logger.Println("Планируем агентов...")
	for i := 1; i <= vars.N_agents; i++ {
		go agent.Agent(NewAgentComm(i)) // Запускаем горутину агента и передаем ей структуру агента
		logger.Printf("Запланировали агента %v\n", i)
	}

	// Проверяем на возобновление работы
	orchestrator.CheckWorkLeft(manager)

	// Запускаем сервер
	go orchestrator.Launch(manager)
}

func launchAgent() {

}



// Конструктор структуры агента (в backend/cmd/agent)
func NewAgentComm(i int) *agent.AgentComm {
	// Возвращаем структуру, которую передадим агенту
	return &agent.AgentComm{
		N:				i,
		Host:			"localhost",
		Port:			vars.PortGrpc,
		Timeout:		*agentsTimeout,
		N_machines:		vars.N_machines,
	}
}