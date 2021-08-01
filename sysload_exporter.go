package main

import (
	"bufio"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/alecthomas/kingpin.v2"
	"log"
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

//type myCollector struct{}

var refreshRate = 5

//var (
//	sysload = prometheus.NewGauge(prometheus.GaugeOpts{
//		Namespace: namespace,
//		Name:      "sysload",
//		Help:      "Sysload",
//	})
//	sysloadOne = prometheus.NewGauge(prometheus.GaugeOpts{
//		Namespace: namespace,
//		Name:      "sysload_one",
//		Help:      "Sysload 1 min",
//	})
//	sysloadFive = prometheus.NewGauge(prometheus.GaugeOpts{
//		Namespace: namespace,
//		Name:      "sysload_five",
//		Help:      "sysload five help",
//	})
//
//	sysloadFifteen = prometheus.NewGauge(prometheus.GaugeOpts{
//		Namespace: namespace,
//		Name:      "sysload_fifteen",
//		Help:      "Sysload 15 min",
//	})
//
//)

var metrics map[string]prometheus.Gauge


//func (c myCollector) Describe(ch chan<- *prometheus.Desc) {
//	ch <- sysloadFive.Desc()
//}
//
//func (c myCollector) Collect(ch chan<- prometheus.Metric) {
//	exampleValue := float64(12345)
//
//	timeNow := time.Now()
//	fmt.Println(timeNow)
//
//	ch <- prometheus.MustNewConstMetric(
//		sysloadFive.Desc(),
//		prometheus.GaugeValue,
//		float64(exampleValue),
//	)
//}

func init() {

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

var (
	verbose = kingpin.Flag("verbose", "Verbose mode.").Short('v').Bool()
	targetBlockDevice  = kingpin.Flag("target-block-devices", "Target block devices to track io utils").Short('b').String()
	listenAddress = kingpin.Flag("listen-address", "The address to listen on for HTTP requests.").Default(":5000").String()
)

type Parameter struct {
	Verbose bool
	TargetBlockDevices []string
	InterruptThreshold float32
}

var globalParam Parameter

//var addr = flag.String("listen-address", ":5000", "The address to listen on for HTTP requests.")

func main() {

	kingpin.Parse()

	fmt.Println("listen-address:")
	fmt.Println(*listenAddress)

	rand.Seed(42)

	metrics = make(map[string]prometheus.Gauge)
	metrics["sysload30"] = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "sysload30",
		Help:      "Sysload30",
	})

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

	//prometheus.MustRegister(metrics...)
	//
	////var c myCollector
	//prometheus.MustRegister(sysload)
	//prometheus.MustRegister(sysloadOne)
	//prometheus.MustRegister(sysloadFive)
	//prometheus.MustRegister(sysloadFifteen)
	for _, e := range metrics {
		prometheus.MustRegister(e)
	}


	//sysloadFive.Set(100)
	//init()

	// initMetrics
	//refreshRate := 3
	findBlockDevices()
	findInterruptedCpu("virtio0-input")

	go update(refreshRate)

	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(*listenAddress, nil))

}

func update(refreshRate int) {
	i := 0
	for {
		i++
		time.Sleep(time.Duration(refreshRate) * time.Second)
		fmt.Printf("metric updated: %d \n", i)
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