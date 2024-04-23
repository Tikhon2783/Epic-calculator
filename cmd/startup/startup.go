package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"
	"strings"

	"calculator/internal"
	"calculator/internal/config"
	"calculator/internal/frontend/server"
	"calculator/internal/frontend/server/utils"
	// "calculator/internal/backend/application"

	"github.com/jackc/pgx"
	_ "github.com/jackc/pgx/v5"
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
	// go orchestrator.Launch() // Горутина запуска состовляющих калькулятора
	serverExitChannel, orchestratorReviveChannel := frontserver.LaunchServer()

	// Ждем сигнала об остановке программы
	Loop:
	for {	
		select {
		case <-serverExitChannel: // Сигнал от сервера
			logger.Println("HTTP сервер прислал сигнал об остановке, закрываем БД и завершаем работу...")
			fmt.Println("Похоже, HTTP сервер калькулятора остановил свою работу.")
			if err = frontserver.Srv.Close(); err != nil {
				loggerErr.Println("Не смогли закрыть HTTP сервер:", err)
			}
			break Loop
		case <-exitChannel: // Сигнал от пользователя
			logger.Println("Пользователь прислал сигнал об остановке, закрываем БД и завершаем работу...")
			fmt.Println("Поймал попытку завершить работу с ^C :)")
			if err = frontserver.Srv.Close(); err != nil {
				loggerErr.Println("Не смогли закрыть HTTP сервер:", err)
			}
			break Loop
		default:
			select{
			case <-orchestratorReviveChannel: // Сигнал о том, что нужно повторно запустить оркестратор
				// TODO
			default:
			}
		}
	}

	logger.Println("...")
	if db == nil {
		// Такого быть не должно
		fmt.Println("!!! Ошибка с БД: пустой указатель !!!")
		logger.Println("!!! Ошибка с БД: пустой указатель !!!")
		loggerErr.Println("!!! Ошибка с БД: пустой указатель !!!")
	}
	db.Close()
	// Закрываем файлы логов
	logger.Println(shared.OpenFiles)
	time.Sleep(time.Millisecond * 1010)
	for _, f := range shared.OpenFiles {
		if f != nil {
			log.Println("Закрываем файл:", f.Name())
			if err = f.Close(); err != nil {
				logger.Println("Не смогли закрыть файл", f.Name())
				loggerErr.Println(err)
			}
		} else {
			logger.Println("Файл пустой:", f.Name())	
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
				request_id char(36) PRIMARY KEY,
				username varchar(50) NOT NULL,
				expression varchar(100) NOT NULL,
				calculated boolean DEFAULT FALSE,
				result varchar DEFAULT 0,
				errors boolean DEFAULT FALSE,
				agent_proccess integer DEFAULT 0
				);`,
			)
			if err != nil {
				panic(err)
			}
			db_info.T_requests = true
			logger.Println("Создали таблицу с запросами.")
		}()
	}

	if !db_info.T_agent {
		wg.Add(1)
		go func() {
			_, err = db.Exec(
				`CREATE TABLE IF NOT EXISTS agent_proccesses (
				request_id char(36) references requests(request_id),
				proccess_id integer PRIMARY KEY,
				expression varchar(1000) NOT NULL,
				parts varchar(2000),
				parts_results varchar[],
				result varchar
				);`,
			)
			if err != nil {
				panic(err)
			}
			db_info.T_agent = true
			logger.Println("Создали таблицу с процессами агента.")
			wg.Done()
		}()
	}

	if !db_info.T_users {
		wg.Add(1)
		go func() {
			_, err = db.Exec(
				`CREATE TABLE IF NOT EXISTS users (
				username varchar(50) PRIMARY KEY,
				password_hash varchar(500) NOT NULL,
				perms bool DEFAULT FALSE
				);`,
			)
			if err != nil {
				panic(err)
			}
			db_info.T_users = true
			logger.Println("Создали таблицу с пользователями.")
			wg.Done()
		}()
	}

	if !db_info.T_vars {
		// Записываем пользователей с правами
		for _, u := range vars.Admins {
			wg.Add(1)
			go func(u struct{Username string; Password string}) {
				// Генерируем хеш
				hashedPswd, err := utils.GenerateHashFromPswd(u.Password)
				if err != nil {
					logger.Println("Ошибка при хешировании пароля, таблица не создана.")
					loggerErr.Println("Ошибка при хешировании пароля:", err)
				}

				// Записываем пользователя в БД
				_, err = db.Exec(
					`INSERT INTO users (username, password_hash, perms) VALUES ($1, $2, TRUE);`,
					u.Username,
					hashedPswd,
				)
				if err != nil {
					panic(err)
				}
				logger.Printf("Записали пользователя %s в БД.", u.Username)

				// Создаем схему для пользователя
				_, err = db.Exec(fmt.Sprintf("CREATE SCHEMA %s;", strings.ReplaceAll(u.Username, " ", "")))
				if err != nil {
					panic(err)
				}
				// Создаем таблицу в созданной схеме
				_, err = db.Exec(
					fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.time_vars (", strings.ReplaceAll(u.Username, " ", ""))+
					`action varchar (15) NOT NULL,
					time integer NOT NULL
					);`,
				)
				if err != nil {
					panic(err)
				}
				for i := 0; i < 4; i++ {
					_, err = db.Exec(fmt.Sprintf("INSERT INTO %s.time_vars (action, time) VALUES ($1, $2);", strings.ReplaceAll(u.Username, " ", "")),
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
				db_info.T_vars = true
				logger.Printf("Создали таблицу с переменными для пользователя '%s'.", u.Username)
				wg.Done()
			}(u)
		}
	}

	if db_info.T_timeout {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err = db.Exec(
				`CREATE TABLE IF NOT EXISTS agent_timeout (time integer NOT NULL);`,
			)
			if err != nil {
				panic(err)
			}
			_, err = db.Exec("INSERT INTO agent_timeout (time) VALUES ($1)", vars.T_agentTimeout/1_000_000)
			db_info.T_timeout = true
			logger.Println("Создали таблицу с таймаутом.")
		}()
	}

	wg.Wait()

	// Обновляем данные о состоянии БД
	db_existance, err = json.MarshalIndent(db_info, "", "\t")
	if err != nil {
		panic(err)
	}
	f, err := os.Create("internal/config/db_existance.json")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	f.Write(db_existance)
	fmt.Println("Закончили первичную настройку БД :)")
}