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
	shared "calculator/cmd"
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
	agentsTimeout     *time.Duration
)

// Структура для мониторинга агентов
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
		Agents: [vars.N_agents + 1]int{},
		// AgentsFree:   make([]int, vars.N_agents),
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
	ctxAgent, ctxAgentCancel := context.WithCancel(context.Background())
	hb := make(chan struct{})
	ti := make(chan int, 1)
	ri := make(chan bool)
	m.Agents[i] = 1
	m.Ctx[i] = ctxAgentCancel
	m.Hb[i] = hb
	m.HbTime[i] = time.Now()
	m.HbTimeout[i] = *agentsTimeout
	m.TaskInformer[i] = ti
	m.ResInformer[i] = ri
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
		logger.Println("Hello postgresql")
		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM requests WHERE request_id=$1)", id).Scan(&exists)
		if err != nil {
			logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
			loggerErr.Println("Оркестратор: ошибка проверки ключа в базе данных.")
			http.Error(w, "Ошибка проверки ключа в базе данных", http.StatusInternalServerError)
			return
		}
		logger.Println("Bye postgresql")
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

	_, err = db.Exec("orchestratorPut", id, exp, -1)
	if err != nil {
		logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
		loggerErr.Printf("Оркестратор: ошибка записи выражения в таблицу с выражениями: %s.", err)
		http.Error(w, "Ошибка помещения выражения в базу данных", http.StatusInternalServerError)
		// mu.Unlock()
		return
	}

	if _, ok := manager.TaskIds.Pop(); ok {
		logger.Println("Очередь выражений не пустая, помещаем туда выражение.")
		manager.TaskIds.Append(id)
		fmt.Fprint(w, http.StatusText(200), "Выражение поставленно в очередь")
		return
	}
	logger.Println("Ищем свободного агента...")
	// mu.RLock()
	// Ищем свободного агента
	for i := 1; i < vars.N_agents+1; i++ {
		if manager.Agents[i] == 1 {
			err = giveTaskToAgent(i, id)
			if err != nil {
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
	// mu.RUnlock()

	logger.Println("Все агенты заняты, кладем выражение в очередь.")
	manager.TaskIds.Append(id)
	fmt.Fprint(w, http.StatusText(200), "Выражение поставленно в очередь")
}

func checkExpHandler(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Оркестратор: непредвиденная ПАНИКА при получении статуса выражения.")
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()

	logger.Println("Оркестратор получил запрос на получение статуса выражения.")
	// Метод должен быть GET
	if r.Method != http.MethodGet {
		logger.Println("Неправильный метод, выражение не обрабатывается.")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	var exists bool
	id := r.URL.Query().Get("id")
	logger.Println("Hello postgresql")
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM requests WHERE request_id=$1)", id).Scan(&exists)
	logger.Println("Bye postgresql")
	if err != nil {
		logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
		loggerErr.Println("Оркестратор: ошибка проверки ключа в базе данных:", err)
		http.Error(w, "Ошибка проверки ключа в базе данных", http.StatusInternalServerError)
		return
	}
	if !exists {
		logger.Println("Выражение с полученным ID не найдено.")
		fmt.Fprint(w, "Выражение с полученным ID не найдено.")
		return
	}

	var (
		exp      string
		finished bool
		res      string
		errors   bool
		agent    int
	)
	logger.Println("Hello postgresql")
	err = db.QueryRow(
		`SELECT expression, calculated, result, errors, agent_proccess FROM requests
			WHERE request_id=$1;`,
		id,
	).Scan(&exp, &finished, &res, &errors, &agent)
	if err != nil {
		logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
		loggerErr.Printf("Оркестратор: ошибка получения выражения по ключу %s из базы данных: %s", id, err)
		http.Error(w, "Ошибка получения выражения по ключу из базы данных.", http.StatusInternalServerError)
		return
	}
	logger.Println("Bye postgresql")

	if !finished {
		logger.Println("Выражение успешно найдено, результат еще не получен.")
		if agent == -1 {
			fmt.Fprintf(w, "Выражение '%s' еще не посчитанно, все агенты заняты.", exp)
		}
		fmt.Fprintf(w, "Выражение '%s' еще считается. Номер агента — %v", exp, agent)
	} else if errors {
		logger.Printf("Выражение успешно найдено, в выражении была найдена ошибка. Считал агент %v", agent)
		fmt.Fprintf(w, "В выражении '%s' есть ошибка, результат не может быть посчитан. Считал агент %v", exp, agent)
	} else {
		logger.Printf("Выражение успешно найдено, результат получен. Считал агент %v", agent)
		fmt.Fprintf(w, "Выражение найдено! %s = %s. Старался агент %v :)", exp, res, agent)
	}
}

func getExpHandler(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Оркестратор: непредвиденная ПАНИКА при получении статуса выражения.")
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
		logger.Println("Оркестратор обработал запрос на получение списка выражений.")
	}()

	logger.Println("Оркестратор получил запрос на получение списка выражений.")

	// Метод должен быть GET
	if r.Method != http.MethodGet {
		logger.Println("Неправильный метод, выражение не обрабатывается.")
		http.Error(w, `зачем ты сюда постишь, здесь получают список выражений.
		 Тебе не обязательно посылать запросы вручную у меня есть специальный скрипт, в readme это описано.
		 Хотя круто, что кому то не лень самому здесь копаться, спасибо`, http.StatusMethodNotAllowed)
		return
	}

	rows, err := db.Query("SELECT request_id, expression, calculated, result, errors, agent_proccess FROM requests")
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Printf("Оркестратор: ошибка получения выражений из базы данных: %s", err)
		http.Error(w, "Ошибка получения выражений из базы данных.", http.StatusInternalServerError)
		return
	}
	var (
		exps     [][]string = [][]string{{"ID", "выражение", "рез-т", "агент"}}
		id       int
		exp      string
		finished bool
		res      string
		errors   bool
		agent    int
		failed   int
	)
	defer rows.Close()
	for rows.Next() {
		err := rows.Scan(&id, &exp, &finished, &res, &errors, &agent)
		if err != nil {
			logger.Println("Не смогли получить ряд из таблицы с выражениями, записываем, продолжаем получать ряды")
			loggerErr.Printf("Оркестратор: ошибка получения выражения из базы данных: %s", err)
			failed++
			continue
		}

		if !finished {
			if agent == -1 {
				exps = append(exps, []string{fmt.Sprint(id), exp, "не подсчитано", "в очереди"})
			} else {
				exps = append(exps, []string{fmt.Sprint(id), exp, "не подсчитано", fmt.Sprintf("агент %v", agent)})
			}
		} else {
			if errors {
				exps = append(exps, []string{fmt.Sprint(id), exp, "ошибка", fmt.Sprintf("агент %v", agent)})
			} else {
				exps = append(exps, []string{fmt.Sprint(id), exp, res, fmt.Sprintf("агент %v", agent)})
			}
		}
	}
	printTable(exps, w)
	if failed != 0 {
		fmt.Fprintln(w, "Не удалось получить", failed, "строк.")
	}
	err = rows.Err()
	if err != nil {
		logger.Println("Ошибка со строками.")
		loggerErr.Printf("Оркестратор: ошибка получения строк из базы данных: %s", err)
		// http.Error(w, "Ошибка получения выражений из базы данных.", http.StatusInternalServerError)
		return
	}

}

func printTable(table [][]string, w http.ResponseWriter) {
	columnLengths := make([]int, len(table[0]))
	for _, line := range table {
		for i, val := range line {
			columnLengths[i] = max(len(val), columnLengths[i])
		}
	}

	var lineLength int = 1 // +1 для последней границы "|" в ряду
	for _, c := range columnLengths {
		lineLength += c + 3 // +3 для доп символов: "| %s "
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Server does not support Flusher!",
			http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "+%s+\n", strings.Repeat("-", lineLength-2)) // Верхняя граница
	flusher.Flush()
	for i, line := range table {
		for j, val := range line {
			fmt.Fprintf(w, "| %-*s ", columnLengths[j], val)
			if j == len(line)-1 {
				fmt.Fprintf(w, "|\n")
				flusher.Flush()
			}
		}
		if i == 0 || i == len(table)-1 { // Заголовок или нижняя граница
			fmt.Fprintf(w, "+%s+\n", strings.Repeat("-", lineLength-2))
			flusher.Flush()
		}
	}
}

func TimeValues(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Оркестратор: непредвиденная ПАНИКА при обработки запроса на значения времени.")
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()

	logger.Println("Оркестратор получил запрос на получение/изменение значений времени.")
	// Метод должен быть GET
	if r.Method != http.MethodGet {
		logger.Println("Неправильный метод, выражение не обрабатывается.")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	// Получаем аргументы запроса
	tSum := r.URL.Query().Get("sum")
	tSub := r.URL.Query().Get("sub")
	tMult := r.URL.Query().Get("mult")
	tDiv := r.URL.Query().Get("div")
	tAgent := r.URL.Query().Get("timeout")

	fmt.Print("'")
	fmt.Print(strings.Join([]string{tSum, tSub, tMult, tDiv, tAgent}, "' '")) // Ghjdthrf
	fmt.Print("'\n")

	// Изменяем значения
	for i, val := range []string{tSum, tSub, tMult, tDiv, tAgent} {
		if val == "" {
			continue
		}
		fmt.Printf("'%s'\n", val)
		t, err := time.ParseDuration(val)
		if err != nil {
			logger.Println("Оркестратор: ошибка парсинга времени")
			loggerErr.Printf(
				"Оркестратор: ошибка парсинга (%s): %s",
				t,
				err,
			)
			continue
		}
		if t < 0 {
			fmt.Fprintf(
				w, 
				"Время на %s было введено отрицательное, прпускаем...",
				[]string{"сложение", "вычитание", "умножение", "деление", "таймаут"}[i],
			)
			continue
		}
		_, err = db.Exec(
			`UPDATE time_vars
				SET time = $2
				WHERE action = $1`,
			[]string{"summation", "substraction", "multiplication", "division", "agent_timeout"}[i],
			t/1000000,
		)
		if err != nil {
			logger.Println("Оркестратор: ошибка изменения значения в БД")
			loggerErr.Printf(
				"Оркестратор: ошибка изменения значения %s на %s в БД: %s",
				err,
				[]string{"summation", "substraction", "multiplication", "division", "agent_timeout"}[i],
				t,
			)
		}
	}

	// Получаем значения
	times := getTimes()
	printTable([][]string{
		{"операция", "время"},
		{"сложение", fmt.Sprint(times.Sum)},
		{"вычитание", fmt.Sprint(times.Sub)},
		{"умножение", fmt.Sprint(times.Mult)},
		{"деление", fmt.Sprint(times.Div)},
		}, w)
}

func getTimes() *agent.Times {
	rows, err := db.Query("SELECT action, time from time_vars;")
	if err != nil {
		panic(err)
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
			log.Fatal(err)
		}
		switch t_type {
		case "summation":
			t.Sum = time.Duration(t_time * 1000000)
			logger.Printf("Время на сложение:, %v\n", t.Sum)
			// fmt.Fprintf(w, "Время на сложение:, %v\n", t.Sum)
		case "substraction":
			t.Sub = time.Duration(t_time * 1000000)
			logger.Printf("Время на вычитание:, %v\n", t.Sub)
			// fmt.Fprintf(w, "Время на вычитание:, %v\n", t.Sub)
		case "multiplication":
			t.Mult = time.Duration(t_time * 1000000)
			logger.Printf("Время на умножение:, %v\n", t.Mult)
			// fmt.Fprintf(w, "Время на умножение:, %v\n", t.Mult)
		case "division":
			t.Div = time.Duration(t_time * 1000000)
			logger.Printf("Время на деление:, %v\n", t.Div)
			// fmt.Fprintf(w, "Время на деление:, %v\n", t.Div)
		case "agent_timeout":
			t.AgentTimeout = time.Duration(t_time * 1000000)
			logger.Printf("Таймаут агентов — %v\n", t.AgentTimeout)
			// fmt.Fprintf(w, "Таймаут агентов — %v\n", t.AgentTimeout)
			agentsTimeout = &t.AgentTimeout
		}
	}
	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}
	return t
}

func Launch() {
	logger.Println("Подключился оркестратор.")
	fmt.Println("Оркестратор передаёт привет :)")
	db = shared.Db
	// conn, err := db.Acquire()
	// fmt.Println(conn, err)
	// conn.Close()
	// logger.Println("Hello!")
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

	getTimes()
	mu = &sync.RWMutex{}
	manager = newAgentsManager() // Мониторинг агентов для запущенного оркестратора

	// Настраиваем обработчики для разных путей
	mux := http.NewServeMux()
	mux.Handle("/calculator/kill", &SrvSelfDestruct{}) // Убийство сервера
	mux.HandleFunc("/calculator", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "Куда-то ты не туда забрёл...")
	})
	mux.Handle("/calculator/sendexpression", validityMiddleware(http.HandlerFunc(handleExpression))) // Принять выражение
	mux.HandleFunc("/calculator/checkexpression", checkExpHandler)                                   // Узнать статус выражения
	mux.HandleFunc("/calculator/getexpressions", getExpHandler)                                      // Получить список всех выражений
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

		if _, ok := m.TaskIds.Pop(); ok {
			logger.Println("Очередь выражений не пустая, помещаем туда выражение.")
			m.TaskIds.Append(id)
			return
		}
		logger.Println("Ищем свободного агента...")
		mu.RLock()
		// Ищем свободного агента
		for i := 1; i < vars.N_agents+1; i++ {
			if m.Agents[i] == 1 {
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
		mu.RUnlock()
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

func giveTaskToAgent(n, id int) error {
	logger.Printf("Отдаем задачу с ID %v агенту %v...", id, n)
	// mu.Lock()
	// defer mu.Unlock()
	// fmt.Println(db)
	// conn, err := db.Acquire()
	// fmt.Println("Acquired connection!", err)
	// conn.Close()
	db.Reset()
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
	manager.Agents[n] = 2
	select {
	case manager.TaskInformer[n] <- id:
		logger.Printf("Выражение отдано агенту %v.", n)
	default:
		logger.Printf("Не смогли отдать выражение агенту %v.", n)
	}
	return nil
}

func MonitorAgents(m *AgentsManager) {
	logger.Println("Мониторинг агентов подключился.")
	var n int
	var allDead bool = true
	for {
		n = 0
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
				} else {
					loggerHB.Printf("Оркестратор - получили хартбит от агента %v.\n", i)
					m.HbTime[i] = time.Now()
					allDead = false
					n++
				}
			default:
				if time.Since(m.HbTime[i]) > m.HbTimeout[i] { // Агент не посылал хартбиты слишком долго
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
			case ok := <-m.ResInformer[i]:
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
				// _, err = db.Exec(
				// 	`UPDATE requests
				// 		SET agent_proccess = NULL
				// 		WHERE request_id = $1;`,
				// 	id,
				// )
				if err != nil {
					loggerErr.Println("Паника:", err)
					logger.Println("Критическая ошибка, завершаем работу программы...")
					logger.Println("Отправляем сигнал прерывания...")
					ServerExitChannel <- os.Interrupt
					logger.Println("Отправили сигнал прерывания.")
					return
				}
				mu.Lock()
				m.Agents[i] = 1
				mu.Unlock()
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
