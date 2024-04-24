package orchestrator

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"calculator/internal/backend/application/agent"
	"calculator/internal"
	"calculator/internal/backend/application/orchestrator/monitoring"
	"calculator/internal/config"

	"github.com/jackc/pgx"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // Standard library bindings for pgx
	"google.golang.org/grpc"
	pb "calculator/internal/proto"
)

var (
	ServerExitChannel chan os.Signal = make(chan os.Signal, 1)
	db                *pgx.ConnPool  = shared.Db
	err               error
	logger            *log.Logger = shared.Logger
	loggerErr         *log.Logger = shared.LoggerErr
	loggerHB          *log.Logger = shared.LoggerHeartbeats
	Srv               *http.Server
	manager           *monitoring.AgentsManager
	mu                *sync.RWMutex
	agentsTimeout     *time.Duration
	OrchestratorServiceServer *Server
)

type Server struct {
	pb.OrchestratorServiceClient // сервис из сгенерированного пакета
}

func NewServer() *Server {
	return &Server{}
}

// Основная горутина оркестратора
func Launch() {
	logger.Println("Подключился оркестратор.")
	fmt.Println("Оркестратор передаёт привет :)")
	db = shared.Db

	// Подготавливаем запросы в БД
	_, err = db.Prepare( // Запись выражения в таблицу с выражениями
		"orchestratorPut",
		`INSERT INTO requests (request_id, expression, agent_proccess)
		VALUES ($1, $2, $3);`,
	)
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}
	_, err = db.Prepare( // Запись результата в таблицу с выражениями
		"orchestratorAssign",
		`UPDATE requests
			SET agent_proccess = $2
			WHERE request_id = $1;`,
	)
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}
	_, err = db.Prepare( // Получение результата из таблицы с процессами (агентами)
		"orchestratorReceive",
		`SELECT request_id, result FROM agent_proccesses
			WHERE proccess_id = $1;`,
	)
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}
	_, err = db.Prepare( // Запись результата в таблицу с выражениями
		"orchestratorUpdate",
		`UPDATE requests
			SET calculated = TRUE, result = $2, errors = $3
			WHERE request_id = $1;`,
	)
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}
	_, err = db.Prepare( // Получение результата из таблицы с выражениями
		"orchestratorGet",
		`SELECT expression, calculated, result, errors FROM requests
			WHERE request_id = $1;`,
	)
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}

	// Запускаем gRPC сервер
	host := "localhost"
	port := vars.PortGrpc

	addr := fmt.Sprintf("%s:%s", host, port)
	lis, err := net.Listen("tcp", addr) // будем ждать запросы по этому адресу

	if err != nil {
		loggerErr.Panic("error starting tcp listener: ", err)
	}
	
	logger.Println("tcp listener started at port: ", port)
	// создадим сервер grpc
	grpcServer := grpc.NewServer()
	// объект структуры, которая содержит реализацию 
	// серверной части GeometryService
	OrchestratorServiceServer := NewServer()
	// зарегистрируем нашу реализацию сервера
	pb.RegisterOrchestratorServiceServer(grpcServer, OrchestratorServiceServer)
	// запустим grpc сервер
	if err := grpcServer.Serve(lis); err != nil {
		loggerErr.Println("error serving grpc: ", err)
		logger.Println("GRPC сервер закрылся.")
	}
}

// Функция для получения времени операций из БД
func GetTimes() *agent.Times {
	rows, err := db.Query("SELECT action, time from time_vars;")
	if err != nil {
		loggerErr.Panic(err)
	}
	defer rows.Close()
	t := &agent.Times{}
	for rows.Next() {
		var (
			t_type string
			t_time int
		)
		err := rows.Scan(&t_type, &t_time)
		if err != nil {
			loggerErr.Panic(err)
		}
		switch t_type {
		case "summation":
			t.Sum = time.Duration(t_time * 1000000)
			logger.Printf("Время на сложение:, %v\n", t.Sum)
		case "substraction":
			t.Sub = time.Duration(t_time * 1000000)
			logger.Printf("Время на вычитание:, %v\n", t.Sub)
		case "multiplication":
			t.Mult = time.Duration(t_time * 1000000)
			logger.Printf("Время на умножение:, %v\n", t.Mult)
		case "division":
			t.Div = time.Duration(t_time * 1000000)
			logger.Printf("Время на деление:, %v\n", t.Div)
		case "agent_timeout":
			t.AgentTimeout = time.Duration(t_time * 1000000)
			logger.Printf("Таймаут агентов — %v\n", t.AgentTimeout)
			agentsTimeout = &t.AgentTimeout
		}
	}
	err = rows.Err()
	if err != nil {
		loggerErr.Panic(err)
	}
	return t
}

// Функция, которая проверяет, есть ли в БД непосчитанные выражения
func CheckWorkLeft(m *monitoring.AgentsManager) {
	logger.Println("Оркестратор: проверка на непосчитанные выражения...")
	var id string
	rows, err := db.Query(
		`SELECT request_id FROM requests
			WHERE calculated = FALSE`,
	)
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}
	defer rows.Close()

	for rows.Next() {
		logger.Println("Оркестратор нашел непосчитанное выражение...")
		err := rows.Scan(&id)
		if err != nil {
			loggerErr.Println("Паника:", err)
			logger.Println("Критическая ошибка, завершаем работу программы...")
			logger.Println("Отправляем сигнал прерывания...")
			ServerExitChannel <- os.Interrupt
			logger.Println("Отправили сигнал прерывания.")
		}

		logger.Println("Отдаем выражение менеджеру...")
		m.HandleExpression(id)
	}
	err = rows.Err()
	if err != nil {
		loggerErr.Println("Паника:", err)
		logger.Println("Критическая ошибка, завершаем работу программы...")
		logger.Println("Отправляем сигнал прерывания...")
		ServerExitChannel <- os.Interrupt
		logger.Println("Отправили сигнал прерывания.")
	}
	logger.Println("Оркестратор закончил проверку на непосчитанные выражения.")
}

/*
                                                  .:
                                               .-==
                                 :---:    *+++*+.
                             :=++-----++:-+----+=
                          -=+=-----===--+#------#
                       -++-----+++=---++-++----=+
                    .=+=----++=---------+=++---*.
                   =*-----++-------------*=*=-+=
                  +=-----*--------------==#+#+#:   .:-:.
.-.              -*-----*----------====---========*=--:=+
  -=:++++=-:     -+-----++------=+=-:::==:  .::-+:-+%@+:#
    +------=+=:  .#------=*=--++-:::::+:    %@#:-=:::=+=*
    ==--------=+=:++-------**=:----:::*     :=: =-:::::+=                          .
     *=-----------+#*=---=*-==-..::-+:==       =+:::::::-+                   -:#+++++:
      -+=-------------=+*+:+.   %%#. *:-==---==:::::::::::*.               *+**+=----++
        :=+=---------=++*:--    -+-  #-*%@*==-:::::::::::::*.             :*-=++------=#.
           .-===+++=*-:*=::*        +=*#*=:::*::::::::::::::*.           .#+++=---=++=-.
                   :*:+%=::-+-:..:-=--+:-=+:+=:::::::::::::::*.        -*%@%*+++==:
                    ++:-*:::::---:::::-=* :+-+::::::::::::::::*     .=%%%#*:
                     .-=*-:::::::::::::::==:::::::::::::::::::-+  -*%*%%+.
                        .*:::::::::::G:O:L:A:N:G:::::::::::::::++###@#-
                         .*::::::::::::::::::::::::::::::::::-*%##%+:
                          :*::::::::::::::::::-::::::::::::=#%*%%:=:
                           :*:::::::::::::::=+-=+-::::::-+%##%*-++*
                            :*:::::--=======*++:+*+++=+###%#=:::-*=
                            :=*=++==-----------=---=+***%+-:::::-+
                            +=------------------=+***=--#::::::::+
                             =+---------------=+%#*=---=%-:::::::+
                              :+-------=++--=**+=------*#:::::::=-
                               .*-------**#**=---------##:::::::+.
                                -#=----=*##*+---------***:::::::*
                                .+=+-*#+=--**=-------=#*=::::::-+
                                 *:-*-==-------------#+#:::::::*=-.
                                 +:::*=-------------**+*::::::#=:=+
                                 .*-::++-----------=#+#-::::+= .--
                                  **+::=+---------=#++*::-+-
                                     -===*=-------#++#*==.
                                      -+-*#=-----#+++*
                                     -+::*.-+---**++*.
                                     **-*.  .+=****-
*/
