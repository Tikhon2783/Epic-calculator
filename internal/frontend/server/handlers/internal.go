package handlers

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"strings"
	"time"
	"os"

	"calculator/internal"
	"calculator/internal/frontend/server/utils"
	"calculator/internal/errors"

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
	Srv               *http.Server
)

type SrvSelfDestruct struct {
	mu sync.Mutex
}

// Нужны для передачи нескольких значений (мапы) через контекст
type myKeys interface{}

var myKey myKeys = "myKey"

// Хендлер на endpoint принятия выражений
func HandleExpressionInternal(w http.ResponseWriter, r *http.Request) {
	logger.Println("Обработчик выражений получил запрос...")

	vals := r.Context().Value(myKey).([2]myKeys)
	id, exp := vals[0].(int), vals[1].(string)
	_, _ = id, exp

	// TODO
}

// Хендлер на endpoint получения результата выражения по ключу
func CheckExpHandlerInternal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: непредвиденная ПАНИКА при получении статуса выражения.")
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()

	db = shared.Db

	logger.Println("Сервер получил запрос на получение статуса выражения.")
	// Метод должен быть GET
	if r.Method != http.MethodGet {
		logger.Println("Неправильный метод, запрос не обрабатывается.")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	var exists bool
	id := r.URL.Query().Get("id")
	username := r.URL.Query().Get("username")

	logger.Println("Hello postgresql")
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM requests WHERE request_id=$1 AND username=$2)", id, username).Scan(&exists)
	logger.Println("Bye postgresql")
	if err != nil {
		logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
		loggerErr.Println("Сервер: ошибка проверки ключа в базе данных:", err)
		http.Error(w, myErrors.FrontSrvErrors.InternalDbKeyCheckError.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		logger.Println("Выражение с полученным ID не найдено.")
		fmt.Fprint(w, myErrors.FrontSrvErrors.InexistantIDError.Error())
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
		loggerErr.Printf("Сервер: ошибка получения выражения по ключу %s из базы данных: %s", id, err)
		http.Error(w, myErrors.FrontSrvErrors.InternalDbKeyGetError.Error(), http.StatusInternalServerError)
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
func GetExpHandlerInternal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: непредвиденная ПАНИКА при получении статуса выражения.")
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
		logger.Println("Сервер обработал запрос на получение списка выражений.")
	}()

	logger.Println("Сервер получил запрос на получение списка выражений.")

	// Метод должен быть GET
	if r.Method != http.MethodGet {
		logger.Println("Неправильный метод, выражение не обрабатывается.")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	username := r.URL.Query().Get("username")

	rows, err := db.Query("SELECT request_id, expression, calculated, result, errors, agent_proccess FROM requests WHERE username=$1", username)
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Printf("Сервер: ошибка получения выражений из базы данных: %s", err)
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
			loggerErr.Printf("Сервер: ошибка получения выражения из базы данных: %s", err)
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
	utils.PrintTable(exps, w) // Выводим таблицу в консоль
	if failed != 0 {
		fmt.Fprintln(w, "Не удалось получить", failed, "строк.")
	}
	err = rows.Err()
	if err != nil {
		logger.Println("Ошибка со строками.")
		loggerErr.Printf("Сервер: ошибка получения строк из базы данных (но таблицу вывели): %s", err)
		// http.Error(w, "Ошибка получения выражений из базы данных.", http.StatusInternalServerError
		return
	}
}

// Хендлер на endpoint с значениями времени
func TimeValuesInternal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: непредвиденная ПАНИКА при обработки запроса на значения времени.")
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()

	logger.Println("Сервер получил запрос на получение/изменение значений времени.")

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
		"Сервер: полученные значения на изменение(sum sub mul div timeout): '",
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
			logger.Println("Сервер: ошибка парсинга времени")
			loggerErr.Printf(
				"Сервер: ошибка парсинга (%s): %s",
				t,
				err,
			)
			continue
		}

		// Проверяем на валидность значения
		if t < 0 {
			fmt.Fprintf(
				w,
				"Время на %s было введено отрицательное, пропускаем...",
				[]string{"сложение", "вычитание", "умножение", "деление", "таймаут"}[i],
			)
			continue
		}

		// Обновляем значение в БД
		// TODO
	}

	// Получаем значения и возвращаем пользователю
	// TODO
}

// Хендлер на endpoint убийства агента
func KillAgentHandlerInternal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: непредвиденная ПАНИКА при обработки запроса на убийство агента.")
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()

	logger.Println("Сервер получил запрос на убийство агента.")

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
		// TODO
	// Пользователь хочет добавить агентов
	} else if act == "revive" {
		// TODO
	} else {
		logger.Println("Неправильный аргумент, выражение не обрабатывается.")
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
}

// Хендлер на endpoint убийства оркестратора
func KillOrchestratorHandlerInternal(w http.ResponseWriter, r *http.Request) {
	// TODO
}

// Хендлер на endpoint мониторинга агентов
func MonitorHandlerInternal(w http.ResponseWriter, r *http.Request) {
	// TODO
}

// Хендлер на endpoint убийства сервера
func (h *SrvSelfDestruct) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Server does not support Flusher!",
			http.StatusInternalServerError)
		return
	}
	logger.Println("Получили запрос на убийство сервера. Сервер умрет через 5 секунд.")
	fmt.Println("Запущено самоуничтожение севера...")
	for i := 5; i > 0; i-- {
		_, err := fmt.Fprintf(w, "Сервер умрет через %v\n", i)
		flusher.Flush()
		fmt.Println(i, err)
		<-time.After(time.Second)
	}
	fmt.Fprintln(w, "Бабах!")
	logger.Println("Сервер умирает...")
	fmt.Println("Сервер умер...")
	flusher.Flush()
	err = Srv.Close()
	if err != nil {
		loggerErr.Println("Не смогли закрыть HTTP сервер:", err)
		http.Error(w, http.StatusText(500), http.StatusInternalServerError)
	}
	logger.Println("Закрыли HTTP сервер.")
}

// Хендлер на endpoint страницы аутентификации
func AuthHandlerInternal(w http.ResponseWriter, r *http.Request) {
	// TODO
}

func RegisterHandlerInternal(w http.ResponseWriter, r *http.Request) {
	// TODO
}

// Хендлер на endpoint выхода из аккаунта
func LogOutHandlerInternal(w http.ResponseWriter, r *http.Request) {
	// TODO
}
