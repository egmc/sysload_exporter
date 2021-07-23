package main

import (
	"flag"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"log"
	"math/rand"
	"net/http"
	"time"
)

// Metricsの定義
const (
	namespace = "sysload"
)

type myCollector struct{} // 今回働いてくれるインスタンス

var refresh_rate = 15

var (
	sysload = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "sysload",
		Help:      "Sysload",
	})
	sysloadOne = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "sysload_one",
		Help:      "Sysload 1 min",
	})
	sysloadFive = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "sysload_five",
		Help:      "sysload five help",
	})

	sysloadFifteen = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "sysload_fifteen",
		Help:      "Sysload 15 min",
	})

)


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


var addr = flag.String("listen-address", ":5000", "The address to listen on for HTTP requests.")

func main() {
	flag.Parse()
	fmt.Print(*addr)

	rand.Seed(42)

	//var c myCollector
	prometheus.MustRegister(sysload)
	prometheus.MustRegister(sysloadOne)
	prometheus.MustRegister(sysloadFive)
	prometheus.MustRegister(sysloadFifteen)

	//sysloadFive.Set(100)
	//init()

	// initMetrics


	http.Handle("/metrics", promhttp.Handler())
	go update(refresh_rate)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

func update(refreshRate int) {
	i := 0
	for {
		i++
		time.Sleep(time.Duration(refreshRate) * time.Second)
		fmt.Println("aaaa")
		fmt.Println(i)
		sysloadFive.Set(rand.Float64())
	}

}