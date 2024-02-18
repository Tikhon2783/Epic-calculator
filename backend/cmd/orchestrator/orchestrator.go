package orchestrator

import (
	// "context"
	// "database/sql"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	// "errors"

	"calculator/backend/cmd/agent"
	"calculator/cmd"
	"calculator/vars"

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
)

// Структура для мониторинга агентов
type AgentsManager struct {
	Agents [vars.N_agents + 1]int // Кол-во живых агентов (0 - мертв, 1 - свободен, 2 - занят)
	// AgentsFree   []int                 	 // Свободные агенты
	Hb           map[int]<-chan struct{} // Канал хартбитов
	HbTime       map[int]time.Time       // Мапа с временем последних хартбитов
	TaskInformer map[int]chan<- int      // Мапа каналов передачи выражения агенту
	ResInformer  map[int]<-chan bool     // Мапа каналов оповещения о посчитанном выражении
	Ctx          map[int]context.CancelFunc
	TaskIds      agent.Queue // Очередь не принятых агентами выражений
}

// Конструктор монитора агентов
func newAgentsManager() *AgentsManager {
	return &AgentsManager{
		Agents: [vars.N_agents + 1]int{},
		// AgentsFree:   make([]int, vars.N_agents),
		Hb:           make(map[int]<-chan struct{}),
		HbTime:       make(map[int]time.Time),
		TaskInformer: make(map[int]chan<- int),
		ResInformer:  make(map[int]<-chan bool),
		Ctx:          make(map[int]context.CancelFunc),
		TaskIds:      &agent.Arr{},
	}
}

// Конструктор структуры агента (в backend/cmd/agent) + обновление монитора агентов
func NewAgentComm(i int, m *AgentsManager) *agent.AgentComm {
	ctxAgent, ctxAgentCancel := context.WithCancel(context.Background())
	hb := make(chan struct{})
	ti := make(chan int, 1)
	ri := make(chan bool)
	m.Agents[i] = 1
	m.Ctx[i] = ctxAgentCancel
	m.Hb[i] = hb
	m.HbTime[i] = time.Now()
	m.TaskInformer[i] = ti
	m.ResInformer[i] = ri
	return &agent.AgentComm{
		N:            i,
		Ctx:          ctxAgent,
		Heartbeat:    hb,
		TaskInformer: ti,
		ResInformer:  ri,
		N_machines:   vars.N_machines,
	}
}

type SrvSelfDestruct struct {
	mu sync.Mutex
}

// type rcvExpHandler struct{}

type myKeys interface{}

var myKey myKeys = "myKey"

// Проверка выражения на валидность
func validityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
				loggerErr.Println("Оркестратор: непредвиденная ПАНИКА при обработке выражения.")
				http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
			}
		}()

		logger.Println("Оркестратор получил запрос на подсчет выражения.")
		// Метод должен быть POST
		if r.Method != http.MethodPost {
			logger.Println("Неправильный метод, выражение не обрабатывается.")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		err := r.ParseForm()
		if err != nil {
			logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
			loggerErr.Println("Оркестратор: ошибка парсинга запроса.")
			http.Error(w, "Ошибка парсинга запроса", http.StatusInternalServerError)
			return
		}

		idStr := r.PostForm.Get("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			logger.Println("Ошибка, выражение не обрабатывается.")
			loggerErr.Println("Оркестратор: ошибка преобразования ключа идемпотентности в тип int.")
			http.Error(w, "Ошибка преобразования ключа идемпотентности в тип int", http.StatusInternalServerError)
			return
		}

		// Проверяем, принято ли уже было выражение с таким же ключом идемпотентности
		var exists bool
		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM requests WHERE id=$1)").Scan(&exists)
		if err != nil {
			logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
			loggerErr.Println("Оркестратор: ошибка проверки ключа в базе данных.")
			http.Error(w, "Ошибка проверки ключа в базе данных", http.StatusInternalServerError)
			return
		}
		if exists {
			logger.Println("Выражение с повторяющимся ключем идемпотентности, возвращаем код 200.")
			fmt.Fprint(w, http.StatusText(200), "Выражение уже было принято к обработке")
			return
		}

		// Проверяем выражение на валидность
		exp := r.PostForm.Get("expression")
		replacer := strings.NewReplacer(
			"+", "",
			"-", "",
			"*", "",
			"/", "",
		)
		if _, err := strconv.Atoi(replacer.Replace(exp)); err != nil {
			logger.Println("Ошибка: нарушен синтаксис выражений, выражение не обрабатывается.")
			http.Error(w, "Выражение невалидно", http.StatusBadRequest)
			return
		}

		// Передаем обработчику ID и выражение через контекст
		ctx := context.WithValue(r.Context(), myKey, [2]myKeys{id, exp})

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func handleExpression(w http.ResponseWriter, r *http.Request) {
	logger.Println("Обработчик выражений получил запрос...")

	vals := r.Context().Value(myKey).([2]myKeys)
	id, exp := vals[0].(int), vals[1].(string)

	_, err = db.Exec("orchestratorPut", id, exp)
	if err != nil {
		logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
		loggerErr.Println("Оркестратор: ошибка записи выражения в таблицу с выражениями.")
		http.Error(w, "Ошибка помещения выражения в базу данных", http.StatusInternalServerError)
		mu.Unlock()
		return
	}

	if _, ok := manager.TaskIds.Pop(); ok {
		logger.Println("Очередь выражений не пустая, помещаем туда выражение.")
		manager.TaskIds.Append(id)
		fmt.Fprint(w, http.StatusText(200), "Выражение поставленно в очередь")
		return
	}
	logger.Println("Ищем свободного агента...")
	mu.RLock()
	// Ищем свободного агента
	for i := 1; i < vars.N_agents+1; i++ {
		if manager.Agents[i] == 1 {
			logger.Println(i, "агент свободен.")
			err = giveTaskToAgent(i, id)
			mu.RUnlock()
			if err != nil {
				logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
				loggerErr.Println("Оркестратор: ошибка назначения агента в таблице с выражениями.")
				http.Error(w, "Ошибка помещения выражения в базу данных", http.StatusInternalServerError)
				return
			}
			manager.Agents[i] = 2
			manager.TaskInformer[i] <- id
			logger.Printf("Выражение отдано агенту %v, возвращем код 200.", i)
			fmt.Fprint(w, http.StatusText(200), "Выражение принято на обработку агентом")
			return
		}
	}
	mu.RUnlock()

	logger.Println("Все агенты заняты, кладем выражение в очередь.")
	manager.TaskIds.Append(id)
	fmt.Fprint(w, http.StatusText(200), "Выражение поставленно в очередь")
}

type checkExpHandler struct{}

func (h *checkExpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {}

type getExpHandler struct{}

func (h *getExpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {}

func TimeValues(w http.ResponseWriter, r *http.Request) {}

// func GetHeartbeat(w http.ResponseWriter, r *http.Request) {}

func Launch() {
	logger.Println("Подключился оркестратор.")
	fmt.Println("Оркестратор передаёт привет :)")
	db = shared.Db
	// Подготавливаем запросы в БД
	_, err = db.Prepare( // Запись выражения в таблицу с выражениями
		"orchestratorPut",
		`INSERT INTO requests (request_id, expression)
		VALUES ($1, $2);`,
	)
	if err != nil {
		loggerErr.Panic(err)
	}
	_, err = db.Prepare( // Запись результата в таблицу с выражениями
		"orchestratorAssign",
		`UPDATE requests
			SET agent_proccess = $2
			WHERE request_id = $1;`,
	)
	if err != nil {
		loggerErr.Panic(err)
	}
	_, err = db.Prepare( // Получение результата из таблицы с процессами (агентами)
		"orchestratorReceive",
		`SELECT request_id, result FROM agent_proccesses
			WHERE proccess_id = $1;`,
	)
	if err != nil {
		loggerErr.Panic(err)
	}
	_, err = db.Prepare( // Запись результата в таблицу с выражениями
		"orchestratorUpdate",
		`UPDATE requests
			SET calculated = TRUE, result = $2, errors = $3
			WHERE request_id = $1;`,
	)
	if err != nil {
		loggerErr.Panic(err)
	}
	_, err = db.Prepare( // Получение результата из таблицы с выражениями
		"orchestratorGet",
		`SELECT expression, calculated, result, errors FROM requests
			WHERE request_id = $1;`,
	)
	if err != nil {
		loggerErr.Panic(err)
	}

	mu = &sync.RWMutex{}
	manager = newAgentsManager() // Мониторинг агентов для запущенного оркестратора

	// Настраиваем обработчики для разных путей
	mux := http.NewServeMux()
	mux.Handle("/calculator/kill", &SrvSelfDestruct{}) // Убийство сервера
	mux.HandleFunc("/calculator", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "Куда-то ты не туда забрёл...")
	})
	mux.Handle("/calculator/sendexpression", validityMiddleware(http.HandlerFunc(handleExpression))) // Принять выражение
	mux.Handle("/calculator/checkexpression", &checkExpHandler{})                                    // Узнать статус выражения
	mux.Handle("/calculator/getexpressions", &getExpHandler{})                                       // Получить список всех выражений
	mux.HandleFunc("/calculator/values", TimeValues)                                                 // Получение списка доступных операций со временем их выполения
	// mux.HandleFunc("/calculator/heartbeats", GetHeartbeat)        // Хартбиты (пинги) от агентов

	// Сам http сервер оркестратор
	Srv = &http.Server{
		Addr:     ":8080",
		Handler:  mux,
		ErrorLog: loggerErr,
	}

	// Планируем агентов
	logger.Println("Планируем агентов...")
	for i := 1; i <= vars.N_agents; i++ {
		go agent.Agent(NewAgentComm(i, manager)) // Запускаем горутину агента и передаем ей структуру агента, обновляя монитор агентов
		logger.Printf("Запланировали агента %v\n", i)
	}

	// Проверяем на возобновление работы
	CheckWorkLeft(manager)

	// Планируем горутину мониторинга агентов
	go MonitorAgents(manager)

	// Запускаем сервер
	logger.Println("Запускаем HTTP сервер...")
	if err = Srv.ListenAndServe(); err != nil {
		loggerErr.Println("HTTP сервер накрылся:", err)
	}

	logger.Println("Отправляем сигнал прерывания...")
	ServerExitChannel <- os.Interrupt
	logger.Println("Отправили сигнал прерывания.")
}

func CheckWorkLeft(m *AgentsManager) {
	var req string
	rows, err := db.Query(
		`SELECT request_id FROM requests
			WHERE calculated = FALSE`,
	)
	if err != nil {
		loggerErr.Panic(err)
	}
	defer rows.Close()
	for rows.Next() {
		err := rows.Scan(&req)
		if err != nil {
			loggerErr.Panic(err)
		}
		log.Println(req)
	}
	err = rows.Err()
	if err != nil {
		loggerErr.Panic(err)
	}
}

func giveTaskToAgent(n, id int) error {
	mu.Lock()
	defer mu.Unlock()
	_, err = db.Exec("orchestratorAssign", id, n)
	if err != nil {
		return err
	}
	manager.Agents[n] = 2
	manager.TaskInformer[n] <- id
	return nil
}

func MonitorAgents(m *AgentsManager) {
	logger.Println("Мониторинг агентов подключился.")
	var n int
	var allDead bool = true
	n = 0
	for {
		for i := 1; i < vars.N_agents+1; i++ {
			mu.RLock()
			if m.Agents[i] == 0 { // Агент не записан как живой
				mu.RUnlock()
				continue
			}
			mu.RUnlock()
			loggerHB.Printf("Оркестратор - проверка агента %v...\n", i)

			// Проверяем, живой ли агент
			select {
			case _, ok := <-m.Hb[i]:
				if !ok {
					loggerHB.Printf("Оркестратор - агент %v умер (закрыт канал хартбитов).\n", i)
					m.Ctx[i]()
					mu.Lock()
					m.Agents[i] = 0
					mu.Unlock()
					n--
				} else {
					loggerHB.Printf("Оркестратор - получили хартбит от агента %v.\n", i)
					m.HbTime[i] = time.Now()
					allDead = false
					n++
				}
			default:
				if time.Since(m.HbTime[i]) > vars.T_agentTimeout { // Агент не посылал хартбиты слишком долго
					loggerHB.Printf("Оркестратор - агент %v умер (таймаут).\n", i)
					m.Ctx[i]()
					close(m.TaskInformer[i])
					mu.Lock()
					m.Agents[i] = 0
					mu.Unlock()
					n--
				}
			}
			// Проверяем, не посчитал ли агент свое выражение
			select {
			case ok := <-m.ResInformer[i]:
				var id int
				var res string
				err = db.QueryRow("orchestratorReceive", i).Scan(&id, &res)
				if err != nil {
					loggerErr.Panic(err)
				}
				if ok {
					logger.Printf("Получили результат от агента %v: %v.", i, res)
					_, err = db.Exec("orchestratorUpdate", id, res, false)
					if err != nil {
						loggerErr.Panic(err)
					}
				} else {
					logger.Printf("Получили результат от агента %v: ошибка.", i)
					_, err = db.Exec("orchestratorUpdate", id, res, true)
					if err != nil {
						loggerErr.Panic(err)
					}
				}
				logger.Println("Положили результат в БД.")
				mu.Lock()
				m.Agents[i] = 1
				mu.Unlock()
			default:
			}

			logger.Println("Живых агентов:", n)
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

// Тестовый вариант "убийства" оркестратора, в разработке
func (h *SrvSelfDestruct) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Server does not support Flusher!",
			http.StatusInternalServerError)
		return
	}
	fmt.Println("The button has been pressed...")
	for i := 10; i > 0; i-- {
		_, err := fmt.Fprintf(w, "Self destruction in %v\n", i)
		flusher.Flush()
		fmt.Println(i, err)
		<-time.After(time.Second)
	}
	fmt.Fprintln(w, "KaBOOM!")
	fmt.Println("Server is getting self destructed...")
	flusher.Flush()
	err = Srv.Close()
	if err != nil {
		loggerErr.Println("Failed to close Server:", err)
	}
	logger.Println("Closed the Server.")
}
