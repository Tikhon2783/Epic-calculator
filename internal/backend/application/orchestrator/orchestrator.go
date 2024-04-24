package orchestrator

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"calculator/internal/backend/application/agent"
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
	Agents [vars.N_agents + 1]int // Кол-во живых агентов (0 - мертв, 1 - свободен, 2 - занят)
	// AgentsFree   []int                 	 // Свободные агенты
	Hb           map[int]<-chan struct{} // Канал хартбитов
	HbTime       map[int]time.Time       // Мапа с временем последних хартбитов
	HbTimeout    map[int]time.Duration   // Мапа с временем таймаутов для каждого агента
	TaskInformer map[int]chan<- int      // Мапа каналов передачи выражения агенту
	ResInformer  map[int]<-chan bool     // Мапа каналов оповещения о посчитанном выражении
	Ctx          map[int]context.CancelFunc
	TaskIds      agent.Queue // Очередь не принятых агентами выражений
}

// Конструктор монитора агентов
func newAgentsManager() *AgentsManager {
	return &AgentsManager{
		Agents:       [vars.N_agents + 1]int{},
		Hb:           make(map[int]<-chan struct{}),
		HbTime:       make(map[int]time.Time),
		HbTimeout:    make(map[int]time.Duration),
		TaskInformer: make(map[int]chan<- int),
		ResInformer:  make(map[int]<-chan bool),
		Ctx:          make(map[int]context.CancelFunc),
		TaskIds:      &agent.Arr{},
	}
}

// Конструктор структуры агента (в backend/cmd/agent) + обновление монитора агентов
func NewAgentComm(i int, m *AgentsManager) *agent.AgentComm {
	// Создаем каналы и контекст
	ctxAgent, ctxAgentCancel := context.WithCancel(context.Background())
	hb := make(chan struct{}, 1)
	ti := make(chan int, 1)
	ri := make(chan bool)
	mu.Lock()
	defer mu.Unlock()
	// Добавляем информацию об агенте в структуру менеджера агентов
	m.Agents[i] = 1
	m.Ctx[i] = ctxAgentCancel
	m.Hb[i] = hb
	m.HbTime[i] = time.Now()
	m.HbTimeout[i] = *agentsTimeout
	m.TaskInformer[i] = ti
	m.ResInformer[i] = ri
	// Возвращаем структуру, которую передадим агенту
	return &agent.AgentComm{
		N:            i,
		Ctx:          ctxAgent,
		Heartbeat:    hb,
		Timeout:      *agentsTimeout,
		TaskInformer: ti,
		ResInformer:  ri,
		N_machines:   vars.N_machines,
	}
}

type SrvSelfDestruct struct {
	mu sync.Mutex
}

// Нужны для передачи нескольких значений (мапы) через контекст
type myKeys interface{}

var myKey myKeys = "myKey"

// Хендлер на endpoint принятия выражений
func handleExpression(w http.ResponseWriter, r *http.Request) {
	logger.Println("Обработчик выражений получил запрос...")

	vals := r.Context().Value(myKey).([2]myKeys)
	id, exp := vals[0].(int), vals[1].(string)

	_, err = db.Exec("orchestratorPut", id, exp, -1)
	if err != nil {
		logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
		loggerErr.Printf("Оркестратор: ошибка записи выражения в таблицу с выражениями: %s.", err)
		http.Error(w, "Ошибка помещения выражения в базу данных", http.StatusInternalServerError)
		return
	}

	if !manager.TaskIds.IsEmpty() {
		logger.Println("Очередь выражений не пустая, помещаем туда выражение.")
		manager.TaskIds.Append(id)
		fmt.Fprint(w, http.StatusText(200), "Выражение поставленно в очередь")
		return
	}
	logger.Println("Ищем свободного агента...")
	// Ищем свободного агента
	for i := 1; i < vars.N_agents+1; i++ {
		mu.RLock()
		agentStatus := manager.Agents[i]
		mu.RUnlock()
		// В теории две горутины могут попытаться дать выражение одному и тому же агенту,
		// если одна из них получит статус агента как свободного, пока вторая проверяет,
		// чему равен статус агента (следующая строка) — то есть до того, как функция giveTaskToAgent полностью заблокирует мьютекс.
		// Такой сценарий маловероятен, но если он произойдет, агент не примет новое выражение — он уже прочитал канал и не слушает,
		// функция вернет ошибку "agent did not receive task", и мы продолжим искать свободных агентов, не вызывая паники.
		// Мне кажется, это логичнее, чем полностью блокировать мьютекс до конца итерации, и еще мьютекс находится в
		// функции, где и происходит изменение данных, которое может привести к гонке без использования мьютексов.
		if agentStatus == 1 {
			err = giveTaskToAgent(i, id)
			if err == fmt.Errorf("agent did not receive task") { // Агент не принял выражение, ищем еще агентов
				continue
			} else if err != nil {
				logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
				loggerErr.Println("Оркестратор: ошибка назначения агента в таблице с выражениями:", err)
				http.Error(w, "Ошибка помещения выражения в базу данных", http.StatusInternalServerError)
				return
			}
			logger.Printf("Выражение отдано агенту %v, возвращем код 200.", i)
			fmt.Fprintf(w, http.StatusText(200), "Выражение принято на обработку агентом %v", i)
			return
		}
	}

	logger.Println("Все агенты заняты, кладем выражение в очередь.")
	manager.TaskIds.Append(id)
	fmt.Fprint(w, http.StatusText(200), "Выражение поставленно в очередь")
}

// Функция для получения времени операций из БД
func GetTimes() *agent.Times {
	rows, err := db.Query("SELECT action, time from time_vars;")
	if err != nil {
		loggerErr.Panic(err)
	}
	defer rows.Close()
	t := &agent.Times{}
	for rows.Next() {
		var (
			t_type string
			t_time int
		)
		err := rows.Scan(&t_type, &t_time)
		if err != nil {
			loggerErr.Panic(err)
		}
		switch t_type {
		case "summation":
			t.Sum = time.Duration(t_time * 1000000)
			logger.Printf("Время на сложение:, %v\n", t.Sum)
		case "substraction":
			t.Sub = time.Duration(t_time * 1000000)
			logger.Printf("Время на вычитание:, %v\n", t.Sub)
		case "multiplication":
			t.Mult = time.Duration(t_time * 1000000)
			logger.Printf("Время на умножение:, %v\n", t.Mult)
		case "division":
			t.Div = time.Duration(t_time * 1000000)
			logger.Printf("Время на деление:, %v\n", t.Div)
		case "agent_timeout":
			t.AgentTimeout = time.Duration(t_time * 1000000)
			logger.Printf("Таймаут агентов — %v\n", t.AgentTimeout)
			agentsTimeout = &t.AgentTimeout
		}
	}
	err = rows.Err()
	if err != nil {
		loggerErr.Panic(err)
	}
	return t
}

// Хендлер на endpoint убийства агента
func KillHandler(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Оркестратор: непредвиденная ПАНИКА при обработки запроса на убийство агента.")
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()

	logger.Println("Оркестратор получил запрос на убийство агента.")

	// Метод должен быть GET
	if r.Method != http.MethodGet {
		logger.Println("Неправильный метод, выражение не обрабатывается.")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	// Получаем аргументы запроса
	act := r.URL.Query().Get("action")

	// Пользователь хочет сократить агентов
	if act == "kill" {
		for i := 1; i < vars.N_agents+1; i++ { // Ищем живого агента
			mu.RLock()
			agentStatus := manager.Agents[i]
			mu.RUnlock()
			if agentStatus != 0 {
				logger.Printf("Оркестратор отключает агента %v.\n", i)
				manager.Ctx[i]() // Отменяем контекст агента
				logger.Printf("Оркестратор отключил агента %v.\n", i)
				fmt.Fprintf(w, "Оркестратор отключил агента %v.\n", i)
				return
			}
		}
		fmt.Fprintf(w, "Похоже, все агенты уже мертвы. Оркестратор не смог никого отключить.\n")
		// Пользователь хочет добавить агентов
	} else if act == "revive" {
		for i := 1; i < vars.N_agents+1; i++ { // Ищем мертвого агента
			mu.RLock()
			agentStatus := manager.Agents[i]
			mu.RUnlock()
			if agentStatus == 0 {
				go agent.Agent(NewAgentComm(i, manager)) // Запускаем агента
				logger.Printf("Запланировали агента %v\n", i)
				logger.Printf("Оркестратор подключил агента %v.\n", i)
				fmt.Fprintf(w, "Оркестратор оживил агента %v.\n", i)
				if !manager.TaskIds.IsEmpty() { // Если в очереди есть выражения, даем одно из них добавленному агенту
					if t, ok := manager.TaskIds.Pop(); ok {
						logger.Printf("Очередь не пустая, оркестратор дал агенту %v выражение с ID %v.\n", i, t)
						err = giveTaskToAgent(i, t)
						if err == fmt.Errorf("agent did not receive task") { // Агент не принял выражение, видимо, уже занят
							loggerErr.Println(
								`Оркестратор: не смогли отдать выражению агенту (агент не принял),
								 оставляем агента без задачи, кладем выражение обратно в очередь.`)
							manager.TaskIds.Append(t)
							continue
						} else if err != nil {
							logger.Println("Внутренняя ошибка, выражение возвращается в очередь.")
							loggerErr.Println("Оркестратор: ошибка назначения выражению агенту:", err)
							manager.TaskIds.Append(t)
							return
						}
					}
				}
				return
			}
		}
		fmt.Fprintf(w, "Похоже, все агенты уже живы. Оркестратор не смог никого подключить.\n")
	} else {
		logger.Println("Неправильный аргумент, выражение не обрабатывается.")
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
}

// Хендлер на endpoint мониторинга агентов
func MonitorHandler(w http.ResponseWriter, r *http.Request) {
	var agents = [][]string{{"Агент", "Состояние"}}
	for i := 1; i < vars.N_agents+1; i++ {
		mu.RLock()
		agentStatus := manager.Agents[i]
		mu.RUnlock()
		agents = append(agents, []string{fmt.Sprint("Агент ", i), map[int]string{0: "мертв", 1: "свободен", 2: "считает"}[agentStatus]})
	}
	printTable(agents, w)
}

// Основная горутина оркестратора
func Launch() {
	logger.Println("Подключился оркестратор.")
	fmt.Println("Оркестратор передаёт привет :)")
	db = shared.Db

	// Подготавливаем запросы в БД
	_, err = db.Prepare( // Запись выражения в таблицу с выражениями
		"orchestratorPut",
		`INSERT INTO requests (request_id, expression, agent_proccess)
		VALUES ($1, $2, $3);`,
	)
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}
	_, err = db.Prepare( // Запись результата в таблицу с выражениями
		"orchestratorAssign",
		`UPDATE requests
			SET agent_proccess = $2
			WHERE request_id = $1;`,
	)
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}
	_, err = db.Prepare( // Получение результата из таблицы с процессами (агентами)
		"orchestratorReceive",
		`SELECT request_id, result FROM agent_proccesses
			WHERE proccess_id = $1;`,
	)
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}
	_, err = db.Prepare( // Запись результата в таблицу с выражениями
		"orchestratorUpdate",
		`UPDATE requests
			SET calculated = TRUE, result = $2, errors = $3
			WHERE request_id = $1;`,
	)
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}
	_, err = db.Prepare( // Получение результата из таблицы с выражениями
		"orchestratorGet",
		`SELECT expression, calculated, result, errors FROM requests
			WHERE request_id = $1;`,
	)
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}


	// Планируем агентов
	logger.Println("Планируем агентов...")
	for i := 1; i <= vars.N_agents; i++ {
		go agent.Agent(NewAgentComm(i, manager)) // Запускаем горутину агента и передаем ей структуру агента, обновляя монитор агентов
		logger.Printf("Запланировали агента %v\n", i)
	}

	// Планируем горутину мониторинга агентов
	go MonitorAgents(manager)

	// Проверяем на возобновление работы
	CheckWorkLeft(manager)

	// Запускаем сервер
	logger.Println("Запускаем HTTP сервер...")
	fmt.Println("Калькулятор готов к работе!")
	if err = Srv.ListenAndServe(); err != nil {
		loggerErr.Println("HTTP сервер накрылся:", err)
	}

	logger.Println("Отправляем сигнал прерывания...")
	ServerExitChannel <- os.Interrupt
	logger.Println("Отправили сигнал прерывания.")
}

// Функция, которая проверяет, есть ли в БД непосчитанные выражения
func CheckWorkLeft(m *AgentsManager) {
	logger.Println("Оркестратор: проверка на непосчитанные выражения...")
	var id int
	rows, err := db.Query(
		`SELECT request_id FROM requests
			WHERE calculated = FALSE`,
	)
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}
	defer rows.Close()

	for rows.Next() {
		logger.Println("Оркестратор нашел непосчитанное выражение...")
		err := rows.Scan(&id)
		if err != nil {
			loggerErr.Println("Паника:", err)
			logger.Println("Критическая ошибка, завершаем работу программы...")
			logger.Println("Отправляем сигнал прерывания...")
			ServerExitChannel <- os.Interrupt
			logger.Println("Отправили сигнал прерывания.")
		}

		if !m.TaskIds.IsEmpty() { // Очередь не пустая
			if _, ok := m.TaskIds.Pop(); ok {
				logger.Println("Очередь выражений не пустая, помещаем туда выражение.")
				m.TaskIds.Append(id)
				return
			} else {
				// Этой ситуации никогда быть не должно и я ее не встретил ни разу :)
				loggerErr.Println("Слайс не пустой но пустой...")
			}
		}

		logger.Println("Ищем свободного агента...")
		// Ищем свободного агента
		for i := 1; i < vars.N_agents+1; i++ {
			mu.RLock()
			agentStatus := manager.Agents[i]
			mu.RUnlock()
			if agentStatus == 1 {
				logger.Println(i, "агент свободен.")
				err = giveTaskToAgent(i, id)
				if err != nil {
					loggerErr.Println("Паника:", err)
					logger.Println("Критическая ошибка, завершаем работу программы...")
					logger.Println("Отправляем сигнал прерывания...")
					ServerExitChannel <- os.Interrupt
					logger.Println("Отправили сигнал прерывания.")
				}
				break
			}
		}
	}
	err = rows.Err()
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}
	logger.Println("Оркестратор закончил проверку на непосчитанные выражения.")
}

// Функция, которая назначает агенту выражение
func giveTaskToAgent(n, id int) error {
	logger.Printf("Отдаем задачу с ID %v агенту %v...", id, n)
	mu.Lock()
	defer mu.Unlock()
	// fmt.Println(db)
	// conn, err := db.Acquire()
	// fmt.Println("Acquired connection!", err)
	// conn.Close()

	// Получить подключение получалось, но запрос все равно зависал без db.Reset()

	db.Reset() // Без этой команды запрос зависает и последующие действия не выполняются
	logger.Println("Сбросили подключения")
	_, err = db.Exec(
		`UPDATE requests
			SET agent_proccess = $2
			WHERE request_id = $1;`,
		id,
		n,
	)
	if err != nil {
		return err
	}

	logger.Printf("Отдаем выражение агенту %v.", n)
	select {
	case manager.TaskInformer[n] <- id:
		manager.Agents[n] = 2 // Записываем агента как занятого
		logger.Printf("Выражение отдано агенту %v.", n)
	default:
		logger.Printf("Не смогли отдать выражение агенту %v.", n)

		_, err = db.Exec(
			`UPDATE requests
				SET agent_proccess = -1
				WHERE request_id = $1;`,
			id,
		)
		if err != nil {
			loggerErr.Println(err)
		}

		return fmt.Errorf("agent did not receive task")
	}
	return nil
}

// Горутина менеджера агентов, которая следит за их состоянием
func MonitorAgents(m *AgentsManager) {
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
					m.Ctx[i]()
					close(m.TaskInformer[i])
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
					m.Ctx[i]()
					close(m.TaskInformer[i])
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

/*
                                                  .:
                                               .-==
                                 :---:    *+++*+.
                             :=++-----++:-+----+=
                          -=+=-----===--+#------#
                       -++-----+++=---++-++----=+
                    .=+=----++=---------+=++---*.
                   =*-----++-------------*=*=-+=
                  +=-----*--------------==#+#+#:   .:-:.
.-.              -*-----*----------====---========*=--:=+
  -=:++++=-:     -+-----++------=+=-:::==:  .::-+:-+%@+:#
    +------=+=:  .#------=*=--++-:::::+:    %@#:-=:::=+=*
    ==--------=+=:++-------**=:----:::*     :=: =-:::::+=                          .
     *=-----------+#*=---=*-==-..::-+:==       =+:::::::-+                   -:#+++++:
      -+=-------------=+*+:+.   %%#. *:-==---==:::::::::::*.               *+**+=----++
        :=+=---------=++*:--    -+-  #-*%@*==-:::::::::::::*.             :*-=++------=#.
           .-===+++=*-:*=::*        +=*#*=:::*::::::::::::::*.           .#+++=---=++=-.
                   :*:+%=::-+-:..:-=--+:-=+:+=:::::::::::::::*.        -*%@%*+++==:
                    ++:-*:::::---:::::-=* :+-+::::::::::::::::*     .=%%%#*:
                     .-=*-:::::::::::::::==:::::::::::::::::::-+  -*%*%%+.
                        .*:::::::::::G:O:L:A:N:G:::::::::::::::++###@#-
                         .*::::::::::::::::::::::::::::::::::-*%##%+:
                          :*::::::::::::::::::-::::::::::::=#%*%%:=:
                           :*:::::::::::::::=+-=+-::::::-+%##%*-++*
                            :*:::::--=======*++:+*+++=+###%#=:::-*=
                            :=*=++==-----------=---=+***%+-:::::-+
                            +=------------------=+***=--#::::::::+
                             =+---------------=+%#*=---=%-:::::::+
                              :+-------=++--=**+=------*#:::::::=-
                               .*-------**#**=---------##:::::::+.
                                -#=----=*##*+---------***:::::::*
                                .+=+-*#+=--**=-------=#*=::::::-+
                                 *:-*-==-------------#+#:::::::*=-.
                                 +:::*=-------------**+*::::::#=:=+
                                 .*-::++-----------=#+#-::::+= .--
                                  **+::=+---------=#++*::-+-
                                     -===*=-------#++#*==.
                                      -+-*#=-----#+++*
                                     -+::*.-+---**++*.
                                     **-*.  .+=****-
*/
