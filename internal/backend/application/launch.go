package application

import (
	// "log"

	// "calculator/internal"
	// "calculator/internal/config"

	// "calculator/internal/backend/application/agent"
	// "calculator/internal/backend/application/orchestrator"
	// "calculator/internal/frontend/server"
)

// var (
// 	err          error
// 	db_existance []byte
// 	logger       *log.Logger = shared.Logger
// 	loggerErr    *log.Logger = shared.LoggerErr
// )

// // Основная горутина оркестратора
// func Launch() {
// 	logger.Println("Подключился оркестратор.")
// 	fmt.Println("Оркестратор передаёт привет :)")
// 	db = shared.Db

// 	// Подготавливаем запросы в БД
// 	_, err = db.Prepare( // Запись выражения в таблицу с выражениями
// 		"orchestratorPut",
// 		`INSERT INTO requests (request_id, expression, agent_proccess)
// 		VALUES ($1, $2, $3);`,
// 	)
// 	if err != nil {
// 		loggerErr.Println("Паника:", err)
// 		logger.Println("Критическая ошибка, завершаем работу программы...")
// 		logger.Println("Отправляем сигнал прерывания...")
// 		ServerExitChannel <- os.Interrupt
// 		logger.Println("Отправили сигнал прерывания.")
// 	}
// 	_, err = db.Prepare( // Запись результата в таблицу с выражениями
// 		"orchestratorAssign",
// 		`UPDATE requests
// 			SET agent_proccess = $2
// 			WHERE request_id = $1;`,
// 	)
// 	if err != nil {
// 		loggerErr.Println("Паника:", err)
// 		logger.Println("Критическая ошибка, завершаем работу программы...")
// 		logger.Println("Отправляем сигнал прерывания...")
// 		ServerExitChannel <- os.Interrupt
// 		logger.Println("Отправили сигнал прерывания.")
// 	}
// 	_, err = db.Prepare( // Получение результата из таблицы с процессами (агентами)
// 		"orchestratorReceive",
// 		`SELECT request_id, result FROM agent_proccesses
// 			WHERE proccess_id = $1;`,
// 	)
// 	if err != nil {
// 		loggerErr.Println("Паника:", err)
// 		logger.Println("Критическая ошибка, завершаем работу программы...")
// 		logger.Println("Отправляем сигнал прерывания...")
// 		ServerExitChannel <- os.Interrupt
// 		logger.Println("Отправили сигнал прерывания.")
// 	}
// 	_, err = db.Prepare( // Запись результата в таблицу с выражениями
// 		"orchestratorUpdate",
// 		`UPDATE requests
// 			SET calculated = TRUE, result = $2, errors = $3
// 			WHERE request_id = $1;`,
// 	)
// 	if err != nil {
// 		loggerErr.Println("Паника:", err)
// 		logger.Println("Критическая ошибка, завершаем работу программы...")
// 		logger.Println("Отправляем сигнал прерывания...")
// 		ServerExitChannel <- os.Interrupt
// 		logger.Println("Отправили сигнал прерывания.")
// 	}
// 	_, err = db.Prepare( // Получение результата из таблицы с выражениями
// 		"orchestratorGet",
// 		`SELECT expression, calculated, result, errors FROM requests
// 			WHERE request_id = $1;`,
// 	)
// 	if err != nil {
// 		loggerErr.Println("Паника:", err)
// 		logger.Println("Критическая ошибка, завершаем работу программы...")
// 		logger.Println("Отправляем сигнал прерывания...")
// 		ServerExitChannel <- os.Interrupt
// 		logger.Println("Отправили сигнал прерывания.")
// 	}

// 	getTimes()
// 	mu = &sync.RWMutex{}
// 	manager = newAgentsManager() // Мониторинг агентов для запущенного оркестратора

// 	// Настраиваем обработчики для разных путей
// 	mux := http.NewServeMux()
// 	mux.Handle("/calculator/kill/orchestrator", &SrvSelfDestruct{}) // Убийство сервера
// 	mux.HandleFunc("/calculator/kill/agent", KillHandler)
// 	mux.HandleFunc("/calculator", func(w http.ResponseWriter, _ *http.Request) {
// 		fmt.Fprintln(w, "Куда-то ты не туда забрёл...")
// 	})
// 	mux.Handle("/calculator/sendexpression", validityMiddleware(http.HandlerFunc(handleExpression))) // Принять выражение
// 	mux.HandleFunc("/calculator/checkexpression", checkExpHandler)                                   // Узнать статус выражения
// 	mux.HandleFunc("/calculator/getexpressions", getExpHandler)                                      // Получить список всех выражений
// 	mux.HandleFunc("/calculator/values", TimeValues)                                                 // Получение списка доступных операций со временем их выполения
// 	mux.HandleFunc("/calculator/monitor", MonitorHandler)

// 	// Сам http сервер оркестратор
// 	Srv = &http.Server{
// 		Addr:     ":8080",
// 		Handler:  mux,
// 		ErrorLog: loggerErr,
// 	}

// 	// Планируем агентов
// 	logger.Println("Планируем агентов...")
// 	for i := 1; i <= vars.N_agents; i++ {
// 		go agent.Agent(NewAgentComm(i, manager)) // Запускаем горутину агента и передаем ей структуру агента, обновляя монитор агентов
// 		logger.Printf("Запланировали агента %v\n", i)
// 	}

// 	// Планируем горутину мониторинга агентов
// 	go MonitorAgents(manager)

// 	// Проверяем на возобновление работы
// 	CheckWorkLeft(manager)

// 	// Запускаем сервер
// 	logger.Println("Запускаем HTTP сервер...")
// 	fmt.Println("Калькулятор готов к работе!")
// 	if err = Srv.ListenAndServe(); err != nil {
// 		loggerErr.Println("HTTP сервер накрылся:", err)
// 	}

// 	logger.Println("Отправляем сигнал прерывания...")
// 	ServerExitChannel <- os.Interrupt
// 	logger.Println("Отправили сигнал прерывания.")
// }