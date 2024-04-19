package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"calculator/internal"
	"calculator/internal/config"

	"github.com/jackc/pgx"
	_ "github.com/jackc/pgx/v5"
	// "github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // Standard library bindings for pgx
)

var (
	db           *pgx.ConnPool = shared.Db
	db_existance []byte
	logger       *log.Logger = shared.Logger
	loggerErr    *log.Logger = shared.LoggerErr
)

func main() {
	logger := shared.GetDebugLogger()
	logger.Println("Останавливаем калькулятор...")

	fmt.Printf("Хотите завершить последнюю сессию работы с калькулятором (1)")
	fmt.Printf(" или завершить работу с проектом, удалив из базы PostgreSQL все использовавшиеся данные (2)?")
	fmt.Printf(" Отправьте цифру выбранного варианта:\t")
	var opt string
	fmt.Scanln(&opt)
	switch opt {
	case "1":
		logger.Printf("Был выбран вариант '%s'.", "Заввершение сессии")
		tempShutDown(shared.GetDBSTate())
	case "2":
		logger.Printf("Был выбран вариант '%s'.", "Полное заввершение работы")
		completeShutDown(shared.GetDBSTate())
	default:
		logger.Printf("Был выбран неизвестный вариант, завершаем программу")
		fmt.Printf("ОШИБКА: Был получен неизвестный ответ, никакого завершения работы выполнено не было.")
		fmt.Printf(" Чтобы запустить завершение работы, запустите файл shutdown.go еще раз.")
	}
	fmt.Println("Чистка завершена.")
}

func completeShutDown(dbInfo shared.Db_info) {
	defer func() { // Закрываем БД в случае паники
		if r := recover(); r != nil {
			db.Close()
			loggerErr.Fatal(r)
		}
	}()
	fmt.Println("Очищаем базу данных...")
	
	// Проверяем, существует ли база
	if !dbInfo.Db {
		logger.Print("База отмечена как еще не созданная. Завершаем программу.")
		fmt.Print("Базы не существует. Сначала запустите программу командой 'go run cmd/startup/startup.go'")
		return
	}
	
	// Подключаемся к базе
	config := pgx.ConnConfig{
		Host:     vars.DBHost,
		Port:     vars.DBPort,
		Database: vars.DBNameDefault,
		User:     vars.DBUsername,
		Password: vars.DBPassword,
	}

	dbc, err := pgx.Connect(config)
	if err != nil {
		panic(err)
	}
	fmt.Println("Подключились к", vars.DBNameDefault)
	
	_, err = dbc.Exec(fmt.Sprintf("DROP DATABASE %s;", vars.DBName))
	if err != nil {
		loggerErr.Println(err)
		fmt.Println("Возникла ошибка, проверьте логгер ошибок.")
	} else {
		dbInfo.Db = false
		dbInfo.T_requests = false
		dbInfo.T_agent = false
		dbInfo.T_vars = false
		logger.Printf("Удалили базу %s.", vars.DBNameDefault)
	}

	// Обновляем данные о состоянии БД
	db_existance, err = json.MarshalIndent(dbInfo, "", "\t")
	if err != nil {
		fmt.Println("Возникла ошибка, проверьте логгер ошибок.")
		panic(err)
	}
	f, err := os.Create("vars/db_existance.json")
	if err != nil {
		fmt.Println("Возникла ошибка, проверьте логгер ошибок.")
		panic(err)
	}
	defer f.Close()
	f.Write(db_existance)
}

func tempShutDown(dbInfo shared.Db_info) {
	defer func() { // Закрываем БД в случае паники
		if r := recover(); r != nil {
			db.Close()
			loggerErr.Fatal(r)
		}
	}()
	fmt.Println("Очищаем базу данных...")
	
	// Проверяем, существует ли база
	if !dbInfo.Db {
		logger.Print("База отмечена как еще не созданная. Завершаем программу.")
		fmt.Print("Базы не существует. Сначала запустите программу командой 'go run cmd/startup/startup.go'")
		return
	}
	
	// Подключаемся к базе
	config := pgx.ConnConfig{
		Host:     vars.DBHost,
		Port:     vars.DBPort,
		Database: vars.DBName,
		User:     vars.DBUsername,
		Password: vars.DBPassword,
	}

	dbc, err := pgx.Connect(config)
	if err != nil {
		fmt.Println("Возникла ошибка, проверьте логгер ошибок.")
		panic(err)
	}
	fmt.Println("Подключились к", vars.DBName)
	
	if dbInfo.T_agent {
		_, err = dbc.Exec("DROP TABLE agent_proccesses;")
		if err != nil {
			loggerErr.Println(err)
			fmt.Println("Возникла ошибка, проверьте логгер ошибок.")
		} else {
			dbInfo.T_agent = false
			logger.Println("Удалили таблицу с агентами.")
		}
	}

	if dbInfo.T_requests {
		_, err = dbc.Exec("DROP TABLE requests;")
		if err != nil {
			loggerErr.Println(err)
		} else {
			dbInfo.T_requests = false
			logger.Println("Удалили таблицу с выражениями.")
		}
	}

	if dbInfo.T_vars {
		_, err = dbc.Exec("DROP TABLE time_vars;")
		if err != nil {
			loggerErr.Println(err)
			fmt.Println("Возникла ошибка, проверьте логгер ошибок.")
		} else {
			dbInfo.T_vars = false
			logger.Println("Удалили таблицу с временем.")
		}
	}

	// Обновляем данные о состоянии БД
	db_existance, err = json.MarshalIndent(dbInfo, "", "\t")
	if err != nil {
		fmt.Println("Возникла ошибка, проверьте логгер ошибок.")
		panic(err)
	}
	f, err := os.Create("vars/db_existance.json")
	if err != nil {
		fmt.Println("Возникла ошибка, проверьте логгер ошибок.")
		panic(err)
	}
	defer f.Close()
	f.Write(db_existance)
}
