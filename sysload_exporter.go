package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"log"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
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
		fmt.Println(e[2])
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


func findInterruptedCpu() {
	cpuNum := getCpuNum()
	fmt.Printf("num: %d\n", cpuNum)
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

var addr = flag.String("listen-address", ":5000", "The address to listen on for HTTP requests.")

func main() {
	flag.Parse()
	fmt.Print(*addr)

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
	findInterruptedCpu()

	go update(refreshRate)

	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(*addr, nil))

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