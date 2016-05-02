package goshin

import (
	"fmt"
	"github.com/amir/raidman"
	"github.com/tjgq/broadcast"
	"log/syslog"
	"math"
	"strings"
	"time"
)

var logger, _ = syslog.New(syslog.LOG_DAEMON, "goshin")

type Metric struct {
	Service, Description, State string
	Value                       interface{}
}

func NewMetric() *Metric {
	return &Metric{State: "ok"}
}

type Threshold struct {
	Warning, Critical float64
}

func NewThreshold() *Threshold {
	return &Threshold{}
}

type Goshin struct {
	Address       string
	EventHost     string
	Interval      int
	Tag           []string
	Ttl           float32
	Ifaces        map[string]bool
	IgnoreIfaces  map[string]bool
	Devices       map[string]bool
	IgnoreDevices map[string]bool
	Thresholds    map[string]*Threshold
	Checks        map[string]bool
	ConnectionType string
	Timeout int
}

func NewGoshin() *Goshin {
	return &Goshin{
		Thresholds: make(map[string]*Threshold),
	}
}

func (g *Goshin) Start() {
	defer logger.Close()

	cputime := NewCPUTime()
	memoryusage := NewMemoryUsage()
	loadaverage := NewLoadAverage()
	netstats := NewNetStats(g.Ifaces, g.IgnoreIfaces)
	diskspace := NewDiskSpace()
	diskstats := NewDiskStats(g.Devices, g.IgnoreDevices)

	logger.Info(fmt.Sprintf("starting Goshin : will report each %d seconds", g.Interval))

	// channel size has to be large enough
	// to allow Goshin send all metrics to Riemann
	// in g.Interval
	var collectQueue chan *Metric = make(chan *Metric, 100)

	ticker := time.NewTicker(time.Second * time.Duration(g.Interval))

	b := broadcast.New(10)

	if g.Checks["cpu"] {
		logger.Debug("collector 'cpu' is enabled")
		go cputime.Collect(collectQueue, b.Listen())
	}
	if g.Checks["memory"] {
		logger.Debug("collector 'memory' is enabled")
		go memoryusage.Collect(collectQueue, b.Listen())
	}
	if g.Checks["load"] {
		logger.Debug("collector 'load' is enabled")
		go loadaverage.Collect(collectQueue, b.Listen())
	}
	if g.Checks["net"] {
		logger.Debug("collector 'net' is enabled")
		go netstats.Collect(collectQueue, b.Listen())
	}
	if g.Checks["disk"] {
		logger.Debug("collector 'disk' is enabled")
		go diskspace.Collect(collectQueue, b.Listen())
	}
	if g.Checks["diskstats"] {
		logger.Debug("collector 'diskstats' is enabled")
		go diskstats.Collect(collectQueue, b.Listen())
	}

	go g.Report(collectQueue)

	for t := range ticker.C {
		b.Send(t)
	}
}

func (g *Goshin) EnforceState(metric *Metric) {

	// disk /boot => disk
	// cpu => cpu
	service := strings.Split(metric.Service, " ")[0]

	value := metric.Value
	typeofvalue := fmt.Sprintf("%T", value)
	if strings.HasPrefix(typeofvalue, "float") {
		metric.Value = RoundPlus(value.(float64), 2)
	}

	threshold, present := g.Thresholds[service]

	if present {

		// only for int and float type
		switch {
		case value.(float64) > threshold.Critical:
			metric.State = "critical"
		case value.(float64) > threshold.Warning:
			metric.State = "warning"
		default:
			metric.State = "ok"
		}
	}

}

func (g *Goshin) Report(reportQueue chan *Metric) {

	connected := false
	var connError error
	var c *raidman.Client

	for {
		if connected == false {
			c, connError = raidman.DialWithTimeout(g.ConnectionType, g.Address, time.Duration(g.Timeout) * time.Second)
		}

		if connError != nil {
			logger.Err(fmt.Sprintf("error : can not connect to host %s", g.Address))
			connected = false
		} else {
			connected = true
		}

		metric := <-reportQueue

		if connected {
			g.EnforceState(metric)
			connError = c.Send(&raidman.Event{
				Metric:      metric.Value,
				Ttl:         g.Ttl,
				Service:     metric.Service,
				Description: metric.Description,
				Tags:        g.Tag,
				Host:        g.EventHost,
				State:       metric.State})

			if connError != nil {
				logger.Err(fmt.Sprintf("error : %s", connError))
				c.Close()
				connected = false
			}
		}

		metric = nil
	}
}

// https://gist.github.com/DavidVaini/10308388#gistcomment-1391788
func Round(f float64) float64 {
	return math.Floor(f + .5)
}

func RoundPlus(f float64, places int) float64 {
	shift := math.Pow(10, float64(places))
	return Round(f*shift) / shift
}
