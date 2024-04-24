package agent

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"calculator/internal"

	pb "calculator/internal/proto"

	"github.com/jackc/pgx"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // Standard library bindings for pgx
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure" // для упрощения не будем использовать SSL/TLS аутентификация
)

var (
	db        *pgx.ConnPool = shared.Db
	err       error
	logger    *log.Logger = shared.Logger
	loggerErr *log.Logger = shared.LoggerErr
	loggerHB  *log.Logger = shared.LoggerHeartbeats
	Server    *http.Server
	pulse     *time.Ticker
)

type Times struct {
	Sum          time.Duration `json:"sum"`
	Sub          time.Duration `json:"sub"`
	Mult         time.Duration `json:"mult"`
	Div          time.Duration `json:"div"`
	AgentTimeout time.Duration `json:"timeout"`
}

// Структура агента
type AgentComm struct{
	N			int
	Host		string
	Port		string
	Timeout		time.Duration
	N_machines	int
}

// Горутина агента
func Agent(a *AgentComm) {
	defer func() {
		if rec := recover(); rec != nil {
			loggerErr.Printf("Перехватили панику агента %v: %s\n— Отключаем агента %v...\n", a.N, rec, a.N)
		}
		pulse.Stop()
		logger.Printf("Агент %v отключился.\n", a.N)
	}()

	logger.Printf("Подключился агент %v.\n", a.N)
	fmt.Printf("Агент %v передает привет!\n", a.N) // TODO: добавить имена
	db = shared.Db
	wg := sync.WaitGroup{}
	
	// Устанавливаем gRPC соединение
	addrGrpc := fmt.Sprintf("%s:%s", a.Host, a.Port) // используем адрес сервера
	// установим соединение
	conn, err := grpc.Dial(addrGrpc, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		loggerErr.Println("Агент не смог подключиться к gRPC серверу оркестратора: ", err)
		return
	}
	// закроем соединение, когда выйдем из функции
	defer conn.Close()
	grpcClient := pb.NewOrchestratorServiceClient(conn)

	// Готовим пинги
	pulse = time.NewTicker(min(a.Timeout / 5, time.Second))
	deathChannel := make(chan struct{}, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				loggerHB.Printf("Агент %v: паника горутины хартбитов, отключаем агента.\n", a.N)
			}
			deathChannel <- struct{}{}
			logger.Printf("Агент %v – ВЫХОД\n", a.N)
		}()

		loggerHB.Printf("Агент %v - ЗАПУСК\n", a.N)
		for {
			select {
			case <-pulse.C: // Пора посылать хартбит
				loggerHB.Printf("Агент %v - отправляем...\n", a.N)
				resp, err := grpcClient.SendHeartbeat(context.TODO(), &pb.HeartbeatRequest{AgentID: int32(a.N)})
				loggerHB.Printf("Агент %v - отправили.\n", a.N)
				if err != nil {
					loggerErr.Panic("Ошибка отправки хартбита:", err)
				}
				if resp.Error == "dead" {
					loggerHB.Printf("Агент %v - смерть.\n", a.N)
					logger.Printf("Агент %v: запланированная смерть, отключаем агента...", a.N)
					return
				} else {
					loggerErr.Println("???\t", resp.Error)
				}
			case <-deathChannel:
				loggerHB.Printf("Агент %v - смерть.\n", a.N)
				return
			}
		}
	}()

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				loggerErr.Printf("Перехватили панику агента %v: %s\n— Отключаем агента %v...", a.N, rec, a.N)
			}
			pulse.Stop()
			logger.Printf("Агент %v отключается...\n", a.N)
			wg.Done()
		}()

		logger.Printf("Агент %v начинает работу...\n", a.N)
		
		
		for {
			
			// Переменные ниже понадобятся позже - иначе их не видит второй селект
			chRes := make(chan [4]float32) // Канал для получения значений от вычислителей
			timesNow := getTimes(a.N)
			// fmt.Println(a.N, "agent sum:", timesNow.Sum)
			expParts := make(map[[3]int]string) // Мапа для мониторинга статусов частей выражения
			// ^ ключ - координаты, значение - делитель(1), множитель (2) или сложение (-1)
			// partStatus := make(map[[2]int]bool) // Мапа для мониторинга статусов чисел
			var (
				comps []string         // Слайс со слагаемыми
				newParts      [][]string // Слайс со всем
				stoppedAt     int = 1 // Индекс последней части, принятой вычеслителем
				activeWorkers int     // Кол-во занятых вычислителей
				taskId        string  // ID выражения
				task          string  // Само выражение
				times *Times		  // Время на выполнение операций
			)

			// Фаза 1: агент свободен
			logger.Printf("Агент %v ждет задачи...\n", a.N)
			select {
			case <-deathChannel: // Смерть агента
				logger.Printf("Агент %v умирает...\n", a.N)
				return
			default: // Посылаем оркестратору запросы, чтобы получить выражение раз в секунду
				for {
					resp, err := grpcClient.SeekForExp(context.TODO(), &pb.ExpSeekRequest{AgentID: int32(a.N)})
					if err != nil {
						loggerErr.Panic("Ошибка отправки запроса на получение выражения:", err)
					}
					if resp.Found {
						times.Sum = time.Duration(resp.Times.Summation)
						times.Sub = time.Duration(resp.Times.Substraction)
						times.Mult = time.Duration(resp.Times.Multiplication)
						times.Div = time.Duration(resp.Times.Division)
						taskId = resp.ExpressionID
						task = resp.Expression
						
						respNew, err := grpcClient.ConfirmTakeExp(context.TODO(), &pb.ExpConfirmRequest{
							AgentID: int32(a.N),
							ExpressionID: taskId,
						})
						if err != nil {
							loggerErr.Panic("Ошибка отправки запроса на получение выражения:", err)
						}
						if respNew.Error == "not in queue" { // Выражение уже взял другой агент
							continue
						}

						break
					}
					time.Sleep(1 * time.Second)
				}
			}
			logger.Printf("Агент %v получил ID выражения: %v\n", a.N, taskId)
			
			newParts, err = proccessExp(newParts, taskId, a.N ,task)
			if err != nil {
				panic(err)
			}

			// Обрабатываем слагаемые:
			logger.Printf("Агент %v начинает обработку слагаемых\n", a.N)
			for i := 0; i < len(newParts); i++ {
				logger.Printf("Агент %v обрабатывает %v слагаемое...\n", a.N, i)
				if activeWorkers == a.N_machines {
					logger.Printf("Агент %v задействовал всех доступных вычислителей: %v (уровень слагаемых).\n", a.N, activeWorkers)
					break
				}
				stoppedAt++
				elem := newParts[i]
				if len(elem) == 1 { // Слагаемое - единственное число, записываем его в слайс со слагаемыми
					logger.Printf("Агент %v добавляет слагаемое '%s'.\n", a.N, elem[0])
					comps = append(comps, elem[0])
					if len(comps)%2 == 1 && len(comps) != 1 {
						comps = append(comps, "0")
					}
				} else { // Внутри слагаемого нужно производить вычисления
					logger.Printf("Агент %v обрабатывает множители %v слагаемого...\n", a.N, i)
					for j := 1; j < len(elem); j += 2 { // Проходимся по множителям
						v1, v2 := elem[j-1], elem[j]
						logger.Printf("Агент %v: '%s', '%s'\n", a.N, v1, v2)
						pos := [3]int{i, j - 1, j} // Координаты двух чисел
						newParts[i][j-1] = "X"
						newParts[i][j] = "X"
						// Получаем значение для мапы статусов и запускаем вычислитель
						val, err := calcMult(v1, v2, pos, chRes, timesNow)
						if err != nil {
							panic(err)
						}
						logger.Printf("Агент %v запустил вычислитель умножения(деления) (%s * %s).\n", a.N, v1, v2)
						expParts[pos] = val
						activeWorkers++
						// Если задействовали всех доступных вычислителей, больше внутрь слагаемых заходить нет смысла
						if activeWorkers == a.N_machines {
							logger.Printf("Агент %v задействовал всех доступных вычислителей: %v (уровень множителей).\n", a.N, activeWorkers)
							break
						}
					}
					logger.Printf("Агент %v обработал слагаемое '%s'.\n", a.N, strings.Join(elem, "*"))
				}
			}

			// Если не осталось свободных вычислителей, агент переходит в фазу работающего,
			// то есть ждет значения от вычислителей
			if activeWorkers == a.N_machines {
				logger.Printf("Агент %v задействовал всех доступных вычислителей: %v.\n", a.N, activeWorkers)
				break
			}

			// Иначе проходимся по однозначным слагаемым и складываем их
			logger.Printf("Агент %v начинает производить сложение слагаемых.\n", a.N)
			logger.Println("Слайс слагаемых:", fmt.Sprint("[", strings.Join(comps, "'"), "]"))
			for i := 1; i < len(comps); i += 2 {
				logger.Printf("Агент %v обрабатывает %v слагаемое...\n", a.N, i)
				v1, v2 := comps[i-1], comps[i]
				logger.Printf("Агент %v: '%s', '%s'\n", a.N, v1, v2)
				pos := [3]int{-1, i - 1, i} // Координаты двух чисел
				comps[i-1] = "X"
				comps[i] = "X"
				// Запускаем вычислитель
				err := calcSum(v1, v2, pos, chRes, timesNow)
				if err != nil {
					panic(err)
				}
				logger.Printf("Агент %v запустил вычислитель сложения(вычитания) (%s + %s).\n", a.N, v1, v2)
				expParts[pos] = "sum"
				activeWorkers++
				if activeWorkers == a.N_machines {
					logger.Printf("Агент %v задействовал всех доступных вычислителей: %v (уровень обработки слагаемых).\n", a.N, activeWorkers)
					break
				}
			}
			logger.Printf("Агент %v произвел первичную обработку частей выражения, переходит в занятую фазу.\n", a.N)
			
			if val := countReal(comps); val != "" &&activeWorkers == 0 {
				logger.Printf("Агент %v посчитал значение выражения с ID %v.\n", a.N, taskId)
				fmt.Println(taskId, ":", val, "=", val)
				grpcClient.SendResult(context.TODO(), &pb.ResultRequest{
					AgentID: int32(a.N),
					DivByZeroError: false,
					Result: val,
					ExpressionID: taskId,
				})
				continue
			}
			
			// Фаза 2: агент управляет решением выражения - ждет результатов от вычислителей из канала chRes
			Busy:
			for {
				logger.Printf("Агент %v ждет вычислителей...\n", a.N)
				select {
				case <-deathChannel: // Смерть агента
					logger.Printf("Агент %v умирает...\n", a.N)
					return
				case num := <-chRes: // Получили значение от вычислителя
					logger.Printf("Агент %v получил значение: ", a.N)
					if num == [4]float32{-1, -1, -1, -1} {
						db.Exec("dbRes", a.N, "0")
						loggerErr.Printf("Агент %v: в выражении присутствует деление на ноль.\n", a.N)
						logger.Printf("Агент %v посчитал значение выражения с ID %v.\n", a.N, taskId)
						fmt.Println(taskId, ":", task, "— ошибка: деление на ноль.")
						grpcClient.SendResult(context.TODO(), &pb.ResultRequest{
							AgentID: int32(a.N),
							DivByZeroError: true,
							ExpressionID: taskId,
						})
						break Busy
					}
					// fmt.Println("Didnt break busy")
					numPos := floatsToInts(num[:3]) // Координаты числа
					if expParts[numPos] == "sum" {  // Число - результат сложения / вычитания
						logger.Printf("— %s + %s = '%v'.\n", comps[numPos[1]], comps[numPos[2]], num[3])
						// Обновляем слайс слагаемых
						comps[numPos[1]] = ""
						comps[numPos[2]] = fmt.Sprint(num[3])
					} else {
						// Обновляем слагаемое
						if expParts[numPos] == "mult" {
							logger.Printf("— %s * %s = '%v'.\n", newParts[numPos[0]][numPos[1]], newParts[numPos[0]][numPos[2]], num[3])
							newParts[numPos[0]][numPos[2]] = fmt.Sprint(num[3])
						} else if expParts[numPos] == "div" {
							logger.Printf("— %s / %s = '%v'.\n", newParts[numPos[0]][numPos[1]], newParts[numPos[0]][numPos[2]], num[3])
							newParts[numPos[0]][numPos[2]] = fmt.Sprint("/", num[3])
						} else {
							loggerErr.Println("- ERROR - Got unknown type! - ERROR -")
							db.Exec("dbRes", a.N, "0")
							loggerErr.Printf("Агент %v: в выражении присутствует деление на ноль.\n", a.N)
							logger.Printf("Агент %v посчитал значение выражения с ID %v.\n", a.N, taskId)
							fmt.Println(taskId, ":", task, "— ошибка: деление на ноль.")
							grpcClient.SendResult(context.TODO(), &pb.ResultRequest{
								AgentID: int32(a.N),
								DivByZeroError: true,
								ExpressionID: taskId,
							})
							break Busy
						}
						newParts[numPos[0]][numPos[1]] = ""
						if val := countReal(newParts[numPos[0]]); val != "" { // Все операции внутри слагаемого закончены (получили одно число)
							logger.Printf("Агент %v добавляет слагаемое '%s'.\n", a.N, val)
							comps = append(comps, val) // Обновляем слайс слагаемых
							if len(comps)%2 == 1 && len(comps) != 1 {
								comps = append(comps, "0")
							}
						}
					}
					activeWorkers--

					// Ищем, что можно умножить / поделить
					logger.Printf("Агент %v начинает обработку слагаемых...\n", a.N)
					for i := 0; i < len(newParts); i++ {
						logger.Printf("Агент %v заходит в %v итерацию обработки слагаемых...\n", a.N, i)
						if activeWorkers == a.N_machines {
							logger.Printf("Агент %v задействовал всех доступных вычислителей: %v (2 уровень обработки слагаемых).\n", a.N, activeWorkers)
							break
						}
						elem := newParts[i]
						if len(elem) > 1 { // Однозначные элементы на данном этапе уже в слайсе 'comps'
							var free QueueInt = &ArrInt{}
							if !free.IsEmpty() {
								panic("- ERROR - free.pop() is wrong! - ERROR -")
							}
							logger.Printf("Агент %v обрабатывает %v слагаемое...\n", a.N, i)
							for j := 1; j < len(elem); j += 2 {
								v1, v2 := elem[j-1], elem[j]
								logger.Printf("Агент %v: '%s', '%s'\n", a.N, v1, v2)
								var pos [3]int
								if v2 == "" || v2 == "X" {
									logger.Printf("Агент %v: 2 множитель пустой, пропускаем итерацию %v.\n", a.N, j)
									continue
								} else if v1 == "" || v1 == "X" {
									if !free.IsEmpty() {
										if index, ok := free.Pop(); ok {
											v1 = elem[index]
											pos = [3]int{i, index, j}
											newParts[i][index] = "X"
											newParts[i][j] = "X"
										} else {
											loggerErr.Println("Слайс не пустой но пустой...")
										}
									} else {
										free.Append(j)
										logger.Printf("Агент %v: Не нашли пару ко 2 множителю, записываем, пропускаем итерацию %v.\n", a.N, j)
										continue
									}
								} else {
									pos = [3]int{i, j - 1, j}
									newParts[i][j-1] = "X"
									newParts[i][j] = "X"
								}
								if _, ok := expParts[pos]; ok {
									logger.Printf("Агент %v: Вычисление произведения уже производится, пропускаем итерацию %v.\n", a.N, j)
									continue
								}
								val, err := calcMult(v1, v2, pos, chRes, timesNow)
								if err != nil {
									panic(err)
								}
								logger.Printf("Агент %v запустил вычислитель умножения(деления) (%s * %s).\n", a.N, v1, v2)
								expParts[pos] = val
								activeWorkers++
								if activeWorkers == a.N_machines {
									logger.Printf("Агент %v задействовал всех доступных вычислителей: %v (2 уровень обработки слагаемого).\n", a.N, activeWorkers)
									break
								}
							}
						}
					}

					if activeWorkers == a.N_machines {
						logger.Printf("Агент %v задействовал всех доступных вычислителей: %v.\n", a.N, activeWorkers)
						break
					}

					// Ищем, что можно сложить / вычесть
					logger.Printf("Агент %v начинает производить сложение слагаемых.\n", a.N)
					logger.Println("Слайс слагаемых:", fmt.Sprint("[", strings.Join(comps, "'"), "]"))
					var free QueueInt = &ArrInt{}
					if !free.IsEmpty() {
						panic("- ERROR - free.pop() is wrong! - ERROR -")
					}
					for i := 1; i < len(comps); i += 2 {
						logger.Printf("Агент %v обрабатывает %v слагаемое...\n", a.N, i)
						v1, v2 := comps[i-1], comps[i]
						logger.Printf("Агент %v: '%s', '%s'\n", a.N, v1, v2)
						var pos [3]int
						if v2 == "" || v2 == "X" {
							logger.Printf("Агент %v: 2 слагаемое пустое, пропускаем итерацию %v.\n", a.N, i)
							continue
						} else if v1 == "" || v1 == "X" {
							logger.Println("Первое слагаемое пустое")
							if !free.IsEmpty() {
								if index, ok := free.Pop(); ok {
									v1 = comps[index]
									pos = [3]int{-1, index, i}
									comps[index] = "X"
									comps[i] = "X"
								}
							} else {
								logger.Printf("Агент %v: Не нашли пару ко 2 слагаемому, записываем, пропускаем итерацию %v.\n", a.N, i)
								free.Append(i)
								continue
							}
						} else {
							pos = [3]int{-1, i - 1, i}
							comps[i-1] = "X"
							comps[i] = "X"
						}
						if _, ok := expParts[pos]; ok {
							logger.Printf("Агент %v: Вычисление суммы уже производится, пропускаем итерацию %v.\n", a.N, i)
							continue
						}
						err := calcSum(v1, v2, pos, chRes, timesNow)
						if err != nil {
							panic(err)
						}
						logger.Printf("Агент %v запустил вычислитель сложения(вычитания) (%s + %s).\n", a.N, v1, v2)
						expParts[pos] = "sum"
						activeWorkers++
						if activeWorkers == a.N_machines {
							logger.Printf("Агент %v задействовал всех доступных вычислителей: %v (2 уровень сложения).\n", a.N, activeWorkers)
							break
						}
					}

					// Проверяем, готов ли ответ
					if val := countReal(comps); val != "" && activeWorkers == 0 {
						_, err = db.Exec("dbRes", a.N, val)
						if err != nil {
							panic(err)
						}
						logger.Printf("Агент %v посчитал значение выражения с ID %v.\n", a.N, taskId)
						fmt.Printf("%v: '%s' = '%v'\n", taskId, task, val)
						grpcClient.SendResult(context.TODO(), &pb.ResultRequest{
							AgentID: int32(a.N),
							DivByZeroError: false,
							Result: val,
							ExpressionID: taskId,
						})
						break Busy
					}
				}
			}
		}
	}()

	wg.Add(1)
	wg.Wait()
}

type Queue interface {
	Append(n string)
	Pop() (string, bool)
	IsEmpty() bool
}

type QueueInt interface {
	Append(n int)
	Pop() (int, bool)
	IsEmpty() bool
}


type ArrInt struct {
	arr []int
	mu  sync.Mutex
}

func (a *ArrInt) Append(n int) {
	a.mu.Lock()
	a.arr = append(a.arr, n)
	a.mu.Unlock()
}

func (a *ArrInt) Pop() (int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.arr) == 0 {
		return 0, false
	}
	n := a.arr[0]
	a.arr = a.arr[1:]
	return n, true
}

func (a *ArrInt) IsEmpty() bool {
	return len(a.arr) == 0
}

type ArrStr struct {
	arr []string
	mu  sync.Mutex
}


func (a *ArrStr) Append(n string) {
	a.mu.Lock()
	a.arr = append(a.arr, n)
	a.mu.Unlock()
}

func (a *ArrStr) Pop() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.arr) == 0 {
		return "", false
	}
	n := a.arr[0]
	a.arr = a.arr[1:]
	return n, true
}

func (a *ArrStr) IsEmpty() bool {
	return len(a.arr) == 0
}

func getTimes(n int) Times {
	rows, err := db.Query("SELECT action, time from time_vars;")
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	t := &Times{}
	for rows.Next() {
		var (
			t_type string
			t_time int
		)
		err := rows.Scan(&t_type, &t_time)
		if err != nil {
			log.Fatal(err)
		}
		switch t_type {
		case "summation":
			t.Sum = time.Duration(t_time * 1000000)
			logger.Printf("Время на сложение агента %v:, %v\n", n, t.Sum)
		case "substraction":
			t.Sub = time.Duration(t_time * 1000000)
			logger.Printf("Время на вычитание агента %v:, %v\n", n, t.Sub)
		case "multiplication":
			t.Mult = time.Duration(t_time * 1000000)
			logger.Printf("Время на умножение агента %v:, %v\n", n, t.Mult)
		case "division":
			t.Div = time.Duration(t_time * 1000000)
			logger.Printf("Время на деление агента %v:, %v\n", n, t.Div)
		case "agent_timeout":
			t.AgentTimeout = time.Duration(t_time * 1000000)
			logger.Printf("Агент %v думает, что его таймаут — %v\n", n, t.AgentTimeout)
		}
	}
	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}
	return *t
}

func proccessExp(_ [][]string, taskId string, N int, task string) ([][]string, error) {
	// Разбивка выражения на слагаемые
	parts := []string{}         // Слайс со слагаемыми
	var next int                // Индекс следующего слагаемого
	var lastIsMinusOrSlash bool // True если часть идет с вычитанием(-) или делением(/)
	// Жертвуем микросекундами ради читаемости и критерия 7 =)
	for i, digit := range task + "+" {
		if string(digit) == "+" || string(digit) == "-" {
			if !lastIsMinusOrSlash { // Операция сложения
				parts = append(parts, task[next:i]) // Записываем часть до знака операции
				next = i + 1                        // Обновляем индекс - следующий после знака символ
			} else { // Операция вычитания
				parts = append(parts, "-"+task[next:i]) // Добавляем минус перед числом если операция вычитания
				next = i + 1
			}
			lastIsMinusOrSlash = !(string(digit) == "+") // Обновляем знак для следующей части
		}
	}
	// fmt.Println(strings.Join(parts, " | "))

	// Разбивка слагаемых на множители (делители)
	newParts := make([][]string, len(parts))
	for j, part := range parts { // Проходимся по каждому слагаемому
		newPart := []string{}
		next = 0 // Обновляем индекс
		lastIsMinusOrSlash = false
		// Аналогично с разбивкой на слагаемые
		for i, digit := range part + "*" {
			if string(digit) == "*" || string(digit) == "/" {
				if !lastIsMinusOrSlash {
					newPart = append(newPart, part[next:i])
					next = i + 1
				} else {
					newPart = append(newPart, "/"+part[next:i])
					next = i + 1
				}
				lastIsMinusOrSlash = !(string(digit) == "*")
			}
		}
		if len(newPart)%2 != 0 && len(newPart) != 1 {
			newPart = append(newPart, "1")
		}
		newParts[j] = newPart
	}
	exp := make([]string, len(parts))
	for i, part := range newParts {
		exp[i] = strings.Join(part, " | ")
	}
	logger.Printf("Агент %v обработал выражение: [ %s ] - ID: %v\n", N, strings.Join(exp, " ' "), taskId)

	// Записываем обработанное выражение в БД
	_, err = db.Exec("dbPut", taskId, N, task, strings.Join(exp, " ' "))
	if err != nil {
		return [][]string{}, err
	}
	logger.Printf("Агент %v записал в БД части выражения с ID %v\n", N, taskId)
	// fmt.Println(strings.Join(exp, " ' "))
	return newParts, nil
}

func convertStrsToFloat32(a, b string) (float32, float32, error) {
	aFloat, err := strconv.ParseFloat(a, 32)
	if err != nil {
		return 0, 0, err
	}
	bFloat, err := strconv.ParseFloat(b, 32)
	if err != nil {
		return 0, 0, err
	}
	return float32(aFloat), float32(bFloat), nil
}

func intsToFloats(arr [3]int) [3]float32 {
	return [3]float32{float32(arr[0]), float32(arr[1]), float32(arr[2])}
}

func floatsToInts(arr []float32) [3]int {
	return [3]int{int(arr[0]), int(arr[1]), int(arr[2])}
}

func calcMult(v1, v2 string, pos [3]int, ch chan<- [4]float32, t Times) (string, error) {
	// fmt.Println(t)
	switch string(v1[0]) {
	case "/": // Первое число - делитель
		switch string(v2[0]) {
		case "/": // Второе число - тоже делитель (a / b / c = a / (bc))
			vFloat1, vFloat2, err := convertStrsToFloat32(v1[1:], v2[1:])
			if err != nil {
				return "", err
			}
			go mult(vFloat1, vFloat2, intsToFloats(pos), ch, t.Mult)
			return "div", nil
		default: // (a / b * c = a * (c / b))
			vFloat1, vFloat2, err := convertStrsToFloat32(v1[1:], v2)
			if err != nil {
				return "", err
			}
			go div(vFloat2, vFloat1, intsToFloats(pos), ch, t.Div)
			return "mult", nil
		}
	default:
		switch string(v2[0]) {
		case "/": // (a * (b / c))
			vFloat1, vFloat2, err := convertStrsToFloat32(v1, v2[1:])
			if err != nil {
				return "", err
			}
			go div(vFloat1, vFloat2, intsToFloats(pos), ch, t.Div)
			return "mult", nil
		default: // (a * (b * c))
			vFloat1, vFloat2, err := convertStrsToFloat32(v1, v2)
			if err != nil {
				return "", err
			}
			go mult(vFloat1, vFloat2, intsToFloats(pos), ch, t.Mult)
			return "mult", nil
		}
	}
}

func calcSum(v1, v2 string, pos [3]int, ch chan<- [4]float32, t Times) error {
	// fmt.Println(t)
	if string(v2[0]) == "-" {
		vFloat1, vFloat2, err := convertStrsToFloat32(v1, v2[1:])
		if err != nil {
			return err
		}
		go sub(vFloat1, vFloat2, intsToFloats(pos), ch, t.Sub)
	} else {
		vFloat1, vFloat2, err := convertStrsToFloat32(v1, v2)
		if err != nil {
			return err
		}
		go sum(vFloat1, vFloat2, intsToFloats(pos), ch, t.Sum)
	}
	return nil
}

func countReal(arr []string) string {
	var theOnlyValue string
	var n int
	for _, elem := range arr {
		if elem != "" {
			theOnlyValue = elem
			n++
		}
	}
	if n == 1 {
		return theOnlyValue
	}
	return ""
}

func sum(a, b float32, n [3]float32, ch chan<- [4]float32, t time.Duration) {
	now := time.Now()
	// fmt.Println("вычислитель", t)
	res := [4]float32{}
	_ = append(append(res[:0], n[:]...), a+b)
	time.Sleep(t - time.Since(now))
	ch <- res
}

func sub(a, b float32, n [3]float32, ch chan<- [4]float32, t time.Duration) {
	now := time.Now()
	res := [4]float32{}
	_ = append(append(res[:0], n[:]...), a-b)
	time.Sleep(t - time.Since(now))
	ch <- res
}

func mult(a, b float32, n [3]float32, ch chan<- [4]float32, t time.Duration) {
	now := time.Now()
	res := [4]float32{}
	_ = append(append(res[:0], n[:]...), a*b)
	time.Sleep(t - time.Since(now))
	ch <- res
}

func div(a, b float32, n [3]float32, ch chan<- [4]float32, t time.Duration) {
	if b == 0 {
		ch <- [4]float32{-1, -1, -1, -1}
	}
	now := time.Now()
	res := [4]float32{}
	_ = append(append(res[:0], n[:]...), a/b)
	time.Sleep(t - time.Since(now))
	ch <- res
}
