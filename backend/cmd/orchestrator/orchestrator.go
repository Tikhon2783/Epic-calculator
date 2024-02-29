package orchestrator

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

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
		// Проверяем, нет ли посторонних символов в выражении
		// Ошибка возвращается еще и в случае, если кол-во цифр превышает лимит int64
		if _, err := strconv.Atoi(replacer.Replace(exp)); err != nil {
			logger.Println("Ошибка: нарушен синтаксис выражений, выражение не обрабатывается.")
			loggerErr.Println("Ошибка с проверкой выражения парсингом:", err)
			http.Error(w, "Выражение невалидно", http.StatusBadRequest)
			return
		}

		// Передаем обработчику ID и выражение через контекст
		ctx := context.WithValue(r.Context(), myKey, [2]myKeys{id, exp})

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

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
		// Такой сценарий маловероятен, но если так произойдет, постгрес выдаст ошибку — нельзя записать два выражения на одного агента,
		// и пользователю будет возвращена ошибка.
		// Мне кажется, это логичнее, чем полностью блокировать мьютекс до конца итерации + мьютекс находится в
		// функции, где и происходит изменение данных, которое может привести к гонке без использования мьютексов.

		// Поправка: ошибка выдана не будет, потому что нам нужны повторяющиеся агенты для сообщений пользователю о том, какой агент считал выражение
		// Буду над этим работать (скорее всего канал отправки выражения зависнет,
		// так как агент уже получил выражение, в теории можно сделать проверку и, если агент не принимает выражение,
		// отправлять другому/в очередь)
		if agentStatus == 1 {
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

	logger.Println("Все агенты заняты, кладем выражение в очередь.")
	manager.TaskIds.Append(id)
	fmt.Fprint(w, http.StatusText(200), "Выражение поставленно в очередь")
}

// Хендлер на endpoint получения результата выражения по ключу
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
	logger.Println("Bye postgresql")
	if err != nil {
		logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
		loggerErr.Printf("Оркестратор: ошибка получения выражения по ключу %s из базы данных: %s", id, err)
		http.Error(w, "Ошибка получения выражения по ключу из базы данных.", http.StatusInternalServerError)
		return
	}

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

// Хендлер на endpoint получения списка выражений
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
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
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
		exps     [][]string = [][]string{{"ID", "выражение", "рез-т", "агент"}} // Слайс с выражениями (и заголовком)
		id       int
		exp      string
		finished bool
		res      string
		errors   bool
		agent    int
		failed   int // Сколько строк не смогли получить (ошибка от постгреса)
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
	printTable(exps, w) // Выводим таблицу в консоль
	if failed != 0 {
		fmt.Fprintln(w, "Не удалось получить", failed, "строк.")
	}
	err = rows.Err()
	if err != nil {
		logger.Println("Ошибка со строками.")
		loggerErr.Printf("Оркестратор: ошибка получения строк из базы данных (но таблицу вывели): %s", err)
		// http.Error(w, "Ошибка получения выражений из базы данных.", http.StatusInternalServerError
		return
	}

}

// Функция для вывода красивой таблицы в консоль
func printTable(table [][]string, w http.ResponseWriter) {
	// Находим максимальные длины столбцов
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

// Хендлер на endpoint с значениями времени
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

	// Выводим в консоль калькулятора полученные значения для упрощенного дебага
	fmt.Print("'", strings.Join([]string{tSum, tSub, tMult, tDiv, tAgent}, "' '"), "'\n")
	logger.Print(
		"Оркестратор: полученные значения на изменение(sum sub mul div timeout): '",
		strings.Join([]string{tSum, tSub, tMult, tDiv, tAgent}, "' '"), "'\n",
	)

	// Изменяем значения
	for i, val := range []string{tSum, tSub, tMult, tDiv, tAgent} {
		if val == "" { // Не получили запрос на изменение данной переменной
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

		// Проверяем на валидность значения
		if t < 0 {
			fmt.Fprintf(
				w,
				"Время на %s было введено отрицательное, прпускаем...",
				[]string{"сложение", "вычитание", "умножение", "деление", "таймаут"}[i],
			)
			continue
		}

		// Обновляем значение в БД
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

	// Получаем значения и возвращаем пользователю
	times := getTimes()
	printTable([][]string{
		{"операция", "время"},
		{"сложение", fmt.Sprint(times.Sum)},
		{"вычитание", fmt.Sprint(times.Sub)},
		{"умножение", fmt.Sprint(times.Mult)},
		{"деление", fmt.Sprint(times.Div)},
		{"таймаут", fmt.Sprint(times.AgentTimeout)},
	}, w)
}

// Функция для получения времени операций из БД
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
		log.Fatal(err)
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
						if err != nil {
							loggerErr.Println("Паника:", err)
							logger.Println("Критическая ошибка, завершаем работу программы...")
							logger.Println("Отправляем сигнал прерывания...")
							ServerExitChannel <- os.Interrupt
							logger.Println("Отправили сигнал прерывания.")
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

	getTimes()
	mu = &sync.RWMutex{}
	manager = newAgentsManager() // Мониторинг агентов для запущенного оркестратора

	// Настраиваем обработчики для разных путей
	mux := http.NewServeMux()
	mux.Handle("/calculator/kill/orchestrator", &SrvSelfDestruct{}) // Убийство сервера
	mux.HandleFunc("/calculator/kill/agent", KillHandler)
	mux.HandleFunc("/calculator", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "Куда-то ты не туда забрёл...")
	})
	mux.Handle("/calculator/sendexpression", validityMiddleware(http.HandlerFunc(handleExpression))) // Принять выражение
	mux.HandleFunc("/calculator/checkexpression", checkExpHandler)                                   // Узнать статус выражения
	mux.HandleFunc("/calculator/getexpressions", getExpHandler)                                      // Получить список всех выражений
	mux.HandleFunc("/calculator/values", TimeValues)                                                 // Получение списка доступных операций со временем их выполения
	mux.HandleFunc("/calculator/monitor", MonitorHandler)

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
	manager.Agents[n] = 2 // Записываем агента как занятого
	select {
	case manager.TaskInformer[n] <- id:
		logger.Printf("Выражение отдано агенту %v.", n)
	default:
		logger.Printf("Не смогли отдать выражение агенту %v.", n)
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
						if err != nil {
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

// Хендлер на endpoint убийства оркестратора
func (h *SrvSelfDestruct) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Server does not support Flusher!",
			http.StatusInternalServerError)
		return
	}
	logger.Println("Получили запрос на убийство оркестратора. Оркестратор умрет через 5 секунд.")
	fmt.Println("Запущено самоуничтожение севера...")
	for i := 5; i > 0; i-- {
		_, err := fmt.Fprintf(w, "Оркестратор умрет через %v\n", i)
		flusher.Flush()
		fmt.Println(i, err)
		<-time.After(time.Second)
	}
	fmt.Fprintln(w, "Бабах!")
	logger.Println("Оркестратор умирает...")
	fmt.Println("Оркестратор умер...")
	flusher.Flush()
	err = Srv.Close()
	if err != nil {
		loggerErr.Println("Не смогли закрыть HTTP сервер:", err)
		http.Error(w, http.StatusText(500), http.StatusInternalServerError)
	}
	logger.Println("Закрыли HTTP сервер.")
	// Агенты сами отключатся при остановке программы
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
