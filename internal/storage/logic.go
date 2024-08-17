package storage

type ExpItem struct {
    ID   string
    Exp string
	Result string
	Agent int
	Username string
}

type Storage interface {
	RegisterUser(username, password string) error
	LogInUser(username, password string) (bool, error)
	ExpressionStore(id, username, expression string) error
	ExpressionCheck(id, username string) (bool, error)
	ExpressionAddResult(id, result string, err bool) error
	GetExpressionsUser(username string) []ExpItem
	GetExpressionsAll() []ExpItem
}
