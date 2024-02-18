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
	TaskIds      []int
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
		TaskIds:      make([]int, 0),
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

// Проверка выражения на валидность
func validityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Метод должен быть POST
		if r.Method != http.MethodPost {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		err := r.ParseForm()
		if err != nil {
			http.Error(w, "Ошибка парсинга запроса", http.StatusInternalServerError)
			return
		}

		idStr := r.PostForm.Get("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			http.Error(w, "Ошибка преобразования ключа идемпотентности в тип int", http.StatusInternalServerError)
			return
		}

		// Проверяем, принято ли уже было выражение с таким же ключом идемпотентности
		var exists bool
		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM requests WHERE id=$1)").Scan(&exists)
		if err != nil {
			http.Error(w, "Ошибка проверки ключа в базе данных", http.StatusInternalServerError)
			return
		}
		if exists {
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
			http.Error(w, "Выражение невалидно", http.StatusBadRequest)
			return
		}

		// Передаем обработчику ID и выражение через контекст
		type myKeys interface{}
		var keyId myKeys = "id"
		var keyExp myKeys = "expression"
		ctx1 := context.WithValue(r.Context(), keyId, id)
		ctx2 := context.WithValue(ctx1, keyExp, exp)

		next.ServeHTTP(w, r.WithContext(ctx2))
	})
}

func handleExpression(w http.ResponseWriter, r *http.Request) {
	logger.Println("Got expression request...")
	// exp := "1*2*3/4*5/0*1"
	// _, err = db.Exec(
	// 	`INSERT INTO requests (request_id, expression, agent_proccess)
	// 		VALUES ($1, $2, $3)`,
	// 	1,
	// 	exp,
	// 	1,
	// )
	// if err != nil {
	// 	loggerErr.Panic(err)
	// }
	// logger.Println("Put expression in DB, sending signal to agent 1...")
	// fmt.Println(manager.TaskInformer)
	// manager.TaskInformer[1] <- 1
	// logger.Println("Sent task to agent 1, returning code 200")
	// fmt.Fprint(w, http.StatusText(200))

	// exp := r.Body
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
		`INSERT INTO requests (request_id, expression, agent_proccess)
		VALUES ($1, $2, $3);`,
	)
	if err != nil {
		panic(err)
	}
	_, err = db.Prepare( // Получение результата из таблицы с процессами (агентами)
		"orchestratorReceive",
		`SELECT result, request_id FROM requests
			WHERE proccess_id = $1;`,
	)
	if err != nil {
		panic(err)
	}
	_, err = db.Prepare( // Запись результата в таблицу с выражениями
		"orchestratorUpdate",
		`UPDATE requests
			SET calculated = TRUE, result = $2, errors = $3
			WHERE request_id = $1;`,
	)
	if err != nil {
		panic(err)
	}
	_, err = db.Prepare( // Получение результата из таблицы с выражениями
		"orchestratorGet",
		`SELECT expression, calculated, result, errors FROM requests
			WHERE request_id = $1;`,
	)
	if err != nil {
		panic(err)
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

func MonitorAgents(m *AgentsManager) {
	logger.Println("Мониторинг агентов подключился.")
	var n int
	var allDead bool = true
	n = 0
	for {
		for i := 1; i < len(m.Agents); i++ {
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
				var res string
				var id int
				err = db.QueryRow("orchestratorReceive", i).Scan(&res, &id)
				if ok {
					logger.Printf("Получили результат от агента %v: %v.", i, res)
					_, err = db.Exec("orchestratorUpdate", id, res, false)
				} else {
					logger.Printf("Получили результат от агента %v: ошибка.", i)
					_, err = db.Exec("orchestratorUpdate", id, res, true)
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
