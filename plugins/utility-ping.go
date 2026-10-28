package plugins

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	startTime       = time.Now()
	cachedCpuServer float64
	cachedCpuBot    float64
	cpuMu           sync.RWMutex
)

func init() {
	Register(&Plugin{
		Command:     []string{"ping", "stats", "status"},
		Description: "Check bot response speed + system resources",
		Category:    "utility",
		Handler:     utilPingHandler,
	})

	go func() {
		for {
			cpuServer, cpuBot, err := calculateCPUStats()
			if err == nil {
				cpuMu.Lock()
				cachedCpuServer = cpuServer
				cachedCpuBot = cpuBot
				cpuMu.Unlock()
			}
			time.Sleep(3 * time.Second)
		}
	}()
}

func utilPingHandler(_ context.Context, c *Ctx) error {
	elapsed := time.Since(time.UnixMilli(c.ReceivedAt))
	speedMs := float64(elapsed.Microseconds()) / 1000.0

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	botRamMB := float64(mem.Alloc) / 1024 / 1024

	totalGB, usedGB, percentRAM, errMem := getServerMemory()

	cpuMu.RLock()
	cpuServer := cachedCpuServer
	cpuBot := cachedCpuBot
	cpuMu.RUnlock()

	platform := runtime.GOARCH
	if platform == "arm64" {
		platform = "ARM64"
	} else if platform == "amd64" {
		platform = "AMD64"
	} else {
		platform = strings.ToUpper(platform)
	}

	var statLines []string
	statLines = append(statLines, "*Pong!*")
	statLines = append(statLines, "")
	statLines = append(statLines, fmt.Sprintf("Platform: %s", platform))
	statLines = append(statLines, fmt.Sprintf("Speed: %.2f ms", speedMs))
	statLines = append(statLines, "")
	statLines = append(statLines, fmt.Sprintf("CPU Server: %.2f%%", cpuServer))
	statLines = append(statLines, fmt.Sprintf("CPU Bot: %.2f%%", cpuBot))
	statLines = append(statLines, "")

	if errMem == nil {
		statLines = append(statLines, fmt.Sprintf("RAM Server: %.2f GB / %.2f GB (%.2f%%)", usedGB, totalGB, percentRAM))
	} else {
		statLines = append(statLines, "RAM Server: 0.00 GB / 0.00 GB (0.00%)")
	}
	statLines = append(statLines, fmt.Sprintf("RAM Bot: %.2f MB", botRamMB))
	statLines = append(statLines, "")
	statLines = append(statLines, fmt.Sprintf("Uptime: %s", formatUptime(time.Since(startTime))))

	msg := strings.Join(statLines, "\n")
	return c.Reply(msg)
}

func getServerMemory() (totalGB, usedGB, percent float64, err error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, err
	}
	var memTotal, memAvailable int64
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := fields[0]
		val, _ := strconv.ParseInt(fields[1], 10, 64)
		if key == "MemTotal:" {
			memTotal = val
		} else if key == "MemAvailable:" {
			memAvailable = val
		}
	}
	if memTotal == 0 {
		return 0, 0, 0, fmt.Errorf("failed to parse meminfo")
	}
	used := memTotal - memAvailable
	totalGB = float64(memTotal) / 1024 / 1024
	usedGB = float64(used) / 1024 / 1024
	percent = (float64(used) / float64(memTotal)) * 100
	return totalGB, usedGB, percent, nil
}

func calculateCPUStats() (cpuServer, cpuBot float64, err error) {
	getServerTicks := func() (idle, total int64, err error) {
		data, err := os.ReadFile("/proc/stat")
		if err != nil {
			return 0, 0, err
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) == 0 {
			return 0, 0, fmt.Errorf("empty stat")
		}
		fields := strings.Fields(lines[0])
		if len(fields) < 5 {
			return 0, 0, fmt.Errorf("invalid stat format")
		}
		var sum int64
		for i := 1; i < len(fields); i++ {
			val, _ := strconv.ParseInt(fields[i], 10, 64)
			sum += val
		}
		idleVal, _ := strconv.ParseInt(fields[4], 10, 64)
		return idleVal, sum, nil
	}

	getBotTicks := func() (int64, error) {
		data, err := os.ReadFile("/proc/self/stat")
		if err != nil {
			return 0, err
		}
		fields := strings.Fields(string(data))
		if len(fields) < 15 {
			return 0, fmt.Errorf("invalid self stat")
		}
		utime, _ := strconv.ParseInt(fields[13], 10, 64)
		stime, _ := strconv.ParseInt(fields[14], 10, 64)
		return utime + stime, nil
	}

	idle1, total1, err := getServerTicks()
	if err != nil {
		return 0, 0, err
	}
	bot1, err := getBotTicks()
	if err != nil {
		return 0, 0, err
	}

	time.Sleep(100 * time.Millisecond)

	idle2, total2, err := getServerTicks()
	if err != nil {
		return 0, 0, err
	}
	bot2, err := getBotTicks()
	if err != nil {
		return 0, 0, err
	}

	totalDelta := total2 - total1
	idleDelta := idle2 - idle1
	botDelta := bot2 - bot1

	if totalDelta > 0 {
		cpuServer = float64(totalDelta-idleDelta) / float64(totalDelta) * 100
		cpuBot = (float64(botDelta) / float64(totalDelta)) * 100 * float64(runtime.NumCPU())
	} else {
		if load, err := getCPULoad(); err == nil {
			cpuServer = load
		}
		if botDelta > 0 {
			cpuBot = float64(botDelta) / 100.0 * float64(runtime.NumCPU())
		}
	}

	return cpuServer, cpuBot, nil
}

func getCPULoad() (float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, fmt.Errorf("invalid loadavg format")
	}
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, err
	}
	cpuCount := float64(runtime.NumCPU())
	if cpuCount == 0 {
		cpuCount = 1
	}
	return (load1 / cpuCount) * 100, nil
}

func formatUptime(d time.Duration) string {
	sec := int(d.Seconds())
	day := sec / 86400
	hour := (sec % 86400) / 3600
	min := (sec % 3600) / 60
	second := sec % 60
	var parts []string
	if day > 0 {
		parts = append(parts, fmt.Sprintf("%dd", day))
	}
	if hour > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hour))
	}
	if min > 0 {
		parts = append(parts, fmt.Sprintf("%dm", min))
	}
	parts = append(parts, fmt.Sprintf("%ds", second))
	return strings.Join(parts, " ")
}
