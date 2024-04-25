package orchestrator

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"calculator/internal"
	"calculator/internal/backend/application/agent"
	"calculator/internal/backend/application/orchestrator/monitoring"
	"calculator/internal/config"

	pb "calculator/internal/proto"

	"github.com/jackc/pgx"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // Standard library bindings for pgx
	"google.golang.org/grpc"
)

var (
	ServerExitChannel chan os.Signal = make(chan os.Signal, 1)
	db                *pgx.ConnPool  = shared.GetDb()
	err               error
	logger            *log.Logger = shared.Logger
	loggerErr         *log.Logger = shared.LoggerErr
	Srv               *http.Server
	Manager           *monitoring.AgentsManager
	OrchestratorServiceServer *Server
)

type Server struct {
	pb.OrchestratorServiceServer // сервис из сгенерированного пакета
}

func NewServer() *Server {
	return &Server{}
}

// Основная горутина оркестратора
func Launch(manager *monitoring.AgentsManager) {
	logger.Println("Подключился оркестратор.")
	fmt.Println("Оркестратор передаёт привет :)")
	db = shared.GetDb()
	Manager = manager

	// Подготавливаем запросы в БД
	_, err = db.Prepare( // Запись выражения в таблицу с выражениями
		"orchestratorPut",
		`INSERT INTO requests (request_id, username, expression)
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

func (s *Server) SendExp(ctx context.Context, req *pb.ExpSendRequest) (*pb.AgentAndErrorResponse, error) {
	id := req.Id
	exp := req.Expression
	username := req.Username

	_, err = db.Exec("orchestratorPut", id, username, exp)
	if err != nil {
		logger.Println("Оркестратор: ошибка помещения выражения в БД.")
		loggerErr.Println("Ошибка помещения выражения в БД:", err)
		return &pb.AgentAndErrorResponse{
			Error: []string{err.Error()},
			Agent: 0,
		}, nil

	}
	Manager.HandleExpression(id, exp)
	return &pb.AgentAndErrorResponse{
		Error: []string{},
		Agent: -1,
	}, nil
}

func (s *Server) Monitor(ctx context.Context, in *pb.EmptyMessage) (*pb.MonitorResponse, error) {
	agents := Manager.Monitor()
	var agentsReturn []*pb.MonitorResponse_SingleObj
	for _, a := range agents {
		agentsReturn = append(agentsReturn, &pb.MonitorResponse_SingleObj{
			Id:  a[0],
			State: a[1],
		})
	}
	return &pb.MonitorResponse{
		States: agentsReturn,
	}, nil
}

func (s *Server) KillOrch(ctx context.Context, in *pb.EmptyMessage) (*pb.ErrorResponse, error) {
	return &pb.ErrorResponse{}, nil
}

func (s *Server) SendHeartbeat(ctx context.Context, in *pb.HeartbeatRequest) (*pb.ErrorResponse, error) {
	if Manager.ToKill > 0 {
		Manager.ToKill--
		return &pb.ErrorResponse{Error: "dead"}, nil
	}
	Manager.RegisterHeartbeat(int(in.AgentID))
	return &pb.ErrorResponse{}, nil
}

func (s *Server) SendResult(ctx context.Context, in *pb.ResultRequest) (*pb.EmptyMessage, error) {
	Manager.RegisterHeartbeat(int(in.AgentID))
	i := in.AgentID
	res := in.Result
	divByZero := in.DivByZeroError
	id := in.ExpressionID
	if !divByZero {
		logger.Printf("Получили результат от агента %v: %v.", i, res)
		_, err = db.Exec("orchestratorUpdate", id, res, false)
	} else {
		logger.Printf("Получили результат от агента %v: ошибка.", i)
		_, err = db.Exec("orchestratorUpdate", id, res, true)
	}
	if err != nil {
		loggerErr.Println("Ошибка обновления выражения в БД:", err)
		logger.Println("Ошибка обновления выражения в БД.")
		return &pb.EmptyMessage{}, err
	}

	logger.Println("Положили результат в БД.")
	_, err = db.Exec("DELETE FROM agent_proccesses WHERE proccess_id = $1;", i)
	if err != nil {
		loggerErr.Println("Ошибка удаления выражения из таблицы с агентами:", err)
		logger.Println("Ошибка обновления выражения в таблице агентов.")
		return &pb.EmptyMessage{}, err
	}
	logger.Println("Возвращаем путой ответ агенту.")
	return &pb.EmptyMessage{}, nil
}

func (s *Server) UpdateResult(context.Context, *pb.ExpUpdateRequest) (*pb.EmptyMessage, error) {
	return &pb.EmptyMessage{}, nil
}

func (s *Server) SeekForExp(ctx context.Context, in *pb.ExpSeekRequest) (*pb.ExpSeekResponse, error) {
	Manager.RegisterHeartbeat(int(in.AgentID))
	id, exp, username, ok := Manager.GiveExpression()
	if !ok {
		return &pb.ExpSeekResponse{Found: false}, nil
	}
	t := GetTimes(username)
	return &pb.ExpSeekResponse{
		Found: true,
		Expression: exp,
		Times: &pb.TimesResponse{
			Summation: t.Sum.Nanoseconds(),
			Substraction: t.Sub.Nanoseconds(),
			Multiplication: t.Mult.Nanoseconds(),
			Division: t.Div.Nanoseconds(),
			AgentTimeout: t.AgentTimeout.Nanoseconds(),
			Error: []string{},
			Username: username,
		},
		ExpressionID: id,
	}, nil
}

func (s *Server) ConfirmTakeExp(ctx context.Context, in *pb.ExpConfirmRequest) (*pb.ErrorResponse, error) {
	agentID := int(in.AgentID)
	id := in.ExpressionID
	if !Manager.TakeExpression(id, agentID) {
		return &pb.ErrorResponse{Error:"not in queue"}, nil
	}
	return &pb.ErrorResponse{}, nil
}

// Функция для получения времени операций из БД
func GetTimes(username string) *agent.Times {
	db = shared.GetDb()
	rows, err := db.Query(fmt.Sprintf("SELECT action, time FROM %s.time_vars;", strings.ReplaceAll(username, " ", "")))
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
			t.Sum = time.Duration(t_time * 1_000_000)
			logger.Printf("Время на сложение пользователя %s:, %v\n", username, t.Sum)
		case "substraction":
			t.Sub = time.Duration(t_time * 1_000_000)
			logger.Printf("Время на вычитание пользователя %s:, %v\n", username, t.Sub)
		case "multiplication":
			t.Mult = time.Duration(t_time * 1_000_000)
			logger.Printf("Время на умножение пользователя %s:, %v\n", username, t.Mult)
		case "division":
			t.Div = time.Duration(t_time * 1_000_000)
			logger.Printf("Время на деление пользователя %s:, %v\n", username, t.Div)
		}
	}
	err = rows.Err()
	if err != nil {
		loggerErr.Panic(err)
	}
	var timeout int
	err = db.QueryRow("SELECT time FROM agent_timeout").Scan(&timeout)
	if err != nil {
		loggerErr.Panic(err)
	}
	t.AgentTimeout = time.Duration(timeout * 1_000_000)
	logger.Printf("Время на таймаут:, %v\n", t.AgentTimeout)

	return t
}

// Функция, которая проверяет, есть ли в БД непосчитанные выражения
func CheckWorkLeft(m *monitoring.AgentsManager) {
	db = shared.GetDb()
	logger.Println("Оркестратор: проверка на непосчитанные выражения...")
	var id string
	var exp string
	rows, err := db.Query(
		`SELECT request_id, expression FROM requests
			WHERE calculated = FALSE;`,
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
		err := rows.Scan(&id, &exp)
		if err != nil {
			loggerErr.Println("Паника:", err)
			logger.Println("Критическая ошибка, завершаем работу программы...")
			logger.Println("Отправляем сигнал прерывания...")
			ServerExitChannel <- os.Interrupt
			logger.Println("Отправили сигнал прерывания.")
		}

		logger.Println("Отдаем выражение менеджеру...")
		m.HandleExpression(id, exp)
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
