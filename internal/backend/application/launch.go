package application

import (
	"log"
	"time"

	"calculator/internal"
	"calculator/internal/config"

	"calculator/internal/backend/application/agent"
	"calculator/internal/backend/application/orchestrator"
	"calculator/internal/backend/application/orchestrator/monitoring"
)

var (
	logger       *log.Logger = shared.Logger
	agentsTimeout	*time.Duration
)

func Launch() {

	manager := monitoring.NewAgentsManager()

	// Проверяем на возобновление работы
	orchestrator.CheckWorkLeft(manager)
	
	// Запускаем сервер
	go orchestrator.Launch(manager)

	time.Sleep(time.Second)
	// Планируем агентов
	logger.Println("Планируем агентов...")
	for i := 1; i <= vars.N_agents; i++ {
		go agent.Agent(NewAgentComm(i)) // Запускаем горутину агента и передаем ей структуру агента
		logger.Printf("Запланировали агента %v\n", i)
	}
}


// Конструктор структуры агента (в internal/backend/application/agent)
func NewAgentComm(i int) *agent.AgentComm {
	// Возвращаем структуру, которую передадим агенту
	return &agent.AgentComm{
		N:				i,
		Host:			"localhost",
		Port:			vars.PortGrpc,
		Timeout:		monitoring.GetTimeout(),
		N_machines:		vars.N_machines,
	}
}