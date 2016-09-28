package metrics

import (
	"encoding/csv"
	"fmt"
	"io"
	"net"
	"net/http"
	_ "net/http/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

const (
	namespace = "haproxy" // For Prometheus metrics.

	// HAProxy 1.4
	// # pxname,svname,qcur,qmax,scur,smax,slim,stot,bin,bout,dreq,dresp,ereq,econ,eresp,wretr,wredis,status,weight,act,bck,chkfail,chkdown,lastchg,downtime,qlimit,pid,iid,sid,throttle,lbtot,tracked,type,rate,rate_lim,rate_max,check_status,check_code,check_duration,hrsp_1xx,hrsp_2xx,hrsp_3xx,hrsp_4xx,hrsp_5xx,hrsp_other,hanafail,req_rate,req_rate_max,req_tot,cli_abrt,srv_abrt,
	// HAProxy 1.5
	// pxname,svname,qcur,qmax,scur,smax,slim,stot,bin,bout,dreq,dresp,ereq,econ,eresp,wretr,wredis,status,weight,act,bck,chkfail,chkdown,lastchg,downtime,qlimit,pid,iid,sid,throttle,lbtot,tracked,type,rate,rate_lim,rate_max,check_status,check_code,check_duration,hrsp_1xx,hrsp_2xx,hrsp_3xx,hrsp_4xx,hrsp_5xx,hrsp_other,hanafail,req_rate,req_rate_max,req_tot,cli_abrt,srv_abrt,comp_in,comp_out,comp_byp,comp_rsp,lastsess,
	expectedCsvFieldCount = 52
	statusField           = 17
	current_session_index = 4

	frontend = "0"
	backend  = "1"
	server   = "2"
	listener = "3"

	LINKER_HAPROXY_STACK_SERVERS = "stack_servers"
	//	LINKER_HAPROXY_HSS_SERVERS = "hss_servers"
	LINKER_HAPROXY_HSS_SERVERS = "hssdcos_hss_10068"
)

var (
	frontendLabelNames = []string{"frontend"}
	backendLabelNames  = []string{"backend"}
	serverLabelNames   = []string{"backend", "server"}
)

func newFrontendMetric(metricName string, docString string, constLabels prometheus.Labels) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "frontend_" + metricName,
			Help:        docString,
			ConstLabels: constLabels,
		},
		frontendLabelNames,
	)
}

func newBackendMetric(metricName string, docString string, constLabels prometheus.Labels) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "backend_" + metricName,
			Help:        docString,
			ConstLabels: constLabels,
		},
		backendLabelNames,
	)
}

func newServerMetric(metricName string, docString string, constLabels prometheus.Labels) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "server_" + metricName,
			Help:        docString,
			ConstLabels: constLabels,
		},
		serverLabelNames,
	)
}

type metrics map[int]*prometheus.GaugeVec

func (m metrics) String() string {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	s := make([]string, len(keys))
	for i, k := range keys {
		s[i] = strconv.Itoa(k)
	}
	return strings.Join(s, ",")
}

var (
	serverMetrics = metrics{
		4:  newServerMetric("current_sessions", "Current number of active sessions.", nil),
		5:  newServerMetric("max_sessions", "Maximum observed number of active sessions.", nil),
		7:  newServerMetric("connections_total", "Total number of connections.", nil),
		8:  newServerMetric("bytes_in_total", "Current total of incoming bytes.", nil),
		9:  newServerMetric("bytes_out_total", "Current total of outgoing bytes.", nil),
		13: newServerMetric("connection_errors_total", "Total of connection errors.", nil),
		14: newServerMetric("response_errors_total", "Total of response errors.", nil),
		15: newServerMetric("retry_warnings_total", "Total of retry warnings.", nil),
		16: newServerMetric("redispatch_warnings_total", "Total of redispatch warnings.", nil),
		17: newServerMetric("up", "Current health status of the server (1 = UP, 0 = DOWN).", nil),
		18: newServerMetric("weight", "Current weight of the server.", nil),
		33: newServerMetric("current_session_rate", "Current number of sessions per second over last elapsed second.", nil),
		35: newServerMetric("max_session_rate", "Maximum observed number of sessions per second.", nil),
		38: newServerMetric("check_duration_milliseconds", "Previously run health check duration, in milliseconds", nil),
		39: newServerMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "1xx"}),
		40: newServerMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "2xx"}),
		41: newServerMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "3xx"}),
		42: newServerMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "4xx"}),
		43: newServerMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "5xx"}),
		44: newServerMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "other"}),
	}
)

// Exporter collects HAProxy stats from the given URI and exports them using
// the prometheus metrics package.
type Exporter struct {
	URI   string
	mutex sync.RWMutex

	up                                             prometheus.Gauge
	totalScrapes, csvParseFailures                 prometheus.Counter
	frontendMetrics, backendMetrics, serverMetrics map[int]*prometheus.GaugeVec
	client                                         *http.Client
}

// NewExporter returns an initialized Exporter.
func NewExporter(uri string, selectedServerMetrics map[int]*prometheus.GaugeVec, timeout time.Duration) *Exporter {
	return &Exporter{
		URI: uri,
		up: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "up",
			Help:      "Was the last scrape of haproxy successful.",
		}),
		totalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "exporter_total_scrapes",
			Help:      "Current total HAProxy scrapes.",
		}),
		csvParseFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "exporter_csv_parse_failures",
			Help:      "Number of errors while parsing CSV.",
		}),
		frontendMetrics: map[int]*prometheus.GaugeVec{
			4:  newFrontendMetric("current_sessions", "Current number of active sessions.", nil),
			5:  newFrontendMetric("max_sessions", "Maximum observed number of active sessions.", nil),
			6:  newFrontendMetric("limit_sessions", "Configured session limit.", nil),
			7:  newFrontendMetric("connections_total", "Total number of connections.", nil),
			8:  newFrontendMetric("bytes_in_total", "Current total of incoming bytes.", nil),
			9:  newFrontendMetric("bytes_out_total", "Current total of outgoing bytes.", nil),
			10: newFrontendMetric("requests_denied_total", "Total of requests denied for security.", nil),
			12: newFrontendMetric("request_errors_total", "Total of request errors.", nil),
			33: newFrontendMetric("current_session_rate", "Current number of sessions per second over last elapsed second.", nil),
			34: newFrontendMetric("limit_session_rate", "Configured limit on new sessions per second.", nil),
			35: newFrontendMetric("max_session_rate", "Maximum observed number of sessions per second.", nil),
			39: newFrontendMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "1xx"}),
			40: newFrontendMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "2xx"}),
			41: newFrontendMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "3xx"}),
			42: newFrontendMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "4xx"}),
			43: newFrontendMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "5xx"}),
			44: newFrontendMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "other"}),
			48: newFrontendMetric("http_requests_total", "Total HTTP requests.", nil),
		},
		backendMetrics: map[int]*prometheus.GaugeVec{
			2:  newBackendMetric("current_queue", "Current server queue length.", nil),
			3:  newBackendMetric("max_queue", "Maximum observed server queue length.", nil),
			4:  newBackendMetric("current_sessions", "Current number of active sessions.", nil),
			5:  newBackendMetric("max_sessions", "Maximum observed number of active sessions.", nil),
			6:  newBackendMetric("limit_sessions", "Configured session limit.", nil),
			7:  newBackendMetric("connections_total", "Total number of connections.", nil),
			8:  newBackendMetric("bytes_in_total", "Current total of incoming bytes.", nil),
			9:  newBackendMetric("bytes_out_total", "Current total of outgoing bytes.", nil),
			13: newBackendMetric("connection_errors_total", "Total of connection errors.", nil),
			14: newBackendMetric("response_errors_total", "Total of response errors.", nil),
			15: newBackendMetric("retry_warnings_total", "Total of retry warnings.", nil),
			16: newBackendMetric("redispatch_warnings_total", "Total of redispatch warnings.", nil),
			17: newBackendMetric("up", "Current health status of the backend (1 = UP, 0 = DOWN).", nil),
			18: newBackendMetric("weight", "Total weight of the servers in the backend.", nil),
			33: newBackendMetric("current_session_rate", "Current number of sessions per second over last elapsed second.", nil),
			35: newBackendMetric("max_session_rate", "Maximum number of sessions per second.", nil),
			39: newBackendMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "1xx"}),
			40: newBackendMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "2xx"}),
			41: newBackendMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "3xx"}),
			42: newBackendMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "4xx"}),
			43: newBackendMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "5xx"}),
			44: newBackendMetric("http_responses_total", "Total of HTTP responses.", prometheus.Labels{"code": "other"}),
		},
		serverMetrics: selectedServerMetrics,
		client: &http.Client{
			Transport: &http.Transport{
				Dial: func(netw, addr string) (net.Conn, error) {
					c, err := net.DialTimeout(netw, addr, timeout)
					if err != nil {
						return nil, err
					}
					if err := c.SetDeadline(time.Now().Add(timeout)); err != nil {
						return nil, err
					}
					return c, nil
				},
			},
		},
	}
}

// Describe describes all the metrics ever exported by the HAProxy exporter. It
// implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range e.frontendMetrics {
		m.Describe(ch)
	}
	for _, m := range e.backendMetrics {
		m.Describe(ch)
	}
	for _, m := range e.serverMetrics {
		m.Describe(ch)
	}
	ch <- e.up.Desc()
	ch <- e.totalScrapes.Desc()
	ch <- e.csvParseFailures.Desc()
}

// Collect fetches the stats from configured HAProxy location and delivers them
// as Prometheus metrics. It implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) (value int64, total int) {
	csvRows := make(chan []string)

	go e.scrape(csvRows)

	e.mutex.Lock() // To protect metrics from concurrent collects.
	defer e.mutex.Unlock()
	//	e.resetMetrics()
	return e.setMetrics(csvRows)
	//	ch <- e.up
	//	ch <- e.totalScrapes
	//	ch <- e.csvParseFailures
	//	e.collectMetrics(ch)
}

func (e *Exporter) scrape(csvRows chan<- []string) {
	defer close(csvRows)

	e.totalScrapes.Inc()

	resp, err := e.client.Get(e.URI)
	if err != nil {
		e.up.Set(0)
		log.Errorf("Can't scrape HAProxy: %v", err)
		return
	}
	defer resp.Body.Close()
	e.up.Set(1)

	reader := csv.NewReader(resp.Body)
	reader.TrailingComma = true
	reader.Comment = '#'

	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Errorf("Can't read CSV: %v", err)
			e.csvParseFailures.Inc()
			break
		}
		if len(row) == 0 {
			continue
		}
		csvRows <- row
	}
}

func (e *Exporter) resetMetrics() {
	for _, m := range e.frontendMetrics {
		m.Reset()
	}
	for _, m := range e.backendMetrics {
		m.Reset()
	}
	for _, m := range e.serverMetrics {
		m.Reset()
	}
}

func (e *Exporter) collectMetrics(metrics chan<- prometheus.Metric) {
	for _, m := range e.frontendMetrics {
		m.Collect(metrics)
	}
	for _, m := range e.backendMetrics {
		m.Collect(metrics)
	}
	for _, m := range e.serverMetrics {
		m.Collect(metrics)
	}
}

func (e *Exporter) setMetrics(csvRows <-chan []string) (value int64, total int) {

	current_session_value, current_sesion_count := e.fetchCsvData(csvRows, current_session_index, LINKER_HAPROXY_HSS_SERVERS)

	return current_session_value, current_sesion_count

}

func (e *Exporter) fetchCsvData(csvRows <-chan []string, index int, target string) (value int64, total int) {
	//	currentSessionValue := ""
	count := 0
	var value64 int64
	for csvRow := range csvRows {
		if len(csvRow) < expectedCsvFieldCount {
			log.Errorf("Wrong CSV field count: %d vs. %d", len(csvRow), expectedCsvFieldCount)
			e.csvParseFailures.Inc()
			continue
		}

		pxname, _, type_ := csvRow[0], csvRow[1], csvRow[32]

		if pxname == target {
			if type_ == frontend {
				fmt.Printf("frontend csvRow is %v\n", csvRow)
				fmt.Printf("frontend labels: %s \n", pxname)
				fmt.Printf("frontend Value: %v \n", csvRow[index])
				valueStr := csvRow[index]
				var err error
				value, err := strconv.ParseInt(valueStr, 10, 64)
				value64 = value
				if err != nil {
					log.Errorf("Can't parse CSV field value %s: %v", valueStr, err)
					e.csvParseFailures.Inc()
					continue
				}

				fmt.Printf("frontend Value: %v \n", value)

			}

			//			if type_ == backend {
			//				fmt.Printf("backend csvRow is %v\n", csvRow)
			//				fmt.Printf("backend labels: %s\n", pxname)
			//				fmt.Printf("backend Value: %v \n", csvRow[index])
			//				e.exportCsvFields(e.frontendMetrics, csvRow, pxname)
			//			}

			if type_ == server {
				count++
				fmt.Printf("server csvRow is %v\n", csvRow)
				fmt.Printf("server labels: %s\n", pxname)
				fmt.Printf("server Value: %v \n", csvRow[index])
				e.exportCsvFields(e.frontendMetrics, csvRow, pxname)
			}
		}

		fmt.Printf("server count Value: %v \n", count)

	}
	return value64, count
}

func (e *Exporter) findFieldValue(csvRows <-chan []string, index int) {
	for csvRow := range csvRows {
		if len(csvRow) < expectedCsvFieldCount {
			log.Errorf("Wrong CSV field count: %d vs. %d", len(csvRow), expectedCsvFieldCount)
			e.csvParseFailures.Inc()
			continue
		}

		pxname, _, type_ := csvRow[0], csvRow[1], csvRow[32]

		const (
			frontend = "0"
			backend  = "1"
			server   = "2"
			listener = "3"
		)

		if type_ == frontend {
			fmt.Printf("csvRow is %v\n", csvRow)
			fmt.Printf("labels: %s\n", pxname)
			e.exportCsvFields(e.frontendMetrics, csvRow, pxname)
		}

		if type_ == backend {
			fmt.Printf("backend csvRow is %v\n", csvRow)
			fmt.Printf("backend labels: %s\n", pxname)
			e.exportCsvFields(e.backendMetrics, csvRow, pxname)
		}

		if type_ == server {
			fmt.Printf("server csvRow is %v\n", csvRow)
			fmt.Printf("server labels: %s\n", pxname)
			e.exportCsvFields(e.serverMetrics, csvRow, pxname)
		}

	}
}

func parseStatusField(value string) int64 {
	switch value {
	case "UP", "UP 1/3", "UP 2/3", "OPEN", "no check":
		return 1
	case "DOWN", "DOWN 1/2", "NOLB", "MAINT":
		return 0
	}
	return 0
}

func (e *Exporter) exportCsvFields(metrics map[int]*prometheus.GaugeVec, csvRow []string, labels ...string) {
	for fieldIdx, metric := range metrics {
		valueStr := csvRow[fieldIdx]
		if valueStr == "" {
			continue
		}

		var value int64
		switch fieldIdx {
		case statusField:
			value = parseStatusField(valueStr)
		default:
			var err error
			value, err = strconv.ParseInt(valueStr, 10, 64)
			if err != nil {
				log.Errorf("Can't parse CSV field value %s: %v", valueStr, err)
				e.csvParseFailures.Inc()
				continue
			}
		}
		metric.WithLabelValues(labels...).Set(float64(value))
		//		fmt.Printf("exportCsvFields field %d, value is %s \n", fieldIdx, value)

		//		for _, lableValue := range labels {
		//			fmt.Printf("lableValue: %s \n", lableValue)
		//		}
	}
}

// filterServerMetrics returns the set of server metrics specified by the comma
// separated filter.
func filterServerMetrics(filter string) (map[int]*prometheus.GaugeVec, error) {
	metrics := map[int]*prometheus.GaugeVec{}
	if len(filter) == 0 {
		return metrics, nil
	}

	selected := map[int]struct{}{}
	for _, f := range strings.Split(filter, ",") {
		field, err := strconv.Atoi(f)
		if err != nil {
			return nil, fmt.Errorf("invalid server metric field number: %v", f)
		}
		selected[field] = struct{}{}
	}

	for field, metric := range serverMetrics {
		if _, ok := selected[field]; ok {
			metrics[field] = metric
		}
	}
	return metrics, nil
}

func initHaproxyExporter(haProxyScrapeURI, haProxyServerMetricFields string, ch chan<- prometheus.Metric) (value int64, total int) {
	haProxyTimeout := 5 * time.Second
	selectedServerMetrics, _ := filterServerMetrics(haProxyServerMetricFields)
	exporter := NewExporter(haProxyScrapeURI, selectedServerMetrics, haProxyTimeout)
	return exporter.Collect(ch)
}
