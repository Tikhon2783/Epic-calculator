package jwtstuff

import (
	"time"
	"net/http"

	"calculator/internal/config"
	"calculator/internal/errors"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	Username string			`json:"username"`
	jwt.RegisteredClaims
}

func GenerateToken(username string) (*http.Cookie, error) {
	expirationTime := time.Now().Add(vars.JWTValidyDuration)
	claims := Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(vars.SecretJWTSignature)
	if err != nil {
		return nil, err
	}

	return &http.Cookie{
		Name:    "token",
		Value:   tokenString,
		Expires: expirationTime,
		HttpOnly: true,
	}, nil
}

func CheckToken(tokenString string) (string, error) {
	claims := &Claims{}

	tkn, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return vars.SecretJWTSignature, nil
	})
	if err != nil {
		if err == jwt.ErrSignatureInvalid {
			return "", myErrors.JWTErrors.InvalidTokenErr
		}
		return "", myErrors.JWTErrors.UnknownErr
	}
	if !tkn.Valid {
		return "", myErrors.JWTErrors.InvalidTokenErr
	}

	return claims.Username, nil
}
