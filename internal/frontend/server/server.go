package frontserver

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"calculator/internal"
	"calculator/internal/config"
	"calculator/internal/frontend/server/handlers"
	"calculator/internal/frontend/server/middlewares"
)

var (
	ServerExitChannel chan os.Signal = make(chan os.Signal, 1)
	OrchestratorReviveChannel chan os.Signal = make(chan os.Signal, 1)
	err               error
	logger            *log.Logger = shared.Logger
	loggerErr         *log.Logger = shared.LoggerErr
	Srv               *http.Server
)

// Горутина хоста HTTP сервера
func LaunchServer() (chan os.Signal, chan os.Signal) {
	logger.Println("Настраиваем HTTP сервер...")

	// Настраиваем обработчики для разных путей
	mux := http.NewServeMux()
	// Внутренние endpoin-ы
	mux.Handle("/calculator/internal/kill/server", &handlers.SrvSelfDestruct{})	// Убийство сервера
	mux.HandleFunc("/calculator/internal/kill/agent", handlers.KillAgentHandlerInternal)
	mux.HandleFunc("/calculator/internal/kill/orchestrator", handlers.KillOrchestratorHandlerInternal)
	mux.HandleFunc("/calculator/internal", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "Куда-то ты не туда забрёл...")
	})
	// Принять выражение
	mux.Handle(
		"/calculator/internal/sendexpression",
		middlewares.InternalAuthorizationMiddleware(middlewares.ValidityMiddleware(http.HandlerFunc(handlers.HandleExpressionInternal))),
	)
	// Узнать статус выражения
	mux.Handle(
		"/calculator/internal/checkexpression",
		middlewares.InternalAuthorizationMiddleware(http.HandlerFunc(handlers.CheckExpHandlerInternal)),
	)
	// // Получить список всех выражений
	// mux.Handle(
	// 	"/calculator/internal/getexpressions",
	// 	middlewares.InternalAuthorizationMiddleware(http.HandlerFunc(handlers.GetExpHandlerInternal)),
	// )
	// Получение списка доступных операций со временем их выполения
	mux.Handle(
		"/calculator/internal/values",
		middlewares.InternalAuthorizationMiddleware(http.HandlerFunc(handlers.TimeValuesInternal)),
	)
	// // Получение списка агентов и оркестратора
	// mux.Handle(
	// 	"/calculator/internal/monitor",
	// 	middlewares.InternalAuthorizationMiddleware(http.HandlerFunc(handlers.MonitorHandlerInternal)),
	// )
	mux.HandleFunc("/calculator/internal/signin", handlers.LogInHandlerInternal)		// Аутентификация пользователя
	mux.HandleFunc("/calculator/internal/register", handlers.RegisterHandlerInternal)	// Регистрация пользователя
	mux.HandleFunc("/calculator/internal/logout", handlers.LogOutHandlerInternal)		// Выход из аккаунта

	// Внешние endpoint-ы
	mux.HandleFunc("/calculator/home", handlers.HomeHandler)
	// // Отправить выражение
	// mux.Handle("/calculator/send", middlewares.ExternalAuthorizationMiddleware(http.HandlerFunc(handlers.SendHandlerExternal)))
	// Получить список всех выражений
	mux.Handle(
		"/calculator/check",
		middlewares.ExternalAuthorizationMiddleware(http.HandlerFunc(handlers.GetExpHandlerExternal)),
	)
	// Получение списка доступных операций со временем их выполения
	mux.Handle(
		"/calculator/values",
		middlewares.ExternalAuthorizationMiddleware(http.HandlerFunc(handlers.TimeValuesHandlerExternal)),
	)
	// Получение списка агентов и оркестратора
	mux.Handle(
		"/calculator/monitor",
		middlewares.ExternalAuthorizationMiddleware(http.HandlerFunc(handlers.MonitorHandlerExternal)),
	)
	// Страница входа/регистрации
	mux.HandleFunc(
		"/calculator/auth",
		 handlers.AuthHandlerExternal,
	)


	// Сам http сервер оркестратор
	Srv = &http.Server{
		Addr:     vars.PortHttp,
		Handler:  mux,
		ErrorLog: loggerErr,
	}

	go func() {
		// Запускаем сервер
		logger.Println("Запускаем HTTP сервер...")
		fmt.Println("HTTP сервер готов к работе!")
		if err = Srv.ListenAndServe(); err != nil {
			loggerErr.Println("HTTP сервер накрылся:", err)
		}

		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}()
	return ServerExitChannel, OrchestratorReviveChannel
}
