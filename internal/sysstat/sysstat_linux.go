//go:build linux

package sysstat

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// prevIdle/prevTotal hold the last /proc/stat sample. CPU time in /proc is
// cumulative since boot, so a single reading says nothing about current
// load — only the delta between two readings does.
var prevIdle, prevTotal uint64

func cpuUsage() float64 {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0
	}

	var total, idle uint64
	for i := 1; i < len(fields); i++ {
		v, _ := strconv.ParseUint(fields[i], 10, 64)
		total += v
		if i == 4 { // the idle column
			idle = v
		}
	}

	diffIdle := idle - prevIdle
	diffTotal := total - prevTotal
	prevIdle, prevTotal = idle, total
	if diffTotal == 0 {
		return 0
	}
	return clamp(1.0 - float64(diffIdle)/float64(diffTotal))
}

func memUsage() float64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()

	var total, available float64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total = parseMeminfoValue(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			available = parseMeminfoValue(line)
		}
	}
	if total <= 0 {
		return 0
	}
	return clamp((total - available) / total)
}

// parseMeminfoValue pulls the number out of a "MemTotal:  16316456 kB" line.
// The unit is always kB, and the ratio is unit-free, so it's never converted.
func parseMeminfoValue(line string) float64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[1], 64)
	return v
}
