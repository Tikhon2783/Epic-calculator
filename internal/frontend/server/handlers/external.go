package handlers

import (
	"fmt"
	"html/template"
	"log"
	"net/http"

	// "calculator/internal"
	shared "calculator/internal"
	"calculator/internal/jwt-stuff"
	// "calculator/internal/errors"

	"github.com/jackc/pgx"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // Standard library bindings for pgx
)

type expData struct {
	Message string
	Items []expItem
}

type expItem struct {
    ID   string
    Exp string
	Result string
	Agent int
	Username string
}

func AuthHandlerExternal(w http.ResponseWriter, r *http.Request) {
	files := []string{
		"internal/frontend/pages/auth/auth.page.tmpl",
		"internal/frontend/pages/base.layout.tmpl",
		"internal/frontend/pages/header.partial.tmpl",
	}

	ts, err := template.ParseFiles(files...)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
		return
	}

	err = ts.Execute(w, nil)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
	}
}

func HomeHandler(w http.ResponseWriter, r *http.Request) {
	files := []string{
		"internal/frontend/pages/home/home.page.tmpl",
		"internal/frontend/pages/base.layout.tmpl",
		"internal/frontend/pages/header.partial.tmpl",
	}

	ts, err := template.ParseFiles(files...)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
		return
	}

	err = ts.Execute(w, nil)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
	}
}

func GetExpHandlerExternal(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: непредвиденная ПАНИКА при получении статуса выражения:", rec)
			http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
		}
		logger.Println("Сервер обработал запрос на получение списка выражений.")
	}()
	logger.Println("Получили внешний запрос на страницу с выражениями")

	db = shared.Db
	// Получаем имя пользователя через jwt токен из cookie файлов
	cookie, err := r.Cookie("token")
	if err != nil {
		if err == http.ErrNoCookie {
			// Токена нет, перенаправляем пользователя на страницу входа
			logger.Println("Сервер: пользователь неавторизирован, возвращаем ошибку 401.")
			http.Error(w, "пользователь неавторизирован", http.StatusUnauthorized)
		} else {
			logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Ошибка получения jwt токена:", err)
			http.Error(w, "ошибка получения jwt токена", http.StatusBadRequest)
		}
		return
	}
	tokenStr := cookie.Value
	username, err := jwtstuff.CheckToken(tokenStr)
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Println("Ошибка парсинга jwt токена:", err)
		http.Error(w, "ошибка парсинга jwt токена", http.StatusInternalServerError)
	}

	var perms bool
	err = db.QueryRow("SELECT perms FROM users WHERE username=$1", username).Scan(&perms)
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
			loggerErr.Println("Сервер: ошибка проверки пользователя в БД:", err)
			http.Error(w, "Ошибка проверки пользователя", http.StatusInternalServerError)
			return
	}

	var rows *pgx.Rows
	if !perms {
		rows, err = db.Query("SELECT request_id, expression, calculated, result, errors, agent_proccess FROM requests WHERE username=$1", username)
	} else {
		rows, err = db.Query("SELECT request_id, expression, calculated, result, errors, agent_proccess FROM requests")
	}
	if err != nil {
		logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
		loggerErr.Printf("Сервер: ошибка получения выражений из базы данных: %s", err)
		http.Error(w, "Ошибка получения выражений из базы данных.", http.StatusInternalServerError)
		return
	}

	var (
		exps     []expItem // Слайс с выражениями (и заголовком)
		id       string
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
				exps = append(exps, expItem{
					ID: id,
					Exp: exp,
					Result: "В очереди",
					Agent: agent,
					Username: username,
				})
			} else {
				exps = append(exps, expItem{
					ID: id,
					Exp: exp,
					Result: "Еще считается",
					Agent: agent,
					Username: username,
				})
			}
		} else {
			if errors {
				exps = append(exps, expItem{
					ID: id,
					Exp: exp,
					Result: "Деление на ноль",
					Agent: agent,
					Username: username,
				})
			} else {
				exps = append(exps, expItem{
					ID: id,
					Exp: exp,
					Result: res,
					Agent: agent,
					Username: username,
				})
			}
		}
	}
	if failed != 0 {
		logger.Println(w, "Не удалось получить", failed, "строк.")
	}
	err = rows.Err()
	if err != nil {
		logger.Println("Ошибка со строками.")
		loggerErr.Printf("Сервер: ошибка получения строк из базы данных (но таблицу вывели): %s", err)
		http.Error(w, "Ошибка получения выражений из базы данных.", http.StatusInternalServerError)
		return
	}

	files := []string{
		"internal/frontend/pages/send-exp/send-exp.page.tmpl",
		"internal/frontend/pages/base.layout.tmpl",
		"internal/frontend/pages/header.partial.tmpl",
	}

	ts, err := template.ParseFiles(files...)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
		return
	}

	var msg string
	if failed != 0 {
		msg = fmt.Sprintln("Не удалось получить", failed, "строк.")
	} else {
		msg = ""
	}

	err = ts.Execute(w, expData{
		Message: msg,
		Items: exps,
	})
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
	}
}

func TimeValuesHandlerExternal(w http.ResponseWriter, r *http.Request) {
	files := []string{
		"internal/frontend/pages/values/values.page.tmpl",
		"internal/frontend/pages/base.layout.tmpl",
		"internal/frontend/pages/header.partial.tmpl",
	}

	ts, err := template.ParseFiles(files...)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
		return
	}

	err = ts.Execute(w, nil)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
	}
}

func MonitorHandlerExternal(w http.ResponseWriter, r *http.Request) {
	files := []string{
		"internal/frontend/pages/monitor/monitor.page.tmpl",
		"internal/frontend/pages/base.layout.tmpl",
		"internal/frontend/pages/header.partial.tmpl",
	}

	ts, err := template.ParseFiles(files...)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
		return
	}

	err = ts.Execute(w, nil)
	if err != nil {
		log.Println(err.Error())
		http.Error(w, "Internal Server Error", 500)
	}
}

