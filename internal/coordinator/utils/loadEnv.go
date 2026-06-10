package utils

import (
	"github.com/joho/godotenv"
)

func EnvInit(fileName string) error {
	err := godotenv.Load(fileName)
	return FailedOnError("env", err, "failed to load environment variables")
}
