package secrets

import (
	"crypto/rand"


	"lotusforge.au/api-server/globals"
)

// RandomString returns an alphanumeric string of the given length using crypto/rand.
func RandomString(string_length int) (string, error) {
	return_str := make([]byte, string_length)
	_, err := rand.Read(return_str)

	if err != nil {
		return "", err
	}

	for i := range string_length {
		return_str[i] = globals.RandomChars[int(return_str[i])%len(globals.RandomChars)]
	}

	return string(return_str), nil
}
