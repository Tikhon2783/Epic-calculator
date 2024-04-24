package monitoring

import (
	"errors"
	"fmt"
	"log"
	"net/http"
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
	db                *pgx.ConnPool  = shared.Db
	err               error
	logger            *log.Logger = shared.Logger
	loggerErr         *log.Logger = shared.LoggerErr
	loggerHB          *log.Logger = shared.LoggerHeartbeats
	Srv               *http.Server
	manager           *AgentsManager
	mu                *sync.RWMutex
	agentsTimeout     *time.Duration
)

// Структура для мониторинга агентов (менеджер агентов)
type AgentsManager struct {
	Agents [vars.N_agents + 1]int	// Кол-во живых агентов (0 - мертв, 1 - свободен, 2 - занят)
	HbTime     	  map[int]time.Time	// Мапа с временем последних хартбитов
	HbTimeout    map[int]time.Duration	// Мапа с временем таймаутов для каждого агента
	TaskIds      *Queue					// Очередь не принятых агентами выражений
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
	return &AgentsManager{
		Agents:       [vars.N_agents + 1]int{},
		HbTime:       make(map[int]time.Time),
		HbTimeout:    make(map[int]time.Duration),
		TaskIds:      &Queue{},
	}
}

// Хендлер на принятие выражения
func (m *AgentsManager) HandleExpression(id, exp string) {
	manager.TaskIds.Append(id, exp)
	log.Print("Менеджер агентов поставил выражение в очередь")
}

func (m *AgentsManager) GiveExpression() (id string, exp string, username string, ok bool) {
	return m.TaskIds.Find()
}

func (m *AgentsManager) TakeExpression(id string, agentID int) bool {
	ok := m.TaskIds.Take(id)
	// Выражения с таким id нет в очереди
	if !ok {
		return false
	}
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
	allDead := true
	for i := 1; i < vars.N_agents+1; i++ { // Ищем мертвого агента
		mu.RLock()
		agentStatus := manager.Agents[i]
		mu.RUnlock()
		if agentStatus != 0 {
			allDead = false
			break
		}
	}
	if allDead {
		return errors.New("нет живых агентов")
	}

	m.ToKill += 1
	return nil
}

// Хендлер на endpoint мониторинга агентов
func (m *AgentsManager) Monitor() [][]int32 {
	var agents = [][]int32{}
	for i := 1; i < vars.N_agents+1; i++ {
		mu.RLock()
		agentStatus := manager.Agents[i]
		mu.RUnlock()
		agents = append(agents, []int32{int32(i), int32(agentStatus)})
	}
	return agents
}

// Heartbeat
func (m *AgentsManager) RegisterHeartbeat(i int) {
	loggerHB.Printf("Оркестратор - получили хартбит от агента %v.\n", i)
	mu.Lock()
	m.HbTime[i] = time.Now()
	mu.Unlock()
}

// Горутина менеджера агентов, которая следит за их состоянием
func (m *AgentsManager) MonitorAgents() {
	logger.Println("Мониторинг агентов подключился.")
	var n int
	var allDead bool = true
	for {
		n = 0
		for i := 1; i < vars.N_agents+1; i++ {
			mu.RLock()
			agentStatus := manager.Agents[i]
			mu.RUnlock()
			if agentStatus == 0 { // Агент не записан как живой
				continue
			}
			loggerHB.Printf("Оркестратор - проверка агента %v...\n", i)

			// Проверяем, живой ли агент
			select {
			case _, ok := <-m.Hb[i]:
				if !ok {
					loggerHB.Printf("Оркестратор - агент %v умер (закрыт канал хартбитов).\n", i)
					mu.Lock()
					m.Agents[i] = 0
					mu.Unlock()
				} else {
					loggerHB.Printf("Оркестратор - получили хартбит от агента %v.\n", i)
					m.HbTime[i] = time.Now()
					allDead = false
					n++
				}
			default:
				if time.Since(m.HbTime[i]) > m.HbTimeout[i] { // Агент не посылал хартбиты слишком долго
					loggerHB.Println(time.Since(m.HbTime[i]), m.HbTime[i], m.HbTimeout[i])
					loggerHB.Printf("Оркестратор - агент %v умер (таймаут).\n", i)
					mu.Lock()
					m.Agents[i] = 0
					mu.Unlock()
				}
			}
			// Проверяем, не посчитал ли агент свое выражение
			select {
			case ok, alive := <-m.ResInformer[i]:
				if !alive {
					continue
				}
				var id int
				var res string
				err = db.QueryRow("orchestratorReceive", i).Scan(&id, &res)
				fmt.Println(id, res)
				if err != nil {
					loggerErr.Println("Паника:", err)
					logger.Println("Критическая ошибка, завершаем работу программы...")
					logger.Println("Отправляем сигнал прерывания...")
					ServerExitChannel <- os.Interrupt
					logger.Println("Отправили сигнал прерывания.")
					return
				}
				if ok {
					logger.Printf("Получили результат от агента %v: %v.", i, res)
					_, err = db.Exec("orchestratorUpdate", id, res, false)
					if err != nil {
						loggerErr.Println("Паника:", err)
						logger.Println("Критическая ошибка, завершаем работу программы...")
						logger.Println("Отправляем сигнал прерывания...")
						ServerExitChannel <- os.Interrupt
						logger.Println("Отправили сигнал прерывания.")
						return
					}
				} else {
					logger.Printf("Получили результат от агента %v: ошибка.", i)
					_, err = db.Exec("orchestratorUpdate", id, res, true)
					if err != nil {
						loggerErr.Println("Паника:", err)
						logger.Println("Критическая ошибка, завершаем работу программы...")
						logger.Println("Отправляем сигнал прерывания...")
						ServerExitChannel <- os.Interrupt
						logger.Println("Отправили сигнал прерывания.")
						return
					}
				}
				logger.Println("Положили результат в БД.")
				_, err = db.Exec("DELETE FROM agent_proccesses WHERE proccess_id = $1;", i)
				if err != nil {
					loggerErr.Println("Паника:", err)
					logger.Println("Критическая ошибка, завершаем работу программы...")
					logger.Println("Отправляем сигнал прерывания...")
					ServerExitChannel <- os.Interrupt
					logger.Println("Отправили сигнал прерывания.")
					return
				}
				if !m.TaskIds.IsEmpty() {
					if t, ok := m.TaskIds.Pop(); ok {
						logger.Printf("Очередь выражений не пустая, отдаем агенту %v выражение с id %v.", i, t)
						err = giveTaskToAgent(i, t)
						if err == fmt.Errorf("agent did not receive task") { // Агент не принял выражение, видимо, уже занят
							loggerErr.Println(
								`Оркестратор: не смогли отдать выражению агенту (агент не принял),
								 оставляем агента без задачи, кладем выражение обратно в очередь.`)
							m.TaskIds.Append(t)
							continue
						} else if err != nil {
							loggerErr.Println("Паника при попытке отдать агенту выражение:", err)
							logger.Println("Критическая ошибка, завершаем работу программы...")
							logger.Println("Отправляем сигнал прерывания...")
							ServerExitChannel <- os.Interrupt
							logger.Println("Отправили сигнал прерывания.")
						}
						return
					} else {
						loggerErr.Println("Слайс не пустой но пустой...")
					}
				} else {
					logger.Printf("Очередь выражений пустая, агент %v отдыхает.", i)
					mu.Lock()
					m.Agents[i] = 1
					mu.Unlock()
				}
			default:
			}

			loggerHB.Println("Живых агентов:", n)
			if n < 0 {
				fmt.Printf("Оркестратор считает, что у нас %v живых агентов (где-то ошибка) :|\n", n)
			} else if n == 0 && !allDead {
				loggerHB.Println("Оркестратор - не осталось живых агентов!")
				fmt.Println("Не осталось живых агентов!")
				allDead = true
			}
			time.Sleep(min(time.Second, vars.T_agentTimeout/5))
		}
	}
}
