package jwtstuff

import (
	"net/http"
	"time"

	"calculator/internal/config"
	"calculator/internal/errors"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	Username string			`json:"username"`
	jwt.RegisteredClaims
}

func GenerateToken(username string) (*http.Cookie, error) {
	now := time.Now()
	expirationTime := now.Add(vars.JWTValidyDuration)
	claims := Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt: &jwt.NumericDate{now},
			NotBefore: &jwt.NumericDate{now},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(vars.SecretJWTSignature))
	if err != nil {
		return nil, err
	}

	return &http.Cookie{
		Name:    "token",
		Value:   tokenString,
		Expires: expirationTime,
		// HttpOnly: true,
		Path: "/",
		SameSite: http.SameSiteNoneMode,
	}, nil
}

func CheckToken(tokenString string) (string, error) {
	claims := &Claims{}

	tkn, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return []byte(vars.SecretJWTSignature), nil
	})
	if err != nil {
		if err == jwt.ErrSignatureInvalid {
			return "", myErrors.JWTErrors.InvalidTokenErr
		}
		return "", err
	}
	if !tkn.Valid {
		return "", myErrors.JWTErrors.InvalidTokenErr
	}

	return claims.Username, nil
}
