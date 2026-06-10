package utils

import "log"

func FailedOnError(packageName string, err error, msg string) error {
	if err != nil {
		log.Printf("[%s] %s: %s", packageName, msg, err)
		return err
	}

	return nil
}
