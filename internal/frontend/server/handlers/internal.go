package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"calculator/internal"
	"calculator/internal/backend/application/orchestrator"
	"calculator/internal/config"
	"calculator/internal/errors"
	"calculator/internal/frontend/server/middlewares"
	"calculator/internal/frontend/server/utils"
	"calculator/internal/jwt-stuff"

	pb "calculator/internal/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // Standard library bindings for pgx
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure" // для упрощения не будем использовать SSL/TLS аутентификация
)

var (
	ServerExitChannel chan os.Signal = make(chan os.Signal, 1)
	db                *pgx.ConnPool  = shared.GetDb()
	err               error
	logger            *log.Logger = shared.Logger
	loggerErr         *log.Logger = shared.LoggerErr
	Srv               *http.Server
)

type SrvSelfDestruct struct {
	mu sync.Mutex
}



func enableCors(w *http.ResponseWriter) {
    (*w).Header().Set("Access-Control-Allow-Origin", "*")
    (*w).Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
    (*w).Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// Хендлер на endpoint принятия выражений
func HandleExpressionInternal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: неожиданная ПАНИКА при отправке выражения:", rec)
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()
	logger.Println("Обработчик выражений получил запрос...")

	// Получаем значения из контекста
	v, ok := middlewares.FromContext(r.Context())
	if !ok {
		loggerErr.Println("Ошибка получения имени пользователя из контекста:", err)
		logger.Println("Внутренняя ошибка контекста, выражение не обрабатывается.")
		http.Error(w, "ошибка валидации запроса", http.StatusInternalServerError)
		return
	}
	username := v.Username
	exp := v.Expression

	// Генерируем uuid
	uuidWithHyphen := uuid.New()
    logger.Println("Сгенерированный uuid:", uuidWithHyphen)
    uuid := strings.ReplaceAll(uuidWithHyphen.String(), "-", "")

	// Отправляем запрос оркестратору
	host := "localhost"
	port := vars.PortGrpc
	addr := fmt.Sprintf("%s:%s", host, port) // используем адрес сервера
	// установим соединение
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Println("Сервер: не получилось подключиться к серверу gRPC: ", err)
		http.Error(w, "ошибка подключения к gRPC", http.StatusInternalServerError)
		return
	}

	defer func() {
		if err = conn.Close(); err != nil {
			loggerErr.Println("Сервер: ошибка закрытия соединения:", err)
		}
	}()

	grpcClient := pb.NewOrchestratorServiceClient(conn)
	resp, err := grpcClient.SendExp(r.Context(), &pb.ExpSendRequest{
		Username: username,
		Id: uuid,
		Expression: exp,
	})

	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Println("Сервер: запрос оркестратору вернулся ошибкой: ", err)
		http.Error(w, "ошибка запроса оркестратору", http.StatusInternalServerError)
		return
	}

	// Проверяем на наличие ошибок
	if len(resp.Error) != 0 {
		logger.Println("Оркестратор вернул ошибки - проверьте лог ошибок.")
		for i, myErr := range resp.Error {
			loggerErr.Printf("Оркестратор вернул ошибку (%d/%d): %s", i, len(resp.Error), myErr)
		}
		loggerErr.Println("...")
	}

	switch resp.Agent {
	case 0:
		fmt.Fprintln(w, "Ошибка отправки запроса")
	case -1:
		fmt.Fprintln(w, "Выражение успешно принятно оркестратором и поставлено в очередь. ID:", uuid)
	default:
		fmt.Fprintf(w, "Выражение успешно принятно оркестратором и переданно агенту %d. ID: %s", resp.Agent, uuid)
	}
	logger.Printf("Запрос на подсчет выражения обработан (resp.Agent = %d).", resp.Agent)
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

	db = shared.GetDb()

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
	v, ok := middlewares.FromContext(r.Context())
	if !ok {
		loggerErr.Println("Ошибка получения имени пользователя из контекста:", err)
		logger.Println("Внутренняя ошибка контекста, выражение не обрабатывается.")
		http.Error(w, "ошибка валидации запроса", http.StatusInternalServerError)
		return
	}
	username := v.Username
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

// Хендлер на endpoint с значениями времени
func TimeValuesInternal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: непредвиденная ПАНИКА при обработки запроса на значения времени:", rec)
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()

	logger.Println("Сервер получил запрос на изменение значений времени.")
	// Проверяем пользователя на наличие прав изменения таймаута
	v, ok := middlewares.FromContext(r.Context())
	if !ok {
		loggerErr.Println("Ошибка получения имени пользователя из контекста:", err)
		logger.Println("Внутренняя ошибка контекста, выражение не обрабатывается.")
		http.Error(w, "ошибка валидации запроса", http.StatusInternalServerError)
		return
	}
	username := v.Username
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
		for i, val := range []string{tSum, tSub, tMult, tDiv} {
			if val == "" { // Не получили запрос на изменение данной переменной
				continue
			}
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
					[]string{"сложение", "вычитание", "умножение", "деление"}[i],
				)
				continue
			}

			u := strings.ReplaceAll(username, " ", "")
			// Обновляем значение в БД
			_, err = db.Exec(
				fmt.Sprintf("UPDATE %s.time_vars\n", u)+
					`SET time = $2
					WHERE action = $1`,
				[]string{"summation", "substraction", "multiplication", "division"}[i],
				t/1_000_000,
			)
			if err != nil {
				logger.Println("Оркестратор: ошибка изменения значения в БД")
				loggerErr.Printf(
					"Оркестратор: ошибка изменения значения %s на %s в БД: %s",
					err,
					[]string{"summation", "substraction", "multiplication", "division"}[i],
					t,
				)
			}
		}
		// Таймаут
		if tAgent != "" {
			t, err := time.ParseDuration(tAgent)
			if err != nil {
				logger.Println("Сервер: ошибка парсинга времени")
				loggerErr.Printf(
					"Сервер: ошибка парсинга (%s): %s",
					tAgent,
					err,
				)
			} else {
				// Проверяем на валидность значения
				if t < 0 {
					fmt.Fprintf(w, "Время на таймаут было введено отрицательное, пропускаем...")
				} else {
					_, err = db.Exec("UPDATE agent_timeout (time) VALUES ($1) WHERE field=TRUE", t/1_000_000)
					if err != nil {
						panic(err)
					}
				}
			}
		}

	// Если метод не POST и не GET, возвращаем ошибку
	} else if r.Method != http.MethodGet {
		logger.Println("Неправильный метод, выражение не обрабатывается.")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	// Получаем значения и возвращаем
	times := orchestrator.GetTimes(username)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(times); err != nil {
		http.Error(w, "Error encoding default data", http.StatusInternalServerError)
	}
}

// Хендлер на endpoint убийства агента
func KillAgentHandlerInternal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: неожиданная ПАНИКА при обработки запроса на убийство агента:", rec)
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()

	logger.Println("Сервер получил запрос на действие с агентом.")

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
		// Отправляем запрос оркестратору
		host := "localhost"
		port := vars.PortGrpc
		addr := fmt.Sprintf("%s:%s", host, port) // используем адрес сервера
		// установим соединение
		conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: не получилось подключиться к серверу gRPC: ", err)
			http.Error(w, "ошибка подключения к gRPC", http.StatusInternalServerError)
			return
		}
		defer func() {
			if err = conn.Close(); err != nil {
				loggerErr.Println("Сервер: ошибка закрытия соединения:", err)
			}
		}()

		grpcClient := pb.NewOrchestratorServiceClient(conn)
		resp, err := grpcClient.KillAgent(r.Context(), &pb.EmptyMessage{})

		if err != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: запрос оркестратору вернулся ошибкой: ", err)
			http.Error(w, "ошибка запроса оркестратору", http.StatusInternalServerError)
			return
		}

		// Проверяем на наличие ошибок
		if len(resp.Error) != 0 {
			logger.Println("Оркестратор вернул ошибки - проверьте лог ошибок.")
			for i, myErr := range resp.Error {
				loggerErr.Printf("Оркестратор вернул ошибку (%d/%d): %s", i, len(resp.Error), myErr)
			}
			loggerErr.Println("...")
		}

		switch resp.Agent {
		case 0:
			fmt.Fprintln(w, "Ошибка отправки запроса")
		case -1:
			fmt.Fprintln(w, "Не осталось живых агентов.")
		default:
			fmt.Fprintf(w, "Агент %d отключен.", resp.Agent)
		}
		logger.Printf("Запрос на отключение агента обработан (resp.Agent = %d).", resp.Agent)
	// Пользователь хочет добавить агентов
	} else if act == "revive" {
		// Отправляем запрос оркестратору
		host := "localhost"
		port := vars.PortGrpc
		addr := fmt.Sprintf("%s:%s", host, port) // используем адрес сервера
		// установим соединение
		conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: не получилось подключиться к серверу gRPC: ", err)
			http.Error(w, "ошибка подключения к gRPC", http.StatusInternalServerError)
			return
		}
		defer func() {
			if err = conn.Close(); err != nil {
				loggerErr.Println("Сервер: ошибка закрытия соединения:", err)
			}
		}()

		grpcClient := pb.NewOrchestratorServiceClient(conn)
		resp, err := grpcClient.ReviveAgent(r.Context(), &pb.EmptyMessage{})

		if err != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: запрос оркестратору вернулся ошибкой: ", err)
			http.Error(w, "ошибка запроса оркестратору", http.StatusInternalServerError)
			return
		}

		// Проверяем на наличие ошибок
		if len(resp.Error) != 0 {
			logger.Println("Оркестратор вернул ошибки - проверьте лог ошибок.")
			for i, myErr := range resp.Error {
				loggerErr.Printf("Оркестратор вернул ошибку (%d/%d): %s", i, len(resp.Error), myErr)
			}
			loggerErr.Println("...")
		}

		switch resp.Agent {
		case 0:
			fmt.Fprintln(w, "Ошибка отправки запроса")
		case -1:
			fmt.Fprintln(w, "Не осталось мертвых агентов.")
		default:
			fmt.Fprintf(w, "Агент %d подключен.", resp.Agent)
		}
		logger.Printf("Запрос на подключение агента обработан (resp.Agent = %d).", resp.Agent)
	} else {
		logger.Println("Неправильный аргумент, выражение не обрабатывается.")
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
	}
}

// Хендлер на endpoint убийства оркестратора
func KillOrchestratorHandlerInternal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: неожиданная ПАНИКА при запросе на убийство оркестратора:", rec)
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
	}()
	logger.Println("Обработчик оркестратора получил запрос...")

	// Отправляем запрос оркестратору
	host := "localhost"
	port := vars.PortGrpc
	addr := fmt.Sprintf("%s:%s", host, port) // используем адрес сервера
	// установим соединение
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Println("Сервер: не получилось подключиться к серверу gRPC: ", err)
		http.Error(w, "ошибка подключения к gRPC", http.StatusInternalServerError)
		return
	}

	defer func() {
		if err = conn.Close(); err != nil {
			loggerErr.Println("Сервер: ошибка закрытия соединения:", err)
		}
	}()

	grpcClient := pb.NewOrchestratorServiceClient(conn)
	resp, err := grpcClient.KillOrch(r.Context(), &pb.EmptyMessage{})
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Println("Сервер: запрос оркестратору вернулся ошибкой:", err)
		http.Error(w, "ошибка запроса оркестратору", http.StatusInternalServerError)
		return
	}
	if resp.Error != "" {
		logger.Println("Оркестратор вернул ошибку.")
		loggerErr.Println("Сервер: оркестратору вернул ошибку:", err)
		http.Error(w, "ошибка запроса оркестратору", http.StatusInternalServerError)
		return
	}

	logger.Println("Оркестратор должен быть отключен, возвращаем код 200.")
}

// // Хендлер на endpoint мониторинга агентов
// func MonitorHandlerInternal(w http.ResponseWriter, r *http.Request) {
// 	// TODO
// }

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
	db = shared.GetDb()
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
	logger.Printf("register- username '%s', password '%s'", username, password)

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
	for i := 0; i < 4; i++ {
		db.Exec(fmt.Sprintf("INSERT INTO %s.time_vars (action, time) VALUES ($1, $2);", u),
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

	db = shared.GetDb()
	logger.Println("Сервер получил запрос на вход пользователя.")
	// Метод должен быть POST
	if r.Method != http.MethodPost {
		logger.Println("Неправильный метод, запрос не обрабатывается:", r.Method)
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
	logger.Printf("log in- username '%s', password '%s'", username, password)

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
		loggerErr.Panic(err)
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
	logger.Println(cookie.Valid(), cookie.Value)
	http.SetCookie(w, cookie)
	logger.Println("Записали jwt токен в cookie.")
}

// Хендлер на endpoint выхода из аккаунта
func LogOutHandlerInternal(w http.ResponseWriter, r *http.Request) {
	// получаем имя пользователя из контекста запроса
	v, ok := middlewares.FromContext(r.Context())
	if !ok {
		loggerErr.Println("Ошибка получения имени пользователя из контекста:", err)
		logger.Println("Внутренняя ошибка контекста, запрос не обрабатывается.")
		http.Error(w, "ошибка авторизации", http.StatusInternalServerError)
		return
	}
	username := v.Username
	logger.Printf("Сервер получил запрос на выход пользователя %s из аккаунта.\nСбрасываем cookie с токеном.", username)
	http.SetCookie(w, &http.Cookie{
		Name:    "token",
		Expires: time.Now().Add(time.Millisecond),
		Value: "",
	})
}
