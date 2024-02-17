package main

import (
	// "database/sql" // TODO
	"fmt"
	"os"

	// "calculator/vars" // TODO
	"calculator/cmd"
	"calculator/backend/cmd/orchestrator"

	// _ "github.com/jackc/pgx/v5"
	// _ "github.com/jackc/pgx/v5/stdlib" // Standard library bindings for pgx // TODO
)


// TODO

// var (
// 	db           *sql.DB
// 	err          error
// 	db_existance []byte
// )


func main() {
	logger := shared.Logger
	logger.Println("Останавливаем калькулятор...")
	os.Exit(2)

	fmt.Printf("Хотите завершить последнюю сессию работы с калькулятором (1)")
	fmt.Printf(" или завершить работу с проектом, удалив из базы PostgreSQL все использовавшиеся данные (2)?")
	fmt.Printf(" Отправьте цифру выбранного варианта:\t")
	var opt string
	fmt.Scanln(&opt)
	switch opt {
	case "1":
		logger.Printf("Был выбран вариант '%s'", "Заввершение сессии")
		tempShutDown(shared.GetDBSTate())
	case "2":
		logger.Printf("Был выбран вариант '%s'", "Полное заввершение работы")
		completeShutDown(shared.GetDBSTate())
	default:
		logger.Printf("Был выбран неизвестный вариант, завершаем программу")
		fmt.Printf("ОШИБКА: Был получен неизвестный ответ, никакого завершения работы выполнено не было.")
		fmt.Printf(" Чтобы запустить завершение работы, запустите файл shutdown.go еще раз.")
	}

	// dbFull := shared.Db_info{true, true, true, true}
}

func completeShutDown(dbInfo shared.Db_info) {
	// TODO
}

func tempShutDown(dbInfo shared.Db_info) {
	orchestrator.ServerExitChannel <- os.Interrupt
}
