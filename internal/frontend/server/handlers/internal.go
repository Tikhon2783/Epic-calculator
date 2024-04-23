package handlers

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"encoding/json"

	"calculator/internal"
	"calculator/internal/backend/application/orchestrator"
	"calculator/internal/config"
	"calculator/internal/errors"
	"calculator/internal/frontend/server/utils"
	"calculator/internal/jwt-stuff"

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

func enableCors(w *http.ResponseWriter) {
    (*w).Header().Set("Access-Control-Allow-Origin", "*")
    (*w).Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
    (*w).Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

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
			loggerErr.Println("Сервер: непредвиденная ПАНИКА при получении статуса выражения:", rec)
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
	// получаем значение из контекста запроса
    username := r.Context().Value(myKey).(string)
	var perms bool
	err = db.QueryRow("SELECT perms FROM users WHERE username=$1", username).Scan(&perms)

	logger.Println("Hello postgresql")
	if !perms {
		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM requests WHERE request_id=$1 AND username=$2)", id, username).Scan(&exists)
	} else {
		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM requests WHERE request_id=$1)", id).Scan(&exists)
	}
	logger.Println("Bye postgresql")
	if err != nil {
		logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
		loggerErr.Println("Сервер: ошибка проверки ключа в базе данных:", err)
		http.Error(w, myErrors.FrontSrvErrors.InternalDbKeyCheckError.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		logger.Println("Выражение с полученным ID не найдено.")
		http.Error(w, myErrors.FrontSrvErrors.InexistantIDError.Error(), http.StatusBadRequest)
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

// // Хендлер на endpoint получения списка выражений
// func GetExpHandlerInternal(w http.ResponseWriter, r *http.Request) {
// 	defer func() {
// 		if rec := recover(); rec != nil {
// 			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
// 			loggerErr.Println("Сервер: непредвиденная ПАНИКА при получении статуса выражения:", rec)
// 			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
// 		}
// 		logger.Println("Сервер обработал запрос на получение списка выражений.")
// 	}()

// 	logger.Println("Сервер получил запрос на получение списка выражений.")

// 	// Метод должен быть GET
// 	if r.Method != http.MethodGet {
// 		logger.Println("Неправильный метод, выражение не обрабатывается.")
// 		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
// 		return
// 	}

// 	username := r.Header.Get("X-Username")
// 	var perms bool
// 	err = db.QueryRow("SELECT perms FROM users WHERE username=$1", username).Scan(&perms)
// 	if err != nil {
// 		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
// 			loggerErr.Println("Сервер: ошибка проверки пользователя в БД:", err)
// 			http.Error(w, "Ошибка проверки пользователя", http.StatusInternalServerError)
// 			return
// 	}

// 	var rows *pgx.Rows
// 	if !perms {
// 		rows, err = db.Query("SELECT request_id, expression, calculated, result, errors, agent_proccess FROM requests WHERE username=$1", username)
// 	} else {
// 		rows, err = db.Query("SELECT request_id, expression, calculated, result, errors, agent_proccess FROM requests", username)
// 	}
// 	if err != nil {
// 		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
// 		loggerErr.Printf("Сервер: ошибка получения выражений из базы данных: %s", err)
// 		http.Error(w, "Ошибка получения выражений из базы данных.", http.StatusInternalServerError)
// 		return
// 	}

// 	var (
// 		exps     [][]string = [][]string{{"ID", "выражение", "рез-т", "агент"}} // Слайс с выражениями (и заголовком)
// 		id       int
// 		exp      string
// 		finished bool
// 		res      string
// 		errors   bool
// 		agent    int
// 		failed   int // Сколько строк не смогли получить (ошибка от постгреса)
// 	)
// 	defer rows.Close()
// 	for rows.Next() {
// 		err := rows.Scan(&id, &exp, &finished, &res, &errors, &agent)
// 		if err != nil {
// 			logger.Println("Не смогли получить ряд из таблицы с выражениями, записываем, продолжаем получать ряды")
// 			loggerErr.Printf("Сервер: ошибка получения выражения из базы данных: %s", err)
// 			failed++
// 			continue
// 		}

// 		if !finished {
// 			if agent == -1 {
// 				exps = append(exps, []string{fmt.Sprint(id), exp, "не подсчитано", "в очереди"})
// 			} else {
// 				exps = append(exps, []string{fmt.Sprint(id), exp, "не подсчитано", fmt.Sprintf("агент %v", agent)})
// 			}
// 		} else {
// 			if errors {
// 				exps = append(exps, []string{fmt.Sprint(id), exp, "ошибка", fmt.Sprintf("агент %v", agent)})
// 			} else {
// 				exps = append(exps, []string{fmt.Sprint(id), exp, res, fmt.Sprintf("агент %v", agent)})
// 			}
// 		}
// 	}
// 	utils.PrintTable(exps, w) // Выводим таблицу в консоль
// 	if failed != 0 {
// 		fmt.Fprintln(w, "Не удалось получить", failed, "строк.")
// 	}
// 	err = rows.Err()
// 	if err != nil {
// 		logger.Println("Ошибка со строками.")
// 		loggerErr.Printf("Сервер: ошибка получения строк из базы данных (но таблицу вывели): %s", err)
// 		// http.Error(w, "Ошибка получения выражений из базы данных.", http.StatusInternalServerError
// 		return
// 	}
// }

// Хендлер на endpoint с значениями времени
func TimeValuesInternal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: непредвиденная ПАНИКА при обработки запроса на значения времени:", rec)
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()

	logger.Println("Сервер получил запрос на получение/изменение значений времени.")
	// Проверяем пользователя на наличие прав изменения таймаута
	username := r.Context().Value(myKey).(string)
	var perms bool
	err = db.QueryRow("SELECT perms FROM users WHERE username=$1", username).Scan(&perms)
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: ошибка проверки пользователя в БД:", err)
			http.Error(w, "Ошибка проверки пользователя", http.StatusInternalServerError)
			return
	}

	// Если метод POST, обновляем значения
	if r.Method == http.MethodPost {
		// Получаем аргументы запроса
		err := r.ParseForm()
		if err != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: ошибка парсинга запроса на изменение переменных:", err)
			http.Error(w, "Ошибка парсинга запроса", http.StatusInternalServerError)
			return
		}
		tSum := r.PostForm.Get("sum")
		tSub := r.PostForm.Get("sub")
		tMult := r.PostForm.Get("mult")
		tDiv := r.PostForm.Get("div")
		tAgent := r.PostForm.Get("timeout")
		if !perms {
			tAgent = ""
		}

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

			u := strings.ReplaceAll(username, " ", "")
			// Обновляем значение в БД
			_, err = db.Exec(
				fmt.Sprintf("UPDATE %s.time_vars (", u)+
					`SET time = $2
					WHERE action = $1`,
				[]string{"summation", "substraction", "multiplication", "division", "agent_timeout"}[i],
				t/1_000_000,
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
	// Если метод не POST и не GET, возвращаем ошибку
	} else if r.Method != http.MethodGet {
		logger.Println("Неправильный метод, выражение не обрабатывается.")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	// Получаем значения и возвращаем
	times := orchestrator.GetTimes()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(times); err != nil {
		http.Error(w, "Error encoding default data", http.StatusInternalServerError)
	}
	w.WriteHeader(http.StatusOK)
}

// Хендлер на endpoint убийства агента
func KillAgentHandlerInternal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: непредвиденная ПАНИКА при обработки запроса на убийство агента:", rec)
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

// Хендлер регистрации
func RegisterHandlerInternal(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	db = shared.Db
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: непредвиденная ПАНИКА при обработки запроса на регистрацию пользователя:", rec)
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()

	logger.Println("Сервер получил запрос на регистрацию пользователя.")
	// Метод должен быть POST
	if r.Method != http.MethodPost {
		logger.Println("Неправильный метод, запрос не обрабатывается.")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Println("Сервер: ошибка парсинга запроса.")
		http.Error(w, "Ошибка парсинга запроса", http.StatusInternalServerError)
		return
	}

	username := r.Form.Get("username")
	password := r.Form.Get("password")
	logger.Printf("username '%s', password '%s'", username, password)

	// Проверяем, существует ли уже пользователь с таким именем
	var exists bool
	logger.Println("Hello postgresql")
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)", username).Scan(&exists)
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Println("Сервер: ошибка проверки имени пользователя в базе данных.")
		http.Error(w, "Ошибка проверки имени пользователя в базе данных", http.StatusInternalServerError)
		return
	}
	logger.Println("Bye postgresql")
	if exists {
		logger.Println("Пользователь уже существует, возвращаем код 409.")
		http.Error(w, "user already exists", http.StatusConflict)
		return
	}

	// Генерируем хеш
	hashedPswd, err := utils.GenerateHashFromPswd(password)
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Println("Ошибка при хешировании пароля:", err)
		http.Error(w, "hashing error", http.StatusInternalServerError)
	}

	// Записываем пользователя в БД
	_, err = db.Exec(
		`INSERT INTO users (username, password_hash) VALUES ($1, $2);`,
		username,
		hashedPswd,
	)
	if err != nil {
		loggerErr.Panic(err)
	}
	logger.Printf("Записали пользователя %s в БД.", username)

	u := strings.ReplaceAll(username, " ", "")
	// Создаем схему для пользователя
	_, err = db.Exec(fmt.Sprintf("CREATE SCHEMA %s;", u))
	if err != nil {
		loggerErr.Panic(err)
	}
	// Создаем таблицу в созданной схеме
	_, err = db.Exec(
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.time_vars (", u)+
		`action varchar (15) NOT NULL,
		time integer NOT NULL
		);`,
	)
	if err != nil {
		loggerErr.Panic(err)
	}
	_, err = db.Prepare("fill_times", fmt.Sprintf("INSERT INTO %s.time_vars (action, time) VALUES ($1, $2);", u))
	if err != nil {
		loggerErr.Panic(err)
	}
	for i := 0; i < 4; i++ {
		_, err = db.Exec("fill_times",
			[]string{"summation", "substraction", "multiplication", "division"}[i],
			fmt.Sprint(int([]time.Duration{
				vars.T_sum,
				vars.T_sub,
				vars.T_mult,
				vars.T_div}[i])/1000000),
		)
		if err != nil {
			loggerErr.Panic("не смогли добавить время в БД (", []string{
				"summation",
				"substraction",
				"multiplication",
				"division"}[i], "): ", err)
		}
	}
	logger.Printf("Создали таблицу с переменными для пользователя '%s'.", username)
	logger.Println("Пользователь зарегистрирован, производим процесс входа...")

	http.Redirect(w, r, "../signin", http.StatusSeeOther)
}

// Хендлер на endpoint входа
func LogInHandlerInternal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: непредвиденная ПАНИКА при обработки запроса на вход пользователя:", rec)
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()

	logger.Println("Сервер получил запрос на вход пользователя.")
	// Метод должен быть POST
	if r.Method != http.MethodPost {
		logger.Println("Неправильный метод, запрос не обрабатывается.")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Println("Сервер: ошибка парсинга запроса.")
		http.Error(w, "Ошибка парсинга запроса", http.StatusInternalServerError)
		return
	}

	username := r.PostForm.Get("username")
	password := r.PostForm.Get("password")

	// Проверяем, существует ли пользователь с таким именем
	var exists bool
	logger.Println("Hello postgresql")
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)", username).Scan(&exists)
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Println("Сервер: ошибка проверки имени пользователя в базе данных:", err)
		http.Error(w, "Ошибка проверки имени пользователя в базе данных", http.StatusInternalServerError)
		return
	}
	logger.Println("Bye postgresql")
	if !exists {
		logger.Println("Пользователь с таким именем не существует, возвращаем код 401.")
		http.Error(w, "user doesn't exists", http.StatusUnauthorized)
		return
	}

	// Достаем хеш пароля из БД
	var hash string
	err = db.QueryRow(
		`SELECT password_hash FROM users WHERE username=$1`, username).Scan(&hash)
	if err != nil {
		panic(err)
	}
	// Проверяем пароль
	err = utils.CompareHashAndPassword(hash, password)
	if err != nil {
		logger.Println("Хеш пароля не совпадает с хешем из БД, возвращаем код 401.")
		http.Error(w, "password is incorrect", http.StatusUnauthorized)
		return
	}

	// Генерируем JWT
	cookie, err := jwtstuff.GenerateToken(username)
	if err != nil {
		logger.Println("Ошибка генерации токена, возвращаем код 400")
		loggerErr.Println("Ошибка генерации токена:", err)
		http.Error(w, "error generating jwt token", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, cookie)
	logger.Println("Записали jwt токен в cookie.")
}

// Хендлер на endpoint выхода из аккаунта
func LogOutHandlerInternal(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:    "token",
		Expires: time.Now(),
	})
}
