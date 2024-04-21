package utils

import (
	"golang.org/x/crypto/bcrypt"
)

func GenerateHashFromPswd(s string) (string, error) {
	saltedBytes := []byte(s)
	hashedBytes, err := bcrypt.GenerateFromPassword(saltedBytes, bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	hash := string(hashedBytes[:])
	return hash, nil
}

func CompareHashAndPassword(hash string, password string) error {
	incoming := []byte(password)
	existing := []byte(hash)
	return bcrypt.CompareHashAndPassword(existing, incoming)
}
