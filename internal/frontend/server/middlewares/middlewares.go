package middlewares

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"calculator/internal"
	"calculator/internal/errors"
	"calculator/internal/jwt-stuff"
)

var (
	ServerExitChannel chan os.Signal = make(chan os.Signal, 1)
	logger            *log.Logger = shared.Logger
	loggerErr         *log.Logger = shared.LoggerErr
	Srv               *http.Server
)


// Data is the type of value stored in the Contexts.
type Data struct {
	Username string
	Id string
	Expression string
}

// key is an unexported type for keys defined in this package.
// This prevents collisions with keys defined in other packages.
type key int

// dataKey is the key for Data values in Contexts. It is
// unexported; clients use NewContext and FromContext
// instead of using this key directly.
var dataKey key

// NewContext returns a new Context that carries value d.
func NewContext(ctx context.Context, d *Data) context.Context {
    return context.WithValue(ctx, dataKey, d)
}

// FromContext returns the Data value stored in ctx, if any.
func FromContext(ctx context.Context) (*Data, bool) {
    u, ok := ctx.Value(dataKey).(*Data)
    return u, ok
}

// Проверка выражения на валидность
func ValidityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
				loggerErr.Println("Сервер: непредвиденная ПАНИКА при обработке выражения:", rec)
				http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
			}
		}()

		logger.Println("Сервер получил запрос на подсчет выражения.")
		// Метод должен быть POST
		if r.Method != http.MethodPost {
			logger.Println("Неправильный метод, выражение не обрабатывается.")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		err := r.ParseForm()
		if err != nil {
			logger.Println("Внутренняя ошибка, выражение не обрабатывается.")
			loggerErr.Println("Сервер: ошибка парсинга запроса.")
			http.Error(w, "Ошибка парсинга запроса", http.StatusInternalServerError)
			return
		}

		// Проверяем выражение на валидность
		exp := r.PostForm.Get("expression")
		exp = strings.ReplaceAll(exp, " ", "") // Убираем пробелы
		log.Printf("Получили выражение: '%s'", exp)

		// Проверяем на постановку знаков
		replacer := strings.NewReplacer(
			"-", "+",
			"*", "+",
			"/", "+",
		)
		replacedExp := replacer.Replace(exp)
		if strings.Contains(replacedExp, "++") ||
			string(replacedExp[0]) == "+" || string(replacedExp[len(replacedExp)-1]) == "+" {
			logger.Println("Ошибка: нарушен синтаксис выражений, выражение не обрабатывается.")
			loggerErr.Println("Ошибка с проверкой выражения парсингом:", "неправильная постановка знаков")
			http.Error(w, "Выражение невалидно", http.StatusBadRequest)
			return
		}

		// Проверяем, нет ли посторонних символов в выражении
		replacer = strings.NewReplacer(
			"+", "",
			"-", "",
			"*", "",
			"/", "",
		)
		if _, err := strconv.ParseFloat(replacer.Replace(exp), 64); err != nil {
			logger.Println("Ошибка: нарушен синтаксис выражений, выражение не обрабатывается.")
			loggerErr.Println("Ошибка с проверкой выражения парсингом:", err)
			http.Error(w, "Выражение невалидно", http.StatusBadRequest)
			return
		}

		// Передаем обработчику ID и выражение через контекст
		v, ok := FromContext(r.Context())
		if !ok {
			loggerErr.Println("Ошибка получения имени пользователя из контекста:", err)
			logger.Println("Внутренняя ошибка контекста, выражение не обрабатывается.")
			http.Error(w, "ошибка валидации запроса", http.StatusInternalServerError)
			return
		}
		ctx := NewContext(r.Context(), &Data{Username: v.Username, Expression: exp})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Авторизация пользователя проверкой jwt токена
func ExternalAuthorizationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
				loggerErr.Println("Сервер: непредвиденная ПАНИКА при обработке выражения:", rec)
				http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
			}
		}()

		logger.Println("Получили запрос на внешнюю страницу, проверяем токен...")
		
		// Получаем jwt токен из cookie файлов
		cookie, err := r.Cookie("token")
		if err != nil {
			if err == http.ErrNoCookie {
				// Токена нет, перенаправляем пользователя на страницу входа
				logger.Println("Сервер: пользователь неавторизирован, перенаправляем на страницу входа.")
				http.Redirect(w, r, "/calculator/auth", http.StatusUnauthorized)
			} else {
				logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
				loggerErr.Println("Ошибка получения jwt токена:", err)
				http.Error(w, "ошибка получения jwt токена", http.StatusBadRequest)
			}
			return
		}

		tokenStr := cookie.Value
		username, err := jwtstuff.CheckToken(tokenStr)
		if err != nil {
			if err == myErrors.JWTErrors.InvalidTokenErr {
				// Токен неверный, перенаправляем пользователя на страницу входа
				logger.Println("Сервер: неверный токен, перенаправляем на страницу входа.")
				http.Redirect(w, r, "/calculator/auth", http.StatusUnauthorized)
			} else {
				logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
				loggerErr.Println("Ошибка проверки jwt токена:", err)
				http.Error(w, "ошибка проверки jwt токена", http.StatusBadRequest)
			}
			return
		}

		// Передаем обработчику имя пользователя через контекст
		ctx := NewContext(r.Context(), &Data{Username: username})
		logger.Printf("Пользователь %s авторизирован.", username)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Авторизация пользователя проверкой jwt токена
func InternalAuthorizationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
				loggerErr.Println("Сервер: непредвиденная ПАНИКА при обработке выражения:", rec)
				http.Error(w, "На сервере что-то сломалось", http.StatusInternalServerError)
			}
		}()

		fmt.Println(1)
		logger.Println("Получили запрос на внутренний обработчик, проверяем токен...")

		// Получаем jwt токен из cookie файлов
		cookie, err := r.Cookie("token")
		if err != nil {
			if err == http.ErrNoCookie {
				// Токена нет, перенаправляем пользователя на страницу входа
				logger.Println("Сервер: пользователь неавторизирован, возвращаем ошибку 401.")
				http.Error(w, "пользователь неавторизирован", http.StatusUnauthorized)
			} else {
				logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
				loggerErr.Println("Ошибка получения jwt токена:", err)
				http.Error(w, "ошибка получения jwt токена", http.StatusBadRequest)
			}
			return
		}

		tokenStr := cookie.Value
		username, err := jwtstuff.CheckToken(tokenStr)
		if err != nil {
			if err == myErrors.JWTErrors.InvalidTokenErr {
				// Токен неверный, перенаправляем пользователя на страницу входа
				logger.Println("Сервер: неверный токен, возвращаем ошибку 401.")
				http.Error(w, "пользователь неавторизирован", http.StatusUnauthorized)
			} else if err == myErrors.JWTErrors.UnknownErr {
				logger.Println("Внутренняя ошибка, запрос не обрабатывается.")
				loggerErr.Println("Ошибка проверки jwt токена:", err)
				http.Error(w, "ошибка проверки jwt токена", http.StatusBadRequest)
			}
			return
		}

		// Передаем обработчику имя пользователя через контекст
		logger.Printf("Запрос пользователя %s авторизирован.", username)
		ctx := NewContext(r.Context(), &Data{Username: username})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
