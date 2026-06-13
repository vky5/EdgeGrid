package utils

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// EnsureVenv automatically creates a Python virtual environment and installs pip dependencies.
func EnsureVenv(runnerPath string) (string, error) {
	venvPath := filepath.Join(filepath.Dir(runnerPath), ".venv")
	pythonBin := filepath.Join(venvPath, "bin", "python3")

	// If the virtual environment python binary already exists, we assume it's set up
	if _, err := os.Stat(pythonBin); err == nil {
		return pythonBin, nil
	}

	log.Printf("Python virtual environment not found at %s. Creating one...", venvPath)

	// Create the venv
	cmd := exec.Command("python3", "-m", "venv", venvPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to create virtual environment: %w", err)
	}

	log.Println("Virtual environment created successfully. Installing dependencies (protobuf, sentence-transformers)...")

	// Install pip packages
	cmd = exec.Command(pythonBin, "-m", "pip", "install", "protobuf", "sentence-transformers")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to install dependencies in virtual environment: %w", err)
	}

	log.Println("Dependencies installed successfully in virtual environment.")
	return pythonBin, nil
}
