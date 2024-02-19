package vars

import (
	"time"
	"log"
)

const ( // Переменные для работы Postgres
	DBHost string = "127.0.0.1" 		// ip адресс 
	DBPort uint16 = 5432				// Порт Postgres
	DBUsername string = "postgres"		// Имя пользователя
	DBPassword string = "PostgRess146"	// Пароль (если есть)
	DBName string = "calc_test_database" // Имя БД
	DBNameDefault string = "postgres"	// Стандартное имя БД
)

const ( // Дефолтные временные переменные
	T_sum	time.Duration = time.Millisecond * 500 // Время на выполнение сложения
	T_sub	time.Duration = time.Millisecond * 500 // Время на выполнение вычитания
	T_mult	time.Duration = time.Millisecond * 500 // Время на выполнение умножения
	T_div	time.Duration = time.Millisecond * 500 // Время на выполнение деления
	T_agentTimeout time.Duration = time.Second * 5 // Таймаут для агентов
)

var ( // Переменные для работы логгера
	LoggerFlagsDebug int = log.Lshortfile | log.Ltime	// Флаги обычного логгера
	LoggerFlagsError int = log.Lshortfile | log.Ltime	// Флаги логгера ошибок
	LoggerFlagsPings int = log.Ltime					// Флаги логгера хартбитов
	LoggerOutputDebug int = 1		// Вывод обычного логгера (0: Stdout, 1: backendlogs/debug.txt, 2: Stdout + debug.txt)
	LoggerOutputError int = 2		// Вывод логгера ошибок (0: Stderr, 1: backendlogs/errors.txt, 2: Stderr + errors.txt)
	LoggerOutputPings int = 1		// Вывод логгера ошибок (0: Stdout, 1: backendlogs/heartbeats.txt, 2: Stdout + heartbeats.txt)
)

const (
	N_agents int = 3	// Количество поднимаемых агентов
	N_machines int = -1	// Количество поднимаемых вычеслителей-горутин на агента (-1 если можно поднимать сколько нужно без ограничений)
)