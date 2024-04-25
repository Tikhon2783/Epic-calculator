package monitoring

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"calculator/internal"
	"calculator/internal/config"

	"github.com/jackc/pgx"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // Standard library bindings for pgx
)

var (
	ServerExitChannel chan os.Signal = make(chan os.Signal, 1)
	db                *pgx.ConnPool  = shared.GetDb()
	err               error
	logger            *log.Logger = shared.Logger
	loggerErr         *log.Logger = shared.LoggerErr
	loggerHB          *log.Logger = shared.LoggerHeartbeats
	mu                *sync.RWMutex = &sync.RWMutex{}
)

// Структура для мониторинга агентов (менеджер агентов)
type AgentsManager struct {
	Agents [vars.N_agents + 1]int	// Кол-во живых агентов (0 - мертв, 1 - свободен, 2 - занят)
	HbTime     	  map[int]time.Time	// Мапа с временем последних хартбитов
	HbTimeout    map[int]time.Duration	// Мапа с временем таймаутов для каждого агента
	TaskIds      *Queue			// Очередь не принятых агентами выражений
	ToKill		 int
}

type Queue struct {
	exps map[string]string
	mu  sync.Mutex
}

func (q *Queue) Append(id, exp string) {
	q.mu.Lock()
	q.exps[id] = exp
	q.mu.Unlock()
}

func (q *Queue) Find() (id string, expression string, username string, found bool) {
	if q.IsEmpty() {
		return "", "", "", false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	db = shared.GetDb()
	for id, exp := range q.exps {
		var username string
		err = db.QueryRow("SELECT (username) FROM requests WHERE request_id=$1", id).Scan(&username)
		if err != nil {
			logger.Println("\t ПАНИКА, проверьте лог ошибок.")
			loggerErr.Panic("Ошибка получения имени пользователя по ключу выражения из бд - ПАНИКА:", err)
		}
		return id, exp, username, true
	}
	return "", "", "", false
}

func (q * Queue) Take(id string) (bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.exps[id]
	if !ok {
		return false
	}
	delete(q.exps, id)
	return true
}

func (q *Queue) IsEmpty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.exps) == 0
}

// Конструктор монитора агентов
func NewAgentsManager() *AgentsManager {
	logger.Println("Мониторинг агентов реализован.")
	return &AgentsManager{
		Agents:       [vars.N_agents + 1]int{},
		HbTime:       make(map[int]time.Time),
		HbTimeout:    make(map[int]time.Duration),
		TaskIds:      &Queue{
			mu: sync.Mutex{},
			exps: make(map[string]string),
		},
	}
}

func (m *AgentsManager) RegisterAgent(agentID int) error {
	// Проверяем, зарегистрирован ли уже такой агент
	if m.Agents[agentID] != 0 {
		return errors.New("agent already registered")
	}

	m.Agents[agentID] = 1
	m.HbTime[agentID] = time.Now()
	m.HbTimeout[agentID] = GetTimeout()
	return nil
}

// Хендлер на принятие выражения
func (m *AgentsManager) HandleExpression(id, exp string) {
	m.TaskIds.Append(id, exp)
	logger.Printf("Менеджер агентов поставил выражение с id '%s' - '%s' в очередь", id, exp)
}

func (m *AgentsManager) GiveExpression() (id string, exp string, username string, ok bool) {
	return m.TaskIds.Find()
}

func (m *AgentsManager) TakeExpression(id string, agentID int) bool {
	m.RegisterAgent(agentID)
	ok := m.TaskIds.Take(id)
	// Выражения с таким id нет в очереди
	if !ok {
		return false
	}
	db = shared.GetDb()
	_, err = db.Exec("orchestratorAssign", id, agentID)
	if err != nil {
		loggerErr.Println("Ошибка назначения выражению агента. "+
		"До того, как агент отправит рез-т оркестратору, выражение будет отмечено как неотданное в БД, "+
		"но его не будет в очереди менеджера. "+
		"При определенных обстоятельствах оно может быть посчитано дважды. "+
		"Пользователь будет видеть, что оно еще в очереди. Ошибка:",
		err)
		return false
	}
	return true
}

// Хендлер на убийство агента
func (m *AgentsManager) KillAgent() error {
	allDeadLocal := true
	for i := 1; i < vars.N_agents+1; i++ { // Ищем мертвого агента
		mu.RLock()
		agentStatus := m.Agents[i]
		mu.RUnlock()
		if agentStatus != 0 {
			allDeadLocal = false
			break
		}
	}
	if allDeadLocal {
		return errors.New("нет живых агентов")
	}

	m.ToKill += 1
	return nil
}

var allDead bool = true

// Хендлер на endpoint мониторинга агентов
func (m *AgentsManager) Monitor() [][]int32 {
	var agents = [][]int32{}
	var n int
	for i := 1; i < vars.N_agents+1; i++ {
		mu.Lock()
		agentStatus := m.Agents[i]
		if agentStatus != 0 {
			if time.Since(m.HbTime[i]) > m.HbTimeout[i] { // Агент не посылал хартбиты слишком долго
				loggerHB.Println(time.Since(m.HbTime[i]), m.HbTime[i], m.HbTimeout[i])
				loggerHB.Printf("Оркестратор - агент %v умер (таймаут).\n", i)
				m.Agents[i] = 0
				agents = append(agents, []int32{int32(i), int32(0)})
			} else {
				agents = append(agents, []int32{int32(i), int32(agentStatus)})
				n++
			}
		} else {
			agents = append(agents, []int32{int32(i), int32(0)})
		}
		mu.Unlock()
	}

	loggerHB.Println("Живых агентов:", n)
	if n < 0 {
		fmt.Printf("Оркестратор считает, что у нас %v живых агентов (где-то ошибка) :|\n", n)
	} else if n == 0 {
		if !allDead {
			loggerHB.Println("Оркестратор - не осталось живых агентов!")
			fmt.Println("Не осталось живых агентов!")
			allDead = true
		}
	} else {
		allDead = false
	}
	return agents
}

// Heartbeat
func (m *AgentsManager) RegisterHeartbeat(i int) {
	loggerHB.Printf("Оркестратор - получили хартбит от агента %v.\n", i)
	if err = m.RegisterAgent(i); err == nil {
		return
	}
	mu.Lock()
	m.HbTime[i] = time.Now()
	mu.Unlock()
}

// Функция для получения времени операций из БД
func GetTimeout() time.Duration {
	db = shared.GetDb()
	var timeout int
	err = db.QueryRow("SELECT time FROM agent_timeout").Scan(&timeout)
	if err != nil {
		loggerErr.Panic(err)
	}
	AgentTimeout := time.Duration(timeout * 1_000_000)
	logger.Printf("Время на таймаут:, %v\n", AgentTimeout)

	return AgentTimeout
}
