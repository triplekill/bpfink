package pkg

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	graphite "github.com/cyberdelia/go-metrics-graphite"
	goMetrics "github.com/rcrowley/go-metrics"
	"github.com/rs/zerolog"
)

//Metrics struct defining configs for graphite metrics
type Metrics struct {
	GraphiteHost        string
	Namespace           string
	GraphiteMode        int
	MetricsInterval     time.Duration
	EveryHourRegister   goMetrics.Registry
	EveryMinuteRegister goMetrics.Registry
	Hostname            string
	RoleName            string
	Logger              zerolog.Logger
	missedCount         map[string]int64
	hitCount            map[string]int64
}

type bpfMetrics struct {
	hitRate    int64
	missedRate int64
}

const (
	graphiteOff = iota + 1
	graphiteStdout
	graphiteRemote
	provbeVfsWrite  = "pvfs_write"
	provbeVfsRename = "pvfs_rename"
)

//Init method to start up graphite metrics
func (m *Metrics) Init() error {
	m.missedCount = make(map[string]int64)
	m.hitCount = make(map[string]int64)
	m.Logger.Debug().Msgf("fake metrics: %v", m.GraphiteMode)
	switch m.GraphiteMode {
	case graphiteOff:

	case graphiteStdout:
		go goMetrics.Log(m.EveryHourRegister, 30*time.Second, log.New(os.Stderr, "METRICS_HOUR: ", log.Lmicroseconds))
		go goMetrics.Log(m.EveryMinuteRegister, 30*time.Second, log.New(os.Stderr, "METRICS_MINUTE: ", log.Lmicroseconds))

	case graphiteRemote:
		addr, err := net.ResolveTCPAddr("tcp", m.GraphiteHost)
		if err != nil {
			return err
		}
		go graphite.Graphite(m.EveryHourRegister, time.Minute*30, "", addr)
		go graphite.Graphite(m.EveryMinuteRegister, time.Second*30, "", addr)
	}

	return nil
}

//RecordByInstalledHost graphite metric to show how manay host have bpfink installed
func (m *Metrics) RecordByInstalledHost() {
	metricNameByHost := fmt.Sprintf("installed.by_host.%s.count.hourly", quote(m.Hostname))
	goMetrics.GetOrRegisterGauge(metricNameByHost, m.EveryHourRegister).Update(int64(1))
	if m.RoleName != "" {
		metricNameByRole := fmt.Sprintf("installed.by_role.%s.count.hourly", quote(m.RoleName))
		goMetrics.GetOrRegisterGauge(metricNameByRole, m.EveryHourRegister).Update(int64(1))
	}
}

//RecordBPFMetrics send metrics for BPF hits and misses per probe
func (m *Metrics) RecordBPFMetrics() error {
	go func() {
		for range time.Tick(m.MetricsInterval) {

			BPFMetrics, err := m.fetchBPFMetrics()
			if err != nil {
				m.Logger.Error().Err(err).Msg("error fetching bpf metrics")
			}
			for key := range BPFMetrics {
				vfsHit := fmt.Sprintf("bpf.by_host.%s.%s.kprobe.hit_rate.minutely", quote(m.Hostname), key)
				vfsMiss := fmt.Sprintf("bpf.by_host.%s.%s.kprobe.miss_rate.minutely", quote(m.Hostname), key)
				goMetrics.GetOrRegisterGauge(vfsHit, m.EveryMinuteRegister).Update(BPFMetrics[key].hitRate)
				goMetrics.GetOrRegisterGauge(vfsMiss, m.EveryMinuteRegister).Update(BPFMetrics[key].missedRate)
			}
		}
	}()
	return nil
}

func (m *Metrics) fetchBPFMetrics() (map[string]bpfMetrics, error) {
	BPFMetrics := make(map[string]bpfMetrics)

	file, err := os.Open("/sys/kernel/debug/tracing/kprobe_profile")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Fatal(err)
		}
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		tokens := strings.Fields(line)

		if strings.Contains(tokens[0], "pvfs_write") {
			bpfMetric, err := m.parseBPFLine(tokens, provbeVfsWrite)
			if err != nil {
				return nil, err
			}
			BPFMetrics[provbeVfsWrite] = *bpfMetric
		}

		if strings.Contains(tokens[0], "pvfs_rename") {
			bpfMetric, err := m.parseBPFLine(tokens, provbeVfsRename)
			if err != nil {
				return nil, err
			}
			BPFMetrics[provbeVfsRename] = *bpfMetric
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
	return BPFMetrics, nil
}

func (m *Metrics) parseBPFLine(tokens []string, probeName string) (*bpfMetrics, error) {
	currentHit, err := strconv.ParseInt(tokens[1], 10, 64)
	if err != nil {
		return nil, err
	}
	currentMiss, err := strconv.ParseInt(tokens[2], 10, 64)
	if err != nil {
		return nil, err
	}

	hitRate := currentHit - m.hitCount[probeName]
	missedRate := currentMiss - m.missedCount[probeName]
	m.hitCount[probeName] = currentHit
	m.missedCount[probeName] = currentMiss
	return &bpfMetrics{
		hitRate:    hitRate,
		missedRate: missedRate,
	}, nil
}

func quote(str string) string {
	underscorePrecedes := false
	quotedString := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r):
			underscorePrecedes = false
			return unicode.ToLower(r)
		case unicode.IsDigit(r):
			underscorePrecedes = false
			return r
		case underscorePrecedes:
			return -1
		default:
			underscorePrecedes = true
			//maintain - in hostnames
			if string(r) == "-" {
				return r
			}
			return '_'
		}
	}, str)

	return strings.Trim(quotedString, "_")
}
