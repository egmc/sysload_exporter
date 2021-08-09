package main

import (
	"bufio"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	"github.com/tklauser/go-sysconf"
)

const (
	namespace = "sysload"
)

var refreshRate = 5
var UserHz int64

var metrics map[string]prometheus.Gauge
var stats map[string]uint64

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
		//fmt.Println(e)

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
				interruptedCpu = append(interruptedCpu, s)
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

func updateIoStat() {

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
		metrics[dev +  "io_util"] = prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name: dev + "_io_util",
			Help: dev + " IO Util",
		})
	}


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
}

var globalParam Parameter


func main() {

	kingpin.Parse()

	fmt.Println("listen-address:")
	fmt.Println(*listenAddress)
	log.Println("interruptedThreshold")
	log.Println(*interruptedThreshold)

	rand.Seed(42)
	UserHz, err := sysconf.Sysconf(sysconf.SC_CLK_TCK)
	if err == nil {
		fmt.Printf("SC_CLK_TCK: %v\n", UserHz)
	}

	globalParam.TargetBlockDevices = findBlockDevices()

	metrics = make(map[string]prometheus.Gauge)
	stats = make(map[string]uint64)

	initMetrics(metrics)

	for _, e := range metrics {
		prometheus.MustRegister(e)
	}


	findInterruptedCpu("virtio0-input")

	go updateMetrics(refreshRate)

	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(*listenAddress, nil))

}

func updateMetrics(refreshRate int) {

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
		time.Sleep(time.Duration(refreshRate) * time.Second)

		updateIoStat()

		log.Println(stats)

		fmt.Printf("metric updated: %d \n", counter)
		//sysloadFive.Set(rand.Float64())
		//metrics["sysload30"].(prometheus.NewGauge).Set(rand.Float64())
		for _, e := range metrics {
			e.Set(rand.Float64())
			//if g, ok := e.(prometheus.Gauge); ok {
			//	g.Set(rand.Float64())
			//}
		}
	}

}