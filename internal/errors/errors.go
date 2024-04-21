package myErrors

import "errors"

var (
	FrontSrvErrors = frontSrvErrors{
		InternalDbKeyCheckError: errors.New("ошибка проверки ключа в базе данных"),
		InexistantIDError: errors.New("выражение с полученным ID не найдено"),
		InternalDbKeyGetError: errors.New("ошибка получения выражения по ключу из базы данных"),
	}
	JWTErrors = jwtErros{
		InvalidTokenErr: errors.New("неверный токен"),
		UnknownErr: errors.New("ошибка парсинга токена"),
	}
)

type frontSrvErrors struct {
	InternalDbKeyCheckError error
	InexistantIDError error
	InternalDbKeyGetError error
}

type jwtErros struct {
	InvalidTokenErr error
	UnknownErr		error
}