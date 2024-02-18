package main

import (
	// "context"
	// "database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	// "errors"

	"calculator/backend/cmd/orchestrator"
	"calculator/cmd"
	"calculator/vars"

	"github.com/jackc/pgx"
	_ "github.com/jackc/pgx/v5"
	// "github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // Standard library bindings for pgx
)

var (
	db           *pgx.ConnPool = shared.Db
	err          error
	db_existance []byte
	logger       *log.Logger = shared.Logger
	loggerErr    *log.Logger = shared.LoggerErr
)

func main() {
	logger.Println("Запускаем калькулятор...")

	// Проверяем, существует ли БД / возобновляем работу или начинаем заново
	db_info := shared.GetDBSTate() // Получаем JSON структуру из файла db_existance.json
	dbFull := shared.Db_info{Db: true, T_requests: true, T_agent: true, T_vars: true}
	if db_info != dbFull { // Чего-то нехватает, значит это первый запуск калькулятора
		StartUp(logger, loggerErr, db_info) // Подготавливаем БД
	} else {
		fmt.Println("Настройка базы данных не требуется.")
		poolConfig := pgx.ConnPoolConfig{
			ConnConfig:     pgx.ConnConfig{
				Host:     vars.DBHost,
				Port:     vars.DBPort,
				Database: vars.DBName,
				User:     vars.DBUsername,
				Password: vars.DBPassword,
			},
			MaxConnections: vars.N_agents,
		}
		db, err = pgx.NewConnPool(poolConfig)
		if err != nil {
			loggerErr.Fatal(err)
		}
		logger.Printf("Подключились к %s.\n", vars.DBName)
	}

	shared.Db = db
	// Запускаем сервер оркестратора — передаем ему управление
	logger.Println("Передаем управление оркестратору...")
	exitChannel := make(chan os.Signal, 1)
	signal.Notify(exitChannel, os.Interrupt)
	go orchestrator.Launch() // Запускаем горутину оркестратора
	select {                 // Ждем сигнала об остановке программы
	case <-orchestrator.ServerExitChannel: // Сигнал от оркестратора
		logger.Println("HTTP сервер прислал сигнал об остановке, закрываем БД и завершаем работу...")
		fmt.Println("Похоже, HTTP сервер калькулятора остановил свою работу.")
		if err = orchestrator.Srv.Close(); err != nil {
			loggerErr.Println("Не смогли закрыть HTTP сервер:", err)
		}
	case <-exitChannel: // Сигнал от пользователя
		logger.Println("Пользователь прислал сигнал об остановке, закрываем БД и завершаем работу...")
		fmt.Println("Поймал попытку завершить работу с ^C :)")
		if err = orchestrator.Srv.Close(); err != nil {
			loggerErr.Println("Не смогли закрыть HTTP сервер:", err)
		}
		// <-orchestrator.ServerExitChannel
	}
	logger.Println("Hmmm")
	if db == nil {
		fmt.Println("WHAT")
	}
	db.Close()
	time.Sleep(time.Second)
	for _, f := range shared.OpenFiles {
		logger.Println("А файл то пустой", f.Name())
		if f != nil {
			logger.Println("Закрываем файл", f.Name())
			err = f.Close()
			if err != nil {
				loggerErr.Println(err)
			}
		}
	}
	logger.Print("Программа завершенна.")
	fmt.Print("Программа завершенна.")
	os.Exit(0)
}

func StartUp(logger *log.Logger, loggerErr *log.Logger, db_info shared.Db_info) {
	defer func() { // Закрываем БД в случае паники при подготовке к запуску
		if r := recover(); r != nil {
			db.Close()
			loggerErr.Panic(r)
		}
	}()
	fmt.Println("Подготавливаем базу данных калькулятора к запуску...")

	config := pgx.ConnConfig{
		Host:     vars.DBHost,
		Port:     vars.DBPort,
		Database: vars.DBNameDefault,
		User:     vars.DBUsername,
		Password: vars.DBPassword,
	}
	poolConfig := pgx.ConnPoolConfig{
		ConnConfig:     pgx.ConnConfig{
			Host:     vars.DBHost,
			Port:     vars.DBPort,
			Database: vars.DBName,
			User:     vars.DBUsername,
			Password: vars.DBPassword,
		},
		MaxConnections: vars.N_agents,
	}

	if !db_info.Db {
		// Подключаемся к серверу БД (по умолчанию Postgres)
		dbc, err := pgx.Connect(config)
		if err != nil {
			loggerErr.Fatal(err)
		}
		// Ping()
		logger.Printf("Подключились к Postgres.\n")
		// Создаем новую базу с данными (vars/variables.go - DBName)
		_, err = dbc.Exec(fmt.Sprintf("CREATE DATABASE %s;", vars.DBName))
		if err != nil {
			fmt.Println("ERROR CREATING DATABASE")
			panic(err)
		}
		
		db_info.Db = true
		dbc.Close()
		logger.Printf("Создали новое хранилище '%s'.\n", vars.DBName)
	}
	
	// Подключаемся к созданной базе
	db, err = pgx.NewConnPool(poolConfig)
	if err != nil {
		loggerErr.Fatal(err)
	}
	logger.Printf("Подключились к %s.\n", vars.DBName)

	// Создаем необходимые таблицы
	wg := sync.WaitGroup{}

	if !db_info.T_requests {
		func() {
			_, err = db.Exec(
				`CREATE TABLE IF NOT EXISTS requests (
				request_id integer PRIMARY KEY,
				expression varchar(100) NOT NULL,
				calculated boolean DEFAULT FALSE,
				result varchar,
				errors boolean DEFAULT FALSE,
				agent_proccess integer UNIQUE
				);`,
			)
			if err != nil {
				panic(err)
			}
			// db_info.T_requests = true
			logger.Println("Создали таблицу с запросами.")
		}()
	}

	if !db_info.T_agent {
		wg.Add(1)
		go func() {
			_, err = db.Exec(
				`CREATE TABLE IF NOT EXISTS agent_proccesses (
				request_id integer references requests(request_id),
				proccess_id integer PRIMARY KEY references requests(agent_proccess),
				expression varchar(1000) NOT NULL,
				parts varchar(2000),
				parts_results varchar[],
				result varchar
				);`,
			)
			if err != nil {
				panic(err)
			}
			// db_info.T_agent = true
			logger.Println("Создали таблицу с процессами агента.")
			wg.Done()
		}()
	}

	if !db_info.T_vars {
		wg.Add(1)
		go func() {
			// dbT, err := db.Begin()
			// if err != nil {
			// 	panic(err)
			// }
			_, err = db.Exec(
				`CREATE TABLE IF NOT EXISTS time_vars (
				action varchar (15) NOT NULL,
				time integer NOT NULL);`,
			)
			if err != nil {
				panic(err)
			}
			_, err := db.Prepare("fill_times", "INSERT INTO time_vars (action, time) VALUES ($1, $2)")
			if err != nil {
				panic(err)
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
					loggerErr.Fatal("не смогли добавить время в БД (", []string{
						"summation",
						"substraction",
						"multiplication",
						"division"}[i], "): ", err)
				}
			}
			_, err = db.Exec("fill_times", "agent_timeout", vars.T_agentTimeout / 1000000)
			if err != nil {
				loggerErr.Fatalln("не смогли добавить время в БД (таймаут агентов):", err)
			}
			db_info.T_vars = true
			logger.Println("Создали таблицу с переменными.")
			wg.Done()
		}()
	}

	wg.Wait()

	// Обновляем данные о состоянии БД
	db_existance, err = json.MarshalIndent(db_info, "", "\t")
	if err != nil {
		panic(err)
	}
	f, err := os.Create("vars/db_existance.json")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	f.Write(db_existance)
	fmt.Println("Закончили первичную настройку БД :)")
}