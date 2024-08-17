// W I P
package postgresDB

import (
	"fmt"
	"log"
	"strings"
	"time"

	"calculator/internal"
	"calculator/internal/config"
	"calculator/internal/errors"
	"calculator/internal/storage"
	"calculator/internal/frontend/server/utils"

	"github.com/jackc/pgx"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // Standard library bindings for pgx
)

var (
	err               error
	errs			  = myErrors.StorageErrors
	loggerErr         *log.Logger = shared.LoggerErr
	loggerStor		  *log.Logger = shared.LoggerStorage
)

type Storage struct {
	db *pgx.ConnPool
}

func NewDB() Storage

func (s *Storage) RegisterUser(username, password string) error {
	username, err = sanitzieName(username)
	if err != nil {
		return err
	}

	// Проверяем, существует ли уже пользователь с таким именем
	exists, err := s.userExists(username)
	if err != nil {
		loggerErr.Println("Сервер: ошибка проверки имени пользователя в базе данных:", err)
		return err
	}
	if exists {
		return errs.AlreadyUsedName
	}

	loggerStor.Println("Hello postgres")
	tx, err := s.db.Begin()
	loggerStor.Println("Bye postgres")
	if err != nil {
		loggerErr.Println("БД: не удалось открыть транзакцию")
	}
	defer tx.Rollback() // Откатывать после комитта безопасно, транзакция выполнится

	// Генерируем хеш
	hashedPswd, err := utils.GenerateHashFromPswd(password)
	if err != nil {
		loggerErr.Println("БД: Ошибка при хешировании пароля:", err)
		// Здесь и дальше не откатываем транзакцию, так как это уже запланированно в отложенной функции
		return err
	}

	// Записываем пользователя в БД
	loggerStor.Println("Hello postgres")
	_, err = tx.Exec(
		`INSERT INTO users (username, password_hash) VALUES ($1, $2);`,
		username,
		hashedPswd,
	)
	loggerStor.Println("Bye postgres")
	if err != nil {
		loggerErr.Println("БД: Ошибка при записи пользователя:", err)
		return err
	}

	// Создаем схему для пользователя
	loggerStor.Println("Hello postgres")
	_, err = tx.Exec(fmt.Sprintf("CREATE SCHEMA %s;", username))
	loggerStor.Println("Bye postgres")
	if err != nil {
		loggerErr.Println("БД: Ошибка при создании схемы для пользователя:", err)
		return err
	}
	// Создаем таблицу в созданной схеме
	loggerStor.Println("Hello postgres")
	_, err = tx.Exec(
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.time_vars (", username)+
		`action varchar (15) NOT NULL,
		time integer NOT NULL
		);`,
	)
	loggerStor.Println("Bye postgres")
	if err != nil {
		loggerErr.Println("БД: Ошибка при создании таблицы в схеме пользователя:", err)
		return err
	}
	for i := 0; i < 4; i++ {
		loggerStor.Println("Hello postgres")
		_, err = tx.Exec(fmt.Sprintf("INSERT INTO %s.time_vars (action, time) VALUES ($1, $2);", username),
			[]string{"summation", "substraction", "multiplication", "division"}[i],
			fmt.Sprint(int([]time.Duration{
				vars.T_sum,
				vars.T_sub,
				vars.T_mult,
				vars.T_div}[i])/1000000),
		)
		loggerStor.Println("Bye postgres")
		if err != nil {
			loggerErr.Println("не смогли добавить время в БД (", []string{
				"summation",
				"substraction",
				"multiplication",
				"division"}[i], "): ", err)
		}
		return err
	}

	tx.Commit()
	return nil
}

func (s *Storage) LogInUser(username, password string) (bool, error) {
	username, err = sanitzieName(username)
	if err != nil {
		return false, err
	}

	// Проверяем, существует ли пользователь с таким именем
	exists, err := s.userExists(username)
	if err != nil {
		loggerErr.Println("Сервер: ошибка проверки имени пользователя в базе данных:", err)
		return false, err
	}
	if !exists {
		return false, nil
	}

	// Достаем хеш пароля из БД
	var hash string
	loggerStor.Println("Hello postgres")
	err = s.db.QueryRow(`SELECT password_hash FROM users WHERE username=$1`, username).Scan(&hash)
	loggerStor.Println("Bye postgres")
	if err != nil {
		loggerErr.Println("БД: ошибка получения хеша пароля:", err)
	}
	// Проверяем пароль
	err = utils.CompareHashAndPassword(hash, password)
	if err != nil {
		return false, nil
	}

	return true, nil
}

func (s *Storage) ExpressionStore(id, user, expression string) error
func (s *Storage) ExpressionCheck(id, user string) (bool, error)
func (s *Storage) ExpressionAddResult(id, result string, err bool) error
func (s *Storage) GetExpressionsUser(user string) []storage.ExpItem
func (s *Storage) GetExpressionsAll() []storage.ExpItem

// Проверка имени и удаление из него пробелов
func sanitzieName(username string) (string, error) {
	if strings.ContainsAny(username, ";,$()") {
		return "", errs.IllegalName
	}
	return strings.ReplaceAll(username, " ", ""), nil
}

func (s *Storage) userExists(u string) (bool, error) {
	var exists bool
	loggerStor.Println("Hello postgres")
	err = s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)", u).Scan(&exists)
	loggerStor.Println("Bye postgresql")
	if err != nil {
		return false, err
	}
	return exists, nil
}
