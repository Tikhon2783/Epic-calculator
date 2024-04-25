package shared

import (
	"bufio"
	"calculator/internal/config"
	"encoding/json"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx"
	// "golang.org/x/text/encoding/charmap"
)

var (
	Logger			 *log.Logger = GetDebugLogger()
	LoggerErr        *log.Logger = GetErrLogger()
	LoggerHeartbeats *log.Logger = GetHeartbeatLogger()
	LoggerQueue		 *log.Logger = GetQueueLogger()
	db               *pgx.ConnPool
	OpenFiles		 []*os.File = make([]*os.File, 0)
	mu				 *sync.Mutex = &sync.Mutex{}
)

type Db_info struct {
	Db			bool
	T_requests	bool
	T_agent		bool
	T_vars		bool
	T_users		bool
	T_timeout	bool
}

func GetDb() *pgx.ConnPool {
	mu.Lock()
	defer mu.Unlock()
	v := db
	return v
}

func SetDb(v *pgx.ConnPool) {
	mu.Lock()
	defer mu.Unlock()
	db = v
}

func GetDBSTate() Db_info { // Возвращает JSON структуру из файла db_existance.json
	logger := GetDebugLogger()
	db_existance, err := os.ReadFile("internal/config/db_existance.json")
	if err != nil {
		logger.Fatal(err)
	}
	var db_info Db_info
	err = json.Unmarshal([]byte(db_existance), &db_info)
	if err != nil {
		logger.Fatal(err)
	}
	return db_info
}

var flushInterval = 1 * time.Second // Adjust the flush interval as needed

func autoFlushBuffer(writer *bufio.Writer) {
    ticker := time.NewTicker(flushInterval)
    defer ticker.Stop()

    for {
        <-ticker.C
		if err := writer.Flush(); err != nil && err != io.ErrShortWrite{
			LoggerErr.Printf("Error flushing buffer: %v\n", err)
		}
    }
}

func GetErrLogger() *log.Logger {
	switch vars.LoggerOutputError {
	case 0:
		return log.New(os.Stderr, "", vars.LoggerFlagsError)
	case 1:
		f, err := os.OpenFile("internal/logs/errors.txt", os.O_APPEND | os.O_WRONLY, 0600)
		if err != nil {
			log.Println("Не смогли открыть файл для логгера ошибок, их логи записаны не будут:", err)
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		f.WriteString("\nНОВАЯ СЕССИЯ\n")
		return log.New(f, "", vars.LoggerFlagsError)
	case 2:
		f, err := os.OpenFile("internal/logs/errors.txt", os.O_APPEND | os.O_WRONLY, 0600)
		if err != nil {
			log.Println("Не смогли открыть файл для логгера ошибок, будет использоваться только Stderr:", err)
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		f.WriteString("\nНОВАЯ СЕССИЯ\n")
		return log.New(io.MultiWriter(os.Stdout, f), "", vars.LoggerFlagsError)
	}
	return log.Default()
}

func GetDebugLogger() *log.Logger {
	switch vars.LoggerOutputDebug {
	case 0:
		return log.New(os.Stdout, "", vars.LoggerFlagsDebug)
	case 1:
		f, err := os.OpenFile("internal/logs/debug.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
        if err != nil {
            log.Println(`Не смогли открыть файл для логгера дебага, логи записаны не будут.
			Укажите Stdout в качестве вывода, чтобы выводить логи в консоль:`, err)
            f = nil
        }
        OpenFiles = append(OpenFiles, f)
        writer := bufio.NewWriter(f)
        go autoFlushBuffer(writer)
        return log.New(writer, "", vars.LoggerFlagsDebug)
	case 2:
		f, err := os.OpenFile("internal/logs/debug.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			log.Println("Не смогли открыть файл для логгера дебага, будет использоваться только Stdout:", err)
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		return log.New(io.MultiWriter(os.Stdout, f), "", vars.LoggerFlagsDebug)
	}
	return log.Default()
}

func GetHeartbeatLogger() *log.Logger {
	switch vars.LoggerOutputPings {
	case 0:
		return log.New(os.Stdout, "PULSE ", vars.LoggerFlagsPings)
	case 1:
		f, err := os.Create("internal/logs/heartbeats.txt")
		if err != nil {
			log.Println("Не смогли открыть файл для логгера пингов, их логи записаны не будут:", err)
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		return log.New(f, "PULSE ", vars.LoggerFlagsPings)
	case 2:
		f, err := os.Create("internal/logs/heartbeats.txt")
		if err != nil {
			log.Println("Не смогли открыть файл для логгера пингов, будет использоваться только Stdout:", err)
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		return log.New(io.MultiWriter(os.Stdout, f), "", vars.LoggerFlagsPings)
	}
	return log.Default()
}

func GetQueueLogger() *log.Logger {
	switch vars.LoggerOutputQueue {
	case 0:
		return log.New(os.Stdout, "", vars.LoggerFlagsQueue)
	case 1:
		f, err := os.Create("internal/logs/queue.txt")
		if err != nil {
			log.Println("Не смогли открыть файл для логгера обращений агентов, их логи записаны не будут:", err)
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		return log.New(f, "PULSE ", vars.LoggerFlagsQueue)
	case 2:
		f, err := os.Create("internal/logs/queue.txt")
		if err != nil {
			log.Println("Не смогли открыть файл для логгера обращений агентов, будет использоваться только Stdout:", err)
			f = nil
		}
		OpenFiles = append(OpenFiles, f)
		return log.New(io.MultiWriter(os.Stdout, f), "", vars.LoggerFlagsQueue)
	}
	return log.Default()
}

/*                                                       
                                               .::-+*=                                    
                                         :++**#####%#%*                                   
                                         .#%###%%###***++=-:.                             
                                      :+#%###%%#####*++++*#%%#*=                          
                                   :+##*************#%*+++++*####.                        
                                .=%%#****************#%++++++++*%*                        
                              :+#%#*******************%++++++++++**-                      
                          :::**+%#********************%++++++++++++**.                    
                       .==-:=**##********************#%++++++++++++++#-                   
                  :-=++#:.:=-:-%*********************%*+++++++++++++++**                  
               .+**++++#:.*@@*.:##*******************%*++++++++++++++++*%*=:              
               :+****++#*-:=++:.:#%###***************@+++++++++++**#####*++**+:           
                      .+.=+:.....:-:-=++**###########%###########**+++++++++++**-         
                      *-....................::::-----:-+***+++++++++++++++++++++*#:       
                     .*..................................:=+***+++++++++++++++++++#       
                     =-......................................:-=++******++++*****+:       
                     *:............................................::--=====#:.           
                    .*............................:::::................::::.*             
                    -=........................:==--:::--==:.......:-==-----=*=            
                    *:......................:+-           -+:...:==.         .=-          
                   .#......................:+               *:.:+:             .+         
                   ==......................*.               .*:+-               :=        
                   *:......................*                 #:#.                #        
                  .+.......................*                 #:+:               :+        
                  *:.......................-+         :=+-  +-.:*.        :=+=  +         
                 :+.........................-+:     .#+@@=:+==+++*-     .#+@@+-+          
                 *:..........................:-=-:. .=++==-:#@@@@%-==-:..+***=.           
                +=..............................::-===-::..=*%@@%*=:.::---::*             
               :+.........................................++::--::=+.......-+             
               *:........................................:*:::::::-*.......+:             
              *:..........................................-+*=#=+++::---===%-:..          
             =-..........................:..................* +..*:-+:........:--=*:      
            :+..........................:*.................:*-#**%+=           .==.       
            #:.......................:=++++=:..............-+#%**%:  G  O      -=.         
           +-....................-+==+*+:::-+======-:....:*##- :+.  L A N G .:-*:           
          .*....................:-:....:=+-::::::::-=**#%%#%*:-=       .===---=*          
          *-.............................:=+=::::::::-#=::.:-#= ..:::--#-:::::=+          
          #:.............................:*#%%*=::::-+=.....-+===--::::-*=====:           
         :+..............................:++=:::====-:.................:*                 
     ----+=............................................................=-                 
    #-:::=+...........................................................:#                  
    .-====#:..........................................................+:                  
         .+=.........................................................:+                   
           +-.......................................................:*.                   
            +=......................................................+:                    
             :+:...:==:...:=:......................................==                     
               -+-.:=#**+*+*-.....................................==                      
                 -==*--:-*......................................:+-                       
                   +-:::=+.....................................-+.                        
                  -+::::+*-:.................................:**:                         
                  %:::::-=.-===::.........................:=+=::-====-.                   
                 .#::::::*     :-=====-:::.......::::--====*=:::::::::*.                  
                  +=:::::==           .:------------::.     :===-:::=+-                   
                   .---===.                                    .:--:.                     
*/
