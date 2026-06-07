// a single place to close all connections from db, queue etc

package main

import (
	"log"
)

func shutdownGracefully() {
	log.Println("👋 Shutting down orchestrator gracefully...")
}