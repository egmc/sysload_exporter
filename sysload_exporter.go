package main

import (
	"bufio"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tklauser/go-sysconf"
	"gopkg.in/alecthomas/kingpin.v2"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	namespace = "sysload"
)

var refreshRate = 5
var UserHz int64
var NumCPU int

var ProcStatFieldMap = map[string]int {
	"user": 1,
	"nice": 2,
	"system": 3,
	"idle": 4,
	"wio": 5,
	"intr": 6,
	"sintr": 7,
}

var metrics map[string]prometheus.Gauge

// return wrapped value
func counterWrap(num float64) float64 {
	if num > math.MaxUint32 {
		num = math.MaxUint32
	} else if num < 0 {
		if (num + math.MaxUint32 + 1) >= 0 {
			// 32bit
			num += math.MaxUint32 + 1
		} else if (num + math.MaxUint64 + 1) >= 0 && (num + math.MaxUint64 + 1) <= math.MaxUint32 {
			num += math.MaxUint64 + 1
		} else {
			num =  math.MaxUint32
		}
	}
	return num
}

func findBlockDevices() []string {

	var devices []string
	r := regexp.MustCompile("^(x?[svh]d[a-z]|cciss\\/c0d0|fio[a-z])$")

	f, err := os.Open("/proc/diskstats")
	if err != nil{
		fmt.Println("error")
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		e := strings.Fields(scanner.Text())
		//fmt.Println(e[2])
		m := r.Find([]byte(e[2]))
		if m != nil {
			devices = append(devices, string(m))
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	fmt.Println(devices)

	return devices
}

// findInterruptedCpu cpu num from /proc/interrupts
func findInterruptedCpu(targetDevice string) []string {
	var interruptedCpu []string

	cpuNum := getCpuNum()
	fmt.Printf("num: %d\n", cpuNum)

	f, err := os.Open("/proc/interrupts")
	if err != nil{
		fmt.Println("error")
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
		log.Println("interrupted cpu field: ")
		log.Println(e)

		for i := range make([]int, cpuNum) {
			fmt.Println(i)
			s := e[i]
			n, err :=  strconv.Atoi(s)
			if err != nil {
				fmt.Println("error")
			}
			r, _ := utf8.DecodeLastRuneInString(s)

			if unicode.IsDigit(r)  && n > 0 {
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
	f, err := os.Open("/proc/cpuinfo")
	if err != nil{
		fmt.Println("error")
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	r := regexp.MustCompile("^processor");

	for scanner.Scan() {
		if r.Match(scanner.Bytes()) {
			num++
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
	return num
}

func addJiffies(e []string, prefix string, stats map[string]uint64) {
	for k, v := range ProcStatFieldMap {
		u, _ := strconv.ParseUint(e[v], 10, 64)
		stats[prefix + "_" + k] += u
		stats[prefix + "_total"] += u
	}

}

func addAllCpuJiffies(e []string, stats map[string]uint64) {
	addJiffies(e, "all_cpu", stats)
}

func updateCpuStat(stats map[string]uint64) {

	f, err := os.Open("/proc/stat")
	if err != nil{
		fmt.Println("error")
	}

	defer f.Close()

	for dev, cpus := range globalParam.InterruptedCpuGroup {
		stats["proc_ctxt"] = 0
		stats["proc_intr"] = 0

		allcpu := false
		if len(cpus) == NumCPU{
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
			if e[0] == "ctxt" &&  stats["proc_ctxt"] == 0 {
				u, _ := strconv.ParseUint(e[1], 10, 64)
				stats["proc_ctxt"] = u
				continue
			}
			if e[0] == "intr" &&  stats["proc_intr"] == 0 {
				u, _ := strconv.ParseUint(e[1], 10, 64)
				stats["proc_intr"] = u
				continue
			}
			if !strings.Contains(e[0], "cpu") {
				continue
			}

			if e[0] == "cpu" {
				addAllCpuJiffies(e, stats)
				if allcpu {
					addJiffies(e, dev, stats)
				}
			} else {
				n := strings.Replace(e[0],"cpu", "", -1)
				_, converter := strconv.Atoi(n)
				if converter == nil {
					isInterrupted := false
					for _, iCpu := range globalParam.InterruptedCpuGroup[dev] {
						if iCpu == n {
							isInterrupted = true
							break
						}
					}
					if (isInterrupted) {
						addJiffies(e, dev, stats)
					}
				}
			}
		}
	}
}

func updateIoStat(stats map[string]uint64) {

	f, err := os.Open("/proc/diskstats")
	if err != nil{
		fmt.Println("error")
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
			stats[k + "_io_util"] = v
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}

func initMetrics(metrics map[string]prometheus.Gauge) {

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

	for _, dev := range globalParam.TargetBlockDevices {
		metrics[dev +  "_io_util"] = prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name: dev + "_io_util",
			Help: dev + " IO Util",
		})
	}

	metrics["si_cpu_user"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "si_cpu_user",
		Help:      "Software Interrupted CPU User",
	})
	metrics["si_cpu_nice"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "si_cpu_nice",
		Help:      "Software Interrupted CPU Nice",
	})
	metrics["si_cpu_system"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "si_cpu_system",
		Help:      "Software Interrupted CPU System",
	})
	metrics["si_cpu_idle"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "si_cpu_idle",
		Help:      "Software Interrupted CPU Idle",
	})
	metrics["si_cpu_wio"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "si_cpu_wio",
		Help:      "Software Interrupted CPU wio",
	})
	metrics["si_intr"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "si_cpu_intr",
		Help:      "Software Interrupted CPU Intr",
	})
	metrics["si_sintr"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "si_cpu_sintr",
		Help:      "Software Interrupted CPU SIntr",
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

}

var (
	verbose = kingpin.Flag("verbose", "Verbose mode.").Short('v').Bool()
	targetBlockDevice  = kingpin.Flag("target-block-devices", "Target block devices to track io utils").Short('b').String()
	listenAddress = kingpin.Flag("listen-address", "The address to listen on for HTTP requests.").Default(":5000").String()
	interruptedThreshold = kingpin.Flag("interrupted-threshold", "Threshold to consider interrupted cpu usage as sysload").Default("40.0").Float32()
)

type Parameter struct {
	Verbose bool
	TargetBlockDevices []string
	InterruptThreshold float32
	TargetNetworkDevices []string
	InterruptedCpuGroup map[string][]string
}

var globalParam Parameter


func main() {

	kingpin.Parse()

	fmt.Println("listen-address:")
	fmt.Println(*listenAddress)
	log.Println("interruptedThreshold")
	log.Println(*interruptedThreshold)

	rand.Seed(42)
	confUserHz, err := sysconf.Sysconf(sysconf.SC_CLK_TCK)
	NumCPU = getCpuNum()
	if err == nil {
		fmt.Printf("SC_CLK_TCK: %v\n", confUserHz)
	}
	UserHz = confUserHz

	globalParam.TargetBlockDevices = findBlockDevices()
	globalParam.TargetNetworkDevices = []string {"eth0", "eth1", "eth2", "eth3", "virtio0-input"}

	// init interrupted cpu group
	globalParam.InterruptedCpuGroup = make(map[string][]string)
	for _,d := range globalParam.TargetNetworkDevices {
		r := findInterruptedCpu(d)
		if len(r) > 0 {
			globalParam.InterruptedCpuGroup[d] = r
		}
	}

	log.Println(globalParam)

	metrics = make(map[string]prometheus.Gauge)

	initMetrics(metrics)

	for _, e := range metrics {
		prometheus.MustRegister(e)
	}

	go updateMetrics(refreshRate)

	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(*listenAddress, nil))

}

func updateMetrics(refreshRate int) {

	ioStats := make(map[string]uint64)
	ioStatsPrev := make(map[string]uint64)
	cpuStats := make(map[string]uint64)
	cpuStatsPrev := make(map[string]uint64)
	var statTime, statTimePrev time.Time

	// init metrics values
	metricsValues := make(map[string]float64)
	for k, _ := range metrics {
		metricsValues[k] = 0.0
	}

	// init sysload map
	sysloadArrayMap := make(map[string][]float32)

	sysloadArrayMap["sys_load_one"] = make([]float32, 60 / refreshRate)
	sysloadArrayMap["sys_load_five"] = make([]float32, 300 / refreshRate)
	sysloadArrayMap["sys_load_fifteen"]  = make([]float32, 900 / refreshRate)
	for _,v := range sysloadArrayMap {
		for i, _ := range v {
			v[i] = 0.0
		}
	}

	log.Println(sysloadArrayMap)

	counter := 0

	for {
		counter++

		statTime = time.Now()

		updateIoStat(ioStats)
		updateCpuStat(cpuStats)


		if !statTimePrev.IsZero() {
			log.Println("prev is  not zero")
			log.Println(ioStats)
			log.Println(ioStatsPrev)

			log.Println(cpuStats)
			log.Println(cpuStatsPrev)

			log.Println(statTime)
			log.Println(statTimePrev)
			timeDiffMs := statTime.Sub(statTimePrev).Milliseconds()
			log.Println(timeDiffMs)
			//log.Println(metrics)

			sintr := 0.0
			busyDev := ""
			for dev, _ := range globalParam.InterruptedCpuGroup {
				devDiff := float64(cpuStats[dev + "_total"] - cpuStatsPrev[dev + "_total"])
				log.Println("dev diff: ")
				log.Println(devDiff)
				for k, _ := range cpuStats {
					if strings.Contains(k, dev) {
						d := float64(cpuStats[k] - cpuStatsPrev[k])
						log.Println("d: ")
						log.Println(d)
						log.Println(k)
						if d > 0 {
							metricsValues[k] = d / devDiff * 100
						} else {
							metricsValues[k] = 0.0
						}
					}
					if k == dev + "_sintr" && sintr <= metricsValues[k] {
						sintr = metricsValues[k]
						busyDev = dev
					}
				}
			}
			for k, _ := range ProcStatFieldMap {
				metricsValues["si_cpu_" + k] = metricsValues[busyDev + "_" + k]
			}


			for k, v := range ioStats {
				re := regexp.MustCompile(`io_util`)
				if re.MatchString(k) {
					diff := counterWrap(float64(v - ioStatsPrev[k]))
					metricsValues[k] = diff / float64(timeDiffMs) * 100
				}
			}
			for k, v := range metrics {
				v.Set(metricsValues[k])
			}

			log.Println(metricsValues)

		}

		// copy
		for k,v := range ioStats {
			ioStatsPrev[k] = v
		}
		for k,v := range cpuStats {
			cpuStatsPrev[k] = v
		}
		statTimePrev = statTime

		fmt.Printf("metric updated: %d \n", counter)
		time.Sleep(time.Duration(refreshRate) * time.Second)
	}

}