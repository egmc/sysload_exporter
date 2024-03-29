package main

import (
	"bufio"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	namespace = "sysload"
)

var ProcStatFieldMap = map[string]int{
	"user":   1,
	"nice":   2,
	"system": 3,
	"idle":   4,
	"wio":    5,
	"intr":   6,
	"sintr":  7,
}

var log *zap.SugaredLogger

var globalParam struct {
	TargetBlockDevices   []string
	InterruptThreshold   float64
	TargetNetworkDevices []string
	InterruptedCpuGroup  map[string][]string
	NumCPU               int
	ProcPath             string
}

// return wrapped value
func counterWrap(num float64) float64 {
	if num > math.MaxUint32 {
		num = math.MaxUint32
	} else if num < 0 {
		if (num + math.MaxUint32 + 1) >= 0 {
			// 32bit
			num += math.MaxUint32 + 1
		} else if (num+math.MaxUint64+1) >= 0 && (num+math.MaxUint64+1) <= math.MaxUint32 {
			num += math.MaxUint64 + 1
		} else {
			num = math.MaxUint32
		}
	}
	return num
}

func findBlockDevices() []string {

	var devices []string
	r := regexp.MustCompile("^(x?[svh]d[a-z]|cciss\\/c0d0|fio[a-z])$")

	statPath := globalParam.ProcPath + "/diskstats"
	f, err := os.Open(statPath)
	if err != nil {
		log.Fatal("couldn't open" + statPath)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		e := strings.Fields(scanner.Text())
		m := r.Find([]byte(e[2]))
		if m != nil {
			devices = append(devices, string(m))
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	return devices
}

// findInterruptedCpu cpu num from /proc/interrupts
func findInterruptedCpu(targetDevice string) []string {
	var interruptedCpu []string

	cpuNum := getCpuNum()

	statPath := globalParam.ProcPath + "/interrupts"
	f, err := os.Open(statPath)
	if err != nil {
		log.Error("couldn't open" + statPath)
		log.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		l := scanner.Text()
		if !strings.Contains(l, targetDevice) {
			continue
		}
		if strings.Contains(l, "tx") {
			continue
		}

		e := strings.Fields(l)[1:]
		log.Debug("interrupted cpu field: ")
		log.Debug(e)

		for i := range make([]int, cpuNum) {
			s := e[i]
			n, err := strconv.Atoi(s)
			if err != nil {
				log.Fatal(e)
			}
			r, _ := utf8.DecodeLastRuneInString(s)

			if unicode.IsDigit(r) && n > 0 {
				for _, v := range interruptedCpu {
					if v == s {
						break
					}
				}
				interruptedCpu = append(interruptedCpu, strconv.Itoa(i))
			}
		}
	}
	return interruptedCpu
}

func getCpuNum() int {
	num := 0
	statPath := globalParam.ProcPath + "/cpuinfo"
	f, err := os.Open(statPath)
	if err != nil {
		log.Error("couldn't open" + statPath)
		log.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	r := regexp.MustCompile("^processor")

	for scanner.Scan() {
		if r.Match(scanner.Bytes()) {
			num++
		}
	}

	if err := scanner.Err(); err != nil {
		log.Error("scan error")
		log.Fatal(err)
	}
	return num
}

func addJiffies(e []string, prefix string, stats map[string]uint64) {
	for k, v := range ProcStatFieldMap {
		u, _ := strconv.ParseUint(e[v], 10, 64)
		stats[prefix+"_"+k] += u
		stats[prefix+"_total"] += u
	}

}

func addAllCpuJiffies(e []string, stats map[string]uint64) {
	addJiffies(e, "all_cpu", stats)
}

func updateCpuStat(stats map[string]uint64) {
	for k, _ := range stats {
		stats[k] = 0
	}

	statPath := globalParam.ProcPath + "/stat"
	f, err := os.Open(statPath)
	if err != nil {
		log.Fatal(err)
	}

	defer f.Close()

	for dev, cpus := range globalParam.InterruptedCpuGroup {
		stats["proc_ctxt"] = 0
		stats["proc_intr"] = 0

		allcpu := false
		if len(cpus) == globalParam.NumCPU {
			allcpu = true
		}

		f.Seek(0, 0)

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			l := scanner.Text()
			e := strings.Fields(l)

			if len(e) < 2 {
				continue
			}
			_, parseError := strconv.ParseUint(e[1], 10, 64)
			if parseError != nil {
				continue
			}
			if e[0] == "ctxt" && stats["proc_ctxt"] == 0 {
				u, _ := strconv.ParseUint(e[1], 10, 64)
				stats["proc_ctxt"] = u
				continue
			}
			if e[0] == "intr" && stats["proc_intr"] == 0 {
				u, _ := strconv.ParseUint(e[1], 10, 64)
				stats["proc_intr"] = u
				continue
			}
			if !strings.Contains(e[0], "cpu") {
				continue
			}

			if e[0] == "cpu" {
				if stats["all_cpu_total"] == 0 {
					// (optimization differ from original cpustats.py)add all cpu stats only first time
					addAllCpuJiffies(e, stats)
				}
				if allcpu {
					addJiffies(e, dev, stats)
				}
			} else {
				n := strings.Replace(e[0], "cpu", "", -1)
				_, converter := strconv.Atoi(n)
				if converter == nil {
					isInterrupted := false
					for _, iCpu := range globalParam.InterruptedCpuGroup[dev] {
						if iCpu == n {
							isInterrupted = true
							break
						}
					}
					if isInterrupted {
						addJiffies(e, dev, stats)
					}
				}
			}
		}
	}
}

func updateIoStat(stats map[string]uint64) {

	for k, _ := range stats {
		stats[k] = 0
	}

	statPath := globalParam.ProcPath + "/diskstats"
	f, err := os.Open(statPath)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		l := scanner.Text()
		e := strings.Fields(l)
		for _, d := range globalParam.TargetBlockDevices {
			if d != e[2] {
				continue
			}
			k := e[2]
			if strings.Contains(e[2], "cciss") {
				k = "cciss"
			}
			v, _ := strconv.ParseUint(e[12], 10, 64)
			stats[k+"_io_util"] = v
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}

func calcSysLoad(metricsValues map[string]float64) float64 {
	sysLoad := 0.0

	for k, v := range metricsValues {
		if strings.Contains(k, "_io_util") {
			if v > sysLoad {
				sysLoad = v
				continue
			}
		}
		if !strings.Contains(k, "_idle") {
			continue
		}
		usage := 100.0 - v
		if usage < sysLoad {
			continue
		}
		if k == "all_cpu_idle" {
			sysLoad = usage
			continue
		}
		if k == "si_cpu_idle" {
			if (metricsValues["si_cpu_intr"] + metricsValues["si_cpu_sintr"] + metricsValues["si_cpu_system"]) > globalParam.InterruptThreshold {
				sysLoad = usage
			}
		}
	}

	return sysLoad
}

func calcMovingAverage(loadList []float64) float64 {

	sum := 0.0
	for _, load := range loadList {
		sum += load
	}

	return sum / float64(len(loadList))

}

func initAndRegisterMetrics(metrics map[string]prometheus.Gauge) {

	metrics["sysload"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "sysload",
		Help:      "Sysload",
	})
	metrics["sysload_one"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "sysload_one",
		Help:      "Sysload 1 min",
	})
	metrics["sysload_five"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "sysload_five",
		Help:      "sysload five help",
	})

	metrics["sysload_fifteen"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "sysload_fifteen",
		Help:      "Sysload 15 min",
	})

	metrics["proc_ctxt"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "proc_ctxt",
		Help:      "Context Switch",
	})
	metrics["proc_intr"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "proc_intr",
		Help:      "Interrupts",
	})

	log.Info("register metrics")
	for _, e := range metrics {
		prometheus.MustRegister(e)
	}

	ioUtilsGaugeVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "io_util",
		Help:      "IO Util",
	}, []string{"device"})
	prometheus.MustRegister(ioUtilsGaugeVec)
	for _, dev := range globalParam.TargetBlockDevices {
		metrics[dev+"_io_util"], _ = ioUtilsGaugeVec.GetMetricWith(prometheus.Labels{"device": dev})
	}

	siCpuGaugeVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "si_cpu",
		Help:      "Software Interrupted CPU",
	}, []string{"mode"})
	prometheus.MustRegister(siCpuGaugeVec)
	for mode, _ := range ProcStatFieldMap {
		metrics["si_cpu_"+mode], _ = siCpuGaugeVec.GetMetricWith(prometheus.Labels{"mode": mode})
	}

}

func updateMetrics(metrics map[string]prometheus.Gauge, refreshRate int) {

	var statTime, statTimePrev time.Time

	ioStats := make(map[string]uint64)
	ioStatsPrev := make(map[string]uint64)
	cpuStats := make(map[string]uint64)
	cpuStatsPrev := make(map[string]uint64)

	// init metrics values
	metricsValues := make(map[string]float64)
	for k, _ := range metrics {
		metricsValues[k] = 0.0
	}

	// init sysload map
	sysloadArrayMap := make(map[string][]float64)

	sysloadArrayMap["sysload_one"] = make([]float64, 60/refreshRate)
	sysloadArrayMap["sysload_five"] = make([]float64, 300/refreshRate)
	sysloadArrayMap["sysload_fifteen"] = make([]float64, 900/refreshRate)
	for _, v := range sysloadArrayMap {
		for i, _ := range v {
			v[i] = 0.0
		}
	}

	counter := 0

	for {
		counter++

		statTime = time.Now()

		updateIoStat(ioStats)
		updateCpuStat(cpuStats)

		if !statTimePrev.IsZero() {

			log.Debug("iostat:")
			log.Debug(ioStats)
			log.Debug(ioStatsPrev)
			log.Debug("cpustat:")
			log.Debug(cpuStats)
			log.Debug(cpuStatsPrev)
			log.Debug("time:")
			log.Debug(statTime)
			log.Debug(statTimePrev)
			timeDiffMs := statTime.Sub(statTimePrev).Milliseconds()
			log.Debugf("time diff %d", timeDiffMs)

			// si cpu
			sintr := 0.0
			busyDev := ""
			for dev, _ := range globalParam.InterruptedCpuGroup {
				devDiff := float64(cpuStats[dev+"_total"] - cpuStatsPrev[dev+"_total"])
				log.Debug("dev diff: ")
				log.Debug(devDiff)
				for k, _ := range cpuStats {
					if strings.Contains(k, dev) {
						d := float64(cpuStats[k] - cpuStatsPrev[k])
						if d > 0 {
							metricsValues[k] = d / devDiff * 100
						} else {
							metricsValues[k] = 0.0
						}
					}
					if k == dev+"_sintr" && sintr <= metricsValues[k] {
						sintr = metricsValues[k]
						busyDev = dev
					}
				}
			}
			for k, _ := range ProcStatFieldMap {
				metricsValues["si_cpu_"+k] = metricsValues[busyDev+"_"+k]
			}

			// all cpu, proc
			allCpuDiff := float64(cpuStats["all_cpu_total"] - cpuStatsPrev["all_cpu_total"])
			for k, _ := range cpuStats {
				if _, exists := cpuStatsPrev[k]; !exists {
					continue
				}
				d := cpuStats[k] - cpuStatsPrev[k]
				if d > 0.0 {
					if strings.Contains(k, "all_cpu") {
						metricsValues[k] = float64(d) / allCpuDiff * 100
					}
					if strings.Contains(k, "proc_ctxt") || strings.Contains(k, "proc_intr") {
						// calc per sec increase
						metricsValues[k] = float64(d) / float64(timeDiffMs/1000)
					}
				} else {
					metricsValues[k] = 0.0
				}
				log.Debugw("all_cpu", k, d, "totaldiff", allCpuDiff)
			}

			//io
			for k, v := range ioStats {
				diff := counterWrap(float64(v - ioStatsPrev[k]))
				metricsValues[k] = diff / float64(timeDiffMs) * 100
				if metricsValues[k] > 100.0 {
					metricsValues[k] = 100.0 //io util can be over 100
				}
			}
			// sysLoad
			metricsValues["sysload"] = calcSysLoad(metricsValues)
			for k, _ := range sysloadArrayMap {
				sysloadArrayMap[k] = append(sysloadArrayMap[k][1:], metricsValues["sysload"])
				metricsValues[k] = calcMovingAverage(sysloadArrayMap[k])
			}

			// SetMetricsValues to export
			for k, v := range metrics {
				v.Set(metricsValues[k])
			}
			log.Debug("metrics values: ")
			log.Debug(metricsValues)

		}

		// copy
		for k, v := range ioStats {
			ioStatsPrev[k] = v
		}
		for k, v := range cpuStats {
			cpuStatsPrev[k] = v
		}
		statTimePrev = statTime

		log.Debugf("metric updated: %d \n", counter)
		time.Sleep(time.Duration(refreshRate) * time.Second)
	}

}

func main() {

	var (
		debug                = kingpin.Flag("debug", "Debug mode.").Bool()
		info                 = kingpin.Flag("info", "show current information and exit").Bool()
		targetBlockDevices   = kingpin.Flag("target-block-devices", "Target block devices to track io utils").Short('b').String()
		targetNetworkDevices = kingpin.Flag("target-network-devices", "Target network devices to check interrupting").Short('i').String()
		listenAddress        = kingpin.Flag("listen-address", "The address to listen on for HTTP requests.").Default(":9858").String()
		interruptedThreshold = kingpin.Flag("interrupted-threshold", "Threshold to consider interrupted cpu usage as sysload").Default("40.0").Float64()
		refreshRate          = kingpin.Flag("refresh-rate", "metrics refresh rate(should be 1 - 30)").Default("15").Int()
		procPath             = kingpin.Flag("path.procfs", "procfs mountpoint.").Default("/proc").String()
	)

	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	// set up logger
	cfg := zap.NewDevelopmentConfig()
	cfg.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	cfg.EncoderConfig.EncodeCaller = nil

	if *debug {
		cfg.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
		cfg.EncoderConfig.EncodeCaller = zapcore.ShortCallerEncoder
	}
	logger, _ := cfg.Build()

	defer logger.Sync() //
	log = logger.Sugar()

	if *refreshRate < 1 || *refreshRate > 30 {
		log.Fatalw("metrics refresh rate(should be 1 - 30)", "supplied", *refreshRate)
	}

	// set proc path first
	globalParam.ProcPath = *procPath

	globalParam.NumCPU = getCpuNum()

	globalParam.InterruptThreshold = *interruptedThreshold
	if *targetBlockDevices == "" {
		globalParam.TargetBlockDevices = findBlockDevices()
	} else {
		globalParam.TargetBlockDevices = strings.Split(*targetBlockDevices, ",")
	}
	if *targetNetworkDevices == "" {
		globalParam.TargetNetworkDevices = []string{"eth0", "eth1", "eth2", "eth3", "virtio0-input", "virtio1-input", "virtio0"}
	} else {
		globalParam.TargetNetworkDevices = strings.Split(*targetNetworkDevices, ",")
	}

	// init interrupted cpu group
	globalParam.InterruptedCpuGroup = make(map[string][]string)
	for _, d := range globalParam.TargetNetworkDevices {
		r := findInterruptedCpu(d)
		if len(r) > 0 {
			globalParam.InterruptedCpuGroup[d] = r
		}
	}

	log.Debug(globalParam)

	if *info {
		log.Infow("stats",
			"TargetBlockDevices", globalParam.TargetBlockDevices,
			"TargetNetworkDevices", globalParam.TargetNetworkDevices,
			"InterruptThreshold", globalParam.InterruptThreshold,
			"InterruptedCpuGroup", globalParam.InterruptedCpuGroup,
			"NumCPU", globalParam.NumCPU,
		)
	} else {

		log.Info("init and register metrics")
		metrics := make(map[string]prometheus.Gauge)
		initAndRegisterMetrics(metrics)

		log.Info("start updater")
		go updateMetrics(metrics, *refreshRate)

		log.Info("start http handler on " + *listenAddress)
		http.Handle("/metrics", promhttp.Handler())
		log.Fatal(http.ListenAndServe(*listenAddress, nil))

	}
}
