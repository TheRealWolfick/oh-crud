package tools

import "crypto/rand"

func generateRandomString(string_length int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"

	return_str := make([]byte, string_length)
	_, err := rand.Read(return_str)

	if err != nil {
		return "", err
	}

	for i := range string_length {
		return_str[i] = chars[int(return_str[i]) % len(chars)]
	}

	return string(return_str), nil
}

func Generate32CharString() (string, error) {
	return generateRandomString(32)
}
