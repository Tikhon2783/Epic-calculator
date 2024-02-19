# "Распределенный вычислитель арифметических выражений"

## Оглавление readme
1. [Что реализовано + контакты](https://github.com/Tikhon2783/Epic-calculator?tab=readme-ov-file#%D1%87%D1%82%D0%BE-%D1%80%D0%B5%D0%B0%D0%BB%D0%B8%D0%B7%D0%BE%D0%B2%D0%B0%D0%BD%D0%BE)
1. [Как запускать](https://github.com/Tikhon2783/Epic-calculator?tab=readme-ov-file#%D0%BA%D0%B0%D0%BA-%D0%B7%D0%B0%D0%BF%D1%83%D1%81%D0%BA%D0%B0%D1%82%D1%8C)
    * [Что установить](https://github.com/Tikhon2783/Epic-calculator?tab=readme-ov-file#%D1%87%D1%82%D0%BE-%D1%83%D1%81%D1%82%D0%B0%D0%BD%D0%B0%D0%B2%D0%BB%D0%B8%D0%B2%D0%B0%D1%82%D1%8C)
    * [Как пользоваться](https://github.com/Tikhon2783/Epic-calculator?tab=readme-ov-file#%D0%BA%D0%B0%D0%BA-%D0%BF%D0%BE%D0%BB%D1%8C%D0%B7%D0%BE%D0%B2%D0%B0%D1%82%D1%8C%D1%81%D1%8F)
    * [Что и как можно ломать](https://github.com/Tikhon2783/Epic-calculator?tab=readme-ov-file#%D1%87%D1%82%D0%BE-%D0%B8-%D0%BA%D0%B0%D0%BA-%D0%BC%D0%BE%D0%B6%D0%BD%D0%BE-%D0%BB%D0%BE%D0%BC%D0%B0%D1%82%D1%8C)
1. [Примеры](https://github.com/Tikhon2783/Epic-calculator?tab=readme-ov-file#%D0%BF%D1%80%D0%B8%D0%BC%D0%B5%D1%80%D1%8B)
1. [Документация](https://github.com/Tikhon2783/Epic-calculator?tab=readme-ov-file#%D0%B4%D0%BE%D0%BA%D1%83%D0%BC%D0%B5%D0%BD%D1%82%D0%B0%D1%86%D0%B8%D1%8F)
    1. [Как все работает](https://github.com/Tikhon2783/Epic-calculator?tab=readme-ov-file#%D0%BF%D1%80%D0%B8%D0%BD%D1%86%D0%B8%D0%BF-%D1%80%D0%B0%D0%B1%D0%BE%D1%82%D1%8B)
    2. Логгеры
    3. Ошибки
---
### Что реализовано 
Я постарался реализовать все, кроме фронтенда, возможность делить выражение и отправлять нескольким агентам тоже не получилась. Если есть какие-то вопросы, что-то не работает или у меня где-то ошибки — мой телеграмм @Tikhon1111 (я есть в чате курса)

#### Общий принцип работы
Калькулятор состоит из 3 частей - оркестратора, агентов и вычислителей. Оркестратор контролирует http сервер и управляет агентами, отправляя им выражения. Агенты представленны горутинами, однако, насколько возможно, имитируют что-то наподобии отдельных серверов. У агента есть горутины-вычислители, которые только умеют считать простейшие операции (a+b a-b a*b a/b), "спать" и отправлять результат агенту. Агент, когда получает выражение, превращает его в набор таких простых операций и организовывает работу вычислителей, а потом возвращает результат оркестратору.   
  Подробнее об этом в пункте 4 (документация) - "как все работает".

#### Реализация
Программа должна переживать перезапуск, поэтому значения, связанные с выражениями хранятся в базе данных PostgreSQL.

   При первом запуске подготавливается БД — создаются необходимые хранилища (таблицы).  
   Потом запускается оркестратор: сначала он запускает агентов, потом горутину мониторинга агентов, и, наконец, http сервер.  
   На один endpoint сервера пользователь отправляет запрос с выражением и ключом
   идемпотентности, оркестратор проверяет его и возвращает код (200, 400, 500). Если все хорошо, передает выражение агенту и ожидает дальнейших событий. Ошибку деления на ноль получает вычислитель и передает агенту, который сразу сообщает об этом оркестратору, не досчитывая выражение. Поддерживаются +, -, *, /, скобки не поддерживаются. Унарный минус нельзя ставить перед другим минусов, операция сложения с отрицательным числом считается за операцию вычитания.  
   Существуют также endpoint-ы для получения статуса/результата выражения по ключу идемпотентности, для получения списка таких статусов для всех выражений, получения и изменения времени на выполнение простейших операций и таймаутов для агентов (насколько долго агент может считаться живым, не посылая хартбиты). Есть еще endpoint-ы для получения статусов агентов (интерфейс мониторинга воркеров) и для "убийства" различных компонентов калькулятора — оркестратора или агентов.

   Я у себя проверял, выражения вроде все считаются правильно, критичных ошибок возникать не должно.

   Для завершения работы можно убить оркестратора или просто отправить сигнал прерывания ^C – программа его распознает, закроет все, что было открыто и завершит работу. Если по какой-то причине оба варианта не сработают, придется открывать диспетчер задач, искать процесс startup.go и завершать его. Скриншот примера приложен. У меня такая проблема возникала только в самом начале, ^C сработает в 99% случаев. Если все-таки у вас возникнет эта ошибка, пожалуйста, напишите мне, чтобы я поправил, а вы и следующие проверяющие не страдали.  

   ![taskmanager](pictures/taskmanager.png "Программа в диспетчере задач")


### Как запускать

#### Что устанавливать
Установить нужно только базу данных PostgreSQL. Я устанавливал здесь: https://www.enterprisedb.com/downloads/postgres-postgresql-downloads. Версия 16, для windows 64.

Использованные библиотеки: "github.com/jackc/pgx", "github.com/jackc/pgx/v5", "github.com/jackc/pgx/v5/stdlib"

#### Как пользоваться
Для начала нужно установить несколько переменных в vars/variablables.go: главное задать константы для работы Postgres, в комментариях они подписаны. Порт по умолчанию '5432', имя пользователя задается при установке БД, по умолчанию "postgres", если я не ошибаюсь. Пароль тоже задается при установке. DBName можно указать любое, главное чтобы базы с таким названием уже не было. DBNameDefault - база, к которой по умолчанию подключается пользователь, обычно "postgres".  
  В файле variables.go можно изменять любые переменные. Флаги логгеров не советую менять, потому что логгеры хартбитов и дебага выводят много информации, будут засорять консоль. В остальных файлах лучше ничего не менять)  
  HTTP порт используется 8080, если что, его можно изменить на 705 строчке файла backend/cmd/orchestrator/orchestrator.go

Перед первым запуском надо проверить, что в файле vars/db_existance.json все значения — false. Команда для запуска калькулятора: 'go run cmd/startup/startup.go'. После того, как запустится сервер, в консоле будет сообщение 'Калькулятор готов к работе!'. После этого можно будет посылать запросы. Я постараюсь сделать удобный интерфейс для консоли вместо curl-ов к понедельнику. Чистые запросы описаны в quick_directories.txt в абзацах с curl-ами, их можно отправлять из терминала (нужно открыть новый, не тот, в котором запущен калькулятор) и получать ответы. Так как данные сохраняются в БД, при следующем запуске все выражения и переменные остануться такими же как в последний раз. Чтобы их сбросить — 'go run cmd/shutdown/shutdown.go' и выбрать вариант 1. Вариант 2 удаляет всю базу калькулятора из postgres, выбирайте его, когда закончили проверку.

Возможные запросы:
- Добавить выражение (посчитать)
- Проверить статус выражения по ключу идемпотентности
- Получить список всех выражений со статусами
- Получить список всех агентов со статусами
- Получить список доступных операций со временем их выполнения и таймаут для агентов с возможностью изменения
- Убить/добавить агента
- Убить оркестратор

#### Что и как можно ломать
Оркестратор и агент поддерживают имитацию перезапуска. Для этого есть endpoint-ы calculator/kill/agent?action=kill и calculator/kill/orchestrator.  
   Убивая оркестратора, программа сразу отключает сервер и завершает работу. При повторном запуске, оркестратор проверяет на наличие в БД непосчитанных выражений и направляет их агентам — ничего потеряно не будет.  
   Можно убить агента: оркестратор отменит его контекст, после чего горутина агента выйдет, а оркестратор узнает о смерти потому, что агент перестанет посылать хартбиты. (мониторинг воркеров получается)  
   Чтобы перезапутить агента, нужно отправить запрос с параметром action=revive (примеры в файле quick_directories.txt)
   Отдельно вычислителей убивать не получится, да и смысла в этом нет.


### Примеры
Пока можно отправлять запросы curl, какие именно и примеры в файле quick_directories.txt

Таблица с символами, которые нужно заменять в выражении  
| символ | на что менять |
|:-------|:-------------:|
|+       |    %2B |
|-       |     -  |
|/       |    %2F |
|*       |    %2A |

---

### Документация

#### Принцип работы
***
##### База данных
База данных состоит из трех таблиц: таблица с выражениями, таблицы с процессами и таблицы со временем.  
   ![Схема](https://github.com/Tikhon2783/Epic-calculator/tree/master/pictures/tableentire.png "Схема")   
   ![Таблица выражений](https://github.com/Tikhon2783/Epic-calculator/tree/master/pictures/tablerequests.png "Таблица выражений")   
   ![Таблица процессов](https://github.com/Tikhon2783/Epic-calculator/tree/master/pictures/tableproccesses.png "Таблица процессов")   

##### Оркестратор

Оркестратор — совокупность главной горутины, на которой поднимается http сервер и подгорутины, отслеживающей состояние агентов (менеджер агентов). Главная горутина подготавливает запросы в БД, запускает агентов, запускает горутину менеджера, проверяет, нет ли непосчитаных выражений в таблицу с запросами